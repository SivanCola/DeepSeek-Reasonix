package workspacestate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"reasonix/internal/pathidentity"
)

func (s *Store) EnsureWorkspace(ctx context.Context, workspace Workspace) error {
	_, err := s.EnsureWorkspaceResolved(ctx, workspace)
	return err
}

// ResolveWorkspaceID returns the persisted owner of root's physical directory.
// Path-based projections must use this owner instead of deriving a fresh ID
// from a newer path spelling or identity scheme.
func ResolveWorkspaceID(state State, root string) (string, bool, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return "", false, nil
	}
	identity, err := pathidentity.Resolve(root, pathidentity.Options{FollowLeaf: true})
	if err != nil {
		return "", false, fmt.Errorf("resolve workspace root: %w", err)
	}
	matches, err := matchingWorkspaceIDs(state, identity)
	if err != nil {
		return "", false, err
	}
	if len(matches) == 0 {
		return "", false, nil
	}
	return matches[0], true, nil
}

func matchingWorkspaceIDs(state State, candidate pathidentity.Identity) ([]string, error) {
	matches := make([]string, 0, 1)
	if candidate.Key == "" {
		return matches, nil
	}
	for id, existing := range state.Workspaces {
		if strings.TrimSpace(existing.Root) == "" {
			continue
		}
		identity, err := pathidentity.Resolve(existing.Root, pathidentity.Options{FollowLeaf: true})
		if err != nil {
			return nil, fmt.Errorf("resolve persisted workspace %q: %w", id, err)
		}
		if identity.Key == candidate.Key {
			matches = append(matches, id)
		}
	}
	slices.Sort(matches)
	if len(matches) > 1 {
		return nil, fmt.Errorf("%w: %s", ErrAmbiguousIdentity, strings.Join(matches, ", "))
	}
	return matches, nil
}

// EnsureWorkspaceResolved registers workspace or returns the authoritative ID
// of an existing workspace with the same physical directory identity.
func (s *Store) EnsureWorkspaceResolved(ctx context.Context, workspace Workspace) (string, error) {
	workspace.ID = strings.TrimSpace(workspace.ID)
	if workspace.ID == "" {
		return "", errors.New("workspace id is required")
	}
	resolvedID := workspace.ID
	err := s.mutate(ctx, func(state *State) error {
		var candidate pathidentity.Identity
		var resolveErr error
		if strings.TrimSpace(workspace.Root) != "" {
			candidate, resolveErr = pathidentity.Resolve(workspace.Root, pathidentity.Options{FollowLeaf: true})
			if resolveErr != nil {
				return fmt.Errorf("resolve workspace root: %w", resolveErr)
			}
		}
		revalidateCandidate := func() error {
			if candidate.Key == "" {
				return nil
			}
			latest, latestErr := pathidentity.Resolve(workspace.Root, pathidentity.Options{FollowLeaf: true})
			if latestErr != nil {
				return fmt.Errorf("revalidate workspace root: %w", latestErr)
			}
			if latest.Key != candidate.Key {
				return fmt.Errorf("%w: workspace root identity changed during registration", ErrMutationConflict)
			}
			return nil
		}
		if candidate.Key != "" {
			matches, matchErr := matchingWorkspaceIDs(*state, candidate)
			if matchErr != nil {
				return matchErr
			}
			if len(matches) == 1 {
				if err := revalidateCandidate(); err != nil {
					return err
				}
				resolvedID = matches[0]
				return nil
			}
		}
		now := time.Now().UTC()
		current, exists := state.Workspaces[workspace.ID]
		if exists {
			if current.Root == workspace.Root || (current.Root == "" && workspace.Root == "") {
				return revalidateCandidate()
			}
			if workspace.ID == GlobalWorkspaceID || candidate.Key == "" {
				return ErrMutationConflict
			}
			resolvedID = versionedWorkspaceID(candidate.Key)
			if fallback, fallbackExists := state.Workspaces[resolvedID]; fallbackExists {
				identity, resolveErr := pathidentity.Resolve(fallback.Root, pathidentity.Options{FollowLeaf: true})
				if resolveErr != nil {
					return fmt.Errorf("resolve collided workspace %q: %w", resolvedID, resolveErr)
				}
				if identity.Key != candidate.Key {
					return ErrMutationConflict
				}
				if err := revalidateCandidate(); err != nil {
					return err
				}
				return nil
			}
		}
		if err := revalidateCandidate(); err != nil {
			return err
		}
		workspace.ID = resolvedID
		workspace.SessionIDs = []string{}
		workspace.CreatedAt, workspace.UpdatedAt = now, now
		state.Workspaces[workspace.ID] = workspace
		state.WorkspaceIDs = append(state.WorkspaceIDs, workspace.ID)
		return nil
	})
	return resolvedID, err
}

func versionedWorkspaceID(identityKey string) string {
	sum := sha256.Sum256([]byte("reasonix-workspace-pathidentity-v2\x00" + identityKey))
	return "project-v2-" + hex.EncodeToString(sum[:12])
}
