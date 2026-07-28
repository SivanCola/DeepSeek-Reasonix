import { test } from "node:test";
import assert from "node:assert/strict";
import {
  CLI_RELEASE_ASSETS,
  cliUpgradeCommand,
  cliReleaseModel,
  desktopReleaseModel,
  fetchFirstJSON,
  releaseAssetMap,
  selectCLIRelease,
} from "./release-channels.js";

function cliAssets(tag, missing = []) {
  const skip = new Set(missing);
  return CLI_RELEASE_ASSETS.filter((name) => !skip.has(name)).map((name) => ({
    name,
    browser_download_url: `https://github.com/esengine/DeepSeek-Reasonix/releases/download/${tag}/${name}`,
  }));
}

test("CLI channel selector emits the saved-channel upgrade syntax", () => {
  assert.equal(cliUpgradeCommand("stable"), "reasonix upgrade stable");
  assert.equal(cliUpgradeCommand("preview"), "reasonix upgrade preview");
  assert.equal(cliUpgradeCommand("canary"), "reasonix upgrade stable");
});

test("CLI channels strictly exclude foreign and internal prereleases", () => {
  const releases = [
    { tag_name: "v1.19.0-rc.1", prerelease: true, assets: cliAssets("v1.19.0-rc.1") },
    { tag_name: "v1.18.0-preview.2", prerelease: true, assets: cliAssets("v1.18.0-preview.2") },
    { tag_name: "desktop-v1.18.0", prerelease: false, assets: cliAssets("desktop-v1.18.0") },
    { tag_name: "v1.17.21", prerelease: false, assets: cliAssets("v1.17.21") },
    { tag_name: "v1.18.0-preview.12", prerelease: true, assets: cliAssets("v1.18.0-preview.12") },
    { tag_name: "v1.18.0-preview.13", prerelease: false, assets: cliAssets("v1.18.0-preview.13") },
  ];
  assert.equal(selectCLIRelease(releases, "stable")?.tag_name, "v1.17.21");
  assert.equal(selectCLIRelease(releases, "preview")?.tag_name, "v1.18.0-preview.12");
  assert.equal(cliReleaseModel(releases, "preview")?.assets.SHA256SUMS,
    "https://github.com/esengine/DeepSeek-Reasonix/releases/download/v1.18.0-preview.12/SHA256SUMS");
});

test("CLI selection rejects incomplete releases instead of synthesizing asset URLs", () => {
  const releases = [
    {
      tag_name: "v1.20.0",
      prerelease: false,
      assets: cliAssets("v1.20.0", ["reasonix-windows-arm64.zip", "SHA256SUMS"]),
    },
    {
      tag_name: "v1.19.5",
      prerelease: false,
      assets: cliAssets("v1.19.5"),
    },
  ];
  assert.equal(releaseAssetMap(releases[0]), null);
  assert.equal(selectCLIRelease(releases, "stable")?.tag_name, "v1.19.5");
  const model = cliReleaseModel(releases, "stable");
  assert.equal(model?.displayVersion, "1.19.5");
  assert.equal(
    model?.assets["reasonix-windows-arm64.zip"],
    "https://github.com/esengine/DeepSeek-Reasonix/releases/download/v1.19.5/reasonix-windows-arm64.zip",
  );
  assert.equal(cliReleaseModel([releases[0]], "stable"), null);
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
