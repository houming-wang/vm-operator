package openstack

import (
	"context"
	vmv1 "easystack.io/vm-operator/pkg/api/v1"
	"easystack.io/vm-operator/pkg/templates"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"sync"
	"time"

	"github.com/go-logr/logr"
	"github.com/gophercloud/gophercloud"
	"github.com/gophercloud/gophercloud/openstack"
	"github.com/gophercloud/gophercloud/openstack/orchestration/v1/stacks"
	"github.com/tidwall/gjson"
)

const (
	defaultTimeout  = 10
	CLOUDADMIN      = "drone"
	stackDefaultTag = "ecns-mixapp"

	vmtplname  = "vm.yaml"
	nettplname = "network.yaml"
	vmgTplName = "vm_group.yaml"
	mainTpl    = vmgTplName
)

const (
	S_CREATE_FAILED      = "CREATE_FAILED"
	S_CREATE_IN_PROGRESS = "CREATE_IN_PROGRESS"
	S_CREATE_COMPLETE    = "CREATE_COMPLETE"
	S_UPDATE_FAILED      = "UPDATE_FAILED"
	S_UPDATE_IN_PROGRESS = "UPDATE_IN_PROGRESS"
	S_UPDATE_COMPLETE    = "UPDATE_COMPLETE"

	S_DELETE_FAILED      = "DELETE_FAILED"
	S_DELETE_IN_PROGRESS = "DELETE_IN_PROGRESS"
	S_DELETE_COMPLETE    = "DELETE_COMPLETE"
)

// template file, it have to dependent on tpl content,
// use single template file easy
var (
	ErrNotFound = errors.New("Not Found")
)

type StatStack struct {
	Id           string
	Name         string
	Status       string
	Statusreason string
}

type vm struct {
	stat       *StatStack
	latestSpec *vmv1.VirtualMachineSpec
}

type OSService struct {
	AdminAuthOpt *gophercloud.AuthOptions
	ClientCache  *ClientCache

	logger logr.Logger
	engine *templates.Template
	tmpDir string //need rw mode
	tmpmu  sync.Mutex

	stackch   chan *StatStack
	getSpecFn func(name string) *vmv1.VirtualMachine
}

type UserCredential struct {
	ApplicationCredentialID     string
	ApplicationCredentialSecret string
}

func NewOSService(nettplpath, vmtplpath, vmgtplpath, tmpdir string, logger logr.Logger, getSpecFn func(name string) *vmv1.VirtualMachine) (*OSService, error) {
	// get ECS cloud admin credential info from env
	adminAuthOpt, err := openstack.AuthOptionsFromEnv()
	if err != nil {
		logger.Error(err, "Failed to get environments")
		return nil, err
	}

	provider, err := openstack.AuthenticatedClient(adminAuthOpt)
	if err != nil {
		logger.Error(err, "Failed to Authenticate to OpenStack")
		return nil, err
	}

	client, err := openstack.NewOrchestrationV1(provider, gophercloud.EndpointOpts{Region: "RegionOne"})
	if err != nil {
		logger.Error(err, "Failed to init heat client")
		return nil, err
	}

	// It's safe for first insert
	cc := new(ClientCache)
	cc.clientMap = make(map[string]*gophercloud.ServiceClient)
	cc.userCredential = make(map[string]*UserCredential)
	cc.clientMap[CLOUDADMIN] = client
	// TODO: init cache for project credential by 'openstack application credential list'

	oss := &OSService{
		AdminAuthOpt: &adminAuthOpt,
		ClientCache:  cc,
		logger:       logger,
		stackch:      make(chan *StatStack, 4),
		getSpecFn:    getSpecFn,
	}
	err = oss.probe(nettplpath, vmtplpath, vmgtplpath, tmpdir)
	return oss, err
}

func (oss *OSService) probe(nettplpath, vmtplpath, vmgtplpath, tmpdir string) error {
	tmpl := templates.NewTemplate(oss.logger)
	for k, v := range map[string]string{
		nettplname: nettplpath,
		vmtplname:  vmtplpath,
		vmgTplName: vmgtplpath,
	} {
		tmpl.AddTempFileMust(k, v)
	}
	oss.engine = tmpl
	//TODO, single template file not need change workdir
	return os.Chdir(tmpdir)
}

