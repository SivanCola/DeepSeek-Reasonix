import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import App from "./App";
import { LocaleProvider } from "./lib/i18n";
import { initTheme } from "./lib/theme";
import "./styles.css";
import "./v1/styles.css";
import "./v1-compat.css";

// Apply the saved appearance (auto/light/dark) before the first paint.
initTheme();

// Inside the Wails shell, suppress the webview's default right-click menu — its
// Reload / Back / Inspect entries are easy to hit by accident and can reset or
// navigate away from the app. Text inputs keep their native Cut/Copy/Paste menu.
// Left alone in a plain browser (pnpm dev) so devtools stay reachable.
if (typeof window !== "undefined" && window.runtime) {
  window.addEventListener("contextmenu", (e) => {
    const target = e.target as HTMLElement | null;
    if (!target?.closest("input, textarea")) e.preventDefault();
  });
}

const root = document.getElementById("root");
if (!root) throw new Error("missing #root");

createRoot(root).render(
  <StrictMode>
    <LocaleProvider>
      <App />
    </LocaleProvider>
  </StrictMode>,
);
