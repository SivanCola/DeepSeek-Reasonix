package main

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

const (
	maxSessionGroups       = 100
	maxSessionGroupIDRunes = 160
	maxSessionGroupRunes   = 120
	maxSessionGroupTopics  = 10_000
)

type desktopProject struct {
	Root             string         `json:"root"`
	Title            string         `json:"title,omitempty"`
	Color            string         `json:"color,omitempty"`
	Topics           []string       `json:"topics"`
	PinnedTopics     []string       `json:"pinnedTopics,omitempty"`
	ManualTopicOrder bool           `json:"manualTopicOrder,omitempty"`
	Groups           []desktopGroup `json:"groups,omitempty"`
}

type desktopGroup struct {
	ID       string   `json:"id"`
	Title    string   `json:"title"`
	TopicIDs []string `json:"topicIds,omitempty"`
}

type desktopProjectFile struct {
	GlobalTitle            string           `json:"globalTitle,omitempty"`
	GlobalColor            string           `json:"globalColor,omitempty"`
	GlobalTopics           []string         `json:"globalTopics,omitempty"`
	GlobalPinnedTopics     []string         `json:"globalPinnedTopics,omitempty"`
	GlobalManualTopicOrder bool             `json:"globalManualTopicOrder,omitempty"`
	GlobalGroups           []desktopGroup   `json:"globalGroups,omitempty"`
	DeletedTopics          []string         `json:"deletedTopics,omitempty"`
	PinnedProjects         []string         `json:"pinnedProjects,omitempty"`
	SidebarOrder           []string         `json:"sidebarOrder,omitempty"`
	Projects               []desktopProject `json:"projects"`
}

func normalizeGroups(groups []desktopGroup) []desktopGroup {
	out := make([]desktopGroup, 0, len(groups))
	seenGroups := make(map[string]bool, len(groups))
	seenTopics := make(map[string]bool)
	for _, group := range groups {
		group.ID = strings.TrimSpace(group.ID)
		group.Title = strings.TrimSpace(group.Title)
		if group.ID == "" || seenGroups[group.ID] {
			continue
		}
		seenGroups[group.ID] = true
		topics := make([]string, 0, len(group.TopicIDs))
		for _, topicID := range group.TopicIDs {
			topicID = strings.TrimSpace(topicID)
			if topicID == "" || seenTopics[topicID] {
				continue
			}
			seenTopics[topicID] = true
			topics = append(topics, topicID)
		}
		group.TopicIDs = topics
		out = append(out, group)
	}
	return out
}

func mergeDesktopGroups(left, right []desktopGroup) []desktopGroup {
	merged := append(append([]desktopGroup(nil), left...), right...)
	byID := make(map[string]int, len(merged))
	out := make([]desktopGroup, 0, len(merged))
	for _, group := range merged {
		group.ID = strings.TrimSpace(group.ID)
		if group.ID == "" {
			continue
		}
		if index, ok := byID[group.ID]; ok {
			if out[index].Title == "" {
				out[index].Title = strings.TrimSpace(group.Title)
			}
			out[index].TopicIDs = append(out[index].TopicIDs, group.TopicIDs...)
			continue
		}
		byID[group.ID] = len(out)
		out = append(out, group)
	}
	return normalizeGroups(out)
}

func validateSessionGroups(groups []desktopGroup) error {
	if len(groups) > maxSessionGroups {
		return fmt.Errorf("too many session groups: %d", len(groups))
	}
	seenGroups := make(map[string]bool, len(groups))
	seenTopics := make(map[string]bool)
	for _, group := range groups {
		id, title := strings.TrimSpace(group.ID), strings.TrimSpace(group.Title)
		if id == "" || title == "" {
			return fmt.Errorf("session group id and title are required")
		}
		if utf8.RuneCountInString(id) > maxSessionGroupIDRunes || utf8.RuneCountInString(title) > maxSessionGroupRunes {
			return fmt.Errorf("session group id or title is too long")
		}
		if seenGroups[id] {
			return fmt.Errorf("duplicate session group %q", id)
		}
		seenGroups[id] = true
		if len(group.TopicIDs) > maxSessionGroupTopics {
			return fmt.Errorf("session group %q has too many topics", id)
		}
		for _, topicID := range group.TopicIDs {
			topicID = strings.TrimSpace(topicID)
			if topicID == "" || seenTopics[topicID] {
				return fmt.Errorf("invalid or duplicate grouped topic %q", topicID)
			}
			seenTopics[topicID] = true
		}
	}
	return nil
}

