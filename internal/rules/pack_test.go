package rules

import (
	"path/filepath"
	"strings"
	"testing"
)

func compileOne(t *testing.T, home string, r PackRule) (*Ruleset, error) {
	t.Helper()
	return Compile(&Pack{Rules: []PackRule{r}}, home)
}

func TestCompileRejectsAmbiguousMatch(t *testing.T) {
	_, err := compileOne(t, t.TempDir(), PackRule{
		Match:    MatchSpec{Basenames: []string{"x"}, HomePath: "y"},
		Variants: []Variant{{Tier: "A", Regen: "r", Why: "w"}},
	})
	if err == nil || !strings.Contains(err.Error(), "exactly one") {
		t.Errorf("ambiguous match accepted: %v", err)
	}
}

func TestCompileRejectsUnknownTier(t *testing.T) {
	_, err := compileOne(t, t.TempDir(), PackRule{
		Match:    MatchSpec{Basenames: []string{"x"}},
		Variants: []Variant{{Tier: "S", Regen: "r", Why: "w"}},
	})
	if err == nil || !strings.Contains(err.Error(), "unknown tier") {
		t.Errorf("unknown tier accepted: %v", err)
	}
}

func TestCompileRejectsBareVariant(t *testing.T) {
	_, err := compileOne(t, t.TempDir(), PackRule{
		Match:    MatchSpec{Basenames: []string{"x"}},
		Variants: []Variant{{Tier: "A", Regen: "r"}},
	})
	if err == nil || !strings.Contains(err.Error(), "evidence or an explicit why") {
		t.Errorf("variant without evidence or why accepted: %v", err)
	}
}

func TestCompileRejectsMissingRegen(t *testing.T) {
	_, err := compileOne(t, t.TempDir(), PackRule{
		Match:    MatchSpec{Basenames: []string{"x"}},
		Variants: []Variant{{Tier: "A", Why: "w"}},
	})
	if err == nil || !strings.Contains(err.Error(), "regen is required") {
		t.Errorf("variant without regen accepted: %v", err)
	}
}

func TestAutoComposedWhyNamesMatchedEvidence(t *testing.T) {
	root := t.TempDir()
	mktree(t, root, "proj/build/", "proj/build.gradle")
	rs, err := compileOne(t, root, PackRule{
		Match: MatchSpec{Basenames: []string{"build"}},
		Variants: []Variant{{
			Category: "gradle build", Tier: "A", Regen: "gradle build",
			Evidence: []EvidenceSpec{{SiblingAny: []string{"build.gradle", "build.gradle.kts"}}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	m := rs.Classify(filepath.Join(root, "proj", "build"))
	if m == nil {
		t.Fatal("expected a match")
	}
	if m.Evidence != "sibling build.gradle" {
		t.Errorf("auto why = %q", m.Evidence)
	}
}

func TestVariantOrderFirstMatchWins(t *testing.T) {
	root := t.TempDir()
	mktree(t, root, "app/junk/", "app/proof.txt")
	rs, err := compileOne(t, root, PackRule{
		Match: MatchSpec{Basenames: []string{"junk"}},
		Variants: []Variant{
			{Category: "junk (proven)", Tier: "A", Regen: "n/a",
				Evidence: []EvidenceSpec{{SiblingAny: []string{"proof.txt"}}}},
			{Category: "junk (guess)", Tier: "B", Regen: "n/a", Why: "no proof",
				Evidence: []EvidenceSpec{}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	m := rs.Classify(filepath.Join(root, "app", "junk"))
	if m == nil || m.Category != "junk (proven)" || m.Tier != TierA {
		t.Errorf("first variant did not win: %+v", m)
	}
}

func TestCategoryDefaultsToBasenameWithPrefix(t *testing.T) {
	root := t.TempDir()
	mktree(t, root, "web/.next/", "web/package.json")
	m := Default(root).Classify(filepath.Join(root, "web", ".next"))
	if m == nil || m.Category != "build output .next" {
		t.Errorf("category = %+v", m)
	}
}
