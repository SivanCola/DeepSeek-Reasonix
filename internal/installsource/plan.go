package installsource

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"reasonix/internal/skill"
)

// plan turns a request into a list of actions plus a warnings slice. It
// does not touch the disk; the apply phase is responsible for side effects.
func (t *installSourceTool) plan(ctx context.Context, req request) ([]action, []string, error) {
	if isURL(req.Source) {
		return t.planURL(ctx, req)
	}
	path := t.resolvePath(req.Source)
	if info, err := os.Stat(path); err == nil {
		return t.planLocal(req, path, info)
	}
	if req.Kind == "auto" || req.Kind == "mcp" {
		if looksLikePackage(req.Source) {
			return []action{t.packageMCPAction(req)}, nil, nil
		}
	}
	return nil, nil, newErr(ErrSourceUnreadable, "source %q is not a readable local path, URL, or supported package name", req.Source)
}

func (t *installSourceTool) planURL(ctx context.Context, req request) ([]action, []string, error) {
	rawURL := rawGitHubBlobURL(req.Source)
	if req.Kind == "mcp" && !looksLikeMarkdownURL(rawURL) && !looksLikeMCPJSONURL(rawURL) {
		return []action{t.remoteMCPAction(req, rawURL)}, nil, nil
	}
	if looksLikeMarkdownURL(rawURL) || looksLikeMCPJSONURL(rawURL) || rawURL != req.Source {
		actions, warnings, err := t.planDownloadedURL(ctx, req, rawURL)
		if err == nil && len(actions) > 0 {
			return actions, warnings, nil
		}
		if req.Kind != "auto" {
			return nil, warnings, err
		}
	}
	if gitActions, warnings := t.tryGitHubRepo(ctx, req); len(gitActions) > 0 {
		return gitActions, warnings, nil
	}
	if req.Kind == "skill" {
		return nil, nil, newErr(ErrUnsupportedKind, "URL %q is not a direct markdown skill file or GitHub SKILL.md", req.Source)
	}
	if req.Kind == "auto" && !looksLikeRemoteMCPEndpoint(req.Source) {
		return nil, nil, newErr(ErrUnsupportedKind, "URL %q is not a direct MCP endpoint or skill manifest; provide a raw SKILL.md, .mcp.json, or use kind='mcp' for a remote MCP endpoint", req.Source)
	}
	return []action{t.remoteMCPAction(req, req.Source)}, nil, nil
}

func (t *installSourceTool) planDownloadedURL(ctx context.Context, req request, sourceURL string) ([]action, []string, error) {
	body, err := t.fetchText(ctx, sourceURL)
	if err != nil {
		return nil, nil, err
	}
	if req.Kind == "auto" || req.Kind == "mcp" {
		entries, warnings, err := parseMCPJSON([]byte(body))
		if err == nil && len(entries) > 0 {
			actions := make([]action, 0, len(entries))
			for _, e := range entries {
				actions = append(actions, t.mcpEntryAction(req, e, sourceURL))
			}
			return actions, warnings, nil
		}
	}
	if req.Kind == "auto" || req.Kind == "skill" {
		name := strings.TrimSpace(req.Name)
		if name == "" {
			name = nameFromURL(sourceURL)
		}
		cand, err := parseSkillContent(body, name, sourceURL, req.strict())
		if err == nil {
			return []action{t.skillAction(req, cand, "copy")}, nil, nil
		}
		return nil, nil, err
	}
	return nil, nil, newErr(ErrUnsupportedKind, "downloaded URL did not contain a requested %s install source", req.Kind)
}

func (t *installSourceTool) tryGitHubRepo(ctx context.Context, req request) ([]action, []string) {
	if req.Kind != "auto" && req.Kind != "skill" && req.Kind != "mcp" {
		return nil, nil
	}
	u, err := url.Parse(req.Source)
	if err != nil || !strings.EqualFold(u.Hostname(), "github.com") {
		return nil, nil
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) < 2 {
		return nil, nil
	}
	owner, repo := parts[0], strings.TrimSuffix(parts[1], ".git")
	var warnings []string
	for _, branch := range []string{"main", "master"} {
		var candidates []string
		if req.Kind == "auto" || req.Kind == "mcp" {
			candidates = append(candidates, fmt.Sprintf("https://raw.githubusercontent.com/%s/%s/%s/.mcp.json", owner, repo, branch))
		}
		if req.Kind == "auto" || req.Kind == "skill" {
			candidates = append(candidates, fmt.Sprintf("https://raw.githubusercontent.com/%s/%s/%s/SKILL.md", owner, repo, branch))
		}
		for _, cand := range candidates {
			actions, _, err := t.planDownloadedURL(ctx, req, cand)
			if err == nil && len(actions) > 0 {
				return actions, warnings
			}
			if err != nil {
				warnings = append(warnings, fmt.Sprintf("%s: %s", cand, err.Error()))
			}
		}
	}
	return nil, warnings
}

