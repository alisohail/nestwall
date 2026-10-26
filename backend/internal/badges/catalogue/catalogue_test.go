package catalogue

import "testing"

func TestBuiltInCatalogueShape(t *testing.T) {
	if n := len(BuiltIn); n != 24 {
		t.Fatalf("built-in badges = %d, want 24", n)
	}

	keys := map[string]bool{}
	groups := map[Group]int{}
	categories := map[string]bool{}
	for _, b := range BuiltIn {
		if keys[b.Key] {
			t.Errorf("duplicate key %q", b.Key)
		}
		keys[b.Key] = true
		groups[b.Group]++
		if b.Target <= 0 {
			t.Errorf("%s: target %d, want positive", b.Key, b.Target)
		}
		if b.Category != "" {
			categories[b.Category] = true
		}
	}

	wantGroups := map[Group]int{
		GroupStreaks: 5, GroupPoints: 5, GroupTasks: 4,
		GroupSkills: 4, GroupTiming: 4, GroupKindness: 2,
	}
	for g, want := range wantGroups {
		if groups[g] != want {
			t.Errorf("group %s: %d badges, want %d", g, groups[g], want)
		}
	}

	for _, c := range []string{"tidy_pro", "homework_hero", "pet_pal", "green_thumb"} {
		if !categories[c] {
			t.Errorf("no skill badge counts category %q", c)
		}
	}
	if len(categories) != 4 {
		t.Errorf("skill categories = %d, want 4", len(categories))
	}
}

func TestByKey(t *testing.T) {
	b, ok := ByKey("k_tidy")
	if !ok {
		t.Fatalf("ByKey(k_tidy) not found")
	}
	if b.Category != "tidy_pro" || b.Target != 20 {
		t.Fatalf("k_tidy = %+v, want category tidy_pro and target 20", b)
	}
	if _, ok := ByKey("nope"); ok {
		t.Fatalf("ByKey(nope) found a badge")
	}
}
