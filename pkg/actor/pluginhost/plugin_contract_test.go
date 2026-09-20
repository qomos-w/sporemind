package pluginhost

import (
	"testing"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	plugincontract "github.com/qomos-w/sporemind/pkg/pluginhost"
)

func TestActorRegisterUnifiedManifest(t *testing.T) {
	a := &Actor{}
	manifest := gen.AppManifest{ID: "com.example.native", Name: "Native", Version: "1.0.0", Runtime: "native", ProtocolVersion: 2, Namespace: "plugin.com.example.native", Callables: []gen.AppCallableDescriptor{{ID: "run", RequestSchema: "Req", ResponseSchema: "Resp"}}}
	_, err := a.handleRegisterActor(nil, registerActorReq{PluginID: manifest.ID, Manifest: &manifest, Abi: &gen.PluginAbi{Name: "spore-plugin", Version: 1, Encoding: "binarycodec-v1", InvokeSymbol: "PluginInvoke", ContractVersion: "1", Isolation: plugincontract.IsolationInProcess, TrustClass: plugincontract.TrustFirstParty, Signer: "sporemind.first-party"}})
	if err != nil {
		t.Fatal(err)
	}
	plugins, err := a.handleListPlugins(nil, listPluginsReq{})
	if err != nil || len(plugins.Plugins) != 1 {
		t.Fatalf("plugins=%+v err=%v", plugins, err)
	}
	if plugins.Plugins[0].Runtime != "native" || plugins.Plugins[0].AbiName != "spore-plugin" {
		t.Fatalf("descriptor=%+v", plugins.Plugins[0])
	}
}
