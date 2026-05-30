package cli

import (
	"fmt"
	"os"
	"os/exec"
)

// clipboardImage reads a PNG image from the system clipboard, returning its
// raw bytes. Returns nil, nil when no image is present.
func clipboardImage() ([]byte, error) {
	// macOS: use AppKit via python3 (always available)
	if _, err := exec.LookPath("python3"); err == nil {
		if _, err := exec.LookPath("osascript"); err == nil {
			return clipboardImageDarwin()
		}
	}
	// Linux: try xclip
	if _, err := exec.LookPath("xclip"); err == nil {
		return clipboardImageLinux()
	}
	return nil, fmt.Errorf("clipboard image not supported on this platform (install xclip)")
}

func clipboardImageDarwin() ([]byte, error) {
	script := `import sys
from AppKit import NSPasteboard, NSPasteboardTypePNG, NSPasteboardTypeTIFF, NSImage, NSBitmapImageRep
pb = NSPasteboard.generalPasteboard()
data = pb.dataForType_(NSPasteboardTypePNG)
if not data:
    data = pb.dataForType_(NSPasteboardTypeTIFF)
    if data:
        img = NSImage.alloc().initWithData_(data)
        if img:
            tiff = img.TIFFRepresentation()
            rep = NSBitmapImageRep.alloc().initWithData_(tiff)
            data = rep.representationUsingType_properties_(getattr(NSBitmapImageRep, 'NSPNGFileType', 4), None)
if data:
    sys.stdout.buffer.write(data)
else:
    sys.exit(1)`
	cmd := exec.Command("python3", "-c", script)
	cmd.Stderr = nil
	out, err := cmd.Output()
	if err != nil {
		return nil, nil // no image on clipboard
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}
func clipboardImageLinux() ([]byte, error) {
	cmd := exec.Command("xclip", "-selection", "clipboard", "-t", "image/png", "-o")
	out, err := cmd.Output()
	if err != nil {
		return nil, nil
	}
	return out, nil
}

// clipboardImagePath reads the clipboard image and writes it to a temp PNG
// file. Returns the path, or "" when no image is present.
func clipboardImagePath() (string, error) {
	data, err := clipboardImage()
	if err != nil {
		return "", err
	}
	if len(data) == 0 {
		return "", nil
	}
	f, err := os.CreateTemp("", "reasonix-img-*.png")
	if err != nil {
		return "", err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(f.Name())
		return "", err
	}
	if err := f.Close(); err != nil {
		os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}
