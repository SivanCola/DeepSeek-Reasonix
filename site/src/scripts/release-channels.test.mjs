import { test } from "node:test";
import assert from "node:assert/strict";
import {
  cliReleaseModel,
  desktopReleaseModel,
  fetchFirstJSON,
  selectCLIRelease,
} from "./release-channels.js";

test("CLI channels strictly exclude foreign and internal prereleases", () => {
  const releases = [
    { tag_name: "v1.19.0-rc.1", prerelease: true },
    { tag_name: "v1.18.0-preview.2", prerelease: true },
    { tag_name: "desktop-v1.18.0", prerelease: false },
    { tag_name: "v1.17.21", prerelease: false },
    { tag_name: "v1.18.0-preview.12", prerelease: true },
    { tag_name: "v1.18.0-preview.13", prerelease: false },
  ];
  assert.equal(selectCLIRelease(releases, "stable")?.tag_name, "v1.17.21");
  assert.equal(selectCLIRelease(releases, "preview")?.tag_name, "v1.18.0-preview.12");
  assert.equal(cliReleaseModel(releases, "preview")?.assets.SHA256SUMS,
    "https://github.com/esengine/DeepSeek-Reasonix/releases/download/v1.18.0-preview.12/SHA256SUMS");
});

test("Desktop manifest models preserve channel boundaries and derive sibling assets", () => {
  const manifest = {
    version: "v1.18.0-preview.62",
    platforms: {
      "darwin-arm64": { url: "https://dl.reasonix.io/desktop-preview/Reasonix-darwin-arm64.zip" },
      "darwin-amd64": { url: "https://dl.reasonix.io/desktop-preview/Reasonix-darwin-amd64.zip" },
      "windows-amd64": { url: "https://dl.reasonix.io/desktop-preview/Reasonix-windows-amd64-installer.exe" },
      "windows-arm64": { url: "https://dl.reasonix.io/desktop-preview/Reasonix-windows-arm64-installer.exe" },
      "linux-amd64": { url: "https://dl.reasonix.io/desktop-preview/Reasonix-linux-amd64.tar.gz" },
    },
    native_packages: {
      "linux-amd64": { url: "https://dl.reasonix.io/desktop-preview/Reasonix-linux-amd64.deb" },
    },
  };
  assert.equal(desktopReleaseModel(manifest, "stable"), null);
  const model = desktopReleaseModel(manifest, "preview");
  assert.equal(model?.displayVersion, "1.18.0-preview.62");
  assert.equal(model?.assets["Reasonix-darwin-universal.dmg"],
    "https://dl.reasonix.io/desktop-preview/Reasonix-darwin-universal.dmg");
  assert.equal(model?.assets["Reasonix-windows-amd64.zip"],
    "https://dl.reasonix.io/desktop-preview/Reasonix-windows-amd64.zip");
});

test("release JSON fetch falls back in order", async () => {
  const calls = [];
  const result = await fetchFirstJSON(["https://one.invalid", "https://two.invalid"], async (url) => {
    calls.push(url);
    if (url.includes("one")) return { ok: false, status: 503, statusText: "Unavailable" };
    return { ok: true, json: async () => ({ version: "v1.2.3" }) };
  });
  assert.deepEqual(calls, ["https://one.invalid", "https://two.invalid"]);
  assert.deepEqual(result, { version: "v1.2.3" });
});