func (t *installSourceTool) planLocal(req request, path string, info os.FileInfo) ([]action, []string, error) {
	var actions []action
	var warnings []string
	if req.Kind == "auto" || req.Kind == "mcp" {
		mcpPath := path
		if info.IsDir() {
			mcpPath = filepath.Join(path, ".mcp.json")
		}
		if filepath.Base(mcpPath) == ".mcp.json" {
			entries, mcpWarnings, err := readMCPJSON(mcpPath)
			if err == nil && len(entries) > 0 {
				for _, e := range entries {
					actions = append(actions, t.mcpEntryAction(req, e, mcpPath))
				}
				warnings = append(warnings, mcpWarnings...)
			} else if req.Kind == "mcp" {
				return nil, nil, err
			}
		}
		if !info.IsDir() && isExecutable(path, info) && filepath.Base(path) != ".mcp.json" {
			actions = append(actions, t.localExecutableMCPAction(req, path))
		}
	}
	if req.Kind == "auto" || req.Kind == "skill" {
		skillActions, err := t.localSkillActions(req, path, info)
		if err != nil && req.Kind == "skill" {
			return nil, nil, err
		}
		if err != nil && req.Kind == "auto" {
			warnings = append(warnings, err.Error())
		}
		actions = append(actions, skillActions...)
	}
	if len(actions) == 0 {
		return nil, warnings, newErr(ErrManifestMissing, "no installable MCP server or skill found at %s", path)
	}
	sort.SliceStable(actions, func(i, j int) bool {
		if actions[i].Kind != actions[j].Kind {
			return actions[i].Kind < actions[j].Kind
		}
		return actions[i].Name < actions[j].Name
	})
	return actions, warnings, nil
}

func (t *installSourceTool) localSkillActions(req request, path string, info os.FileInfo) ([]action, error) {
	strict := req.strict()
	if !info.IsDir() {
		if !strings.EqualFold(filepath.Ext(path), ".md") {
			return nil, newErr(ErrUnsupportedKind, "not a markdown skill file: %s", path)
		}
		fallback := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
		if strings.EqualFold(filepath.Base(path), skill.SkillFile) {
			fallback = filepath.Base(filepath.Dir(path))
		}
		cand, err := readSkillFile(path, fallback, strict)
		if err != nil {
			return nil, err
		}
		if strings.EqualFold(filepath.Base(path), skill.SkillFile) {
			cand.IsDir = true
			cand.SourcePath = filepath.Dir(path)
			cand.RootPath = filepath.Dir(filepath.Dir(path))
		} else {
			cand.RootPath = filepath.Dir(path)
		}
		if req.Name != "" {
			cand.Name = req.Name
		}
		if req.Mode == "register" {
			root := cand.RootPath
			if root == "" {
				root = filepath.Dir(path)
			}
			return []action{t.skillRootAction(req, root, []string{cand.Name})}, nil
		}
		return []action{t.skillAction(req, cand, modeForSingleSkill(req.Mode))}, nil
	}
	if st, err := os.Stat(filepath.Join(path, skill.SkillFile)); err == nil && st.Mode().IsRegular() {
		cand, err := readSkillFile(filepath.Join(path, skill.SkillFile), filepath.Base(path), strict)
		if err != nil {
			return nil, err
		}
		cand.IsDir = true
		cand.SourcePath = path
		cand.RootPath = filepath.Dir(path)
		if req.Name != "" {
			cand.Name = req.Name
		}
		if req.Mode == "register" {
			return []action{t.skillRootAction(req, filepath.Dir(path), []string{cand.Name})}, nil
		}
		return []action{t.skillAction(req, cand, modeForSingleSkill(req.Mode))}, nil
	}
	cands, err := scanSkillRoot(path, strict)
	if err != nil {
		return nil, err
	}
	if len(cands) == 0 {
		return nil, newErr(ErrManifestMissing, "no SKILL.md or <name>.md skills found under %s", path)
	}
	mode := req.Mode
	if mode == "auto" {
		mode = "register"
	}
	if mode == "register" {
		byRoot := map[string][]string{}
		for _, cand := range cands {
			root := cand.RootPath
			if root == "" {
				root = path
			}
			byRoot[root] = append(byRoot[root], cand.Name)
		}
		roots := make([]string, 0, len(byRoot))
		for root := range byRoot {
			roots = append(roots, root)
		}
		sort.Strings(roots)
		actions := make([]action, 0, len(roots))
		for _, root := range roots {
			rootNames := byRoot[root]
			sort.Strings(rootNames)
			actions = append(actions, t.skillRootAction(req, root, rootNames))
		}
		return actions, nil
	}
	actions := make([]action, 0, len(cands))
	for _, cand := range cands {
		actions = append(actions, t.skillAction(req, cand, mode))
	}
	return actions, nil
}

// strict returns the effective strict setting, defaulting to true when the
// caller did not set the field.
func (r request) strict() bool {
	if r.Strict == nil {
		return true
	}
	return *r.Strict
}
