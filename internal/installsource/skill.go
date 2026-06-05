package installsource

import (
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"reasonix/internal/config"
	"reasonix/internal/frontmatter"
	"reasonix/internal/skill"
)

// skillAction builds the DTO for a single-skill install (copy or link).
func (t *installSourceTool) skillAction(req request, cand skillCandidate, mode string) action {
	actionName := "copy_skill"
	if mode == "link" {
		actionName = "link_skill"
	}
	target, _ := t.skillTarget(cand.Name, req.Scope, cand.IsDir)
	a := action{
		Kind:       "skill",
		Action:     actionName,
		Name:       cand.Name,
		Source:     cand.SourcePath,
		Target:     target,
		Scope:      req.Scope,
		Mode:       mode,
		ConfigPath: t.configPath(req.Scope),
		Skills:     []string{cand.Name},
		SkillCount: 1,
		skill:      cand,
	}
	a.RiskLevel, a.RiskReasons = skillActionRisk(mode, cand)
	if mode == "link" && !isLinkTargetSafe(cand.SourcePath, t.home, t.root) {
		a.RiskLevel = RiskHigh
		a.RiskReasons = append(a.RiskReasons, "link target is an absolute path outside the project or home root")
	}
	return a
}

// skillActionRisk explains the risk budget for a skill install. The model
// uses this to decide whether to call apply=true directly or to ask first.
func skillActionRisk(mode string, cand skillCandidate) (RiskLevel, []string) {
	reasons := []string{}
	level := RiskLow
	if mode == "link" {
		// Link installs a pointer into a foreign tree; an untrusted source
		// could expose anything at runtime, so we always classify as medium
		// at minimum.
		level = RiskMedium
		reasons = append(reasons, "symlink to a foreign path")
	}
	if cand.IsDir && mode == "copy" {
		reasons = append(reasons, "copy of a directory")
	}
	return level, reasons
}

// skillRootAction builds the DTO for registering a whole skill directory.
func (t *installSourceTool) skillRootAction(req request, path string, names []string) action {
	return action{
		Kind:        "skill",
		Action:      "register_skill_root",
		Name:        "",
		Source:      path,
		Target:      path,
		ConfigPath:  t.configPath(req.Scope),
		Scope:       req.Scope,
		Mode:        "register",
		Skills:      names,
		SkillCount:  len(names),
		RiskLevel:   RiskMedium,
		RiskReasons: []string{"adds a new skill root to the active config"},
	}
}

// skillTarget computes the install destination path for a skill of the
// given name under the resolved scope. The bool `dir` selects between a
// directory layout (<scope>/skills/<name>/SKILL.md) and a flat one
// (<scope>/skills/<name>.md).
func (t *installSourceTool) skillTarget(name, scope string, dir bool) (string, error) {
	if !config.IsValidSkillName(name) {
		return "", newErr(ErrInvalidManifest, "invalid skill name %q", name)
	}
	var root string
	if scope == "global" {
		if t.home == "" {
			return "", newErr(ErrSourceUnreadable, "global skill install requires a home directory")
		}
		root = filepath.Join(t.home, ".reasonix", skill.SkillsDirname)
	} else {
		root = filepath.Join(t.root, ".reasonix", skill.SkillsDirname)
	}
	if dir {
		return filepath.Join(root, name), nil
	}
	return filepath.Join(root, name+".md"), nil
}

// verifySkill confirms the installed skill is reachable through a freshly
// built Store. It is the post-install guard against partial failures.
func (t *installSourceTool) verifySkill(scope, name string) error {
	custom := []string(nil)
	if scope == "project" {
		cfg := config.LoadForEdit(filepath.Join(t.root, "reasonix.toml"))
		custom = cfg.SkillCustomPaths()
	} else if uc := config.UserConfigPath(); uc != "" {
		cfg := config.LoadForEdit(uc)
		custom = cfg.SkillCustomPaths()
	}
	store := skill.New(skill.Options{HomeDir: t.home, ProjectRoot: t.root, CustomPaths: custom})
	if _, ok := store.Read(name); !ok {
		return newErr(ErrSourceUnreadable, "skill %q is installed but not discoverable", name)
	}
	return nil
}

