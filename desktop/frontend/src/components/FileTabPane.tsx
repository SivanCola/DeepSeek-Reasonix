import { useEffect, useMemo, useState } from "react";
import { FileText, MessageSquarePlus } from "lucide-react";
import { app } from "../lib/bridge";
import { useT } from "../lib/i18n";
import type { FilePreview } from "../lib/types";
import { CodeViewer } from "./CodeViewer";
import { Markdown } from "./Markdown";
import { Tooltip } from "./Tooltip";

function basename(path: string): string {
  const parts = path.split("/").filter(Boolean);
  return parts[parts.length - 1] ?? path;
}

function languageFor(path: string): string | undefined {
  const name = basename(path).toLowerCase();
  const ext = name.includes(".") ? name.slice(name.lastIndexOf(".") + 1) : name;
  const byExt: Record<string, string> = {
    css: "css",
    go: "go",
    html: "html",
    js: "javascript",
    json: "json",
    jsx: "jsx",
    md: "markdown",
    py: "python",
    rs: "rust",
    sh: "bash",
    toml: "toml",
    ts: "typescript",
    tsx: "tsx",
    yaml: "yaml",
    yml: "yaml",
  };
  return byExt[ext];
}

function fenceFor(text: string): string {
  let longest = 0;
  for (const match of text.matchAll(/`+/g)) longest = Math.max(longest, match[0].length);
  return "`".repeat(Math.max(3, longest + 1));
}

function formatFileReference(path: string, text: string): string {
  const body = text.replace(/\r\n|\r/g, "\n").trimEnd();
  const fence = fenceFor(body);
  const lang = languageFor(path);
  return `From \`${path}\`:\n\n${fence}${lang ?? ""}\n${body}\n${fence}`;
}

function formatBytes(n: number): string {
  if (n >= 1024 * 1024) return `${(n / (1024 * 1024)).toFixed(1)} MB`;
  if (n >= 1024) return `${Math.ceil(n / 1024)} KB`;
  return `${n} B`;
}

export function FileTabPane({
  path,
  workspaceName,
  onAddToChat,
}: {
  path: string;
  workspaceName?: string;
  onAddToChat?: (text: string) => void;
}) {
  const t = useT();
  const [preview, setPreview] = useState<FilePreview | null>(null);
  const [loading, setLoading] = useState(false);
  const isMarkdown = path.toLowerCase().endsWith(".md");
  const name = useMemo(() => basename(path), [path]);

  useEffect(() => {
    let live = true;
    setLoading(true);
    setPreview(null);
    app
      .ReadFile(path)
      .then((next) => {
        if (live) setPreview(next);
      })
      .catch((err) => {
        if (live) {
          setPreview({
            path,
            body: "",
            size: 0,
            truncated: false,
            binary: false,
            err: String(err?.message ?? err),
          });
        }
      })
      .finally(() => {
        if (live) setLoading(false);
      });
    return () => {
      live = false;
    };
  }, [path]);

  const canAddToChat = !!preview && !preview.err && !preview.binary;

  return (
    <section className="file-tab-pane">
      <header className="file-tab-pane__head">
        <div className="file-tab-pane__identity">
          <FileText size={16} />
          <div>
            <h1>{name}</h1>
            <p>{workspaceName ? `${workspaceName} / ${path}` : path}</p>
          </div>
        </div>
        <div className="file-tab-pane__actions">
          {preview && preview.size > 0 && <span className="file-tab-pane__size">{formatBytes(preview.size)}</span>}
          <Tooltip label={t("workspace.addFileContentToChat")}>
            <button
              type="button"
              className="topicbar__icon-btn"
              disabled={!canAddToChat}
              onClick={() => {
                if (!preview || preview.err || preview.binary) return;
                const suffix = preview.truncated ? `\n\n${t("workspace.truncated")}` : "";
                onAddToChat?.(formatFileReference(path, preview.body) + suffix);
              }}
            >
              <MessageSquarePlus size={15} />
            </button>
          </Tooltip>
        </div>
      </header>
      <main className="file-tab-pane__body">
        {loading ? (
          <div className="workspace-empty">{t("workspace.loading")}</div>
        ) : preview?.err ? (
          <div className="workspace-empty workspace-empty--error">{preview.err}</div>
        ) : preview?.binary ? (
          <div className="workspace-empty">{t("workspace.binary")}</div>
        ) : preview ? (
          <>
            {preview.truncated && <div className="workspace-note">{t("workspace.truncated")}</div>}
            {isMarkdown ? <Markdown text={preview.body} /> : <CodeViewer value={preview.body || " "} language={languageFor(path)} />}
          </>
        ) : null}
      </main>
    </section>
  );
}
