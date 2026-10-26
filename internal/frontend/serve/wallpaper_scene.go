package serve

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"path"
	"sort"
	"strings"
)

// sceneWallpaper is the part of a Wallpaper Engine scene the page can show
// without its shader runtime: the backdrop, its soundtrack, and how far the
// camera moves with the pointer.
type sceneAudio struct {
	Data []byte
	MIME string
}

type sceneWallpaper struct {
	Image     []byte
	ImageMIME string
	Title     string
	Audios    []sceneAudio
	Parallax  float64
}

var (
	errSceneInvalid     = errors.New("invalid Wallpaper Engine scene")
	errSceneUnsupported = errors.New("unsupported Wallpaper Engine wallpaper type")
	errSceneNoImage     = errors.New("the scene contains no image")
)

type sceneProject struct {
	Title string `json:"title"`
	Type  string `json:"type"`
	File  string `json:"file"`
}

type sceneDocument struct {
	General struct {
		CameraParallax               bool    `json:"cameraparallax"`
		CameraParallaxAmount         float64 `json:"cameraparallaxamount"`
		CameraParallaxMouseInfluence float64 `json:"cameraparallaxmouseinfluence"`
	} `json:"general"`
	Objects []struct {
		Image string   `json:"image"`
		Sound []string `json:"sound"`
	} `json:"objects"`
}

// importSceneWallpaper reads the files a Wallpaper Engine folder keeps beside
// project.json. Shaders and particles are deliberately ignored: this is the
// still backdrop, not a second scene engine.
func importSceneWallpaper(project, pkg, preview []byte) (sceneWallpaper, error) {
	var meta sceneProject
	if err := json.Unmarshal(project, &meta); err != nil {
		return sceneWallpaper{}, fmt.Errorf("%w: project.json: %w", errSceneInvalid, err)
	}
	if !strings.EqualFold(strings.TrimSpace(meta.Type), "scene") {
		return sceneWallpaper{}, fmt.Errorf("%w: type %q", errSceneUnsupported, meta.Type)
	}
	title := strings.TrimSpace(meta.Title)
	if title == "" {
		title = "Wallpaper Engine scene"
	}
	if len(title) > 200 {
		title = title[:200]
	}

	files, err := parseWallpaperPackage(pkg)
	if err != nil {
		return sceneWallpaper{}, fmt.Errorf("%w: %w", errSceneInvalid, err)
	}
	scenePath := cleanAssetPath(meta.File)
	if scenePath == "" {
		scenePath = "scene.json"
	}
	rawScene, ok := files[scenePath]
	if !ok {
		return sceneWallpaper{}, fmt.Errorf("%w: scene file %q is missing", errSceneInvalid, scenePath)
	}
	var scene sceneDocument
	if err := json.Unmarshal(rawScene, &scene); err != nil {
		return sceneWallpaper{}, fmt.Errorf("%w: %s: %w", errSceneInvalid, scenePath, err)
	}

	out := sceneWallpaper{Title: title}
	if imageBytes, ok := sceneBackdrop(files, scene); ok {
		out.Image = imageBytes
		out.ImageMIME = imageMIME(imageBytes)
	}
	if out.Image == nil && len(preview) > 0 {
		if mime := imageMIME(preview); mime != "" {
			out.Image = preview
			out.ImageMIME = mime
		}
	}
	if out.Image == nil {
		return sceneWallpaper{}, errSceneNoImage
	}
	out.Audios = sceneSounds(files, scene)
	out.Parallax = 0.02
	if scene.General.CameraParallax {
		out.Parallax = scene.General.CameraParallaxAmount
		if out.Parallax <= 0 {
			out.Parallax = 0.02
		}
		if out.Parallax > 0.2 {
			out.Parallax = 0.2
		}
	}
	return out, nil
}

func sceneBackdrop(files map[string][]byte, scene sceneDocument) ([]byte, bool) {
	for _, object := range scene.Objects {
		imagePath := cleanAssetPath(object.Image)
		if imagePath == "" {
			continue
		}
		rawModel, ok := files[imagePath]
		if !ok {
			continue
		}
		var model struct {
			Material string `json:"material"`
		}
		if json.Unmarshal(rawModel, &model) != nil {
			continue
		}
		materialPath := cleanAssetPath(model.Material)
		if materialPath != "" {
			if raw, ok := files[materialPath]; ok {
				var material struct {
					Passes []struct {
						Textures []string `json:"textures"`
					} `json:"passes"`
				}
				if json.Unmarshal(raw, &material) == nil {
					for _, pass := range material.Passes {
						for _, texture := range pass.Textures {
							if imageBytes, ok := sceneTexture(files, materialPath, texture); ok {
								return imageBytes, true
							}
						}
					}
				}
			}
		}
	}
	return nil, false
}

func sceneTexture(files map[string][]byte, materialPath, texture string) ([]byte, bool) {
	texture = cleanAssetPath(texture)
	if texture == "" {
		return nil, false
	}
	candidates := []string{texture, texture + ".tex", path.Join("materials", texture+".tex"), path.Join(path.Dir(materialPath), texture+".tex")}
	for _, candidate := range candidates {
		candidate = cleanAssetPath(candidate)
		if candidate == "" {
			continue
		}
		if raw, ok := files[candidate]; ok {
			if imageBytes, _, err := imageFromTEX(raw); err == nil {
				return imageBytes, true
			}
		}
	}
	return nil, false
}

const maxSceneAudioTracks = 8

