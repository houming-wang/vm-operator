package templates

import (
	"os"

	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"testing"
	"github.com/go-logr/logr"
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
	var paramlist = []map[string]interface{}{
		map[string]interface{}{
			"volume":map[string]string{"volume_name":"a","volume_type":"a","volume_size":"a"},
		},
	}
	engine.AddTempFileMust("net","./files/network.yaml.tpl")
	t.Log(engine.RenderByName("net",paramlist[0]))
}