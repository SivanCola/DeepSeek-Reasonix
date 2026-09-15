import assert from "node:assert/strict";
import React from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { JSDOM } from "jsdom";
import { AppBottomRegions } from "../app-shell/AppBottomRegions";
import { register } from "node:module";
import type { Translator } from "../lib/i18n";

const noop = () => {};
register(new URL("../../scripts/svg-loader.mjs", import.meta.url));
const { SidebarRegion } = await import("../app-shell/SidebarRegion");
const t = ((key: string) => key) as Translator;
for (const automation of [false, true]) {
  const markup = renderToStaticMarkup(<>
    <SidebarRegion className="sidebar" collapsed={false} t={t}
      onNewSession={noop} onOpenTrash={noop} onOpenAutomation={noop} onOpenSettings={noop}
      resize={{ min: 180, max: 400, value: 240, onPointerDown: noop, onKeyDown: noop, onReset: noop }}
      projectTree={{ onOpenTopic: noop, onCreateTopic: noop, onTopicsChanged: noop }} />
    <AppBottomRegions terminal={{ surfaceVisible: !automation, open: !automation,
      contentVisible: false, remoteSurface: false, t,
      panel: { tabId: "tab", open: !automation, onClose: noop },
      resizer: { min: 100, max: 400, value: 240, onPointerDown: noop, onKeyDown: noop, onReset: noop },
    }} />
  </>);
  const dom = new JSDOM(markup);
  const doc = dom.window.document;
  assert.equal(doc.querySelectorAll(".terminal-drawer").length, 1, "terminal host survives page projection");
  assert.equal(doc.querySelector(".terminal-drawer")?.hasAttribute("inert"), automation);
  assert.equal(doc.querySelectorAll(".terminal-drawer-resizer").length, automation ? 0 : 1);
  assert.equal(doc.querySelectorAll(".sidebar-collapse-toggle").length, 0, "the workbench sidebar has no collapse toggle");
  const automationButtons = [...doc.querySelectorAll("button")].filter(button => button.querySelector(".lucide-alarm-clock"));
  assert.equal(automationButtons.length, 1, "the workbench sidebar keeps exactly one Automation entry");
  const trashButtons = [...doc.querySelectorAll("button")].filter(button => button.querySelector(".lucide-trash-2"));
  assert.equal(trashButtons.length, 1, "the workbench sidebar keeps the Trash entry");
  dom.window.close();
}
console.log("automation regions: shared layout page projection passed");
