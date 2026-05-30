import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { fileURLToPath } from "node:url";
import react from "@vitejs/plugin-react";
import { type Plugin, defineConfig } from "vite";

const pkg = JSON.parse(
  readFileSync(fileURLToPath(new URL("./package.json", import.meta.url)), "utf8"),
) as { version: string };

/**
 * Strip variable-length lookbehind from mdast-util-gfm-autolink-literal's
 * email regex. Tauri's WKWebView on macOS Monterey (Safari < 16.4) can't
 * parse `(?<=^|\s|\p{P}|\p{S})` — the bundle fails to load with an "invalid
 * group specifier name" SyntaxError before any script runs. The lookbehind
 * was just a fast-path; the package's `previous()` check still filters
 * neighbours after the match. Issue #1209.
 */
function patchGfmAutolinkLookbehind(): Plugin {
  return {
    name: "patch-gfm-autolink-lookbehind",
    enforce: "pre",
    transform(code, id) {
      if (!id.includes("mdast-util-gfm-autolink-literal")) return null;
      if (!code.includes("(?<=^|\\s|\\p{P}|\\p{S})")) return null;
      return {
        code: code.replace("(?<=^|\\s|\\p{P}|\\p{S})", ""),
        map: null,
      };
    },
  };
}

export default defineConfig({
  plugins: [react(), patchGfmAutolinkLookbehind()],
  clearScreen: false,
  server: {
    host: "127.0.0.1",
    port: 1420,
    strictPort: true,
    watch: {
      ignored: ["**/src-tauri/target/**"],
    },
  },
  envPrefix: ["VITE_", "TAURI_"],
  define: {
    __APP_VERSION__: JSON.stringify(pkg.version),
  },
  resolve: {
    alias: {
      "@reasonix/core-utils/compaction": resolve(
        __dirname,
        "../packages/core-utils/src/compaction.ts",
      ),
      "@reasonix/core-utils/derive-prefix": resolve(
        __dirname,
        "../packages/core-utils/src/derive-prefix.ts",
      ),
      "@reasonix/core-utils": resolve(__dirname, "../packages/core-utils/src/index.ts"),
    },
  },
  build: {
    target: "es2022",
    minify: "esbuild",
    sourcemap: true,
    rollupOptions: {
      output: {
        manualChunks(id) {
          if (!id.includes("node_modules")) return undefined;
          if (id.includes("/react/") || id.includes("/react-dom/")) return "vendor-react";
          if (id.includes("/@tauri-apps/")) return "vendor-tauri";
          if (id.includes("/lucide-react/") || id.includes("/react-virtuoso/")) return "vendor-ui";
          if (id.includes("/prism-react-renderer/")) return "vendor-code";
          if (
            id.includes("/react-markdown/") ||
            id.includes("/remark-") ||
            id.includes("/rehype-") ||
            id.includes("/katex/") ||
            id.includes("/unified/") ||
            id.includes("/mdast-") ||
            id.includes("/hast-") ||
            id.includes("/micromark")
          ) {
            return "vendor-markdown";
          }
          return undefined;
        },
      },
    },
  },
});
