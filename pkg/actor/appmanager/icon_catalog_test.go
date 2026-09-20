package appmanager

import (
	"testing"

	"github.com/qomos-w/sporemind/pkg/domain/gen"
)

func TestIconNamesServesCatalog(t *testing.T) {
	a := &Actor{}
	resp, err := a.handleIconNames(nil, gen.AppManagerIconNamesReq{})
	if err != nil {
		t.Fatalf("handleIconNames failed: %v", err)
	}
	if len(resp.Items) < 100 {
		t.Fatalf("expected a full catalog, got %d items", len(resp.Items))
	}
	byName := map[string]gen.AppManagerIconEntry{}
	for _, item := range resp.Items {
		byName[item.Name] = item
	}
	for _, want := range []string{"package", "smartphone", "shield-check", "terminal"} {
		if _, ok := byName[want]; !ok {
			t.Fatalf("catalog missing %q", want)
		}
	}
}

func TestIconNamesFilters(t *testing.T) {
	a := &Actor{}
	resp, err := a.handleIconNames(nil, gen.AppManagerIconNamesReq{Query: "password"})
	if err != nil {
		t.Fatalf("handleIconNames failed: %v", err)
	}
	if len(resp.Items) == 0 {
		t.Fatal("query 'password' should match keyword entries")
	}
	for _, item := range resp.Items {
		if item.Category != "task" && item.Category != "content" && item.Category != "development" &&
			item.Category != "collaboration" && item.Category != "status" && item.Category != "media" && item.Category != "system" {
			t.Fatalf("unexpected category %q", item.Category)
		}
	}

	cat, err := a.handleIconNames(nil, gen.AppManagerIconNamesReq{Category: "development"})
	if err != nil {
		t.Fatalf("handleIconNames failed: %v", err)
	}
	if len(cat.Items) == 0 {
		t.Fatal("category filter returned nothing")
	}
	for _, item := range cat.Items {
		if item.Category != "development" {
			t.Fatalf("category filter leaked %q (%s)", item.Name, item.Category)
		}
	}
}
