package main

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// themeMu serializes theme library mutations (import/save/delete/activate).
var themeMu sync.Mutex

// stagedThemeImport holds a ZIP extract awaiting replace confirmation.
// Host paths stay on the Go side — the frontend only sees pendingId.
type stagedThemeImport struct {
	id      string
	staging string
	pack    ThemePackView
}

var (
	pendingThemeMu    sync.Mutex
	pendingThemeStage *stagedThemeImport
)

func clearPendingThemeImport() {
	pendingThemeMu.Lock()
	defer pendingThemeMu.Unlock()
	if pendingThemeStage != nil && pendingThemeStage.staging != "" {
		_ = os.RemoveAll(pendingThemeStage.staging)
	}
	pendingThemeStage = nil
}

func setPendingThemeImport(id, staging string, pack ThemePackView) string {
	pendingThemeMu.Lock()
	if pendingThemeStage != nil && pendingThemeStage.staging != "" && pendingThemeStage.staging != staging {
		_ = os.RemoveAll(pendingThemeStage.staging)
	}
	pendingID := "pending-" + id + "-" + randomThemeSuffix()
	pendingThemeStage = &stagedThemeImport{id: id, staging: staging, pack: pack}
	pendingThemeMu.Unlock()
	return pendingID
}

func takePendingThemeImport() *stagedThemeImport {
	pendingThemeMu.Lock()
	defer pendingThemeMu.Unlock()
	p := pendingThemeStage
	pendingThemeStage = nil
	return p
}

// ListThemePacks returns built-in directions plus user themes from the local library.
func (a *App) ListThemePacks() ([]ThemePackView, error) {
	themeMu.Lock()
	defer themeMu.Unlock()

	st := loadThemeDesktopState()
	activeID := resolveActiveThemeID(st)
	// If the stored active theme is corrupt/missing, clear it and fall back.
	if st.ActiveThemeID != "" && activeID == "" {
		st.ActiveThemeID = ""
		_ = saveThemeDesktopState(st)
	}

	safe := a.themeSafeMode()
	var out []ThemePackView
	for _, m := range builtinThemePacks() {
		cp := m
		v := manifestToView(&cp, true, !safe && activeID == m.ID, "")
		out = append(out, v)
	}
	if safe {
		return out, nil
	}
	ids, err := listUserThemeIDs()
	if err != nil {
		return out, err
	}
	for _, id := range ids {
		m, err := loadUserThemeManifest(id)
		if err != nil {
			continue
		}
		bgURL := ""
		if m.Background != nil && m.Background.Image != "" {
			bgURL = themeBackgroundURL(id, m.Background.Image)
		}
		out = append(out, manifestToView(m, false, activeID == id, bgURL))
	}
	return out, nil
}

// GetActiveThemePack returns the currently enabled pack (nil pack when none / safe mode).
func (a *App) GetActiveThemePack() (ThemeActiveView, error) {
	themeMu.Lock()
	defer themeMu.Unlock()

	view := ThemeActiveView{SafeMode: a.themeSafeMode()}
	if view.SafeMode {
		return view, nil
	}
	st := loadThemeDesktopState()
	activeID := resolveActiveThemeID(st)
	if st.ActiveThemeID != "" && activeID == "" {
		// Auto-fallback Graphite: clear broken pointer.
		st.ActiveThemeID = ""
		_ = saveThemeDesktopState(st)
		return view, nil
	}
	if activeID == "" {
		return view, nil
	}
	view.ActiveThemeID = activeID
	pack, err := a.loadThemeViewLocked(activeID, true)
	if err != nil {
		st.ActiveThemeID = ""
		_ = saveThemeDesktopState(st)
		view.ActiveThemeID = ""
		return view, nil
	}
	view.Pack = &pack
	return view, nil
}

func (a *App) loadThemeViewLocked(id string, active bool) (ThemePackView, error) {
	if isBuiltinThemeID(id) {
		m := findBuiltinManifest(id)
		if m == nil {
			return ThemePackView{}, fmt.Errorf("unknown built-in theme %q", id)
		}
		return manifestToView(m, true, active, ""), nil
	}
	m, err := loadUserThemeManifest(id)
	if err != nil {
		return ThemePackView{}, err
	}
	bgURL := ""
	if m.Background != nil && m.Background.Image != "" {
		bgURL = themeBackgroundURL(id, m.Background.Image)
	}
	return manifestToView(m, false, active, bgURL), nil
}

