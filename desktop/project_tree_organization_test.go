package main

import (
	"reflect"
	"testing"
)

func TestCompleteTopicOrderPreservesTopicsMissingFromPartialClient(t *testing.T) {
	got, err := completeTopicOrder([]string{"c", "a"}, []string{"a", "b", "c"})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"c", "a", "b"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
	if _, err := completeTopicOrder([]string{"a", "a"}, []string{"a", "b"}); err == nil {
		t.Fatal("duplicate topic order must fail")
	}
	if _, err := completeTopicOrder([]string{"unknown"}, []string{"a", "b"}); err == nil {
		t.Fatal("unknown topic must not be persisted")
	}
}

func TestReorderTopicsEnablesManualOrderOnlyAfterExplicitDrag(t *testing.T) {
	isolateDesktopUserDirs(t)
	root := t.TempDir()
	if err := addProject(root, "Project"); err != nil {
		t.Fatal(err)
	}
	if err := updateProjectsFile(func(f *desktopProjectFile) (bool, error) {
		i := projectIndexByRoot(f.Projects, root)
		f.Projects[i].Topics = []string{"a", "b", "c"}
		return true, nil
	}); err != nil {
		t.Fatal(err)
	}
	app := NewApp()
	for _, topic := range app.metadataProjectTopics("project", root) {
		if topic.SortOrder != -1 {
			t.Fatalf("preference-free sortOrder = %d, want -1", topic.SortOrder)
		}
	}
	if err := app.ReorderTopics("project", root, []string{"c", "a"}); err != nil {
		t.Fatal(err)
	}
	project := loadProjectsFile().Projects[projectIndexByRoot(loadProjectsFile().Projects, root)]
	if want := []string{"c", "a", "b"}; !reflect.DeepEqual(project.Topics, want) {
		t.Fatalf("topics = %v, want %v", project.Topics, want)
	}
	if !project.ManualTopicOrder {
		t.Fatal("manual topic order flag was not persisted")
	}
	for index, topic := range app.metadataProjectTopics("project", root) {
		if topic.SortOrder != index {
			t.Fatalf("topic %q sortOrder = %d, want %d", topic.TopicID, topic.SortOrder, index)
		}
	}
}

func TestSessionGroupsPersistExclusiveMembership(t *testing.T) {
	isolateDesktopUserDirs(t)
	root := t.TempDir()
	if err := addProject(root, "Project"); err != nil {
		t.Fatal(err)
	}
	app := NewApp()
	groups := []desktopGroup{
		{ID: "one", Title: "One", TopicIDs: []string{"a", "b"}},
		{ID: "two", Title: "Two", TopicIDs: []string{"c"}},
	}
	if err := app.SaveSessionGroups("project", root, groups); err != nil {
		t.Fatal(err)
	}
	got, err := app.ListProjectGroups("project", root)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, groups) {
		t.Fatalf("groups = %#v, want %#v", got, groups)
	}
	if err := removeTopicFromProjectsFile("b"); err != nil {
		t.Fatal(err)
	}
	got, err = app.ListProjectGroups("project", root)
	if err != nil || len(got) != 2 || !reflect.DeepEqual(got[0].TopicIDs, []string{"a"}) {
		t.Fatalf("groups after topic deletion = %#v, err=%v", got, err)
	}
	if err := app.SaveSessionGroups("project", root, []desktopGroup{
		{ID: "one", Title: "One", TopicIDs: []string{"same"}},
		{ID: "two", Title: "Two", TopicIDs: []string{"same"}},
	}); err == nil {
		t.Fatal("a topic cannot belong to multiple groups")
	}
	if _, err := app.ListProjectGroups("other", root); err == nil {
		t.Fatal("unsupported scope must fail")
	}
}
