package gitpush

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestPushTransferUsesIndependentObjectsAndShortSourceScopes(t *testing.T) {
	f := newFixture(t)
	plan, err := Preview(t.Context(), f.repo)
	if err != nil {
		t.Fatal(err)
	}
	objects := filepath.Join(f.repo.CommonDir, "objects")
	hidden := filepath.Join(t.TempDir(), "source-objects")
	var acquired, released int
	scope := func(context.Context) (func(), error) {
		acquired++
		if acquired == 2 {
			if f.remoteHead(t) != plan.Head {
				t.Fatal("settlement started before the remote accepted the exact commit")
			}
			if err := os.Rename(hidden, objects); err != nil {
				t.Fatal(err)
			}
		}
		return func() {
			released++
			if released == 1 {
				if err := os.Rename(objects, hidden); err != nil {
					t.Fatal(err)
				}
			}
		}, nil
	}
	t.Cleanup(func() {
		if _, err := os.Stat(hidden); err == nil {
			if err := os.Rename(hidden, objects); err != nil {
				t.Error(err)
			}
		}
	})
	result, err := PushWithScope(t.Context(), f.repo, plan, scope)
	if err != nil || result.TrackingStatus != "updated" {
		t.Fatalf("push depended on source objects during network transfer: %+v %v", result, err)
	}
	if acquired != 2 || released != 2 {
		t.Fatalf("source scopes: acquired=%d released=%d", acquired, released)
	}
}

func TestPushPreservesConcurrentTrackingUpdateAndReportsRemoteSuccess(t *testing.T) {
	for _, refusal := range []string{"changed", "unavailable"} {
		t.Run(refusal, func(t *testing.T) {
			f := newFixture(t)
			plan, err := Preview(t.Context(), f.repo)
			if err != nil {
				t.Fatal(err)
			}
			newHead := run(t, f.work, "commit-tree", "HEAD^{tree}", "-p", "HEAD", "-m", "concurrent fetch")
			var calls int
			scope := func(context.Context) (func(), error) {
				calls++
				if calls == 2 {
					if refusal == "unavailable" {
						return nil, errors.New("source is unavailable")
					}
					run(t, f.work, "update-ref", trackingRef(plan), newHead)
				}
				return func() {}, nil
			}
			result, err := PushWithScope(t.Context(), f.repo, plan, scope)
			if err != nil || result.TrackingStatus != "not_updated" || f.remoteHead(t) != plan.Head {
				t.Fatalf("remote success was lost: %+v %v", result, err)
			}
			if refusal == "changed" && run(t, f.work, "rev-parse", trackingRef(plan)) != newHead {
				t.Fatal("settlement replaced a concurrent tracking update")
			}
		})
	}
}

func TestPushRefusedPreparationDoesNotReachRemote(t *testing.T) {
	f := newFixture(t)
	plan, err := Preview(t.Context(), f.repo)
	if err != nil {
		t.Fatal(err)
	}
	before := f.remoteHead(t)
	_, err = PushWithScope(t.Context(), f.repo, plan, func(context.Context) (func(), error) {
		return nil, context.Canceled
	})
	if !errors.Is(err, context.Canceled) || f.remoteHead(t) != before {
		t.Fatalf("refused preparation reached remote: %v", err)
	}
}
