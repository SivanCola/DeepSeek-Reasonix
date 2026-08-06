package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"reasonix/desktop/internal/browseripc"
	"reasonix/desktop/internal/update"
	"reasonix/internal/config"
	"reasonix/internal/fileutil"
)

var browserComponentInstallMu sync.Mutex

const (
	maxBrowserComponentArchiveBytes = int64(512 << 20)
	maxBrowserComponentExtractBytes = int64(1536 << 20)
	maxBrowserComponentFiles        = 20_000
)

type browserComponentMetadata struct {
	Format          string `json:"format"`
	Version         string `json:"version"`
	ElectronVersion string `json:"electronVersion"`
	ProtocolVersion int    `json:"protocolVersion"`
}

func (a *App) installOrRepairBrowserComponent(ctx context.Context) error {
	browserComponentInstallMu.Lock()
	defer browserComponentInstallMu.Unlock()
	selected := configuredUpdateChannel()
	c, err := httpClient()
	if err != nil {
		return err
	}
	v4, _ := httpClientIPv4()
	manifest, err := fetchManifest(ctx, c, v4, selected)
	if err != nil {
		return fmt.Errorf("load signed browser component manifest: %w", err)
	}
	asset, ok := manifest.BrowserComponent()
	if !ok {
		return fmt.Errorf("browser component is not published for %s", update.CurrentPlatform())
	}
	if asset.Size <= 0 || asset.Size > maxBrowserComponentArchiveBytes {
		return fmt.Errorf("browser component archive size %d is invalid", asset.Size)
	}
	data, err := downloadForChannel(ctx, c, v4, selected, asset.URL, asset.Size, nil)
	if err != nil {
		return fmt.Errorf("download browser component: %w", err)
	}
	sig, err := fetchBytesFallbackForChannelSized(ctx, c, v4, selected, asset.Sig, maxDesktopSignatureSize)
	if err != nil {
		return fmt.Errorf("download browser component signature: %w", err)
	}
	if err := update.Verify(data, sig); err != nil {
		return fmt.Errorf("verify browser component signature: %w", err)
	}
	if err := checkSHA256(data, asset.SHA256); err != nil {
		return fmt.Errorf("verify browser component digest: %w", err)
	}
	if err := installBrowserComponentArchive(data, asset.URL, config.ReasonixHomeDir(), runtime.GOOS); err != nil {
		return err
	}
	if a.browser != nil {
		a.browser.ResetRecovery()
	}
	return nil
}

func installBrowserComponentArchive(data []byte, archiveName, home, goos string) error {
	componentDir := filepath.Join(home, browserComponentDirName)
	if err := os.MkdirAll(componentDir, 0o755); err != nil {
		return err
	}
	stage, err := os.MkdirTemp(componentDir, ".install-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	if strings.HasSuffix(strings.ToLower(archiveName), ".zip") {
		err = extractBrowserComponentZIP(data, stage)
	} else if strings.HasSuffix(strings.ToLower(archiveName), ".tar.gz") || strings.HasSuffix(strings.ToLower(archiveName), ".tgz") {
		err = extractBrowserComponentTarGZ(data, stage)
	} else {
		return fmt.Errorf("unsupported browser component archive %q", filepath.Base(archiveName))
	}
	if err != nil {
		return fmt.Errorf("extract browser component: %w", err)
	}
	var current struct {
		Version string `json:"version"`
	}
	currentRaw, err := os.ReadFile(filepath.Join(stage, browserCurrentManifest))
	if err != nil || json.Unmarshal(currentRaw, &current) != nil || !validComponentVersion(current.Version) {
		return fmt.Errorf("browser component has an invalid current manifest")
	}
	versionDir := filepath.Join(stage, current.Version)
	var metadata browserComponentMetadata
	metaRaw, err := os.ReadFile(filepath.Join(versionDir, "component.json"))
	if err != nil || json.Unmarshal(metaRaw, &metadata) != nil ||
		metadata.Format != "reasonix.browser.component.v1" || metadata.Version != current.Version ||
		!validComponentVersion(metadata.ElectronVersion) || metadata.ProtocolVersion != browseripc.ProtocolVersion {
		return fmt.Errorf("browser component metadata is incompatible")
	}
	binary := filepath.Join(versionDir, browserComponentBinaryDir, browserComponentBinaryNameFor(goos))
	if st, err := os.Stat(binary); err != nil || st.IsDir() {
		return fmt.Errorf("browser component executable is missing")
	}

	target := filepath.Join(componentDir, current.Version)
	backup := target + fmt.Sprintf(".repair-%d", time.Now().UnixNano())
	hadTarget := false
	if _, err := os.Stat(target); err == nil {
		if err := os.Rename(target, backup); err != nil {
			return fmt.Errorf("stage existing browser component: %w", err)
		}
		hadTarget = true
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.Rename(versionDir, target); err != nil {
		if hadTarget {
			_ = os.Rename(backup, target)
		}
		return fmt.Errorf("install browser component: %w", err)
	}
	manifestData, _ := json.MarshalIndent(current, "", "  ")
	tmpManifest := filepath.Join(componentDir, browserCurrentManifest+".tmp")
	if err := os.WriteFile(tmpManifest, append(manifestData, '\n'), 0o644); err != nil {
		rollbackBrowserComponent(target, backup, hadTarget)
		return err
	}
	if err := fileutil.ReplaceFile(tmpManifest, filepath.Join(componentDir, browserCurrentManifest)); err != nil {
		rollbackBrowserComponent(target, backup, hadTarget)
		return err
	}
	if hadTarget {
		_ = os.RemoveAll(backup)
	}
	return nil
}

func rollbackBrowserComponent(target, backup string, hadTarget bool) {
	_ = os.RemoveAll(target)
	if hadTarget {
		_ = os.Rename(backup, target)
	}
}

func extractBrowserComponentZIP(data []byte, dest string) error {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return err
	}
	if len(zr.File) > maxBrowserComponentFiles {
		return fmt.Errorf("archive contains too many files")
	}
	var extracted int64
	for _, f := range zr.File {
		if err := extractComponentEntry(dest, f.Name, f.Mode(), f.UncompressedSize64, func() (io.ReadCloser, error) { return f.Open() }, f.FileInfo().IsDir(), &extracted); err != nil {
			return err
		}
	}
	return nil
}

