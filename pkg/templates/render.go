package templates

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"os"
	"strings"
	"sync"
	"text/template"
	"time"

	"github.com/Masterminds/sprig"
	"github.com/flosch/pongo2"
	"github.com/go-logr/logr"
)

type VmTemplate struct {
	SrcTplDir string
	DstTplDir string
	TplCtx    pongo2.Context
}

var (
	bufpool = sync.Pool{
		New: func() interface{} {
			return new(bytes.Buffer)
		},
	}
)

func New(srcTplDir string, dstTplDir string, tplCtx pongo2.Context) *VmTemplate {
	return &VmTemplate{
		SrcTplDir: srcTplDir,
		DstTplDir: dstTplDir,
		TplCtx:    tplCtx,
	}
}

func (vt *VmTemplate) RenderToFile() error {
	tf, err := vt.getRawTemplateFiles()
	if err != nil {
		fmt.Println("Fail to get vm django template files")
		return err
	}
	for _, fName := range tf {
		rawTpl := strings.Join([]string{vt.SrcTplDir, fName}, "/")
		tpl, err := pongo2.FromFile(rawTpl)
		if err != nil {
			return err
		}

		out, err := tpl.Execute(vt.TplCtx)
		if err != nil {
			return err
		}

		// write render result to template file
		err = os.MkdirAll(vt.DstTplDir, 0755)
		if err != nil {
			fmt.Printf("mkdir failed: %s\n", vt.DstTplDir)
			return err
		}
		cookedTpl := strings.Join([]string{vt.DstTplDir, strings.TrimSuffix(fName, ".tpl")}, "/")
		err = ioutil.WriteFile(cookedTpl, []byte(out), 0644)
		if err != nil {
			return err
		}
	}

	return nil
}

func (vt *VmTemplate) getRawTemplateFiles() ([]string, error) {
	tf := make([]string, 0, 10)
	files, err := ioutil.ReadDir(vt.SrcTplDir)
	if err != nil {
		return nil, err
	}
	for _, f := range files {
		tf = append(tf, f.Name())
	}

	return tf, nil
}

type fileupdate struct {
	fpath      string
	updateTime time.Time
}

type Template struct {
	tpls  map[string]*template.Template
	files map[string]*fileupdate

	mu  sync.RWMutex
	log logr.Logger
}

func NewTemplate(logger logr.Logger) *Template {
	return &Template{
		tpls: make(map[string]*template.Template),
		mu:   sync.RWMutex{},
		log:  logger,
	}
}

func (t *Template) update(name, filepath string) error {
	finfo, err := os.Stat(filepath)
	if err != nil {
		return err
	}
	t.files[name] = &fileupdate{
		fpath:      filepath,
		updateTime: finfo.ModTime(),
	}
	funcmap := sprig.TxtFuncMap()
	funcmap["toChar"]=toChar
	t.tpls[name], err = template.New(name).Funcs(funcmap).ParseFiles(filepath)
	if err != nil {
		return err
	}
	return nil
}

func (t *Template) AddTempFileMust(name string, filepath string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, ok := t.tpls[name]; ok {
		t.log.Info("update template", "name", name)
	}
	err := t.update(name, filepath)
	if err != nil {
		panic(err)
	}
	t.log.Info("add template", "name", name, "filepath", filepath)
	return
}

func (t *Template) RenderByName(name string, params map[string]interface{}) ([]byte, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	buf := bufpool.Get().(*bytes.Buffer)
	defer bufpool.Put(buf)
	if v, ok := t.tpls[name]; ok {
		finfo, err := os.Stat(t.files[name].fpath)
		if err != nil {
			return nil, err
		}
		if t.files[name].updateTime != finfo.ModTime() {
			err = t.update(name, t.files[name].fpath)
			if err != nil {
				return nil, err
			}
			err = t.tpls[name].Execute(buf, params)
			bs := buf.Bytes()
			return bs, err
		}
		err = v.Execute(buf, params)
		bs := buf.Bytes()
		return bs, err
	}

	err := fmt.Errorf("%s not found template", name)
	t.log.Error(err, "reander tpl failed")
	return nil, err
}

//97 - a
func toChar(v interface{}) string{
	switch v := v.(type) {
	case string:
		return v
	case []byte:
		return string(v)
	case int:
		return fmt.Sprintf("%c", rune(v))
	case int32:
		return fmt.Sprintf("%c", rune(v))
	case int64:
		return fmt.Sprintf("%c", rune(v))
	default:
	}
	return ""
}
