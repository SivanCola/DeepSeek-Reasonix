package serve

import (
	"encoding/json"
	"os"

	"reasonix/internal/base/fileutil"
	"reasonix/internal/state/sessionstore"
	"reasonix/internal/state/usagereport"
)

type teamViewCache struct {
	previews fileutil.ReadCache[string]
	usage    fileutil.ReadCache[usagereport.Report]
}

func (t *hubTeam) preview(path string) string {
	v, _ := t.viewCache.previews.Load(path, func(path string) (string, error) {
		return sessionstore.LatestPreview(path), nil
	})
	return v
}

func (t *hubTeam) usage(path string) (usagereport.Report, bool) {
	v, err := t.viewCache.usage.Load(usagereport.Path(path), func(path string) (usagereport.Report, error) {
		var report usagereport.Report
		data, err := os.ReadFile(path)
		if err == nil {
			err = json.Unmarshal(data, &report)
		}
		return report, err
	})
	return v, err == nil
}
