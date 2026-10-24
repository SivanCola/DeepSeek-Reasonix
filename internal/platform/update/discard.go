package update

import "os"

// DiscardDownload removes what a download the person stopped left behind: every
// partial file, and the cached artifact when the cache holds the same version.
// Partials are all removed because only one release is fetched at a time, so a
// partial under another name is an abandoned attempt as well.
func DiscardDownload(dir, version string) {
	if dir == "" {
		return
	}
	cache := Cache{Dir: dir}
	if rec, err := cache.load(); err == nil && SameVersion(rec.Version, version) {
		_ = os.Remove(rec.Path)
		if rec.SignaturePath != "" {
			_ = os.Remove(rec.SignaturePath)
		}
		_ = os.Remove(cache.metadataPath())
	}
	clearPartials(dir, "")
}