// alternateSkillTarget finds the "other" shape for a given target. If the
// planned install writes a directory, the alternate is a flat file with the
// same stem; vice versa. We check both for the "already exists" guard so a
// pre-existing <name>/SKILL.md is not silently shadowed by a new <name>.md.
func alternateSkillTarget(path string, dir bool) string {
	if dir {
		return strings.TrimSuffix(path, string(filepath.Separator)+skill.SkillFile) + ".md"
	}
	return strings.TrimSuffix(path, filepath.Ext(path))
}

// readSkillFile reads and validates a single skill file. The fallback name
// is used when the frontmatter does not declare one.
func readSkillFile(path, fallbackName string, strict bool) (skillCandidate, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return skillCandidate{}, err
	}
	cand, err := parseSkillContent(string(b), fallbackName, path, strict)
	if err != nil {
		return skillCandidate{}, err
	}
	cand.SourcePath = path
	return cand, nil
}

// parseSkillContent validates the YAML frontmatter of a skill file. With
// strict=true (the default) we require a `name` and a `description`; with
// strict=false a missing description is allowed and the body may be empty —
// useful for installing raw files the user already trusts.
func parseSkillContent(content, fallbackName, source string, strict bool) (skillCandidate, error) {
	bom := "\uFEFF"
	content = strings.TrimPrefix(strings.ReplaceAll(content, "\r\n", "\n"), bom)
	fm, body := frontmatter.Split(content)
	name := strings.TrimSpace(fallbackName)
	if v := strings.TrimSpace(fm["name"]); v != "" {
		name = v
	}
	if !config.IsValidSkillName(name) {
		return skillCandidate{}, newErr(ErrInvalidManifest, "skill %q at %s has an invalid name", name, source)
	}
	desc := collapseSpaces(fm["description"])
	if strict {
		if desc == "" {
			return skillCandidate{}, newErr(ErrInvalidManifest, "skill %q at %s is missing description frontmatter", name, source)
		}
		if strings.TrimSpace(body) == "" {
			return skillCandidate{}, newErr(ErrInvalidManifest, "skill %q at %s has an empty body", name, source)
		}
	}
	return skillCandidate{Name: name, Description: desc, SourcePath: source, Content: content}, nil
}

// scanSkillRoot enumerates skills under a directory: any subdirectory
// containing a SKILL.md is a single skill, and any <name>.md at the root is
// a flat skill. Subdirectories without SKILL.md are skipped silently.
func scanSkillRoot(root string, strict bool) ([]skillCandidate, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	var out []skillCandidate
	for _, e := range entries {
		full := filepath.Join(root, e.Name())
		if e.IsDir() {
			cand, err := readSkillFile(filepath.Join(full, skill.SkillFile), e.Name(), strict)
			if err == nil {
				cand.IsDir = true
				cand.SourcePath = full
				out = append(out, cand)
			}
			continue
		}
		if e.Type().IsRegular() && strings.EqualFold(filepath.Ext(e.Name()), ".md") {
			stem := strings.TrimSuffix(e.Name(), filepath.Ext(e.Name()))
			cand, err := readSkillFile(full, stem, strict)
			if err == nil {
				out = append(out, cand)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// copyDir walks src and writes a parallel tree under dst. O_EXCL refuses to
// overwrite a leaf; a leftover partial tree is left on disk for the user to
// inspect (we never rm -rf). Symlinks inside src are followed once and
// copied as the resolved file, which is what skill directories expect.
func copyDir(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if !d.Type().IsRegular() {
			return nil
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer in.Close()
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err != nil {
			return err
		}
		defer out.Close()
		_, err = io.Copy(out, in)
		return err
	})
}

func writeNewFile(path string, content []byte) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Write(content); err != nil {
		return err
	}
	return nil
}
