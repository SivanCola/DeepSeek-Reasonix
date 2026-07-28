import { afterEach, describe, expect, it, vi } from "vitest";
import {
  cliReleaseChannel,
  desktopReleaseChannel,
  handleCLIRelease,
  handleDesktopReleaseManifest,
} from "./desktop_release";

const manifest = JSON.stringify({
  version: "v1.2.0-preview.7",
  platforms: { "windows-amd64": { url: "https://example.invalid/app.exe" } },
});

const cliAssets = [
  "reasonix-darwin-amd64.tar.gz",
  "reasonix-darwin-arm64.tar.gz",
  "reasonix-linux-amd64.tar.gz",
  "reasonix-linux-arm64.tar.gz",
  "reasonix-windows-amd64.zip",
  "reasonix-windows-arm64.zip",
  "SHA256SUMS",
];
const cliRelease = (tag: string, prerelease: boolean) => ({
  tag_name: tag,
  prerelease,
  html_url: `https://github.com/esengine/DeepSeek-Reasonix/releases/tag/${tag}`,
  assets: cliAssets.map((name) => ({
    name,
    browser_download_url: `https://github.com/esengine/DeepSeek-Reasonix/releases/download/${tag}/${name}`,
    size: 42,
  })),
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("desktop Preview release gateway", () => {
  it("recognizes Preview and the legacy Canary compatibility route", () => {
    expect(desktopReleaseChannel("/v1/desktop/releases/preview/latest.json")).toBe("preview");
    expect(desktopReleaseChannel("/v1/desktop/releases/canary/latest.json")).toBe("canary");
    expect(desktopReleaseChannel("/v1/desktop/releases/rc/latest.json")).toBeNull();
  });

  it("serves Preview from the canonical pointer first", async () => {
    const fetchMock = vi.fn(async (_url: string) => new Response(manifest, { status: 200 }));
    vi.stubGlobal("fetch", fetchMock);

    const response = await handleDesktopReleaseManifest("preview");

    expect(response.status).toBe(200);
    expect(response.headers.get("access-control-allow-origin")).toBe("*");
    expect(response.headers.get("x-reasonix-release-source")).toBe("r2-preview");
    expect(fetchMock).toHaveBeenCalledTimes(1);
    expect(fetchMock.mock.calls[0]?.[0]).toBe("https://dl.reasonix.io/preview/latest.json");
  });

  it("falls back to the mirrored Canary pointer for older deployments", async () => {
    const fetchMock = vi
      .fn(async (_url: string) => new Response("missing", { status: 404 }))
      .mockResolvedValueOnce(new Response("missing", { status: 404 }))
      .mockResolvedValueOnce(new Response(manifest, { status: 200 }));
    vi.stubGlobal("fetch", fetchMock);

    const response = await handleDesktopReleaseManifest("preview");

    expect(response.status).toBe(200);
    expect(response.headers.get("x-reasonix-release-source")).toBe("r2-canary-compat");
    expect(fetchMock.mock.calls.map((call) => call[0])).toEqual([
      "https://dl.reasonix.io/preview/latest.json",
      "https://dl.reasonix.io/canary/latest.json",
    ]);
  });
});

describe("CLI public release gateway", () => {
  it("recognizes only Stable and Preview routes", () => {
    expect(cliReleaseChannel("/v1/cli/releases/stable/latest.json")).toBe("stable");
    expect(cliReleaseChannel("/v1/cli/releases/preview/latest.json")).toBe("preview");
    expect(cliReleaseChannel("/v1/cli/releases/rc/latest.json")).toBeNull();
  });

  it("serves a complete strict Preview pointer from R2", async () => {
    const fetchMock = vi.fn(async (_url: string) =>
      new Response(JSON.stringify(cliRelease("v1.18.0-preview.1", true)), { status: 200 }),
    );
    vi.stubGlobal("fetch", fetchMock);

    const response = await handleCLIRelease("preview");
    const body = await response.json() as { tag_name?: string };

    expect(body.tag_name).toBe("v1.18.0-preview.1");
    expect(response.headers.get("access-control-allow-origin")).toBe("*");
    expect(response.headers.get("x-reasonix-release-source")).toBe("r2-cli-preview");
    expect(fetchMock.mock.calls[0]?.[0]).toBe("https://dl.reasonix.io/cli/preview/latest.json");
  });

  it("falls back to GitHub and strictly filters tag and prerelease metadata", async () => {
    const releases = [
      cliRelease("v1.19.0-rc.1", true),
      cliRelease("v1.18.0-preview.2", true),
      cliRelease("v1.18.0-preview.12", true),
      cliRelease("v1.18.0-preview.13", false),
      cliRelease("v1.17.21", false),
    ];
    const fetchMock = vi
      .fn(async (_url: string) => new Response("missing", { status: 404 }))
      .mockResolvedValueOnce(new Response("missing", { status: 404 }))
      .mockResolvedValueOnce(new Response(JSON.stringify(releases), { status: 200 }));
    vi.stubGlobal("fetch", fetchMock);

    const response = await handleCLIRelease("preview");
    const body = await response.json() as { tag_name?: string };

    expect(body.tag_name).toBe("v1.18.0-preview.12");
    expect(response.headers.get("x-reasonix-release-source")).toBe("github-cli-releases");
    expect(fetchMock.mock.calls[1]?.[0]).toContain("releases?per_page=100");
  });
});