// ActivateThemePack enables a built-in or user theme. Empty id clears the pack.
func (a *App) ActivateThemePack(id string) error {
	themeMu.Lock()
	defer themeMu.Unlock()

	id = strings.TrimSpace(id)
	if a.themeSafeMode() && id != "" {
		return fmt.Errorf("safe mode does not load external themes")
	}
	st := loadThemeDesktopState()
	if id == "" {
		st.ActiveThemeID = ""
		return saveThemeDesktopState(st)
	}
	if isBuiltinThemeID(id) {
		// Activating a built-in pack means "use this base style as the active pack
		// identity" — tokens stay empty so CSS falls through to style sheets.
		st.ActiveThemeID = id
		return saveThemeDesktopState(st)
	}
	if _, err := loadUserThemeManifest(id); err != nil {
		return fmt.Errorf("theme %q is missing or invalid", id)
	}
	st.ActiveThemeID = id
	return saveThemeDesktopState(st)
}

// ResetThemePack clears the active theme pack (restore default / Graphite path).
func (a *App) ResetThemePack() error {
	return a.ActivateThemePack("")
}

// SaveThemePack creates or updates a user theme from the editor payload.
func (a *App) SaveThemePack(input ThemeSaveInput) (ThemePackView, error) {
	themeMu.Lock()
	defer themeMu.Unlock()

	if a.themeSafeMode() {
		return ThemePackView{}, fmt.Errorf("safe mode cannot save themes")
	}
	m := &ThemePackManifest{
		SchemaVersion: themePackSchemaVersion,
		ID:            strings.TrimSpace(input.ID),
		Name:          input.Name,
		Author:        input.Author,
		Description:   input.Description,
		License:       input.License,
		BaseStyle:     input.BaseStyle,
		Tokens:        input.Tokens,
		Recipes:       input.Recipes,
		Background:    input.Background,
	}
	if err := validateThemePackManifest(m); err != nil {
		return ThemePackView{}, err
	}
	if isBuiltinThemeID(m.ID) {
		return ThemePackView{}, fmt.Errorf("built-in theme ids are reserved")
	}

	var imageBytes []byte
	var imageName string
	keepExistingImage := false

	if input.ClearBackground {
		m.Background = nil
	} else if strings.TrimSpace(input.BackgroundDataURL) != "" {
		name, data, err := decodeDataURLImage(input.BackgroundDataURL)
		if err != nil {
			return ThemePackView{}, err
		}
		imageBytes = data
		imageName = name
		if m.Background == nil {
			bg := defaultThemePackBackground()
			m.Background = &bg
		}
		m.Background.Image = imageName
		// Re-validate after image assignment.
		bg, err := normalizeThemeBackground(m.Background)
		if err != nil {
			return ThemePackView{}, err
		}
		m.Background = bg
	} else if m.Background != nil && m.Background.Image != "" {
		// Keep existing image from library when editing.
		if userThemeExists(m.ID) {
			keepExistingImage = true
		} else {
			return ThemePackView{}, fmt.Errorf("background image data is required for new themes with a background")
		}
	}

	var staging string
	var err error
	if keepExistingImage {
		existing, err := resolveThemeImageAbs(m.ID, m.Background.Image)
		if err != nil {
			return ThemePackView{}, err
		}
		staging, err = writeThemeStaging(m, existing, nil)
		if err != nil {
			return ThemePackView{}, err
		}
	} else {
		staging, err = writeThemeStaging(m, "", imageBytes)
		if err != nil {
			return ThemePackView{}, err
		}
	}
	defer os.RemoveAll(staging)

	// Honor Replace: create/import-style saves must not silently overwrite.
	// The editor passes Replace=true when editing an existing theme.
	exists := userThemeExists(m.ID)
	if exists && !input.Replace {
		return ThemePackView{}, fmt.Errorf("theme %q already exists; set replace to overwrite", m.ID)
	}
	if err := publishThemeDir(m.ID, staging, exists && input.Replace); err != nil {
		return ThemePackView{}, err
	}

	if input.Activate {
		st := loadThemeDesktopState()
		st.ActiveThemeID = m.ID
		if err := saveThemeDesktopState(st); err != nil {
			return ThemePackView{}, err
		}
	}
	return a.loadThemeViewLocked(m.ID, input.Activate)
}

