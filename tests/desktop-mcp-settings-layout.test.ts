import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const cssPath = fileURLToPath(new URL("../desktop/src/styles.css", import.meta.url));
const css = readFileSync(cssPath, "utf8");

function cssRule(selector: string): string {
  const escaped = selector.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  const match = new RegExp(`${escaped}\\s*\\{(?<body>[^}]*)\\}`).exec(css);
  return match?.groups?.body ?? "";
}

describe("desktop MCP settings layout", () => {
  it("keeps long MCP specs inside the card instead of widening the modal", () => {
    expect(cssRule(".scard .mcp-spec-body")).toContain("min-width: 0");
    expect(cssRule(".scard .mcp-spec-body")).toContain("flex: 1 1 auto");
    expect(cssRule(".scard .mcp-spec-summary")).toContain("overflow-wrap: anywhere");
    expect(cssRule(".scard .mcp-spec-summary")).toContain("word-break: break-word");
    expect(cssRule(".scard .mcp-remove")).toContain("flex: 0 0 auto");
  });

  it("keeps settings rows and control groups on the shared layout primitives", () => {
    expect(cssRule(".setting-row")).toContain("grid-template-columns");
    expect(cssRule(".setting-row")).toContain("minmax(180px, 0.85fr)");
    expect(cssRule(".settings-body")).toContain("overflow-x: hidden");
    expect(cssRule(".settings-control-group")).toContain("display: inline-flex");
    expect(cssRule(".settings-control-group")).toContain("width: 100%");
    expect(cssRule(".settings-control-group")).toContain("flex-wrap: wrap");
  });

  it("renders settings status as badges instead of action buttons", () => {
    expect(cssRule(".settings-status-badge")).toContain("border-radius: 999px");
    expect(cssRule(".settings-status-badge")).toContain("white-space: nowrap");
    expect(cssRule('.settings-status-badge[data-tone="success"]')).toContain("var(--success)");
    expect(cssRule('.settings-status-badge[data-tone="danger"]')).toContain("var(--danger)");
  });

  it("keeps theme cards grouped and responsive", () => {
    expect(cssRule(".theme-style-control")).toContain("display: grid");
    expect(cssRule(".theme-style-control")).toContain("max-width: 520px");
    expect(cssRule(".theme-style-control")).toContain("width: 100%");
    expect(cssRule(".theme-mode-bar")).toContain("justify-content: space-between");
    expect(cssRule(".style-grid")).toContain("grid-template-columns: repeat(2, minmax(0, 1fr))");
    expect(cssRule(".style-card")).toContain("min-width: 0");
    expect(cssRule(".style-card")).toContain("border-radius: 8px");
  });

  it("keeps API credentials on a two-line control layout", () => {
    expect(cssRule(".credential-row-control")).toContain("display: grid");
    expect(cssRule(".credential-row-control")).toContain("max-width: 520px");
    expect(cssRule(".credential-row-control")).toContain("width: 100%");
    expect(cssRule(".credential-primary-line")).toContain(
      "grid-template-columns: minmax(0, 1fr) auto auto",
    );
    expect(cssRule(".credential-meta-line")).toContain("justify-content: flex-start");
  });

  it("keeps update notices away from the settings modal header", () => {
    expect(cssRule(".update-overlay")).toContain("right: 24px");
    expect(cssRule(".update-overlay")).toContain("bottom: 24px");
    expect(cssRule(".update-overlay")).not.toContain("top: 16px");
  });
});
