package templates

import (
	"encoding/json"
	"os"
	"testing"

	vmv1 "easystack.io/vm-operator/pkg/api/v1"
	"github.com/go-logr/logr"
	"github.com/tidwall/gjson"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)
var (
	log logr.Logger
	engine *Template
)
func init() {
	log = zap.New(zap.WriteTo(os.Stdout))
	engine = NewTemplate(log)
}

func TestAddTempFileMust(t *testing.T){
	engine.AddTempFileMust("net","./files/network.yaml.tpl")
	engine.AddTempFileMust("vm","./files/vm.yaml.tpl")
	engine.AddTempFileMust("vmg","./files/vm_group.yaml.tpl")
}

func TestRenderByName(t *testing.T){
	var paramlist = []vmv1.VirtualMachineSpec{
		vmv1.VirtualMachineSpec{
			Project:        vmv1.ProjectSpec{},
			Server:         vmv1.ServerSpec{},
			Network:        vmv1.NetworkSpec{
				NeutronAz: "a",
			},
			Volume:         []vmv1.VolumeSpec{
				vmv1.VolumeSpec{
					VolumeName: "a",
					VolumeType: "a",
					VolumeSize: "a",
				},
			},
			SoftwareConfig: []byte("abc"),
			AssemblyPhase:  "",
			StackID:        "",
			HeatEvent:      nil,
		},
	}
	for _,v:=range paramlist{
		bs,err:=json.Marshal(&v)
		if err!= nil {
			t.Fatalf(err.Error())
		}
		params := Parse(gjson.ParseBytes(bs))
		t.Log(engine.RenderByName("net",params))
		t.Log(engine.RenderByName("vm",params))
		t.Log(engine.RenderByName("vmg",params))
	}
}