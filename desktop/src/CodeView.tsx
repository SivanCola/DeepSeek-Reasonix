import { Highlight, type PrismTheme } from "prism-react-renderer";
import { useEffect, useRef, useState } from "react";

const DARK_THEME: PrismTheme = {
  plain: { color: "#dde1ea", backgroundColor: "transparent" },
  styles: [
    {
      types: ["comment", "prolog", "doctype", "cdata"],
      style: { color: "#6d6e80", fontStyle: "italic" },
    },
    { types: ["punctuation"], style: { color: "#a8a9b8" } },
    {
      types: ["property", "tag", "boolean", "number", "constant", "symbol", "deleted"],
      style: { color: "#fbbf24" },
    },
    {
      types: ["selector", "attr-name", "string", "char", "builtin", "inserted"],
      style: { color: "#86dcb1" },
    },
    { types: ["operator", "entity", "url"], style: { color: "#84b9e8" } },
    { types: ["atrule", "attr-value", "keyword"], style: { color: "#b4a8f0" } },
    {
      types: ["function", "class-name", "maybe-class-name"],
      style: { color: "#84b9e8", fontWeight: "500" },
    },
    { types: ["regex", "important", "variable"], style: { color: "#f0c062" } },
    { types: ["important", "bold"], style: { fontWeight: "bold" } },
    { types: ["italic"], style: { fontStyle: "italic" } },
  ],
};

const LIGHT_THEME: PrismTheme = {
  plain: { color: "#24292e", backgroundColor: "transparent" },
  styles: [
    {
      types: ["comment", "prolog", "doctype", "cdata"],
      style: { color: "#6a737d", fontStyle: "italic" },
    },
    { types: ["punctuation"], style: { color: "#24292e" } },
    {
      types: ["property", "tag", "boolean", "number", "constant", "symbol", "deleted"],
      style: { color: "#d73a49" },
    },
    {
      types: ["selector", "attr-name", "string", "char", "builtin", "inserted"],
      style: { color: "#032f62" },
    },
    { types: ["operator", "entity", "url"], style: { color: "#d73a49" } },
    { types: ["atrule", "attr-value", "keyword"], style: { color: "#d73a49" } },
    {
      types: ["function", "class-name", "maybe-class-name"],
      style: { color: "#6f42c1", fontWeight: "500" },
    },
    { types: ["regex", "important", "variable"], style: { color: "#e36209" } },
    { types: ["important", "bold"], style: { fontWeight: "bold" } },
    { types: ["italic"], style: { fontStyle: "italic" } },
  ],
};

function usePrismTheme(): PrismTheme {
  const [theme, setTheme] = useState<"dark" | "light">(() =>
    document.documentElement.dataset.theme === "light" ? "light" : "dark",
  );
  const prevRef = useRef(theme);
  useEffect(() => {
    const el = document.documentElement;
    const cb = () => {
      const t = el.dataset.theme === "light" ? "light" : "dark";
      if (t !== prevRef.current) {
        prevRef.current = t;
        setTheme(t);
      }
    };
    const mo = new MutationObserver(cb);
    mo.observe(el, { attributes: true, attributeFilter: ["data-theme"] });
    return () => mo.disconnect();
  }, []);
  return theme === "dark" ? DARK_THEME : LIGHT_THEME;
}

export const PRISM_THEME = DARK_THEME;

function hashString(value: string): string {
  let hash = 0;
  for (let i = 0; i < value.length; i++) {
    hash = (hash * 31 + value.charCodeAt(i)) | 0;
  }
  return `${value.length}-${(hash >>> 0).toString(36)}`;
}

function keyed<T>(items: readonly T[], keyFor: (item: T) => string): { item: T; key: string }[] {
  const seen = new Map<string, number>();
  return items.map((item) => {
    const base = keyFor(item);
    const count = seen.get(base) ?? 0;
    seen.set(base, count + 1);
    return { item, key: count === 0 ? base : `${base}-${count}` };
  });
}

const EXTS: Record<string, string> = {
  ts: "typescript",
  tsx: "tsx",
  mts: "typescript",
  cts: "typescript",
  js: "javascript",
  jsx: "jsx",
  mjs: "javascript",
  cjs: "javascript",
  py: "python",
  pyi: "python",
  rs: "rust",
  go: "go",
  json: "json",
  jsonc: "json",
  md: "markdown",
  mdx: "markdown",
  css: "css",
  scss: "scss",
  less: "less",
  html: "markup",
  htm: "markup",
  xml: "markup",
  svg: "markup",
  yaml: "yaml",
  yml: "yaml",
  toml: "toml",
  sh: "bash",
  bash: "bash",
  zsh: "bash",
  fish: "bash",
  sql: "sql",
  rb: "ruby",
  java: "java",
  kt: "kotlin",
  swift: "swift",
  c: "c",
  h: "c",
  cpp: "cpp",
  cc: "cpp",
  cxx: "cpp",
  hpp: "cpp",
  hxx: "cpp",
  cs: "csharp",
  php: "php",
  lua: "lua",
  dart: "dart",
  ex: "elixir",
  exs: "elixir",
  erl: "erlang",
  hs: "haskell",
  clj: "clojure",
  cljs: "clojure",
  zig: "zig",
  vue: "markup",
  svelte: "markup",
  graphql: "graphql",
  gql: "graphql",
  proto: "protobuf",
  dockerfile: "docker",
};

export function langFromPath(path: string | null | undefined): string | null {
  if (!path) return null;
  const base = path.split(/[\\/]/).pop() ?? "";
  if (base.toLowerCase() === "dockerfile") return "docker";
  if (base.toLowerCase() === "makefile") return "makefile";
  const m = /\.([a-z0-9]+)$/i.exec(base);
  if (!m) return null;
  return EXTS[m[1].toLowerCase()] ?? null;
}

export function CodeView({
  text,
  lang,
  startLine = 1,
  showLineNumbers = true,
}: {
  text: string;
  lang: string;
  startLine?: number;
  showLineNumbers?: boolean;
}) {
  const theme = usePrismTheme();
  return (
    <Highlight theme={theme} code={text} language={lang}>
      {({ className, tokens, getLineProps, getTokenProps }) => (
        <pre className={`codeview ${className}`}>
          {keyed(tokens, (line) =>
            hashString(line.map((token) => `${token.types.join(".")}:${token.content}`).join("|")),
          ).map(({ item: line, key }, i) => (
            <div key={key} {...getLineProps({ line })} className="codeview-line">
              {showLineNumbers && <span className="codeview-line-num">{i + startLine}</span>}
              <span className="codeview-line-content">
                {keyed(line, (token) =>
                  hashString(`${token.types.join(".")}:${token.content}`),
                ).map(({ item: token, key: tokenKey }) => (
                  <span key={tokenKey} {...getTokenProps({ token })} />
                ))}
              </span>
            </div>
          ))}
        </pre>
      )}
    </Highlight>
  );
}

export function CollapsibleCode({
  text,
  lang,
  startLine = 1,
  maxLines = 20,
}: {
  text: string;
  lang: string;
  startLine?: number;
  maxLines?: number;
}) {
  const [open, setOpen] = useState(false);
  const lines = text.split("\n");
  const tooLong = lines.length > maxLines;
  const shown = open || !tooLong ? text : lines.slice(0, maxLines).join("\n");
  return (
    <>
      <CodeView text={shown} lang={lang} startLine={startLine} />
      {tooLong && (
        <button type="button" className="tool-more" onClick={() => setOpen((v) => !v)}>
          {open ? "less" : `+ ${lines.length - maxLines} more lines`}
        </button>
      )}
    </>
  );
}
