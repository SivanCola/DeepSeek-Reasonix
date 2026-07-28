package cli

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"reasonix/internal/config"
	"reasonix/internal/i18n"
	"reasonix/internal/netclient"

	"github.com/spf13/pflag"
	"golang.org/x/mod/semver"
)

const (
	ghOwner        = "esengine"
	ghRepo         = "DeepSeek-Reasonix"
	ghAPIReleases  = "https://api.github.com/repos/" + ghOwner + "/" + ghRepo + "/releases?per_page=100"
	ghDownloadBase = "https://github.com/" + ghOwner + "/" + ghRepo + "/releases/download"
	cliGatewayBase = "https://crash.reasonix.io/v1/cli/releases"
	upgradeTimeout = 60 * time.Second
)

// ghRelease is the subset of the GitHub release API response we need.
type ghRelease struct {
	TagName    string `json:"tag_name"`
	Prerelease bool   `json:"prerelease"`
	Assets     []ghAsset
}

// ghAsset is a single release asset.
type ghAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Size               int64  `json:"size"`
}

type cliReleaseChannel string

const (
	cliReleaseStable  cliReleaseChannel = "stable"
	cliReleasePreview cliReleaseChannel = "preview"
)

var (
	stableCLITagPattern  = regexp.MustCompile(`^v(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)$`)
	previewCLITagPattern = regexp.MustCompile(`^v(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)-preview\.(?:0|[1-9][0-9]*)$`)
)

func parseCLIReleaseChannel(value string) (cliReleaseChannel, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", string(cliReleaseStable):
		return cliReleaseStable, nil
	case string(cliReleasePreview):
		return cliReleasePreview, nil
	default:
		return "", fmt.Errorf("release channel %q: must be stable or preview", value)
	}
}

type cliUpgradeSyntax struct {
	checkOnly   bool
	force       bool
	positional  *cliReleaseChannel
	flagChannel *cliReleaseChannel
}

// parseCLIUpgradeSyntax accepts the ergonomic positional channel while keeping
// --channel available for scripts. pflag's interspersed parsing allows both
// `upgrade preview --check` and `upgrade --check preview`.
func parseCLIUpgradeSyntax(args []string) (cliUpgradeSyntax, error) {
	fs := pflag.NewFlagSet("upgrade", pflag.ContinueOnError)
	fs.SetInterspersed(true)
	fs.SetOutput(io.Discard)
	checkOnly := fs.Bool("check", false, "check for updates without installing")
	force := fs.Bool("force", false, "reinstall even if already on the latest version")
	channelValue := fs.String("channel", "", "release channel: stable or preview")
	if err := fs.Parse(args); err != nil {
		return cliUpgradeSyntax{}, err
	}

	var positional *cliReleaseChannel
	if rest := fs.Args(); len(rest) > 1 {
		return cliUpgradeSyntax{}, fmt.Errorf("upgrade accepts at most one positional channel (stable or preview)")
	} else if len(rest) == 1 {
		channel, err := parseCLIReleaseChannel(rest[0])
		if err != nil || strings.TrimSpace(rest[0]) == "" {
			if err == nil {
				err = fmt.Errorf("channel is required")
			}
			return cliUpgradeSyntax{}, err
		}
		positional = &channel
	}

	var flagChannel *cliReleaseChannel
	if fs.Changed("channel") {
		channel, err := parseCLIReleaseChannel(*channelValue)
		if err != nil {
			return cliUpgradeSyntax{}, err
		}
		flagChannel = &channel
	}
	if positional != nil && flagChannel != nil && *positional != *flagChannel {
		return cliUpgradeSyntax{}, fmt.Errorf("conflicting release channels: positional %q and --channel %q", *positional, *flagChannel)
	}
	return cliUpgradeSyntax{
		checkOnly:   *checkOnly,
		force:       *force,
		positional:  positional,
		flagChannel: flagChannel,
	}, nil
}

func resolveCLIUpgradeChannel(syntax cliUpgradeSyntax, configured string) (cliReleaseChannel, bool, error) {
	configuredChannel, err := parseCLIReleaseChannel(config.NormalizeCLIUpdateChannel(configured))
	if err != nil {
		return "", false, err
	}
	if syntax.positional != nil {
		return *syntax.positional, *syntax.positional != configuredChannel, nil
	}
	if syntax.flagChannel != nil {
		return *syntax.flagChannel, false, nil
	}
	return configuredChannel, false, nil
}

var persistCLIReleaseChannel = func(channel cliReleaseChannel) error {
	path := config.UserConfigPath()
	unlock, err := config.LockConfigFileEdits(path)
	if err != nil {
		return err
	}
	defer unlock()
	cfg, err := config.LoadForEditReadOnlyStrict(path)
	if err != nil {
		return err
	}
	if err := cfg.SetCLIUpdateChannel(string(channel)); err != nil {
		return err
	}
	return cfg.SaveTo(path)
}

