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

func SaveImageBytes(declaredMime string, raw []byte) (string, error) {
	if len(raw) == 0 || len(raw) > maxImageAttachmentBytes {
		return "", fmt.Errorf("pasted image must be between 1 byte and 10 MB")
	}
	mime := detectedImageMime(raw)
	if mime == "" {
		return "", fmt.Errorf("pasted data is not a supported image")
	}
	if declaredMime != "" && imageExt(declaredMime) == "" {
		return "", fmt.Errorf("unsupported image type: %s", declaredMime)
	}
	ext := imageExt(mime)
	if err := ensureAttachmentRoot(); err != nil {
		return "", err
	}
	rel := attachmentPath(ext)
	if err := os.WriteFile(rel, raw, 0o644); err != nil {
		return "", err
	}
	return filepath.ToSlash(rel), nil
}

func SaveImageFile(path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("pasted image path must not be a symlink")
	}
	if info.IsDir() || info.Size() <= 0 || info.Size() > maxImageAttachmentBytes {
		return "", fmt.Errorf("pasted image must be between 1 byte and 10 MB")
	}
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil {
		return "", err
	}
	if !os.SameFile(info, opened) {
		return "", fmt.Errorf("pasted image changed while opening")
	}
	raw, err := io.ReadAll(io.LimitReader(f, maxImageAttachmentBytes+1))
	if err != nil {
		return "", err
	}
	if len(raw) == 0 || len(raw) > maxImageAttachmentBytes {
		return "", fmt.Errorf("pasted image must be between 1 byte and 10 MB")
	}
	if after, err := f.Stat(); err != nil {
		return "", err
	} else if !os.SameFile(opened, after) || after.Size() != opened.Size() {
		return "", fmt.Errorf("pasted image changed while reading")
	}
	return SaveImageBytes("", raw)
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
	clean, err := cleanAttachmentPath(path)
	if err != nil {
		return "", err
	}
	info, err := os.Lstat(clean)
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("attachment path must not be a symlink")
	}
	if info.IsDir() || info.Size() <= 0 || info.Size() > maxImageAttachmentBytes {
		return "", fmt.Errorf("attachment image must be between 1 byte and 10 MB")
	}
	f, err := os.Open(clean)
	if err != nil {
		return "", err
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil {
		return "", err
	}
	if !os.SameFile(info, opened) {
		return "", fmt.Errorf("attachment changed while opening")
	}
	raw, err := io.ReadAll(io.LimitReader(f, maxImageAttachmentBytes+1))
	if err != nil {
		return "", err
	}
	if len(raw) == 0 || len(raw) > maxImageAttachmentBytes {
		return "", fmt.Errorf("attachment image must be between 1 byte and 10 MB")
	}
	if after, err := f.Stat(); err != nil {
		return "", err
	} else if !os.SameFile(opened, after) || after.Size() != opened.Size() {
		return "", fmt.Errorf("attachment changed while reading")
	}
	mime := detectedImageMime(raw)
	if mime == "" {
		return "", fmt.Errorf("attachment is not an image")
	}
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(raw), nil
}

func cleanAttachmentPath(path string) (string, error) {
	if filepath.IsAbs(path) {
		return "", fmt.Errorf("attachment path must be relative")
	}
	clean := filepath.Clean(filepath.FromSlash(path))
	root := filepath.Join(".reasonix", "attachments")
	if clean == "." || clean == root || strings.HasPrefix(clean, ".."+string(filepath.Separator)) || !strings.HasPrefix(clean, root+string(filepath.Separator)) {
		return "", fmt.Errorf("attachment path is outside .reasonix/attachments")
	}
	if err := ensureAttachmentRoot(); err != nil {
		return "", err
	}
	if err := rejectSymlinkComponents(clean, root); err != nil {
		return "", err
	}
	return clean, nil
}

func rejectSymlinkComponents(path, root string) error {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return err
	}
	if rel == "." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || rel == ".." {
		return fmt.Errorf("attachment path is outside .reasonix/attachments")
	}
	cur := root
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		if part == "" || part == "." {
			continue
		}
		cur = filepath.Join(cur, part)
		info, err := os.Lstat(cur)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("attachment path must not contain symlinks")
		}
	}
	return nil
}

func ensureAttachmentRoot() error {
	root := filepath.Join(".reasonix", "attachments")
	if info, err := os.Lstat(root); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("attachment directory must not be a symlink")
		}
		if !info.IsDir() {
			return fmt.Errorf("attachment path exists but is not a directory")
		}
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return err
	}
	info, err := os.Lstat(root)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("attachment directory is invalid")
	}
	return nil
}

func saveDarwinClipboardImage() (string, error) {
	for _, class := range []string{"PNGf", "JPEG"} {
		if rel, err := saveDarwinClipboardClass(class); err == nil {
			return rel, nil
		}
	}
	return "", fmt.Errorf("clipboard does not contain a supported image")
}

func saveDarwinClipboardClass(class string) (string, error) {
	if err := ensureAttachmentRoot(); err != nil {
		return "", err
	}
	rel := attachmentPath(".bin")
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
	raw, err := os.ReadFile(rel)
	_ = os.Remove(rel)
	if err != nil {
		return "", err
	}
	return SaveImageBytes("", raw)
}

func attachmentPath(ext string) string {
	return filepath.Join(".reasonix", "attachments", "clipboard-"+time.Now().Format("20060102-150405.000000")+ext)
}

func detectedImageMime(raw []byte) string {
	if len(raw) == 0 {
		return ""
	}
	mime := http.DetectContentType(raw[:min(len(raw), 512)])
	if imageExt(mime) == "" {
		return ""
	}
	return mime
}

func imageExt(mime string) string {
	switch strings.ToLower(strings.TrimSpace(mime)) {
	case "image/png":
		return ".png"
	case "image/jpeg":
		return ".jpg"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	}
	return ""
}
