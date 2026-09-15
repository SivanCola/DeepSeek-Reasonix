package session

import "testing"

func TestQueryStatReturnsHeaderBackedIdentityWithoutReadingHistory(t *testing.T) {
	service, err := NewService("local", NewFilesystemPersistence(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Shutdown(t.Context()) })
	runtime, err := service.Create(t.Context(), CreateOptions{
		SessionID: "header-stat", CWD: "/workspace", Origin: SessionOriginNew,
	})
	if err != nil {
		t.Fatal(err)
	}

	info, err := service.Query().Stat(t.Context(), runtime.Ref())
	if err != nil {
		t.Fatal(err)
	}
	if info.Ref != runtime.Ref() || info.CWD != "/workspace" || info.Origin != SessionOriginNew {
		t.Fatalf("stat = %#v", info)
	}
}
