package project

import "testing"

func TestProjectCardRecordUsesCanonicalRefAndContributions(t *testing.T) {
	projection, err := ProjectCardRecord(&CardRecord{
		Title: "skill:review", Type: "skill", Tags: []string{"component", "skill"}, Body: "Review changes.", Data: map[string]any{
			"componentVersion": float64(3), "source": "project", "scope": "project",
			"requires": []any{"builtin:bundle:debug"}, "tools": []any{"project.read"}, "onDemand": true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if projection.Ref.ID != projection.Card.ID || projection.Ref.ID != "skill:review" {
		t.Fatalf("ref/card ID drift: %+v %+v", projection.Ref, projection.Card)
	}
	if projection.Ref.Version != 3 || projection.Ref.Source != "project" || projection.Ref.Scope != "project" {
		t.Fatalf("unexpected ref: %+v", projection.Ref)
	}
	if projection.Card.Type != "skill" || !projection.Card.OnDemand || len(projection.Card.Dependencies) != 1 {
		t.Fatalf("unexpected card: %+v", projection.Card)
	}
	if len(projection.Card.PromptContributions) != 1 || projection.Card.PromptContributions[0].ID != projection.Ref.ID+":body" {
		t.Fatalf("prompt contribution lost canonical ref: %+v", projection.Card.PromptContributions)
	}
	if len(projection.Card.ToolContributions) != 1 || projection.Card.ToolContributions[0].CallableID != "project.read" {
		t.Fatalf("tool contribution mismatch: %+v", projection.Card.ToolContributions)
	}
}

func TestProjectCardRecordIsReadOnlyAndRejectsMissingKind(t *testing.T) {
	record := &CardRecord{Title: "prompt:role", Tags: []string{"prompt"}, Body: "Be concise."}
	before := record.Body
	if _, err := ProjectCardRecord(record); err != nil {
		t.Fatal(err)
	}
	if record.Body != before {
		t.Fatal("projection mutated source card")
	}
	if _, err := ProjectCardRecord(&CardRecord{Title: "ordinary", Tags: []string{"idea"}}); err == nil {
		t.Fatal("missing card kind should fail")
	}
	if _, err := ProjectCardRecord(nil); err == nil {
		t.Fatal("nil card should fail")
	}
}
