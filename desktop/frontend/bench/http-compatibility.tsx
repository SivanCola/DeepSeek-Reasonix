import React, { useState } from "react";
import { createRoot } from "react-dom/client";
import { ErrorMessage } from "../src/components/ErrorMessage";
import { ProviderHTTPCompatibilityActions } from "../src/components/ProviderHTTPCompatibilityActions";
import { ProviderEditor } from "../src/components/SettingsPanel";
import { LocaleProvider, preloadLocale } from "../src/lib/i18n";
import { installDesktopHostStub } from "../src/__tests__/desktopHostStub";
import type { ModelSettingsChange, ProviderView } from "../src/lib/types";
import "../src/styles.css";

// Local-only browser fixture: no credentials or live model requests.
let provider: ProviderView = {
  name:"fixture", displayName:"deepseek · 1", kind:"responses", baseUrl:"https://api.deepseek.com", requestUrl:"https://api.deepseek.com/responses",
  builtIn:false, added:true, models:["deepseek-flash"], default:"deepseek-flash", visionModels:[], visionModelsConfigured:false,
  apiKeyEnv:"FIXTURE_KEY", keySet:true, modelsUrl:"", balanceUrl:"", contextWindow:1_000_000,
  reasoningProtocol:"", thinking:"", supportedEfforts:[], defaultEffort:"", http1Only:false,
};
let revision=0;
let publish: (value: ProviderView) => void = () => {};
installDesktopHostStub({
  Settings: async () => ({ providers:[provider], modelSettingsFingerprint:`fixture-${revision}` }),
  ApplyModelSettings: async (change:ModelSettingsChange) => {
    if(change.kind!=="http1_compatibility" || change.expectedFingerprint!==`fixture-${revision}`) throw new Error("unexpected fixture edit");
    provider={...provider,http1Only:change.enabled};revision++;publish(provider);
    return {requestId:change.requestId,persisted:true,revision:`fixture-${revision}`,application:"applied",targets:[],issues:[],appliedCatalogs:[]};
  },
});
await preloadLocale("zh");
localStorage.setItem("reasonix:locale", "zh");
const diagnostic={kind:"transport_protocol",providerId:"fixture",transportCode:"PROTOCOL_ERROR"};
function Fixture(){
  const [entry,setEntry]=useState(provider);
  const [saved,setSaved]=useState("");
  publish=setEntry;
  return <main style={{maxWidth:1400,margin:"32px auto",padding:24}}>
    <h1>连接兼容模式</h1><p>实际组件验证 · 本地模拟连接</p>
    <div style={{display:"grid",gridTemplateColumns:"repeat(auto-fit, minmax(min(100%, 480px), 1fr))",gap:32,alignItems:"start"}}>
      <section className="chat-notice" style={{padding:24,border:"1px solid var(--border)",borderRadius:12}}>
        <h2 style={{fontSize:18}}>请求失败</h2>
        <ErrorMessage error="deepseek · 1 · Responses: The provider request failed." diagnostic={diagnostic}/>
        <ProviderHTTPCompatibilityActions diagnostic={diagnostic} tabId="fixture-tab" hostId="local"/>
      </section>
      <section style={{padding:24,border:"1px solid var(--border)",borderRadius:12}}>
        <h2 style={{fontSize:18}}>deepseek · 1</h2>
        <ProviderEditor key={revision} initial={entry} hideConnectionName kinds={["openai","responses","anthropic"]} busy={false} onCancel={()=>{}} onSave={async value=>{provider=value;setEntry(value);setSaved(value.http1Only?"已保存 HTTP/1.1":"已恢复自动协商");}}/>
        <p role="status">{saved}</p>
      </section>
    </div>
  </main>;
}
createRoot(document.getElementById("root")!).render(<LocaleProvider><Fixture/></LocaleProvider>);