// DeleteThemePack removes a user theme. Active theme falls back to none (Graphite path).
func (a *App) DeleteThemePack(id string) error {
	themeMu.Lock()
	defer themeMu.Unlock()

	id = strings.TrimSpace(id)
	if isBuiltinThemeID(id) {
		return fmt.Errorf("built-in themes cannot be deleted")
	}
	if err := deleteUserTheme(id); err != nil {
		return err
	}
	st := loadThemeDesktopState()
	if st.ActiveThemeID == id {
		st.ActiveThemeID = ""
		return saveThemeDesktopState(st)
	}
	return nil
}

// CopyThemePack duplicates a built-in or user theme into a new user theme id.
func (a *App) CopyThemePack(sourceID, newID, newName string) (ThemePackView, error) {
	themeMu.Lock()
	defer themeMu.Unlock()

	if a.themeSafeMode() {
		return ThemePackView{}, fmt.Errorf("safe mode cannot copy themes")
	}
	sourceID = strings.TrimSpace(sourceID)
	newID = strings.TrimSpace(newID)
	if !themePackIDRe.MatchString(newID) || isBuiltinThemeID(newID) {
		return ThemePackView{}, fmt.Errorf("invalid new theme id")
	}
	if userThemeExists(newID) {
		return ThemePackView{}, fmt.Errorf("theme %q already exists", newID)
	}

	var m *ThemePackManifest
	var imageBytes []byte
	if isBuiltinThemeID(sourceID) {
		src := findBuiltinManifest(sourceID)
		if src == nil {
			return ThemePackView{}, fmt.Errorf("unknown source theme")
		}
		cp := *src
		m = &cp
	} else {
		src, err := loadUserThemeManifest(sourceID)
		if err != nil {
			return ThemePackView{}, err
		}
		m = src
		if m.Background != nil && m.Background.Image != "" {
			p, err := resolveThemeImageAbs(sourceID, m.Background.Image)
			if err != nil {
				return ThemePackView{}, err
			}
			imageBytes, err = os.ReadFile(p)
			if err != nil {
				return ThemePackView{}, err
			}
		}
	}
	m.ID = newID
	if strings.TrimSpace(newName) != "" {
		m.Name = strings.TrimSpace(newName)
	} else {
		m.Name = m.Name + " Copy"
	}
	if err := validateThemePackManifest(m); err != nil {
		return ThemePackView{}, err
	}
	staging, err := writeThemeStaging(m, "", imageBytes)
	if err != nil {
		return ThemePackView{}, err
	}
	defer os.RemoveAll(staging)
	if err := publishThemeDir(newID, staging, false); err != nil {
		return ThemePackView{}, err
	}
	return a.loadThemeViewLocked(newID, false)
}

