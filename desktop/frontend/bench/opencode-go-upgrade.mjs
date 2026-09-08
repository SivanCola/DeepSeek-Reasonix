#!/usr/bin/env node
import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

// Run against a Vite dev server so the fixture can use the same public bridge
// exports as the application. Backend migration and controller tests run in Go;
// this fixture verifies real browser interactions and terminal failure states.
const root=path.resolve(path.dirname(fileURLToPath(import.meta.url)),"..");
process.env.PLAYWRIGHT_BROWSERS_PATH ||= path.join(root,".pw-browsers");
const {chromium}=await import("playwright");
const base=process.env.REASONIX_OPENCODE_BROWSER_URL ?? "http://127.0.0.1:5279";
const methods=new Set();
for(const file of fs.readdirSync(path.join(root,"src/lib"))) {
  if(!/\.tsx?$/.test(file)) continue;
  const source=fs.readFileSync(path.join(root,"src/lib",file),"utf8");
  for(const match of source.matchAll(/\b([A-Z][A-Za-z0-9_]*)\??\s*\(/g)) methods.add(match[1]);
}
const browser=await chromium.launch({headless:true});
try {
  const page=await browser.newPage({locale:"en-US",viewport:{width:1500,height:1000}});
  const errors=[];
  page.on("pageerror",error=>errors.push(error.message));
  await page.goto(`${base}/?mock=deepseek_upgrade&bench=1`);
  await page.locator("textarea.composer__input:not([aria-hidden=true])").waitFor();
  await page.evaluate(async methodNames=>{
    // A long-running Vite server can stamp the application's bridge import
    // after HMR. Reuse that exact module so its event listeners are shared.
    const bridgeURL=performance.getEntriesByType("resource").map(entry=>entry.name).find(url=>new URL(url).pathname==="/src/lib/bridge.ts") ?? "/src/lib/bridge.ts";
    const bridge=await import(bridgeURL);
    const original={};
    for(const method of methodNames) if(typeof bridge.app[method]==="function") original[method]=bridge.app[method];
    const settings=await original.Settings();
    const tabs=await original.ListTabs();
    const tabId=tabs.find(tab=>tab.active).id;
    const baseMeta=await original.MetaForTab(tabId);
    const ids=["deepseek-v4-flash","deepseek-v4-pro","deepseek-v4-flash-vision-exp"];
    const options=["disabled","low","high","max"].map(id=>({id,name:id}));
    let selected=ids[0], effort="auto", failure="";
    const resolved=()=>({apiFormat:"openai",protocol:"deepseek",selected:effort,effective:effort==="auto"?"high":effort,default:"high",options});
    let connection={...settings.providers[0],name:"go",builtIn:false,displayName:"OpenCode Go validation",baseUrl:"https://opencode.ai/zen/go/v1",requestUrl:"",models:ids,default:ids[0],apiKeyEnv:"FIXTURE_KEY",headers:{},thinking:"enabled",reasoningProtocol:"deepseek",effort:"max",webSearch:false,serverWebSearchCapability:false,supportedEfforts:options.map(o=>o.id),defaultEffort:"high",modelCapabilities:ids.map(model=>({model,state:model.includes("vision")?"supported":"unsupported",source:"builtin",resolvedReasoning:resolved()}))};
    const state={saved:[],efforts:[],retries:0,tabId};
    const meta=()=>({...baseMeta,label:selected,ready:!failure,startupErr:failure});
    const push=()=>bridge.__emitMockTabMeta({tabId,meta:meta()});
    const catalog=()=>ids.map(model=>({ref:`go/${model}`,provider:"go",model,displayName:"OpenCode Go validation",current:model===selected,contextWindow:1000000,vision:model.includes("vision")}));
    const effortView=()=>({supported:true,current:effort,default:"high",levels:["auto",...options.map(o=>o.id)],options,resolved:resolved()});
    const bindings={...original,
      Settings:async()=>({...settings,providers:[connection],defaultModel:`go/${selected}`,plannerModel:"go/deepseek-v4-pro",officialProviders:[],providerPresets:[]}),
      Meta:async()=>meta(),MetaForTab:async()=>meta(),
      Models:async()=>catalog(),ModelsForTab:async()=>catalog(),
      Effort:async()=>effortView(),EffortForTab:async()=>effortView(),
      SetEffortForTab:async(_tabId,next)=>{state.efforts.push(next);effort=next;push();},
      SetModelForTab:async(_tabId,ref)=>{selected=ref.split("/")[1];push();},
      SaveProvider:async value=>{state.saved.push(value);connection={...connection,...value};},
      SaveProviderWithKey:async(value,key)=>{if(key)throw Error("fixture must not write a credential");state.saved.push(value);connection={...connection,...value};return connection.name;},
      ReloadRuntime:async()=>{state.retries++;failure="";push();},
    };
    window.go={main:{App:bindings}};
    state.fail=()=>{failure='planner model "go/deepseek-v4-pro": effort="invalid" (source=provider, API=openai, supported=[disabled low high max]): UNSUPPORTED_REASONING_EFFORT';push();};
    window.__opencodeValidation=state;
    push();
  },[...methods]);
  // Selecting a model refreshes the same EffortForTab contract consumed by the
  // actual Composer control, including the inherited value of auto.
  await page.getByRole("button",{name:/^(DeepSeek-R1|deepseek-v4-flash) ·/}).click();
  await page.getByRole("option").filter({hasText:"deepseek-v4-pro"}).click();
  await page.getByRole("button",{name:/Reasoning effort:.*auto.*high/i}).waitFor();
  await page.getByRole("button",{name:/Reasoning effort:/i}).click();
  await page.getByRole("menuitemradio",{name:"disabled",exact:true}).click();
  await page.getByRole("button",{name:/Reasoning effort: disabled/i}).waitFor();
  assert.deepEqual(await page.evaluate(()=>window.__opencodeValidation.efforts),["disabled"]);
  console.log("PASS Composer auto inheritance and disabled switch");

  await page.getByRole("button",{name:"Settings",exact:true}).click();
  await page.getByRole("button",{name:"Model services",exact:true}).click();
  const endpoint=page.getByRole("textbox",{name:"API address",exact:true});
  await endpoint.waitFor();
  await page.locator(".provider-editor-advanced > summary").click();
  const capabilitySummary=page.getByLabel("Reasoning capability of the saved configuration",{exact:true});
  await capabilitySummary.waitFor();
  assert.match(await capabilitySummary.innerText(),/deepseek-v4-flash.*openai.*disabled.*low \/ high \/ max/);
  await page.locator(".provider-editor-advanced > summary").click();
  console.log("PASS saved model reasoning capability survives settings normalization");
  await page.getByRole("button",{name:"API format",exact:true}).click();
  await page.getByRole("option").filter({hasText:"Anthropic"}).click();
  await endpoint.fill("https://opencode.ai/zen/go/v1/messages");
  await page.getByRole("button",{name:"Save changes",exact:true}).click();
  await page.waitForFunction(()=>window.__opencodeValidation.saved.length===1);
  const saved=await page.evaluate(()=>window.__opencodeValidation.saved[0]);
  assert.equal(saved.kind,"anthropic");
  assert.equal(saved.requestUrl,"https://opencode.ai/zen/go/v1/messages");
  assert.ok(saved.models.includes("deepseek-v4-flash-vision-exp"));
  console.log("PASS model connection save retains protocol and model IDs");
  await page.getByRole("button",{name:"Back to workspace",exact:true}).click();
  await page.getByRole("button",{name:"Back to workspace",exact:true}).waitFor({state:"hidden"});
  await page.locator("textarea.composer__input:not([aria-hidden=true])").waitFor();
  await page.evaluate(()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve))));
  await page.evaluate(()=>window.__opencodeValidation.fail());
  await page.getByRole("alert").filter({hasText:"UNSUPPORTED_REASONING_EFFORT"}).waitFor();
  assert.equal(await page.locator('[data-navigation-pending="true"]').count(),0);
  await page.getByRole("button",{name:"Open model settings",exact:true}).click();
  await page.getByRole("textbox",{name:"API address",exact:true}).waitFor();
  await page.getByRole("button",{name:"Back to workspace",exact:true}).click();
  await page.getByRole("button",{name:"Retry",exact:true}).click();
  await page.getByRole("alert").filter({hasText:"UNSUPPORTED_REASONING_EFFORT"}).waitFor({state:"hidden"});
  assert.equal(await page.evaluate(()=>window.__opencodeValidation.retries),1);
  assert.deepEqual(errors,[]);
  console.log("PASS startup failure opens model settings and retry restores ready state");
} finally { await browser.close(); }
