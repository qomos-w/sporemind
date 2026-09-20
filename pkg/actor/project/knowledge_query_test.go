package project

import (
	"testing"

	"github.com/qomos-w/sporemind/pkg/domain"
)

func TestConceptQueriesAreOnDemand(t *testing.T) {
	a := newTestActor(t.TempDir())
	a.store.Save(decodeCard("concept:runtime", `---
id: concept:runtime
tags: [concept]
parent: concept:root
---
concept body`))
	concept, err := a.handleWikiGetConceptTree(nil, domain.WikiGetConceptTreeReq{ID: "concept:runtime"})
	if err != nil {
		t.Fatal(err)
	}
	if concept.Root.ID != "concept:runtime" || concept.Root.ParentID != "concept:root" {
		t.Fatalf("unexpected concept projection: %+v", concept)
	}
}