func (oss *OSService) newHeatClient(projectID string, token string) error {
	if projectID == CLOUDADMIN {
		oss.logger.Info("Already has cloudadmin client during initialization")
		return nil
	}

	authOpt := gophercloud.AuthOptions{
		IdentityEndpoint: oss.AdminAuthOpt.IdentityEndpoint,
		TokenID:          token,
	}

	provider, err := openstack.AuthenticatedClient(authOpt)
	if err != nil {
		oss.logger.Error(err, "Failed to Authenticate to OpenStack")
		return err
	}

	client, err := openstack.NewOrchestrationV1(provider, gophercloud.EndpointOpts{Region: "RegionOne"})
	if err != nil {
		oss.logger.Error(err, "Failed to init heat client")
		return err
	}

	oss.ClientCache.setClient(projectID, client)
	return nil
}

func (oss *OSService) getheatClient(projectID string, token string) (*gophercloud.ServiceClient, error) {
	client, err := oss.ClientCache.getClient(projectID)
	if err == nil {
		return client, nil
	}
	oss.logger.WithName("WARN").Info(err.Error())
	err = oss.newHeatClient(projectID, token)
	if err != nil {
		oss.logger.Error(err, "create heat client conn failed", "projectid", projectID, "token", token)
		return nil, err
	}

	return oss.ClientCache.getClient(projectID)
}

func (oss *OSService) PollingForever(ctx context.Context, duratime time.Duration, updateFn func(*StatStack) error) {
	var (
		stackS = new(StatStack)
	)
	go func() {
		for {
			select {
			case <-time.NewTimer(duratime).C:
				cli, err := oss.getheatClient(CLOUDADMIN, "")
				if err != nil {
					continue
				}
				now := time.Now()
				//TODO
				err = iterStat(cli, stacks.ListOpts{Tags: stackDefaultTag}, func(st *stacks.ListedStack) error {
					oss.logger.V(2).Info("iter stack", "stack", st)
					deepcopyStat(st, stackS)
					// oss.getSpecFn(stackS.Name)
					// use use oss.stackCh try again if failed
					return updateFn(stackS)
				})
				if err != nil {
					oss.logger.Error(err, "poll stack status failed")
				}
				subdu := now.Sub(time.Now())
				if subdu > duratime {
					oss.logger.WithValues("level", "WARN").Info(fmt.Sprintf("poll stack take time %s", subdu.String()), "timer", duratime)
				}
			case <-ctx.Done():
				oss.logger.Info("polling exit...")
				return
			}
		}
	}()
	return
}

func (oss *OSService) Delete(ctx context.Context, name, id string, spec *vmv1.VirtualMachineSpec) (*vmv1.VirtualMachineStatus, error) {
	var (
		err error
		sts = vmv1.VirtualMachineStatus{
			VmStatus: S_DELETE_FAILED,
			StackID:  id,
		}
		projectid, projecttoken string
	)
	if name == "" || id == "" {
		return &sts, fmt.Errorf("Id and Name not define!")
	}
	projectid, projecttoken = getIdToken(spec)
	client, err := oss.getheatClient(projectid, projecttoken)
	if err != nil {
		oss.logger.WithValues("function", "Delete").Error(err, "Failed to get heat client")
		return &sts, err
	}
	err = stacks.Delete(client, name, id).ExtractErr()
	if err == nil {
		sts.VmStatus = S_DELETE_IN_PROGRESS
	} else {
		//only one ,deleted
		sts.VmStatus = S_DELETE_COMPLETE
	}
	oss.logger.WithValues("heat", "delete").Info("delete stack", "err", err)
	return &sts, nil
}

// create if id not exist, update if id exist
// check spec before
func (oss *OSService) CreateOrUpdate(ctx context.Context, name, id string, spec *vmv1.VirtualMachineSpec) (*vmv1.VirtualMachineStatus, error) {
	// TODO VirtualMachineStatus pool
	var (
		err error
		sts = vmv1.VirtualMachineStatus{
			VmStatus: S_CREATE_FAILED,
		}
	)
	if name == "" {
		return &sts, fmt.Errorf("Spec is not define or Name not set")
	}
	//TODO auth should from spec
	projectid, projecttoken := getIdToken(spec)
	client, err := oss.getheatClient(projectid, projecttoken)
	if err != nil {
		oss.logger.WithValues("function", "CreateOrUpdate").Error(err, "Failed to get heat client")
		return &sts, err
	}
	if id == "" {
		cs, err := oss.create(client, spec)
		if oss.logger.V(2).Enabled() {
			oss.logger.WithValues("heat", "create").Info("create stack", "err", err)
		}
		if err != nil {
			return &sts, err
		}
		sts.VmStatus = S_CREATE_IN_PROGRESS
		sts.StackID = cs.ID
		return &sts, nil
	}
	sts.StackID = id
	err = oss.update(client, name, id, spec)
	if oss.logger.V(2).Enabled() {
		oss.logger.WithValues("heat", "update").Info("update stack", "err", err, "stackid", id)
	}
	if err != nil {
		sts.VmStatus = S_UPDATE_FAILED
		return &sts, err
	} else {
		sts.VmStatus = S_UPDATE_IN_PROGRESS
	}
	return &sts, err
}