// upgradeCommand handles `reasonix upgrade` (and `reasonix update`).
func upgradeCommand(args []string, version string) int {
	syntax, err := parseCLIUpgradeSyntax(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, i18n.M.ErrorPrefix, err)
		return 2
	}

	// 1. Normalize running version.
	cur, ok := normalizeVersion(version)
	if !ok {
		fmt.Fprintf(os.Stderr, "%s %s\n", i18n.M.ErrorPrefix, i18n.M.UpgradeDevBuild)
		return 1
	}

	// 2. Build HTTP client using configured proxy.
	cfg, _ := config.Load()
	selectedChannel, persistChannel, err := resolveCLIUpgradeChannel(syntax, cfg.CLIUpdateChannel())
	if err != nil {
		fmt.Fprintln(os.Stderr, i18n.M.ErrorPrefix, err)
		return 2
	}
	if persistChannel {
		if err := persistCLIReleaseChannel(selectedChannel); err != nil {
			fmt.Fprintf(os.Stderr, "%s cannot save CLI update channel: %v\n", i18n.M.ErrorPrefix, err)
			return 1
		}
	}
	spec := cfg.NetworkProxySpec()
	c, err := netclient.NewHTTPClient(spec, netclient.TransportOptions{
		ResponseHeaderTimeout: upgradeTimeout,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s %v\n", i18n.M.ErrorPrefix, err)
		return 1
	}

	// 3. Fetch latest release from GitHub API.
	fmt.Printf("%s [%s]\n", i18n.M.UpgradeChecking, selectedChannel)
	rel, err := fetchLatestRelease(c, selectedChannel)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s "+i18n.M.UpgradeFetchFailed+"\n", i18n.M.ErrorPrefix, err)
		return 1
	}

	// 4. Compare versions.
	latest := rel.TagName
	if !strings.HasPrefix(latest, "v") {
		latest = "v" + latest
	}
	if !semver.IsValid(latest) {
		fmt.Fprintf(os.Stderr, "%s "+i18n.M.UpgradeInvalidVersion+"\n", i18n.M.ErrorPrefix, latest)
		return 1
	}
	sameChannel := versionBelongsToCLIChannel(cur, selectedChannel)
	if latest == cur {
		if syntax.force {
			fmt.Println(i18n.M.UpgradeForcing)
		} else {
			fmt.Println(i18n.M.UpgradeAlreadyLatest)
			return 0
		}
	} else if !sameChannel || semver.Compare(latest, cur) > 0 {
		fmt.Printf(i18n.M.UpgradeAvailableFmt+"\n", cur, latest)
	} else if syntax.force {
		fmt.Println(i18n.M.UpgradeForcing)
	} else {
		fmt.Println(i18n.M.UpgradeAlreadyLatest)
		return 0
	}

	if syntax.checkOnly {
		return 0
	}

	// 5. Find the asset for the current platform.
	base := fmt.Sprintf("reasonix-%s-%s", runtime.GOOS, runtime.GOARCH)
	var asset *ghAsset
	for i := range rel.Assets {
		if strings.HasPrefix(rel.Assets[i].Name, base) {
			asset = &rel.Assets[i]
			break
		}
	}
	if asset == nil {
		fmt.Fprintf(os.Stderr, "%s "+i18n.M.UpgradeNoAssetFmt+"\n", i18n.M.ErrorPrefix, base)
		return 1
	}

	// 6. Find the checksum URL.
	checksumURL := fmt.Sprintf("%s/%s/SHA256SUMS", ghDownloadBase, rel.TagName)

	// 7. Download archive.
	fmt.Printf(i18n.M.UpgradeDownloadingFmt+"\n", asset.Name, humanSize(asset.Size))
	archiveData, err := fetchBytes(c, asset.BrowserDownloadURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s "+i18n.M.UpgradeDownloadFailed+"\n", i18n.M.ErrorPrefix, err)
		return 1
	}

	// 8. Verify SHA256 checksum — fail closed: abort on any verification error.
	fmt.Println(i18n.M.UpgradeVerifying)
	checksumData, err := fetchBytes(c, checksumURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s "+i18n.M.UpgradeChecksumFailed+"\n", i18n.M.ErrorPrefix, err)
		return 1
	}
	if err := verifyChecksum(archiveData, asset.Name, checksumData); err != nil {
		fmt.Fprintf(os.Stderr, "%s %v\n", i18n.M.ErrorPrefix, err)
		return 1
	}

	// 9. Extract binary from archive.
	binName := "reasonix"
	if runtime.GOOS == "windows" {
		binName = "reasonix.exe"
	}
	binary, err := extractBinary(archiveData, asset.Name, binName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s "+i18n.M.UpgradeExtractFailed+"\n", i18n.M.ErrorPrefix, err)
		return 1
	}

	// 10. Replace the running binary.
	fmt.Println(i18n.M.UpgradeApplying)
	if err := replaceBinary(binary); err != nil {
		fmt.Fprintf(os.Stderr, "%s "+i18n.M.UpgradeApplyFailed+"\n", i18n.M.ErrorPrefix, err)
		return 1
	}

	fmt.Println(upgradeSuccessMessage(cur, latest))
	return 0
}

