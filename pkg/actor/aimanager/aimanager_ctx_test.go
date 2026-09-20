package aimanager

import (
	"testing"

	"github.com/qomos-w/sporemind/pkg/domain"
)

func TestHandleProviderList_IncludesModelContextLength(t *testing.T) {
	a := &Actor{Providers: []domain.Provider{
		{
			Name:     "openai",
			Kind:     "openai",
			Endpoint: "https://api.openai.com/v1",
			Models: []domain.ProviderModel{
				{Name: "gpt-test", MaxContextLength: 12345, MaxTokens: 4096},
			},
		},
	}}

	list, err := a.handleProviderList(nil)
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}
	if len(list.Items) != 1 {
		t.Fatalf("expected 1 provider, got %d", len(list.Items))
	}
	models := list.Items[0].Models
	if len(models) != 1 || models[0].Name != "gpt-test" {
		t.Fatalf("expected model gpt-test, got %+v", models)
	}
	if models[0].MaxContextLength != 12345 {
		t.Errorf("expected MaxContextLength 12345, got %d", models[0].MaxContextLength)
	}
	if models[0].MaxTokens != 4096 {
		t.Errorf("expected MaxTokens 4096, got %d", models[0].MaxTokens)
	}
}
