package rules

import (
	"os"
	"path/filepath"
	"testing"
)

// mktree creates each relative path under root; paths ending in / become
// directories, everything else an empty file.
func mktree(t testing.TB, root string, paths ...string) {
	t.Helper()
	for _, p := range paths {
		full := filepath.Join(root, filepath.FromSlash(p))
		if p[len(p)-1] == '/' {
			if err := os.MkdirAll(full, 0o755); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestNodeModulesLockfileIsTierA(t *testing.T) {
	root := t.TempDir()
	mktree(t, root, "app/package.json", "app/package-lock.json", "app/node_modules/")
	m := Default(root).Classify(filepath.Join(root, "app", "node_modules"))
	if m == nil {
		t.Fatal("expected a match")
	}
	if m.Tier != TierA {
		t.Errorf("tier = %v, want A", m.Tier)
	}
}

func TestNodeModulesNoLockfileIsTierB(t *testing.T) {
	root := t.TempDir()
	mktree(t, root, "app/package.json", "app/node_modules/")
	m := Default(root).Classify(filepath.Join(root, "app", "node_modules"))
	if m == nil {
		t.Fatal("expected a match")
	}
	if m.Tier != TierB {
		t.Errorf("tier = %v, want B", m.Tier)
	}
}

func TestNodeModulesOrphanedIsTierB(t *testing.T) {
	root := t.TempDir()
	mktree(t, root, "junk/node_modules/")
	m := Default(root).Classify(filepath.Join(root, "junk", "node_modules"))
	if m == nil {
		t.Fatal("expected a match for orphaned node_modules")
	}
	if m.Tier != TierB {
		t.Errorf("tier = %v, want B", m.Tier)
	}
}

func TestCargoTargetRequiresCargoToml(t *testing.T) {
	root := t.TempDir()
	mktree(t, root, "proj/target/", "other/target/", "proj/Cargo.toml")
	rs := Default(root)
	if m := rs.Classify(filepath.Join(root, "proj", "target")); m == nil || m.Tier != TierA {
		t.Errorf("proj/target: got %+v, want tier A match", m)
	}
	if m := rs.Classify(filepath.Join(root, "other", "target")); m != nil {
		t.Errorf("other/target without Cargo.toml matched: %+v", m)
	}
}

func TestVenvDetectedByPyvenvCfg(t *testing.T) {
	root := t.TempDir()
	mktree(t, root, "proj/.venv/pyvenv.cfg", "proj/requirements.txt", "plain/.venv/")
	rs := Default(root)
	if m := rs.Classify(filepath.Join(root, "proj", ".venv")); m == nil || m.Tier != TierA {
		t.Errorf("proj/.venv: got %+v, want tier A match", m)
	}
	if m := rs.Classify(filepath.Join(root, "plain", ".venv")); m != nil {
		t.Errorf("dir named .venv without pyvenv.cfg matched: %+v", m)
	}
}

func TestToolCachesAlwaysTierA(t *testing.T) {
	root := t.TempDir()
	mktree(t, root, "pkg/__pycache__/")
	m := Default(root).Classify(filepath.Join(root, "pkg", "__pycache__"))
	if m == nil || m.Tier != TierA {
		t.Errorf("__pycache__: got %+v, want tier A match", m)
	}
}

func TestBuildOutputRequiresPackageJSON(t *testing.T) {
	root := t.TempDir()
	mktree(t, root, "web/.next/", "web/package.json", "stray/.next/")
	rs := Default(root)
	if m := rs.Classify(filepath.Join(root, "web", ".next")); m == nil || m.Tier != TierA {
		t.Errorf("web/.next: got %+v, want tier A match", m)
	}
	if m := rs.Classify(filepath.Join(root, "stray", ".next")); m != nil {
		t.Errorf(".next without package.json matched: %+v", m)
	}
}

func TestFixedPathsResolveUnderHome(t *testing.T) {
	home := t.TempDir()
	m := Default(home).Classify(filepath.Join(home, "Library", "Developer", "Xcode", "DerivedData"))
	if m == nil || m.Tier != TierA {
		t.Errorf("DerivedData: got %+v, want tier A match", m)
	}
}

func TestUnknownDirDoesNotMatch(t *testing.T) {
	root := t.TempDir()
	mktree(t, root, "docs/notes.txt")
	if m := Default(root).Classify(filepath.Join(root, "docs")); m != nil {
		t.Errorf("plain dir matched: %+v", m)
	}
}

func TestMavenTargetCoexistsWithCargo(t *testing.T) {
	root := t.TempDir()
	mktree(t, root, "jproj/target/", "jproj/pom.xml", "rproj/target/", "rproj/Cargo.toml")
	rs := Default(root)
	if m := rs.Classify(filepath.Join(root, "jproj", "target")); m == nil || m.Category != "maven target" || m.Tier != TierA {
		t.Errorf("maven target: %+v", m)
	}
	if m := rs.Classify(filepath.Join(root, "rproj", "target")); m == nil || m.Category != "cargo target" {
		t.Errorf("cargo target regressed: %+v", m)
	}
}

func TestGradleBuildRequiresGradleFiles(t *testing.T) {
	root := t.TempDir()
	mktree(t, root, "gproj/build/", "gproj/build.gradle.kts", "plain/build/")
	rs := Default(root)
	if m := rs.Classify(filepath.Join(root, "gproj", "build")); m == nil || m.Tier != TierA {
		t.Errorf("gradle build: %+v", m)
	}
	if m := rs.Classify(filepath.Join(root, "plain", "build")); m != nil {
		t.Errorf("bare build dir matched: %+v", m)
	}
}

func TestPodsTierFollowsLockfile(t *testing.T) {
	root := t.TempDir()
	mktree(t, root, "locked/Pods/", "locked/Podfile", "locked/Podfile.lock", "loose/Pods/", "loose/Podfile", "stray/Pods/")
	rs := Default(root)
	if m := rs.Classify(filepath.Join(root, "locked", "Pods")); m == nil || m.Tier != TierA {
		t.Errorf("locked Pods: %+v", m)
	}
	if m := rs.Classify(filepath.Join(root, "loose", "Pods")); m == nil || m.Tier != TierB {
		t.Errorf("unlocked Pods: %+v", m)
	}
	if m := rs.Classify(filepath.Join(root, "stray", "Pods")); m != nil {
		t.Errorf("stray Pods matched: %+v", m)
	}
}

func TestVendorDisambiguatesComposerAndGo(t *testing.T) {
	root := t.TempDir()
	mktree(t, root, "php/vendor/", "php/composer.lock", "gomod/vendor/modules.txt", "gomod/go.mod", "misc/vendor/")
	rs := Default(root)
	if m := rs.Classify(filepath.Join(root, "php", "vendor")); m == nil || m.Category != "composer vendor" {
		t.Errorf("composer vendor: %+v", m)
	}
	if m := rs.Classify(filepath.Join(root, "gomod", "vendor")); m == nil || m.Category != "go vendor" {
		t.Errorf("go vendor: %+v", m)
	}
	if m := rs.Classify(filepath.Join(root, "misc", "vendor")); m != nil {
		t.Errorf("evidence-free vendor matched: %+v", m)
	}
}

func TestTerraformNeedsLockfile(t *testing.T) {
	root := t.TempDir()
	mktree(t, root, "infra/.terraform/", "infra/.terraform.lock.hcl", "wild/.terraform/")
	rs := Default(root)
	if m := rs.Classify(filepath.Join(root, "infra", ".terraform")); m == nil || m.Tier != TierA {
		t.Errorf("terraform: %+v", m)
	}
	if m := rs.Classify(filepath.Join(root, "wild", ".terraform")); m != nil {
		t.Errorf(".terraform without lockfile matched: %+v", m)
	}
}

// BenchmarkClassifyMiss measures the no-match path, which runs once for
// every directory the scanner visits and must stay near zero-cost.
func BenchmarkClassifyMiss(b *testing.B) {
	root := b.TempDir()
	mktree(b, root, "plain/dir/notes.txt")
	dir := filepath.Join(root, "plain", "dir")
	rs := Default(root)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if m := rs.Classify(dir); m != nil {
			b.Fatal("unexpected match")
		}
	}
}