func completeTopicOrder(orderedTopicIDs, previous []string) ([]string, error) {
	available := make(map[string]bool, len(previous))
	for _, id := range previous {
		available[id] = true
	}
	seen := make(map[string]bool, len(orderedTopicIDs))
	next := make([]string, 0, len(previous))
	for _, id := range orderedTopicIDs {
		id = strings.TrimSpace(id)
		if id == "" || !available[id] {
			return nil, fmt.Errorf("unknown topic %q", id)
		}
		if seen[id] {
			return nil, fmt.Errorf("duplicate topic %q", id)
		}
		seen[id] = true
		next = append(next, id)
	}
	for _, id := range previous {
		if !seen[id] {
			next = append(next, id)
		}
	}
	return next, nil
}

func normalizeOrganizationTarget(scope, workspaceRoot string) (string, string, error) {
	scope = strings.TrimSpace(scope)
	if scope == "global" {
		return scope, "", nil
	}
	if scope != "project" {
		return "", "", fmt.Errorf("unsupported scope %q", scope)
	}
	workspaceRoot = normalizeProjectRoot(workspaceRoot)
	if workspaceRoot == "" {
		return "", "", fmt.Errorf("workspaceRoot is required for project scope")
	}
	return scope, workspaceRoot, nil
}

// ReorderTopics persists a manual order without dropping topics omitted by a
// partially loaded client. The manual flag preserves activity sorting for
// existing users until they explicitly drag a topic.
func (a *App) ReorderTopics(scope, workspaceRoot string, orderedTopicIDs []string) error {
	scope, workspaceRoot, err := normalizeOrganizationTarget(scope, workspaceRoot)
	if err != nil {
		return err
	}
	if len(orderedTopicIDs) == 0 {
		return fmt.Errorf("orderedTopicIDs is required")
	}
	if err := updateProjectsFile(func(f *desktopProjectFile) (bool, error) {
		if scope == "global" {
			next, orderErr := completeTopicOrder(orderedTopicIDs, f.GlobalTopics)
			if orderErr != nil {
				return false, orderErr
			}
			changed := !sameStringList(next, f.GlobalTopics) || !f.GlobalManualTopicOrder
			f.GlobalTopics, f.GlobalManualTopicOrder = next, true
			return changed, nil
		}
		i := projectIndexByRoot(f.Projects, workspaceRoot)
		if i < 0 {
			return false, fmt.Errorf("project %q not found", workspaceRoot)
		}
		next, orderErr := completeTopicOrder(orderedTopicIDs, f.Projects[i].Topics)
		if orderErr != nil {
			return false, orderErr
		}
		changed := !sameStringList(next, f.Projects[i].Topics) || !f.Projects[i].ManualTopicOrder
		f.Projects[i].Topics, f.Projects[i].ManualTopicOrder = next, true
		return changed, nil
	}); err != nil {
		return err
	}
	a.emitProjectTreeMetadataChanged()
	return nil
}

func (a *App) ListProjectGroups(scope, workspaceRoot string) ([]desktopGroup, error) {
	scope, workspaceRoot, err := normalizeOrganizationTarget(scope, workspaceRoot)
	if err != nil {
		return nil, err
	}
	f := loadProjectsFile()
	if scope == "global" {
		return normalizeGroups(f.GlobalGroups), nil
	}
	i := projectIndexByRoot(f.Projects, workspaceRoot)
	if i < 0 {
		return nil, nil
	}
	return normalizeGroups(f.Projects[i].Groups), nil
}

func (a *App) SaveSessionGroups(scope, workspaceRoot string, groups []desktopGroup) error {
	scope, workspaceRoot, err := normalizeOrganizationTarget(scope, workspaceRoot)
	if err != nil {
		return err
	}
	if err := validateSessionGroups(groups); err != nil {
		return err
	}
	normalized := normalizeGroups(groups)
	if err := updateProjectsFile(func(f *desktopProjectFile) (bool, error) {
		if scope == "global" {
			if equalGroups(normalized, f.GlobalGroups) {
				return false, nil
			}
			f.GlobalGroups = normalized
			return true, nil
		}
		i := projectIndexByRoot(f.Projects, workspaceRoot)
		if i < 0 {
			return false, fmt.Errorf("project %q not found", workspaceRoot)
		}
		if equalGroups(normalized, f.Projects[i].Groups) {
			return false, nil
		}
		f.Projects[i].Groups = normalized
		return true, nil
	}); err != nil {
		return err
	}
	a.emitProjectTreeMetadataChanged()
	return nil
}

func equalGroups(left, right []desktopGroup) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i].ID != right[i].ID || left[i].Title != right[i].Title || !sameStringList(left[i].TopicIDs, right[i].TopicIDs) {
			return false
		}
	}
	return true
}

func groupsWithoutTopic(groups []desktopGroup, topicID string) ([]desktopGroup, bool) {
	next := make([]desktopGroup, len(groups))
	changed := false
	for i, group := range groups {
		next[i] = group
		next[i].TopicIDs = removeString(group.TopicIDs, topicID)
		changed = changed || !sameStringList(next[i].TopicIDs, group.TopicIDs)
	}
	return next, changed
}
