import { useCallback, useEffect, useMemo, useState } from "react";
import { ArrowLeft, Check, Copy, Download, MoreHorizontal, Pencil, Plus, Trash2, Upload } from "lucide-react";
import { app } from "../lib/bridge";
import { useT } from "../lib/i18n";
import { THEME_STYLES, type ThemeStyle, isThemeStyle } from "../lib/theme";
import {
  type ThemePackView,
  type ThemeSaveInput,
  cancelThemePreview,
  draftPackView,
  emptyThemeTokens,
  themePackKind,
} from "../lib/themePack";
import {
  type GalleryTab,
  type ThemeExperienceView,
  type ThemeSelection,
  activateBaseStyle,
  activateThemePack,
  cancelGlobalPreview,
  commitGlobalPreview,
  groupThemePacks,
  isSelectionActive,
  selectionFromPack,
  startGlobalPreview,
} from "../lib/themeExperience";
import { useToast } from "../lib/toast";
import { ThemePreviewSurface } from "./ThemePreviewSurface";

type EditorState = {
  mode: "create" | "edit";
  id: string;
  name: string;
  author: string;
  description: string;
  license: string;
  baseStyle: ThemeStyle;
  tokens: ThemePackView["tokens"];
  recipes: ThemePackView["recipes"];
  background: ThemePackView["background"];
  backgroundDataUrl: string;
  existingBackgroundUrl: string;
  originalId: string;
};

function slugifyId(name: string): string {
  const s = name
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "")
    .slice(0, 48);
  if (!s) return "my-theme";
  if (/^[a-z][a-z0-9-]*[a-z0-9]$|^[a-z]$/.test(s)) return s;
  return `t-${s}`.slice(0, 48);
}

function packDisplayName(pack: ThemePackView, t: (key: never, vars?: Record<string, string | number>) => string): string {
  return pack.nameKey ? t(pack.nameKey as never) : pack.name;
}

function packDescription(pack: ThemePackView, t: (key: never, vars?: Record<string, string | number>) => string): string {
  if (pack.descriptionKey) return t(pack.descriptionKey as never);
  return pack.description || "";
}

