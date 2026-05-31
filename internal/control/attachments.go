package control

import (
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const maxImageAttachmentBytes = 10 * 1024 * 1024

func SaveImageDataURL(dataURL string) (string, error) {
	const prefix = "data:"
	const marker = ";base64,"

	if !strings.HasPrefix(dataURL, prefix) {
		return "", fmt.Errorf("unsupported pasted image")
	}
	i := strings.Index(dataURL, marker)
	if i <= len(prefix) {
		return "", fmt.Errorf("unsupported pasted image")
	}
	mime := strings.ToLower(dataURL[len(prefix):i])
	raw, err := base64.StdEncoding.DecodeString(dataURL[i+len(marker):])
	if err != nil {
		return "", fmt.Errorf("decode pasted image: %w", err)
	}
	return SaveImageBytes(mime, raw)
}

func SaveImageBytes(mime string, raw []byte) (string, error) {
	ext := imageExt(mime)
	if ext == "" {
		return "", fmt.Errorf("unsupported image type: %s", mime)
	}
	if len(raw) == 0 || len(raw) > maxImageAttachmentBytes {
		return "", fmt.Errorf("pasted image must be between 1 byte and 10 MB")
	}
	rel := attachmentPath(ext)
	if err := os.MkdirAll(filepath.Dir(rel), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(rel, raw, 0o644); err != nil {
		return "", err
	}
	return filepath.ToSlash(rel), nil
}

func SaveImageFile(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		return "", fmt.Errorf("pasted image path is a directory")
	}
	if info.Size() <= 0 || info.Size() > maxImageAttachmentBytes {
		return "", fmt.Errorf("pasted image must be between 1 byte and 10 MB")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	mime := imageMime(raw, path)
	if mime == "" {
		return "", fmt.Errorf("unsupported image file: %s", path)
	}
	return SaveImageBytes(mime, raw)
}

func SaveClipboardImage() (string, error) {
	switch runtime.GOOS {
	case "darwin":
		return saveDarwinClipboardImage()
	default:
		return "", fmt.Errorf("clipboard image paste is not supported on %s yet", runtime.GOOS)
	}
}

func ImageDataURL(path string) (string, error) {
	clean := filepath.Clean(path)
	prefix := filepath.Join(".reasonix", "attachments")
	if clean == "." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) || !strings.HasPrefix(clean, prefix+string(filepath.Separator)) {
		return "", fmt.Errorf("attachment path is outside .reasonix/attachments")
	}
	info, err := lstatAttachmentNoSymlinks(clean)
	if err != nil {
		return "", err
	}
	if info.IsDir() || info.Size() <= 0 || info.Size() > maxImageAttachmentBytes {
		return "", fmt.Errorf("attachment image must be between 1 byte and 10 MB")
	}
	raw, err := readVerifiedAttachment(clean, info)
	if err != nil {
		return "", err
	}
	mime := http.DetectContentType(raw[:min(len(raw), 512)])
	if !strings.HasPrefix(mime, "image/") {
		if extMime := imageMimeFromExt(filepath.Ext(clean)); extMime != "" {
			mime = extMime
		}
	}
	if !strings.HasPrefix(mime, "image/") {
		return "", fmt.Errorf("attachment is not an image")
	}
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(raw), nil
}

func lstatAttachmentNoSymlinks(path string) (os.FileInfo, error) {
	clean := filepath.Clean(path)
	var info os.FileInfo
	cur := ""
	for _, part := range strings.Split(clean, string(filepath.Separator)) {
		if part == "" || part == "." {
			continue
		}
		cur = filepath.Join(cur, part)
		fi, err := os.Lstat(cur)
		if err != nil {
			return nil, err
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("attachment path must not contain symlinks")
		}
		info = fi
	}
	if info == nil {
		return nil, fmt.Errorf("invalid attachment path")
	}
	return info, nil
}

func readVerifiedAttachment(path string, expected os.FileInfo) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	opened, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !os.SameFile(expected, opened) {
		return nil, fmt.Errorf("attachment changed while opening")
	}
	raw, err := io.ReadAll(io.LimitReader(f, maxImageAttachmentBytes+1))
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 || len(raw) > maxImageAttachmentBytes {
		return nil, fmt.Errorf("attachment image must be between 1 byte and 10 MB")
	}
	return raw, nil
}

func saveDarwinClipboardImage() (string, error) {
	for _, format := range []struct {
		class string
		ext   string
	}{
		{class: "PNGf", ext: ".png"},
		{class: "JPEG", ext: ".jpg"},
		{class: "TIFF", ext: ".tiff"},
	} {
		rel, err := saveDarwinClipboardClass(format.class, format.ext)
		if err == nil {
			return rel, nil
		}
	}
	return "", fmt.Errorf("clipboard does not contain a supported image")
}

func saveDarwinClipboardClass(class, ext string) (string, error) {
	rel := attachmentPath(ext)
	if err := os.MkdirAll(filepath.Dir(rel), 0o755); err != nil {
		return "", err
	}
	abs, err := filepath.Abs(rel)
	if err != nil {
		return "", err
	}
	script := fmt.Sprintf(`
set outPath to POSIX file %q
try
	set img to the clipboard as «class %s»
on error
	error "clipboard does not contain this image type"
end try
set f to open for access outPath with write permission
try
	set eof f to 0
	write img to f
	close access f
on error errMsg
	try
		close access f
	end try
	error errMsg
end try
`, abs, class)
	if out, err := exec.Command("osascript", "-e", script).CombinedOutput(); err != nil {
		_ = os.Remove(rel)
		return "", fmt.Errorf("read clipboard image: %s", strings.TrimSpace(string(out)))
	}
	if info, err := os.Stat(rel); err != nil {
		return "", err
	} else if info.Size() == 0 || info.Size() > maxImageAttachmentBytes {
		_ = os.Remove(rel)
		return "", fmt.Errorf("clipboard image must be between 1 byte and 10 MB")
	}
	return filepath.ToSlash(rel), nil
}

func attachmentPath(ext string) string {
	return filepath.Join(".reasonix", "attachments", "clipboard-"+time.Now().Format("20060102-150405.000000")+ext)
}

func imageExt(mime string) string {
	switch strings.ToLower(mime) {
	case "image/png":
		return ".png"
	case "image/jpeg":
		return ".jpg"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	case "image/tiff":
		return ".tiff"
	}
	return ""
}

func imageMimeFromExt(ext string) string {
	switch strings.ToLower(ext) {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".tiff", ".tif":
		return "image/tiff"
	}
	return ""
}