func upgradeSuccessMessage(cur, latest string) string {
	return fmt.Sprintf(i18n.M.UpgradeSuccessFmt, cur, latest)
}

// normalizeVersion returns v as valid semver ("vX.Y.Z") or ok=false for dev.
func normalizeVersion(v string) (string, bool) {
	v = strings.TrimSpace(v)
	if v == "" || v == "dev" {
		return "", false
	}
	if !strings.HasPrefix(v, "v") {
		v = "v" + v
	}
	if !semver.IsValid(v) {
		return "", false
	}
	return semver.Canonical(v), true
}

// isCLITag reports whether a tag belongs to the CLI release namespace (v*).
// Tags like "desktop-v1.5.0" or "npm-v1.4.0" are excluded.
func isCLITag(tag string) bool {
	tag = strings.TrimSpace(tag)
	return len(tag) >= 2 && tag[0] == 'v' && tag[1] >= '0' && tag[1] <= '9'
}

func versionBelongsToCLIChannel(version string, channel cliReleaseChannel) bool {
	switch channel {
	case cliReleasePreview:
		return previewCLITagPattern.MatchString(version)
	default:
		return stableCLITagPattern.MatchString(version)
	}
}

func releaseBelongsToCLIChannel(rel ghRelease, channel cliReleaseChannel) bool {
	if !isCLITag(rel.TagName) || !versionBelongsToCLIChannel(rel.TagName, channel) {
		return false
	}
	return rel.Prerelease == (channel == cliReleasePreview)
}

// pickCLIRelease selects the highest strict tag in the requested public channel.
// Generic prereleases such as RCs remain internal and can never leak into Stable
// or masquerade as Preview.
func pickCLIRelease(rels []ghRelease, channel cliReleaseChannel) *ghRelease {
	best := -1
	for i := range rels {
		if !releaseBelongsToCLIChannel(rels[i], channel) {
			continue
		}
		if best == -1 || semver.Compare(rels[i].TagName, rels[best].TagName) > 0 {
			best = i
		}
	}
	if best == -1 {
		return nil
	}
	return &rels[best]
}

// fetchLatestRelease queries the GitHub Releases API and returns the newest
// strict CLI release in the selected public channel.
func fetchLatestRelease(c *http.Client, channel cliReleaseChannel) (*ghRelease, error) {
	pointerURL := fmt.Sprintf("%s/%s/latest.json", cliGatewayBase, channel)
	pointerRelease, pointerErr := fetchCLIReleasePointer(c, pointerURL, channel)
	if pointerErr == nil {
		return pointerRelease, nil
	}

	req, err := http.NewRequest("GET", ghAPIReleases, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "reasonix-cli")

	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("release gateway: %v; GitHub API: %s", pointerErr, resp.Status)
	}

	var rels []ghRelease
	if err := json.NewDecoder(resp.Body).Decode(&rels); err != nil {
		return nil, err
	}

	if rel := pickCLIRelease(rels, channel); rel != nil {
		return rel, nil
	}
	return nil, fmt.Errorf("release gateway: %v; no %s CLI release found in recent GitHub releases", pointerErr, channel)
}

func fetchCLIReleasePointer(c *http.Client, pointerURL string, channel cliReleaseChannel) (*ghRelease, error) {
	req, err := http.NewRequest("GET", pointerURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "reasonix-cli")

	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s", resp.Status)
	}

	var rel ghRelease
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return nil, err
	}
	if !releaseBelongsToCLIChannel(rel, channel) {
		return nil, fmt.Errorf("pointer tag %q does not belong to %s", rel.TagName, channel)
	}
	return &rel, nil
}

// fetchBytes GETs a URL fully into memory.
func fetchBytes(c *http.Client, url string) ([]byte, error) {
	resp, err := c.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	return io.ReadAll(resp.Body)
}

