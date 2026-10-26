package tasks

import (
	"encoding/json"
	"net/http"
	"testing"
)

// TestBonusTemplateKind covers the kind field of a template: a bonus template, the penalty ban,
// the list filter and the immutable kind (PBI M4-10).
func TestBonusTemplateKind(t *testing.T) {
	f := newFixture(t)

	rec := f.do(t, f.parent, http.MethodPost, templatesPath,
		map[string]any{"name": "Bonus chore", "kind": "bonus", "points": 5}, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create bonus template: status %d body %s", rec.Code, rec.Body.String())
	}
	var created map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created template: %v", err)
	}
	if created["kind"] != "bonus" {
		t.Fatalf("kind = %v, want bonus", created["kind"])
	}
	bonusID, _ := created["id"].(string)

	// A bonus template never carries a penalty.
	rec = f.do(t, f.parent, http.MethodPost, templatesPath,
		map[string]any{"name": "x", "kind": "bonus", "penalty_value": 10, "penalty_unit": "percent"}, nil)
	expectValidation(t, rec, "penalty_value", "not_allowed_for_bonus")

	// Only regular and bonus are known kinds.
	rec = f.do(t, f.parent, http.MethodPost, templatesPath, map[string]any{"name": "x", "kind": "chore"}, nil)
	expectValidation(t, rec, "kind", "invalid")

	// A template without a kind is regular.
	rec = f.do(t, f.parent, http.MethodPost, templatesPath, map[string]any{"name": "Regular chore"}, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create regular template: status %d body %s", rec.Code, rec.Body.String())
	}
	var regular map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &regular); err != nil {
		t.Fatalf("decode regular template: %v", err)
	}
	if regular["kind"] != "regular" {
		t.Fatalf("kind = %v, want regular", regular["kind"])
	}

	// The default list holds regular templates only.
	rec = f.do(t, f.parent, http.MethodGet, templatesPath, nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list templates: status %d", rec.Code)
	}
	var regularList struct {
		Templates []map[string]any `json:"templates"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &regularList); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	for _, tpl := range regularList.Templates {
		if tpl["kind"] != "regular" {
			t.Fatalf("default list returned kind %v, want regular only", tpl["kind"])
		}
	}

	// kind=bonus returns the bonus template only.
	rec = f.do(t, f.parent, http.MethodGet, templatesPath+"?kind=bonus", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list bonus templates: status %d", rec.Code)
	}
	var bonusList struct {
		Templates []map[string]any `json:"templates"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &bonusList); err != nil {
		t.Fatalf("decode bonus list: %v", err)
	}
	if len(bonusList.Templates) != 1 || bonusList.Templates[0]["id"] != bonusID {
		t.Fatalf("kind=bonus list = %v, want only %s", bonusList.Templates, bonusID)
	}

	rec = f.do(t, f.parent, http.MethodGet, templatesPath+"?kind=nope", nil, nil)
	expectValidation(t, rec, "kind", "invalid")

	// kind cannot change after create, whether it is sent or not.
	rec = f.do(t, f.parent, http.MethodPatch, templatesPath+"/"+bonusID,
		map[string]any{"kind": "regular"}, nil)
	expectValidation(t, rec, "kind", "immutable")

	rec = f.do(t, f.parent, http.MethodPatch, templatesPath+"/"+bonusID,
		map[string]any{"name": "Bonus chore renamed"}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch bonus template: status %d body %s", rec.Code, rec.Body.String())
	}
	var patched map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &patched); err != nil {
		t.Fatalf("decode patched template: %v", err)
	}
	if patched["kind"] != "bonus" {
		t.Fatalf("kind after patch = %v, want bonus", patched["kind"])
	}
}