// ImportThemePack opens a file dialog (or uses sourcePath in tests) and imports a ZIP.
// When replace is false and the id exists, the extract is kept as a pending import
// (NeedsReplace=true) so a subsequent ImportThemePack("", true) publishes without
// re-opening the file dialog. Host paths never leave the Go side.
func (a *App) ImportThemePack(sourcePath string, replace bool) (ThemeImportResult, error) {
	themeMu.Lock()
	defer themeMu.Unlock()

	if a.themeSafeMode() {
		return ThemeImportResult{}, fmt.Errorf("safe mode cannot import themes")
	}

	// Confirm a previously staged conflict without re-picking a file.
	path := strings.TrimSpace(sourcePath)
	if path == "" && replace {
		if pending := takePendingThemeImport(); pending != nil {
			defer os.RemoveAll(pending.staging)
			if err := publishThemeDir(pending.id, pending.staging, true); err != nil {
				return ThemeImportResult{}, err
			}
			pack, err := a.loadThemeViewLocked(pending.id, false)
			if err != nil {
				return ThemeImportResult{}, err
			}
			return ThemeImportResult{Pack: pack, Replaced: true}, nil
		}
		// Fall through to dialog/path if nothing was pending (e.g. tests pass path).
	}

	if path == "" {
		if a.ctx == nil {
			return ThemeImportResult{}, fmt.Errorf("no theme package selected")
		}
		picked, err := runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{
			Title: "Import Reasonix Theme",
			Filters: []runtime.FileFilter{
				{DisplayName: "Reasonix Theme (*.reasonix-theme)", Pattern: "*.reasonix-theme"},
				{DisplayName: "ZIP (*.zip)", Pattern: "*.zip"},
			},
		})
		if err != nil {
			return ThemeImportResult{}, err
		}
		path = picked
	}
	if path == "" {
		return ThemeImportResult{}, nil
	}

	m, staging, err := importThemePackZIP(path)
	if err != nil {
		return ThemeImportResult{}, err
	}

	exists := userThemeExists(m.ID)
	if exists && !replace {
		// Stage for confirmation — do not delete staging; pending owns it.
		pack := manifestToView(m, false, false, "")
		pendingID := setPendingThemeImport(m.ID, staging, pack)
		return ThemeImportResult{
			Pack:         pack,
			NeedsReplace: true,
			PendingID:    pendingID,
		}, nil
	}
	defer os.RemoveAll(staging)
	clearPendingThemeImport()

	if err := publishThemeDir(m.ID, staging, replace || exists); err != nil {
		return ThemeImportResult{}, err
	}
	pack, err := a.loadThemeViewLocked(m.ID, false)
	if err != nil {
		return ThemeImportResult{}, err
	}
	return ThemeImportResult{Pack: pack, Replaced: exists && replace}, nil
}

// ExportThemePack writes the theme to a user-selected destination.
func (a *App) ExportThemePack(id, destPath string) (string, error) {
	themeMu.Lock()
	defer themeMu.Unlock()

	id = strings.TrimSpace(id)
	if id == "" {
		return "", fmt.Errorf("theme id is required")
	}
	path := strings.TrimSpace(destPath)
	if path == "" {
		if a.ctx == nil {
			return "", fmt.Errorf("no export path")
		}
		defaultName := id + themePackExt
		picked, err := runtime.SaveFileDialog(a.ctx, runtime.SaveDialogOptions{
			Title:           "Export Reasonix Theme",
			DefaultFilename: defaultName,
			Filters: []runtime.FileFilter{
				{DisplayName: "Reasonix Theme (*.reasonix-theme)", Pattern: "*.reasonix-theme"},
			},
		})
		if err != nil {
			return "", err
		}
		path = picked
	}
	if path == "" {
		return "", nil
	}
	if err := exportThemePackZIP(id, path); err != nil {
		return "", err
	}
	if !strings.HasSuffix(strings.ToLower(path), themePackExt) {
		path += themePackExt
	}
	return path, nil
}

// PickThemeBackground opens a native file dialog for a local background image.
// Returns a data URL for the editor preview — never exposes the absolute path.
func (a *App) PickThemeBackground() (string, error) {
	if a.ctx == nil {
		return "", fmt.Errorf("file dialog unavailable")
	}
	path, err := runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{
		Title: "Choose Theme Background",
		Filters: []runtime.FileFilter{
			{DisplayName: "Images (*.png;*.jpg;*.jpeg;*.webp)", Pattern: "*.png;*.jpg;*.jpeg;*.webp"},
		},
	})
	if err != nil {
		return "", err
	}
	if path == "" {
		return "", nil
	}
	if err := validateThemeImageFile(path); err != nil {
		return "", err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	if int64(len(data)) > themePackMaxImageBytes {
		return "", fmt.Errorf("background image exceeds %d bytes", themePackMaxImageBytes)
	}
	mime := themeImageMIMEFromName(filepath.Base(path))
	// Return as data URL so the frontend never needs the host path.
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data), nil
}
