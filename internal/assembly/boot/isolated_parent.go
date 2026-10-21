package boot

import (
	"context"

	"reasonix/internal/runtime/isolation"
)

type isolatedParent struct {
	store     *isolation.Store
	entry     *isolation.Entry
	source    string
	authorize isolation.SourceAuthorizer
}

func prepareIsolatedEntry(ctx context.Context, parent *isolatedParent, store *isolation.Store, source, resumeID, owner string) (*isolation.Entry, error) {
	if parent != nil {
		if resumeID == "" {
			return parent.store.BeginChild(ctx, parent.entry, store, owner)
		}
		return parent.store.ResumeChild(ctx, parent.entry, store, resumeID, owner)
	}
	if resumeID == "" {
		return store.BeginOwned(ctx, source, owner)
	}
	return store.Resume(ctx, resumeID, owner)
}
