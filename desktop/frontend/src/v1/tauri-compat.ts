import { app, openExternal } from "../lib/bridge";

type DialogOptions = {
  directory?: boolean;
  multiple?: boolean;
  defaultPath?: string;
  filters?: Array<{ name: string; extensions: string[] }>;
};

type UpdateInfo = {
  version: string;
};

function bytesToDataURL(bytes: ArrayBuffer, extension?: string): Promise<string> {
  const mime = extension ? `image/${extension === "jpg" ? "jpeg" : extension}` : "image/png";
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => resolve(typeof reader.result === "string" ? reader.result : "");
    reader.onerror = () => reject(reader.error ?? new Error("failed to read image"));
    reader.readAsDataURL(new Blob([bytes], { type: mime }));
  });
}

export async function invoke<T = unknown>(command: string, args?: Record<string, unknown>): Promise<T> {
  if (command === "save_clipboard_image") {
    const bytes = args?.bytes;
    const extension = typeof args?.extension === "string" ? args.extension : undefined;
    if (!(bytes instanceof ArrayBuffer)) return "" as T;
    const dataUrl = await bytesToDataURL(bytes, extension);
    return app.SavePastedImage(dataUrl) as Promise<T>;
  }
  if (command === "open_in_editor") {
    const editor = typeof args?.command === "string" ? args.command : "";
    const path = typeof args?.path === "string" ? args.path : "";
    const line = typeof args?.line === "number" ? args.line : 0;
    if (path) await app.OpenInEditor(editor, path, line).catch(() => app.OpenPath(path));
    return undefined as T;
  }
  return undefined as T;
}

export async function open(options?: DialogOptions): Promise<string | null> {
  if (options?.directory) {
    const picked = await app.PickWorkspace().catch(() => "");
    return picked || null;
  }
  const picked = await app.PickFile(options?.filters, options?.defaultPath).catch(() => "");
  return picked || null;
}

export async function openPath(path: string): Promise<void> {
  await app.OpenPath(path);
}

export async function version(): Promise<string> {
  return app.Version().catch(() => APP_VERSION);
}

export async function openUrl(url: string): Promise<void> {
  openExternal(url);
}

export async function check(): Promise<UpdateInfo | null> {
  const update = await app.CheckUpdate().catch(() => null);
  if (!update?.available) return null;
  return { version: update.latest };
}

export const APP_VERSION = "v2-wails";
