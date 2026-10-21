//go:build linux

package checkenv

import (
	"errors"
	"path/filepath"

	"reasonix/internal/base/fileutil"
)

func relativeDenies(source string, denied []string) ([]string, error) {
	var result []string
	for _, deny := range denied {
		canonical, err := fileutil.CanonicalWritePath(deny)
		if err != nil {
			return nil, err
		}
		if rel, err := filepath.Rel(canonical, source); err == nil && filepath.IsLocal(rel) {
			return nil, errors.New("verification input is inside a forbidden read root")
		}
		if rel, err := filepath.Rel(source, canonical); err == nil && filepath.IsLocal(rel) {
			result = append(result, filepath.ToSlash(rel))
		}
	}
	return result, nil
}