// verifyChecksum checks that data's SHA256 matches the entry for fileName in
// the SHA256SUMS-format checksum file.
func verifyChecksum(data []byte, fileName string, checksumFile []byte) error {
	sum := sha256.Sum256(data)
	got := hex.EncodeToString(sum[:])

	for _, line := range strings.Split(strings.TrimSpace(string(checksumFile)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) >= 2 && parts[1] == fileName {
			if !strings.EqualFold(parts[0], got) {
				return fmt.Errorf(i18n.M.UpgradeChecksumMismatchFmt, got, parts[0])
			}
			return nil
		}
	}
	return fmt.Errorf(i18n.M.UpgradeChecksumNotFoundFmt, fileName)
}

// extractBinary pulls the "reasonix" binary from a .tar.gz or .zip archive.
func extractBinary(data []byte, archiveName, binaryName string) ([]byte, error) {
	if strings.HasSuffix(archiveName, ".zip") {
		return extractFromZip(data, binaryName)
	}
	return extractFromTarGz(data, binaryName)
}

// extractFromTarGz extracts a named binary from a .tar.gz archive.
func extractFromTarGz(data []byte, name string) ([]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if h.Typeflag == tar.TypeReg && (h.Name == name || strings.HasSuffix(h.Name, "/"+name)) {
			return io.ReadAll(tr)
		}
	}
	return nil, fmt.Errorf("%q not found in archive", name)
}

// extractFromZip extracts a named binary from a .zip archive (Windows).
func extractFromZip(data []byte, name string) ([]byte, error) {
	r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, err
	}
	for _, f := range r.File {
		if f.FileInfo().IsDir() {
			continue
		}
		base := filepath.Base(f.Name)
		if base == name {
			rc, err := f.Open()
			if err != nil {
				return nil, err
			}
			defer rc.Close()
			return io.ReadAll(rc)
		}
	}
	return nil, fmt.Errorf("%q not found in zip archive", name)
}

// replaceBinary writes newBin to the running executable's path atomically.
//
// On Unix this is a simple temp-file + rename. On Windows the running
// executable is memory-mapped and cannot be overwritten directly, so we
// rename it aside to .reasonix.old first, then place the new binary.
// The .old file is cleaned up best-effort (Windows may still hold a lock
// on it; we hide it in that case).
func replaceBinary(newBin []byte) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate executable: %w", err)
	}
	resolved, err := resolveSymlinks(exe)
	if err != nil {
		return fmt.Errorf("resolve symlinks: %w", err)
	}

	dir := filepath.Dir(resolved)
	base := filepath.Base(resolved)
	tmpPath := filepath.Join(dir, fmt.Sprintf(".%s.new", base))

	// Write new binary to .new temp file.
	if err := os.WriteFile(tmpPath, newBin, 0o755); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("write temp: %w", err)
	}

	if runtime.GOOS == "windows" {
		return commitWindows(resolved, tmpPath, base, dir)
	}

	// Unix: atomic rename .new → target.
	if err := os.Rename(tmpPath, resolved); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("rename: %w", err)
	}
	return nil
}

// commitWindows performs the two-phase rename on Windows:
//  1. Rename running exe → .old (allowed while running)
//  2. Rename .new → target
//  3. Best-effort remove .old (hide if still locked)
func commitWindows(target, newPath, base, dir string) error {
	oldPath := filepath.Join(dir, fmt.Sprintf(".%s.old", base))

	// Remove any leftover .old from a previous update.
	_ = os.Remove(oldPath)

	// Move the running executable aside.
	if err := os.Rename(target, oldPath); err != nil {
		os.Remove(newPath)
		return fmt.Errorf("rename running exe aside: %w", err)
	}

	// Move the new binary into place.
	if err := os.Rename(newPath, target); err != nil {
		// Rollback: try to restore the old binary.
		if rerr := os.Rename(oldPath, target); rerr != nil {
			return fmt.Errorf("replace failed (%v); rollback also failed: %w", err, rerr)
		}
		return fmt.Errorf("rename new binary: %w", err)
	}

	// Best-effort cleanup of the old binary.
	if err := os.Remove(oldPath); err != nil {
		// Windows may hold a lock; hide the file so it doesn't clutter the dir.
		hideFileWindows(oldPath)
	}
	return nil
}

// resolveSymlinks follows symlinks; falls back to the original path on error.
func resolveSymlinks(p string) (string, error) {
	r, err := filepath.EvalSymlinks(p)
	if err != nil {
		return p, nil
	}
	return r, nil
}

// humanSize returns a human-readable byte size.
func humanSize(b int64) string {
	const (
		_KiB = 1024
		_MiB = 1024 * _KiB
	)
	switch {
	case b >= _MiB:
		return fmt.Sprintf("%.1f MiB", float64(b)/float64(_MiB))
	case b >= _KiB:
		return fmt.Sprintf("%.1f KiB", float64(b)/float64(_KiB))
	default:
		return fmt.Sprintf("%d B", b)
	}
}