func extractBrowserComponentTarGZ(data []byte, dest string) error {
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	count := 0
	var extracted int64
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		count++
		if count > maxBrowserComponentFiles {
			return fmt.Errorf("archive contains too many files")
		}
		if h.Size < 0 {
			return fmt.Errorf("archive entry %q has a negative size", h.Name)
		}
		if h.Typeflag == tar.TypeSymlink {
			if err := createComponentSymlink(dest, h.Name, h.Linkname); err != nil {
				return err
			}
			continue
		}
		if h.Typeflag != tar.TypeReg && h.Typeflag != tar.TypeDir {
			return fmt.Errorf("unsupported tar entry %q", h.Name)
		}
		isDir := h.Typeflag == tar.TypeDir
		mode := os.FileMode(h.Mode) & 0o777
		opener := func() (io.ReadCloser, error) { return io.NopCloser(io.LimitReader(tr, h.Size)), nil }
		if err := extractComponentEntry(dest, h.Name, mode, uint64(h.Size), opener, isDir, &extracted); err != nil {
			return err
		}
	}
}

func extractComponentEntry(dest, name string, mode os.FileMode, size uint64, open func() (io.ReadCloser, error), isDir bool, extracted *int64) error {
	target, err := safeComponentArchivePath(dest, name)
	if err != nil {
		return err
	}
	if isDir {
		return os.MkdirAll(target, 0o755)
	}
	if mode&os.ModeSymlink != 0 {
		r, err := open()
		if err != nil {
			return err
		}
		link, err := io.ReadAll(io.LimitReader(r, 4097))
		r.Close()
		if err != nil || len(link) > 4096 {
			return fmt.Errorf("invalid symlink %q", name)
		}
		return createComponentSymlink(dest, name, string(link))
	}
	if size > uint64(maxBrowserComponentExtractBytes) || *extracted > maxBrowserComponentExtractBytes-int64(size) {
		return fmt.Errorf("archive exceeds extracted byte budget")
	}
	*extracted += int64(size)
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	r, err := open()
	if err != nil {
		return err
	}
	defer r.Close()
	perm := mode.Perm()
	if perm == 0 {
		perm = 0o644
	}
	f, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, perm)
	if err != nil {
		return err
	}
	n, copyErr := io.Copy(f, io.LimitReader(r, int64(size)+1))
	closeErr := f.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	if n != int64(size) {
		return fmt.Errorf("archive entry %q size mismatch", name)
	}
	return nil
}

func safeComponentArchivePath(dest, name string) (string, error) {
	name = strings.ReplaceAll(name, "\\", "/")
	clean := strings.TrimPrefix(filepath.ToSlash(filepath.Clean(name)), "./")
	if clean == "" || clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || filepath.IsAbs(name) || filepath.VolumeName(name) != "" {
		return "", fmt.Errorf("unsafe archive path %q", name)
	}
	target := filepath.Join(dest, filepath.FromSlash(clean))
	rel, err := filepath.Rel(dest, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("archive path escapes destination: %q", name)
	}
	return target, nil
}

func createComponentSymlink(dest, name, link string) error {
	target, err := safeComponentArchivePath(dest, name)
	if err != nil {
		return err
	}
	if filepath.IsAbs(link) {
		return fmt.Errorf("absolute symlink %q", name)
	}
	resolved := filepath.Clean(filepath.Join(filepath.Dir(target), filepath.FromSlash(link)))
	rel, err := filepath.Rel(dest, resolved)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("symlink escapes destination: %q", name)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	return os.Symlink(link, target)
}

func validComponentVersion(v string) bool {
	if v == "" || len(v) > 128 || v == "." || v == ".." {
		return false
	}
	for _, r := range v {
		if !(r >= 'a' && r <= 'z') && !(r >= 'A' && r <= 'Z') && !(r >= '0' && r <= '9') && r != '.' && r != '-' && r != '_' {
			return false
		}
	}
	return true
}
