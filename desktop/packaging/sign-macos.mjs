#!/usr/bin/env node
// Sign the final bundle, after desktop-build.sh adds the Go service and CLI.
// codesign --deep does not discover all code in Resources or nested frameworks.
import { sign } from "@electron/osx-sign";
import { resolve } from "node:path";
import { fileURLToPath } from "node:url";

export async function signMacOS(app, identity) {
  if (!app || !identity) throw new Error("app and signing identity are required");
  const adhoc = identity === "-";
  await sign({
    app: resolve(app),
    identity,
    identityValidation: !adhoc,
    platform: "darwin",
    type: "distribution",
    preAutoEntitlements: false,
    preEmbedProvisioningProfile: false,
    strictVerify: true,
    optionsForFile: () => ({
      entitlements: fileURLToPath(new URL("../build/darwin/entitlements.plist", import.meta.url)),
      hardenedRuntime: true,
      ...(adhoc ? { timestamp: "none" } : {}),
    }),
  });
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  await signMacOS(...process.argv.slice(2));
}
