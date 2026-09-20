package project

import (
	"encoding/json"
	"os"
	"testing"
)

type manifestCallable struct {
	Namespace  string `json:"namespace"`
	Name       string `json:"name"`
	Visibility string `json:"visibility"`
}

type manifestDocument struct {
	Callables []manifestCallable `json:"callables"`
}

func TestGeneratedManifestProjectCardPolicies(t *testing.T) {
	data, err := os.ReadFile("../../../gen/gmanifest.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest manifestDocument
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"project.card_list":         "public",
		"project.card_mount":        "admin",
		"project.card_unmount":      "admin",
		"project.graph_get":         "public",
		"project.graph_concept_get": "public",
		"project.graph_save":        "public",
	}
	for _, callable := range manifest.Callables {
		key := callable.Namespace + "." + callable.Name
		if expected, ok := want[key]; ok {
			if callable.Visibility != expected {
				t.Errorf("%s visibility = %q, want %q", key, callable.Visibility, expected)
			}
			delete(want, key)
		}
	}
	for key := range want {
		t.Errorf("manifest missing %s", key)
	}
}