export function ThemeGallery({
  experience,
  onExperienceChange,
  onBack,
}: {
  experience: ThemeExperienceView;
  onExperienceChange: (view: ThemeExperienceView) => void;
  onBack: () => void;
}) {
  const t = useT();
  const { showToast } = useToast();
  const [packs, setPacks] = useState<ThemePackView[]>([]);
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);
  const [tab, setTab] = useState<GalleryTab>("official");
  const [selected, setSelected] = useState<ThemeSelection | null>(null);
  const [detailMode, setDetailMode] = useState<"light" | "dark">("dark");
  const [detailScene, setDetailScene] = useState<"home" | "task">("home");
  const [immersive, setImmersive] = useState(false);
  const [editor, setEditor] = useState<EditorState | null>(null);
  const [menuOpen, setMenuOpen] = useState(false);

  const reload = useCallback(async () => {
    setLoading(true);
    try {
      const list = await app.ListThemePacks();
      setPacks(list || []);
    } catch (err) {
      showToast(err instanceof Error ? err.message : String(err), "error");
    } finally {
      setLoading(false);
    }
  }, [showToast]);

  useEffect(() => {
    void reload();
    return () => {
      cancelGlobalPreview();
      cancelThemePreview();
    };
  }, [reload]);

  const groups = useMemo(() => groupThemePacks(packs), [packs]);
  const visible = tab === "official" ? groups.official : tab === "user" ? groups.user : groups.base;

  // Seed selection from active experience.
  useEffect(() => {
    if (selected || packs.length === 0) return;
    if (experience.activePack) {
      setSelected(selectionFromPack(experience.activePack));
      setTab(themePackKind(experience.activePack) === "user" ? "user" : "official");
      return;
    }
    const base = groups.base.find((p) => p.id === experience.baseStyle) || groups.base[0] || groups.official[0];
    if (base) {
      setSelected(selectionFromPack(base));
      if (themePackKind(base) === "base") setTab("base");
    }
  }, [packs, experience, selected, groups.base, groups.official]);

  const selectedPack = selected?.pack || (selected?.kind === "base" ? groups.base.find((p) => p.id === selected.id) : null) || null;
  const isActive = isSelectionActive(selected, experience);

  const onSelectPack = (pack: ThemePackView) => {
    setSelected(selectionFromPack(pack));
    setMenuOpen(false);
  };

  const applySelected = async () => {
    if (!selected) return;
    setBusy(true);
    try {
      if (selected.kind === "base") {
        const view = await activateBaseStyle(selected.id);
        onExperienceChange(view);
        showToast(t("settings.themeGallery.appliedBase", { name: selected.id }), "info");
      } else {
        const view = await activateThemePack(selected.id);
        onExperienceChange(view);
        commitGlobalPreview(view.activePack ?? null);
        showToast(t("settings.themeGallery.applied", { name: packDisplayName(selected.pack, t) }), "info");
      }
      await reload();
    } catch (err) {
      showToast(err instanceof Error ? err.message : String(err), "error");
    } finally {
      setBusy(false);
    }
  };

  const previewSelected = () => {
    if (!selectedPack || selected?.kind === "base") {
      // Base styles: apply temporarily via empty pack draft with baseStyle.
      if (selected?.kind === "base") {
        const draft = draftPackView({
          id: selected.id,
          name: selected.id,
          baseStyle: selected.id,
          tokens: emptyThemeTokens(),
          recipes: { density: "comfortable", corners: "soft" },
        });
        startGlobalPreview(draft);
      }
      return;
    }
    startGlobalPreview(selectedPack);
  };

  const copySelected = async () => {
    if (!selectedPack) return;
    setBusy(true);
    try {
      const newId = slugifyId(`${selectedPack.id}-copy`);
      const created = await app.CopyThemePack(selectedPack.id, newId, `${packDisplayName(selectedPack, t)} Copy`);
      showToast(t("settings.themeLibrary.copied", { name: created.name }), "info");
      await reload();
      setTab("user");
      setSelected(selectionFromPack(created));
    } catch (err) {
      showToast(err instanceof Error ? err.message : String(err), "error");
    } finally {
      setBusy(false);
    }
  };

  const removeSelected = async () => {
    if (!selectedPack || themePackKind(selectedPack) !== "user") return;
    const ok = window.confirm(t("settings.themeLibrary.confirmDelete", { name: packDisplayName(selectedPack, t) }));
    if (!ok) return;
    setBusy(true);
    try {
      await app.DeleteThemePack(selectedPack.id);
      showToast(t("settings.themeGallery.deleted", { name: packDisplayName(selectedPack, t) }), "info");
      setSelected(null);
      await reload();
      // Experience may have changed if we deleted the active pack.
      const { loadThemeExperience, applyExperienceToDOM } = await import("../lib/themeExperience");
      const view = await loadThemeExperience();
      applyExperienceToDOM(view);
      onExperienceChange(view);
    } catch (err) {
      showToast(err instanceof Error ? err.message : String(err), "error");
    } finally {
      setBusy(false);
      setMenuOpen(false);
    }
  };

  const exportSelected = async () => {
    if (!selectedPack || themePackKind(selectedPack) !== "user") return;
    try {
      const ok = window.confirm(t("settings.themeLibrary.exportRights"));
      if (!ok) return;
      const path = await app.ExportThemePack(selectedPack.id, "");
      if (path) showToast(t("settings.themeLibrary.exported"), "info");
    } catch (err) {
      showToast(err instanceof Error ? err.message : String(err), "error");
    }
    setMenuOpen(false);
  };

  const openCreate = () => {
    setEditor({
      mode: "create",
      id: "my-theme",
      name: "My Theme",
      author: "",
      description: "",
      license: "",
      baseStyle: isThemeStyle(experience.baseStyle) ? experience.baseStyle : "graphite",
      tokens: emptyThemeTokens(),
      recipes: { density: "comfortable", corners: "soft" },
      background: null,
      backgroundDataUrl: "",
      existingBackgroundUrl: "",
      originalId: "",
    });
  };

  const openEdit = () => {
    if (!selectedPack || themePackKind(selectedPack) !== "user") return;
    setEditor({
      mode: "edit",
      id: selectedPack.id,
      name: selectedPack.name,
      author: selectedPack.author || "",
      description: selectedPack.description || "",
      license: selectedPack.license || "",
      baseStyle: isThemeStyle(selectedPack.baseStyle) ? selectedPack.baseStyle : "graphite",
      tokens: { light: { ...(selectedPack.tokens?.light || {}) }, dark: { ...(selectedPack.tokens?.dark || {}) } },
      recipes: {
        density: selectedPack.recipes?.density === "compact" ? "compact" : "comfortable",
        corners:
          selectedPack.recipes?.corners === "square" || selectedPack.recipes?.corners === "round"
            ? selectedPack.recipes.corners
            : "soft",
      },
      background: selectedPack.background ? { ...selectedPack.background } : null,
      backgroundDataUrl: "",
      existingBackgroundUrl: selectedPack.backgroundUrl || "",
      originalId: selectedPack.id,
    });
    setMenuOpen(false);
  };

  const doImport = async () => {
    setBusy(true);
    try {
      const result = await app.ImportThemePack("", false);
      if (!result) return;
      if (result.needsReplace) {
        const ok = window.confirm(t("settings.themeLibrary.confirmReplaceImport"));
        if (!ok) return;
        const confirmed = await app.ImportThemePack("", true);
        if (confirmed?.pack) {
          showToast(t("settings.themeLibrary.imported", { name: confirmed.pack.name }), "info");
        }
      } else if (result.pack) {
        showToast(t("settings.themeLibrary.imported", { name: result.pack.name }), "info");
      }
      await reload();
      setTab("user");
    } catch (err) {
      showToast(err instanceof Error ? err.message : String(err), "error");
    } finally {
      setBusy(false);
    }
  };

  const saveEditor = async (activate: boolean) => {
    if (!editor) return;
    setBusy(true);
    try {
      const input: ThemeSaveInput = {
        id: editor.id,
        name: editor.name,
        author: editor.author,
        description: editor.description,
        license: editor.license,
        baseStyle: editor.baseStyle,
        tokens: editor.tokens,
        recipes: editor.recipes,
        background: editor.background || undefined,
        backgroundDataUrl: editor.backgroundDataUrl || undefined,
        clearBackground: !editor.background && !editor.backgroundDataUrl && !editor.existingBackgroundUrl,
        replace: editor.mode === "edit",
        activate,
      };
      const saved = await app.SaveThemePack(input);
      showToast(t("settings.themeLibrary.saved", { name: saved.name }), "info");
      setEditor(null);
      await reload();
      setTab("user");
      setSelected(selectionFromPack(saved));
      if (activate) {
        const view = await activateThemePack(saved.id);
        onExperienceChange(view);
      }
    } catch (err) {
      showToast(err instanceof Error ? err.message : String(err), "error");
    } finally {
      setBusy(false);
    }
  };

  if (immersive && selectedPack) {
    return (
      <div className="theme-gallery theme-gallery--immersive">
        <header className="theme-gallery__top">
          <button type="button" className="btn btn--small" onClick={() => setImmersive(false)}>
            <ArrowLeft size={14} /> {t("settings.themeGallery.back")}
          </button>
          <h2 className="theme-gallery__title">{t("settings.themeGallery.previewTitle")}</h2>
          <div className="theme-gallery__top-actions">
            <button type="button" className="btn btn--small" onClick={() => void doImport()} disabled={busy}>
              <Upload size={13} /> {t("settings.themeLibrary.import")}
            </button>
            <button type="button" className="btn btn--small" onClick={openCreate} disabled={busy}>
              <Plus size={13} /> {t("settings.themeLibrary.new")}
            </button>
          </div>
        </header>
        <div className="theme-gallery__immersive-toolbar">
          <div className="set-seg">
            <button type="button" className={`set-seg__btn${detailScene === "home" ? " set-seg__btn--on" : ""}`} onClick={() => setDetailScene("home")}>
              {t("settings.themeGallery.sceneHome")}
            </button>
            <button type="button" className={`set-seg__btn${detailScene === "task" ? " set-seg__btn--on" : ""}`} onClick={() => setDetailScene("task")}>
              {t("settings.themeGallery.sceneTask")}
            </button>
          </div>
          <div className="set-seg">
            <button type="button" className={`set-seg__btn${detailMode === "light" ? " set-seg__btn--on" : ""}`} onClick={() => setDetailMode("light")}>
              {t("settings.themeLight")}
            </button>
            <button type="button" className={`set-seg__btn${detailMode === "dark" ? " set-seg__btn--on" : ""}`} onClick={() => setDetailMode("dark")}>
              {t("settings.themeDark")}
            </button>
          </div>
        </div>
        <div className="theme-gallery__immersive-body">
          <ThemePreviewSurface pack={selectedPack} mode={detailMode} scene={detailScene} />
          <aside className="theme-gallery__immersive-rail">
            <div className="theme-gallery__detail-head">
              <h3>{packDisplayName(selectedPack, t)}</h3>
              <span className="theme-gallery__badge">{t("settings.themeLibrary.groupOfficial").replace("Reasonix ", "")}</span>
            </div>
            <p className="theme-gallery__detail-desc">{packDescription(selectedPack, t)}</p>
            <button type="button" className="btn btn--primary" disabled={busy || isActive} onClick={() => void applySelected()}>
              {isActive ? t("settings.themeLibrary.active") : t("settings.themeGallery.apply")}
            </button>
            <button type="button" className="btn" disabled={busy} onClick={previewSelected}>
              {t("settings.themeGallery.tempPreview")}
            </button>
            <div className="theme-gallery__rail-list" role="listbox" aria-label={t("settings.themeGallery.title")}>
              {groups.official.map((p) => (
                <button
                  key={p.id}
                  type="button"
                  role="option"
                  aria-selected={selected?.id === p.id}
                  className={`theme-gallery__rail-card${selected?.id === p.id ? " theme-gallery__rail-card--on" : ""}`}
                  onClick={() => onSelectPack(p)}
                >
                  {p.previewUrl || p.backgroundUrl ? (
                    <img src={p.previewUrl || p.backgroundUrl} alt="" loading="lazy" />
                  ) : (
                    <span className="theme-gallery__rail-fallback" />
                  )}
                  <span>{packDisplayName(p, t)}</span>
                </button>
              ))}
            </div>
            <div className="theme-gallery__tabs theme-gallery__tabs--compact">
              {(["official", "user", "base"] as GalleryTab[]).map((id) => (
                <button key={id} type="button" className={`theme-gallery__tab${tab === id ? " theme-gallery__tab--on" : ""}`} onClick={() => setTab(id)}>
                  {id === "official" ? t("settings.themeLibrary.groupOfficial") : id === "user" ? t("settings.themeLibrary.groupUser") : t("settings.themeGallery.tabBase")}
                </button>
              ))}
            </div>
          </aside>
        </div>
      </div>
    );
  }

  return (
    <div className="theme-gallery">
      <header className="theme-gallery__top">
        <div className="theme-gallery__crumbs">
          <button type="button" className="theme-gallery__back" onClick={onBack}>
            <ArrowLeft size={14} />
            <span>
              {t("settings.appearance")} / {t("settings.themeGallery.title")}
            </span>
          </button>
          <h2 className="theme-gallery__title">{t("settings.themeGallery.title")}</h2>
          <p className="theme-gallery__sub">{t("settings.themeGallery.subtitle")}</p>
        </div>
        <div className="theme-gallery__top-actions">
          <button type="button" className="btn btn--small" onClick={openCreate} disabled={busy}>
            <Plus size={13} /> {t("settings.themeLibrary.new")}
          </button>
          <button type="button" className="btn btn--small" onClick={() => void doImport()} disabled={busy}>
            <Upload size={13} /> {t("settings.themeLibrary.import")}
          </button>
        </div>
      </header>

      <div className="theme-gallery__tabs" role="tablist">
        {(
          [
            ["official", t("settings.themeLibrary.groupOfficial"), groups.official.length],
            ["user", t("settings.themeLibrary.groupUser"), groups.user.length],
            ["base", t("settings.themeGallery.tabBase"), groups.base.length],
          ] as const
        ).map(([id, label, count]) => (
          <button
            key={id}
            type="button"
            role="tab"
            aria-selected={tab === id}
            className={`theme-gallery__tab${tab === id ? " theme-gallery__tab--on" : ""}`}
            onClick={() => setTab(id)}
          >
            {label} <span className="theme-gallery__tab-count">{count}</span>
          </button>
        ))}
      </div>

      <div className="theme-gallery__body">
        <div className="theme-gallery__grid" role="listbox" aria-label={t("settings.themeGallery.title")}>
          {loading ? (
            <div className="theme-lib-card__sub">{t("settings.themeLibrary.loading")}</div>
          ) : visible.length === 0 ? (
            <div className="theme-lib-card__sub">{tab === "user" ? t("settings.themeLibrary.emptyUser") : t("settings.themeGallery.empty")}</div>
          ) : (
            visible.map((pack) => {
              const name = packDisplayName(pack, t);
              const active = isSelectionActive(selectionFromPack(pack), experience);
              const sel = selected?.id === pack.id;
              const light = pack.tokens?.light?.bg || "#f4f3ef";
              const accent = pack.tokens?.dark?.accent || pack.tokens?.light?.accent || "#ff6a3d";
              return (
                <button
                  key={pack.id}
                  type="button"
                  role="option"
                  aria-selected={sel}
                  className={`theme-gallery-card${sel ? " theme-gallery-card--selected" : ""}${active ? " theme-gallery-card--active" : ""}`}
                  onClick={() => onSelectPack(pack)}
                  onKeyDown={(e) => {
                    if (e.key === "Enter" || e.key === " ") {
                      e.preventDefault();
                      onSelectPack(pack);
                    }
                  }}
                >
                  <div className="theme-gallery-card__thumb">
                    {pack.previewUrl || pack.backgroundUrl ? (
                      <img src={pack.previewUrl || pack.backgroundUrl} alt={name} loading="lazy" decoding="async" />
                    ) : (
                      <div className="theme-gallery-card__swatches" style={{ background: `linear-gradient(120deg, ${light}, ${accent})` }} />
                    )}
                    {active ? (
                      <span className="theme-gallery-card__check" aria-hidden="true">
                        <Check size={14} strokeWidth={3} />
                      </span>
                    ) : null}
                  </div>
                  <div className="theme-gallery-card__name">{name}</div>
                  {active ? <div className="theme-gallery-card__status">{t("settings.themeGallery.current")}</div> : null}
                </button>
              );
            })
          )}
        </div>

        <aside className="theme-gallery__detail" aria-live="polite">
          {selectedPack ? (
            <>
              <div className="theme-gallery__detail-preview">
                <ThemePreviewSurface pack={selectedPack} mode={detailMode} scene={detailScene} />
              </div>
              <div className="theme-gallery__detail-meta">
                <h3 className="theme-gallery__detail-name">{packDisplayName(selectedPack, t)}</h3>
                <div className="theme-gallery__detail-tags">
                  <span className="theme-gallery__badge">
                    {themePackKind(selectedPack) === "official"
                      ? t("settings.themeGallery.kindOfficial")
                      : themePackKind(selectedPack) === "base"
                        ? t("settings.themeGallery.kindBase")
                        : t("settings.themeGallery.kindUser")}
                  </span>
                  {selectedPack.license ? <span className="theme-gallery__badge theme-gallery__badge--muted">{selectedPack.license}</span> : null}
                </div>
                <p className="theme-gallery__detail-desc">{packDescription(selectedPack, t)}</p>
                <div className="theme-gallery__detail-swatches" aria-hidden="true">
                  <span style={{ background: selectedPack.tokens?.light?.bg || "#f4f3ef" }} />
                  <span style={{ background: selectedPack.tokens?.light?.accent || "#ccc" }} />
                  <span style={{ background: selectedPack.tokens?.dark?.accent || "#888" }} />
                </div>
                <div className="set-seg theme-gallery__detail-toggles">
                  <button type="button" className={`set-seg__btn${detailMode === "light" ? " set-seg__btn--on" : ""}`} onClick={() => setDetailMode("light")}>
                    {t("settings.themeLight")}
                  </button>
                  <button type="button" className={`set-seg__btn${detailMode === "dark" ? " set-seg__btn--on" : ""}`} onClick={() => setDetailMode("dark")}>
                    {t("settings.themeDark")}
                  </button>
                </div>
                <div className="set-seg theme-gallery__detail-toggles">
                  <button type="button" className={`set-seg__btn${detailScene === "home" ? " set-seg__btn--on" : ""}`} onClick={() => setDetailScene("home")}>
                    {t("settings.themeGallery.sceneHome")}
                  </button>
                  <button type="button" className={`set-seg__btn${detailScene === "task" ? " set-seg__btn--on" : ""}`} onClick={() => setDetailScene("task")}>
                    {t("settings.themeGallery.sceneTask")}
                  </button>
                </div>
                <button type="button" className="btn btn--primary theme-gallery__apply" disabled={busy || isActive} onClick={() => void applySelected()}>
                  {isActive ? t("settings.themeLibrary.active") : t("settings.themeGallery.apply")}
                </button>
                <div className="theme-gallery__detail-actions">
                  <button type="button" className="btn btn--small" disabled={busy} onClick={() => setImmersive(true)}>
                    {t("settings.themeGallery.openPreview")}
                  </button>
                  <button type="button" className="btn btn--small" disabled={busy} onClick={previewSelected}>
                    {t("settings.themeGallery.tempPreview")}
                  </button>
                  <button type="button" className="btn btn--small" disabled={busy} onClick={() => void copySelected()}>
                    <Copy size={12} /> {t("settings.themeLibrary.copyFrom")}
                  </button>
                  {themePackKind(selectedPack) === "user" ? (
                    <div className="theme-gallery__more">
                      <button type="button" className="btn btn--small" onClick={() => setMenuOpen((v) => !v)} aria-expanded={menuOpen}>
                        <MoreHorizontal size={14} />
                      </button>
                      {menuOpen ? (
                        <div className="theme-gallery__menu" role="menu">
                          <button type="button" role="menuitem" onClick={openEdit}>
                            <Pencil size={12} /> {t("settings.themeLibrary.edit")}
                          </button>
                          <button type="button" role="menuitem" onClick={() => void exportSelected()}>
                            <Download size={12} /> {t("settings.themeLibrary.export")}
                          </button>
                          <button type="button" role="menuitem" className="theme-gallery__menu-danger" onClick={() => void removeSelected()}>
                            <Trash2 size={12} /> {t("settings.themeLibrary.delete")}
                          </button>
                        </div>
                      ) : null}
                    </div>
                  ) : null}
                </div>
              </div>
            </>
          ) : (
            <div className="theme-lib-card__sub">{t("settings.themeGallery.selectHint")}</div>
          )}
        </aside>
      </div>

      {editor ? (
        <ThemeEditorInline
          state={editor}
          busy={busy}
          onChange={(patch) => setEditor((s) => (s ? { ...s, ...patch } : s))}
          onCancel={() => setEditor(null)}
          onSave={(activate) => void saveEditor(activate)}
        />
      ) : null}
    </div>
  );
}

