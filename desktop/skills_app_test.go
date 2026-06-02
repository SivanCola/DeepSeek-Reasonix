package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNormalizeSkillPathDirectoryLayout(t *testing.T) {
	root := t.TempDir()
	skillDir := filepath.Join(root, "my-skill")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\ndescription: x\n---\nbody"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := normalizeSkillPath(skillDir); got != root {
		t.Fatalf("normalizeSkillPath(%q) = %q, want %q", skillDir, got, root)
	}
}

func TestSkillRootsViewCountsProjectSkills(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	project := t.TempDir()
	root := filepath.Join(project, ".reasonix", "skills")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "proj.md"), []byte("---\ndescription: project\n---\nbody"), 0o644); err != nil {
		t.Fatal(err)
	}
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(wd)
	if err := os.Chdir(project); err != nil {
		t.Fatal(err)
	}

	roots := skillRootsView()
	want := realTestPath(root)
	for _, r := range roots {
		if realTestPath(r.Dir) == want {
			if r.Status != "ok" || r.Skills != 1 || r.Scope != "project" {
				t.Fatalf("project root view = %+v", r)
			}
			return
		}
	}
	t.Fatalf("project skill root %q not found in %+v", root, roots)
}

func realTestPath(path string) string {
	if p, err := filepath.EvalSymlinks(path); err == nil {
		path = p
	}
	return cleanAbsPath(path)
}
