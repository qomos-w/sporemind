package appmanager

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/sporemind/pkg/appbinding"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/persist"
)

func newRegisterProjectTestActor(t *testing.T) *Actor {
	t.Helper()
	return &Actor{
		actorID:  "appmanager-register-project-test",
		store:    persist.NewFSPersist(t.TempDir()),
		bindings: appbinding.NewRegistry(),
		Apps:     map[string]gen.AppManifest{},
		Records:  map[string]appRecord{},
		children: map[string]string{},
	}
}

func registerProjectTestPlanner(t *testing.T, root string) actor.Planner {
	t.Helper()
	return lifecyclePlanner{call: func(callID string, payload any) (any, error) {
		switch callID {
		case "project.info":
			return gen.ProjectInfoResp{Roots: []gen.ProjectInfoRoot{{Name: "test", Path: root}}}, nil
		case "project.read":
			req := payload.(gen.FileSystemReadReq)
			data, err := os.ReadFile(filepath.FromSlash(req.Path))
			if err != nil {
				return gen.FileSystemReadResp{}, err
			}
			return gen.FileSystemReadResp{Content: string(data)}, nil
		case "project.read_base64":
			req := payload.(gen.FileSystemReadBase64Req)
			data, err := os.ReadFile(filepath.FromSlash(req.Path))
			if err != nil {
				return gen.FileSystemReadBase64Resp{}, err
			}
			return gen.FileSystemReadBase64Resp{Content: base64.StdEncoding.EncodeToString(data)}, nil
		case "project.list":
			return "", nil
		default:
			t.Fatalf("unexpected planner call %s", callID)
			return nil, nil
		}
	}}
}
