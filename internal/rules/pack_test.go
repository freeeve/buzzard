package rules

import (
	"os"
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

// writePack drops a pack file into home/.buzzard/rules.d.
func writePack(t *testing.T, home, name, body string) string {
	t.Helper()
	dir := filepath.Join(home, ".buzzard", "rules.d")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadUserPackAddsRules(t *testing.T) {
	home := t.TempDir()
	writePack(t, home, "bazel.json", `{"rules":[{
		"match":{"basenames":["bazel-out"]},
		"variants":[{"category":"bazel output","tier":"A","regen":"bazel build",
			"evidence":[{"sibling_any":["WORKSPACE","MODULE.bazel"]}]}]}]}`)
	rs, err := Load(home)
	if err != nil {
		t.Fatal(err)
	}
	mktree(t, home, "proj/bazel-out/", "proj/WORKSPACE")
	m := rs.Classify(filepath.Join(home, "proj", "bazel-out"))
	if m == nil || m.Category != "bazel output" || m.Tier != TierA {
		t.Errorf("user rule did not classify: %+v", m)
	}
	if m := rs.Classify(filepath.Join(home, "Library", "Caches")); m == nil {
		t.Error("builtin fixed path lost after merge")
	}
}

func TestLoadNamesFileInError(t *testing.T) {
	home := t.TempDir()
	writePack(t, home, "bad.json", `{"rules":[{"match":{},"variants":[]}]}`)
	_, err := Load(home)
	if err == nil || !strings.Contains(err.Error(), "bad.json") {
		t.Errorf("error does not name the file: %v", err)
	}
}

func TestLoadRejectsFixedPathConflict(t *testing.T) {
	home := t.TempDir()
	writePack(t, home, "evil.json", `{"rules":[{
		"match":{"home_path":".npm"},
		"variants":[{"category":"stealthy override","tier":"A","regen":"n/a","why":"w","evidence":[]}]}]}`)
	_, err := Load(home)
	if err == nil || !strings.Contains(err.Error(), "conflicts") {
		t.Errorf("fixed-path override accepted: %v", err)
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
