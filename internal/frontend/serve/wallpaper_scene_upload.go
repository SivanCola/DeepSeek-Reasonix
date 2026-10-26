package serve

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"slices"

	"reasonix/internal/contract/config"
)

// maxSceneAsset bounds the package and each extracted audio track. A scene
// package is a container of pictures and sound, not a second window's worth of
// data held in memory.
const maxSceneAsset = 32 << 20

func (s *Server) uploadWallpaperScene(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, (maxSceneAsset/3)*4*2+1024)
	var body struct {
		Project string `json:"project"`
		Scene   string `json:"scene"`
		Preview string `json:"preview"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		badBody(w)
		return
	}
	project, err := decodeScenePart(body.Project)
	if err != nil {
		refuse(w, http.StatusBadRequest, "wallpaper.scene_invalid", "the folder data was not base64", nil)
		return
	}
	scene, err := decodeScenePart(body.Scene)
	if err != nil {
		refuse(w, http.StatusBadRequest, "wallpaper.scene_invalid", "the folder data was not base64", nil)
		return
	}
	if len(scene) > maxSceneAsset {
		refuse(w, http.StatusUnprocessableEntity, "wallpaper.scene_too_large", "the scene package is too large", map[string]any{"limit": maxSceneAsset >> 20})
		return
	}
	preview, err := decodeScenePart(body.Preview)
	if err != nil {
		refuse(w, http.StatusBadRequest, "wallpaper.scene_invalid", "the folder data was not base64", nil)
		return
	}
	if len(preview) > maxSceneAsset {
		refuse(w, http.StatusUnprocessableEntity, "wallpaper.scene_too_large", "the preview is too large", map[string]any{"limit": maxSceneAsset >> 20})
		return
	}

	got, err := importSceneWallpaper(project, scene, preview)
	if err != nil {
		code := "wallpaper.scene_invalid"
		switch {
		case errors.Is(err, errSceneUnsupported):
			code = "wallpaper.scene_unsupported"
		case errors.Is(err, errSceneNoImage):
			code = "wallpaper.scene_no_image"
		}
		refuse(w, http.StatusUnprocessableEntity, code, err.Error(), nil)
		return
	}

	imageName, err := storeWallpaper(got.ImageMIME, bytes.NewReader(got.Image))
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err)
		return
	}
	audioNames := make([]string, 0, len(got.Audios))
	for _, audio := range got.Audios {
		name, err := writeAudio(audio.MIME, audio.Data)
		if err != nil {
			_ = os.Remove(filepath.Join(appearanceDir(), imageName))
			for _, written := range audioNames {
				_ = os.Remove(filepath.Join(appearanceDir(), written))
			}
			writeErr(w, http.StatusUnprocessableEntity, err)
			return
		}
		audioNames = append(audioNames, name)
	}

	edit := config.LoadForEdit(config.UserConfigPath())
	paper := &edit.Desktop.Appearance.Wallpaper
	prev := paper.File
	prevAudios := wallpaperAudioNames(*paper)
	paper.File = imageName
	paper.Kind = "scene"
	paper.Title = got.Title
	paper.Audio = ""
	paper.Audios = audioNames
	if len(audioNames) > 0 {
		paper.Audio = audioNames[0]
	}
	paper.Parallax = got.Parallax
	paper.Muted = true
	if paper.Volume == nil {
		volume := 1.0
		paper.Volume = &volume
	}
	if prev == "" {
		paper.Opacity = 0.5
		paper.Dim = 0.55
		paper.FocusX = 0.5
		paper.FocusY = 0.5
	}
	if err := edit.SaveTo(config.UserConfigPath()); err != nil {
		_ = os.Remove(filepath.Join(appearanceDir(), imageName))
		for _, written := range audioNames {
			_ = os.Remove(filepath.Join(appearanceDir(), written))
		}
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	if prev != "" && prev != imageName {
		_ = os.Remove(filepath.Join(appearanceDir(), prev))
	}
	for _, old := range prevAudios {
		if !slices.Contains(audioNames, old) {
			_ = os.Remove(filepath.Join(appearanceDir(), old))
		}
	}
	writeJSON(w, viewOf(edit.Desktop.Appearance, edit.DesktopLanguage(), edit.DesktopConversationWidth()))
}

func decodeScenePart(value string) ([]byte, error) {
	if value == "" {
		return nil, nil
	}
	return base64.StdEncoding.DecodeString(value)
}