function ThemeEditorInline({
  state,
  busy,
  onChange,
  onCancel,
  onSave,
}: {
  state: EditorState;
  busy: boolean;
  onChange: (patch: Partial<EditorState>) => void;
  onCancel: () => void;
  onSave: (activate: boolean) => void;
}) {
  const t = useT();
  return (
    <div className="theme-gallery__editor-overlay" role="dialog" aria-modal="true">
      <div className="theme-editor theme-gallery__editor">
        <strong>{state.mode === "create" ? t("settings.themeLibrary.editorCreate") : t("settings.themeLibrary.editorEdit")}</strong>
        <div className="theme-editor__row">
          <div className="theme-editor__label">{t("settings.themeLibrary.fieldId")}</div>
          <div className="theme-editor__fields">
            <input value={state.id} disabled={state.mode === "edit" || busy} onChange={(e) => onChange({ id: e.target.value })} />
            <input value={state.name} disabled={busy} placeholder={t("settings.themeLibrary.fieldName")} onChange={(e) => onChange({ name: e.target.value })} />
          </div>
        </div>
        <div className="theme-editor__row">
          <div className="theme-editor__label">{t("settings.themeLibrary.fieldBase")}</div>
          <div className="set-seg">
            {THEME_STYLES.map((s) => (
              <button key={s} type="button" className={`set-seg__btn${state.baseStyle === s ? " set-seg__btn--on" : ""}`} disabled={busy} onClick={() => onChange({ baseStyle: s })}>
                {s}
              </button>
            ))}
          </div>
        </div>
        <div className="theme-editor__actions">
          <button type="button" className="btn" disabled={busy} onClick={onCancel}>
            {t("common.cancel")}
          </button>
          <button type="button" className="btn" disabled={busy} onClick={() => onSave(false)}>
            {t("common.save")}
          </button>
          <button type="button" className="btn btn--primary" disabled={busy} onClick={() => onSave(true)}>
            {t("settings.themeGallery.saveAndApply")}
          </button>
        </div>
      </div>
    </div>
  );
}