func sceneSounds(files map[string][]byte, scene sceneDocument) []sceneAudio {
	seen := map[string]bool{}
	out := make([]sceneAudio, 0, 2)
	add := func(name string) {
		name = cleanAssetPath(name)
		if name == "" || seen[name] || len(out) >= maxSceneAudioTracks {
			return
		}
		raw, ok := files[name]
		mime := audioMIME(name)
		if !ok || mime == "" || len(raw) == 0 {
			return
		}
		seen[name] = true
		out = append(out, sceneAudio{Data: raw, MIME: mime})
	}
	for _, object := range scene.Objects {
		for _, sound := range object.Sound {
			add(sound)
		}
	}
	if len(out) == 0 {
		names := make([]string, 0, len(files))
		for name := range files {
			if audioMIME(name) != "" {
				names = append(names, name)
			}
		}
		sort.Strings(names)
		for _, name := range names {
			add(name)
		}
	}
	return out
}

// parseWallpaperPackage reads Wallpaper Engine's PKG container. The table is
// the only thing interpreted; entry data stays in memory and is never unpacked
// to a path a name could choose.
func parseWallpaperPackage(raw []byte) (map[string][]byte, error) {
	if len(raw) < 16 || string(raw[4:8]) != "PKGV" {
		return nil, errors.New("not a Wallpaper Engine package")
	}
	count := binary.LittleEndian.Uint32(raw[12:16])
	if count > 10000 {
		return nil, fmt.Errorf("package declares %d entries", count)
	}
	off := 16
	type entry struct {
		name   string
		offset uint32
		length uint32
	}
	entries := make([]entry, 0, count)
	for range count {
		if off+4 > len(raw) {
			return nil, errors.New("package table is truncated")
		}
		nameLen := binary.LittleEndian.Uint32(raw[off : off+4])
		off += 4
		if nameLen > 4096 || off+int(nameLen)+8 > len(raw) {
			return nil, errors.New("package entry name is out of bounds")
		}
		name := string(raw[off : off+int(nameLen)])
		off += int(nameLen)
		entryOffset := binary.LittleEndian.Uint32(raw[off : off+4])
		entryLength := binary.LittleEndian.Uint32(raw[off+4 : off+8])
		off += 8
		entries = append(entries, entry{name: name, offset: entryOffset, length: entryLength})
	}
	data := raw[off:]
	files := make(map[string][]byte, len(entries))
	for _, item := range entries {
		start := uint64(item.offset)
		end := start + uint64(item.length)
		if end > uint64(len(data)) {
			return nil, fmt.Errorf("package entry %q is out of bounds", item.name)
		}
		files[item.name] = data[start:end]
	}
	return files, nil
}

var (
	pngSignature  = []byte("\x89PNG\r\n\x1a\n")
	jpegSignature = []byte{0xff, 0xd8, 0xff}
)

// maxSceneImagePixels bounds the frame jpeg.Decode may allocate for a scene's
// backdrop. A header can declare a huge frame without carrying any image data.
const maxSceneImagePixels = int64(64 << 20)

// imageFromTEX pulls the PNG or JPEG a TEX container may carry. Masks and
// depth maps use compressed GPU formats and are not needed by the still
// backdrop.
func imageFromTEX(raw []byte) ([]byte, string, error) {
	if start := bytes.Index(raw, pngSignature); start >= 0 {
		rest := raw[start:]
		end := bytes.Index(rest, []byte("IEND"))
		if end >= 0 && end+8 <= len(rest) {
			out := rest[:end+8]
			if mime := imageMIME(out); mime != "" {
				return out, mime, nil
			}
		}
	}
	if out, mime, ok := dxtFromTEX(raw); ok {
		return out, mime, nil
	}
	if start := bytes.Index(raw, jpegSignature); start >= 0 {
		rest := raw[start:]
		cfg, err := jpeg.DecodeConfig(bytes.NewReader(rest))
		if err != nil {
			return nil, "", errSceneNoImage
		}
		if int64(cfg.Width)*int64(cfg.Height) > maxSceneImagePixels {
			return nil, "", fmt.Errorf("%w: JPEG %dx%d exceeds %d pixels", errSceneInvalid, cfg.Width, cfg.Height, maxSceneImagePixels)
		}
		// A TEX may hold several mip JPEGs back to back, and EXIF can carry a
		// thumbnail with its own EOI. Decode the first image so the marker
		// scan follows the real structure instead of guessing at "FF D9".
		reader := bytes.NewReader(rest)
		if _, err := jpeg.Decode(reader); err == nil {
			consumed := len(rest) - reader.Len()
			out := rest[:consumed]
			if mime := imageMIME(out); mime != "" {
				return out, mime, nil
			}
		}
	}
	return nil, "", errSceneNoImage
}

func imageMIME(raw []byte) string {
	_, format, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil {
		return ""
	}
	switch format {
	case "png":
		return "image/png"
	case "jpeg":
		return "image/jpeg"
	default:
		return ""
	}
}

func audioMIME(name string) string {
	switch strings.ToLower(path.Ext(name)) {
	case ".mp3":
		return "audio/mpeg"
	case ".flac":
		return "audio/flac"
	default:
		return ""
	}
}

// cleanAssetPath keeps package names relative and inside the package.
func cleanAssetPath(name string) string {
	name = strings.TrimSpace(strings.ReplaceAll(name, "\\", "/"))
	name = strings.TrimPrefix(name, "/")
	name = path.Clean(name)
	if name == "." || name == "" || strings.HasPrefix(name, "../") || name == ".." {
		return ""
	}
	return name
}