func (oss *OSService) create(cli *gophercloud.ServiceClient, spec *vmv1.VirtualMachineSpec) (*stacks.CreatedStack, error) {
	var (
		err    error
		ctOpts *stacks.CreateOpts
	)
	oss.tmpmu.Lock()
	defer oss.tmpmu.Unlock()
	err = oss.generatTemp(spec, func(params map[string]interface{}, template *stacks.Template) {
		ctOpts = &stacks.CreateOpts{
			TemplateOpts: template,
			Timeout:      defaultTimeout,
			Parameters:   params,
			Tags:         []string{stackDefaultTag},
		}
	})
	if err != nil {
		return nil, err
	}
	return stacks.Create(cli, ctOpts).Extract()
}

func (oss *OSService) update(cli *gophercloud.ServiceClient, name, id string, spec *vmv1.VirtualMachineSpec) error {
	var (
		err    error
		upOpts *stacks.UpdateOpts
	)
	oss.tmpmu.Lock()
	defer oss.tmpmu.Unlock()
	err = oss.generatTemp(spec, func(params map[string]interface{}, template *stacks.Template) {
		upOpts = &stacks.UpdateOpts{
			TemplateOpts: template,
			Timeout:      defaultTimeout,
			Parameters:   params,
			Tags:         []string{stackDefaultTag},
		}
	})
	if err != nil {
		return err
	}
	return stacks.Update(cli, name, id, upOpts).ExtractErr()
}

func (oss *OSService) generatTemp(spec *vmv1.VirtualMachineSpec, fn func(params map[string]interface{}, template *stacks.Template)) error {
	var (
		params = make(map[string]interface{})
		data   []byte
		err    error
	)
	data, err = json.Marshal(spec)
	if err != nil {
		return err
	}
	//TODO now we just neet network,volume and server sections
	for _, v := range []string{"network", "volume", "server"} {
		params[v] = parse(gjson.GetBytes(data, v))
	}

	for _, v := range []string{nettplname, vmgTplName, vmtplname} {
		data, err = oss.engine.RenderByName(v, params)
		fi, err := os.OpenFile(path.Join(oss.tmpDir, v), os.O_CREATE|os.O_TRUNC|os.O_RDWR, os.ModePerm)
		if err != nil {
			return err
		}
		_, err = fi.Write(data)
		if err != nil {
			oss.logger.Error(err, "write config file failed", "filename", v)
			fi.Close()
			return err
		}
		err = fi.Close()
		if err != nil {
			oss.logger.Error(err, "close config file failed", "filename", v)
			return err
		}
	}
	template := &stacks.Template{
		TE: stacks.TE{
			URL: "file://" + path.Join(oss.tmpDir, mainTpl),
		},
	}
	fn(params, template)
	return nil
}

func parse(result gjson.Result) interface{} {
	if result.IsArray() {
		var rets []interface{}
		result.ForEach(func(_, value gjson.Result) bool {
			rets = append(rets, parse(value))
			return true
		})
		return rets
	}
	if result.IsObject() {
		var rets = make(map[string]interface{})
		result.ForEach(func(key, value gjson.Result) bool {
			//TODO only string type
			if key.Type == gjson.String {
				rets[key.String()] = parse(value)
			}
			return true
		})
		return rets
	}
	switch result.Type {
	case gjson.False:
		return false
	case gjson.Number:
		return result.Int()
	case gjson.String:
		return result.String()
	case gjson.True:
		return true
	default:
		return ""
	}
}

func iterStat(cli *gophercloud.ServiceClient, opts stacks.ListOpts, fn func(st *stacks.ListedStack) error) error {
	stacks.List(cli, opts)
	allStackPages, err := stacks.List(cli, opts).AllPages()
	if err != nil {
		return err
	}
	lists, err := stacks.ExtractStacks(allStackPages)
	if err != nil {
		return err
	}
	for _, v := range lists {
		err = fn(&v)
		if err != nil {
			return err
		}
	}
	return nil
}

func getIdToken(spec *vmv1.VirtualMachineSpec) (id, token string) {
	if spec == nil {
		return CLOUDADMIN, ""
	}
	return spec.Project.ProjectID, spec.Project.Token
}

func deepcopyStat(st *stacks.ListedStack, dst *StatStack) {
	dst.Name = st.Name
	dst.Id = st.ID
	dst.Status = st.Status
	dst.Statusreason = st.StatusReason
}
