package session

import (
	"context"
	"errors"
)

// CloseAll releases every unbound runtime owned by this service. Long-lived
// hosts normally rely on idle retention; process shutdown and isolated tests
// use this explicit boundary to release writer leases deterministically.
func (s *Service) CloseAll(ctx context.Context) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	runtimes := make([]*Runtime, 0, len(s.active))
	for _, runtime := range s.active {
		runtimes = append(runtimes, runtime)
	}
	s.mu.Unlock()
	var closeErr error
	for _, runtime := range runtimes {
		closeErr = errors.Join(closeErr, s.closeOwned(ctx, runtime, ""))
	}
	return closeErr
}

// Shutdown closes execution ownership and the query projection workers that
// share this service's persistence root. CloseAll intentionally remains the
// runtime-only primitive; hosts and short-lived import services must use this
// lifecycle boundary before releasing or removing the root directory.
func (s *Service) Shutdown(ctx context.Context) error {
	if s == nil {
		return nil
	}
	err := s.CloseAll(ctx)
	s.query.Close()
	return err
}
