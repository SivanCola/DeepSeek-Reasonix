// @vitest-environment jsdom
import { describe, expect, it } from "vitest";
import { MockHub } from "./mock_hub";
import { builtinCompositions } from "./composition";
import { MockCompositions } from "./mock_composition";

describe("remote composition ownership", () => {
  it("uses each remote host's own catalog and selected composition", async () => {
    const hub = new MockHub();
    const local = await hub.open({root:"/same"});
    const remote = await hub.openRemote({host:"gpu-box",workspace:"/same",agentComposition:"builtin:minimal"});
    const another = await hub.openRemote({host:"builder",workspace:"/same"});
    const custom = {...builtinCompositions()[0],id:"user:only-remote",name:"Remote only"};
    await hub.portFor(remote).saveAgentPreset(custom,"");
    expect(remote.agentComposition?.id).toBe("builtin:minimal");
    expect((await hub.portFor(local).agentPresets()).presets.some(v=>v.definition.id===custom.id)).toBe(false);
    expect((await hub.portFor(another).agentPresets()).presets.some(v=>v.definition.id===custom.id)).toBe(false);
    const sibling = await hub.openRemote({host:"gpu-box",workspace:"/same",agentComposition:custom.id});
    expect(sibling.agentComposition?.id).toBe(custom.id);
  });
});

describe("project composition authority", () => {
  it.each([
    ["mcp", {args:["other-package"]}],
    ["mcp", {url:"https://other.invalid/mcp"}],
    ["mcp", {env:{PATH:"${OTHER_PATH}"}}],
    ["mcp", {headers:{Authorization:"${OTHER_TOKEN}"}}],
    ["extensions", {args:["other-script"]}],
    ["extensions", {env:{NODE_OPTIONS:"${OTHER_OPTIONS}"}}],
  ] as const)("rejects project %s launch overrides on save and import", async (kind, parameters) => {
    const presets = new MockCompositions();
    const definition = {...builtinCompositions()[0],id:"project:launch",[kind]:{mode:"select",members:[{id:"authorized",parameters}]}};
    await expect(presets.saveAgentPreset(definition,"")).rejects.toMatchObject({reason:{code:"agent_composition.invalid"}});
    await expect(presets.importAgentPreset(definition)).rejects.toMatchObject({reason:{code:"agent_composition.invalid"}});
    await expect(presets.saveAgentPreset({...definition,id:"user:launch"},"")).resolves.toMatchObject({definition:{id:"user:launch"}});
  });

  it("allows project timeout adjustments", async () => {
    const presets = new MockCompositions();
    const definition = {...builtinCompositions()[0],id:"project:timeouts",mcp:{mode:"select" as const,members:[{id:"authorized",parameters:{callTimeoutSeconds:5,startupTimeoutSeconds:10}}]}};
    await expect(presets.saveAgentPreset(definition,"")).resolves.toMatchObject({definition});
    await expect(presets.importAgentPreset(definition)).resolves.toMatchObject({definition});
  });

  it("refuses a selection whose module is missing and says which one", async () => {
    const presets = new MockCompositions();
    const definition = {...builtinCompositions()[0],id:"user:gone",tools:{mode:"select" as const,members:[{id:"no_such_tool"}]}};
    await presets.saveAgentPreset(definition,"");
    await expect(presets.selectAgentComposition("user:gone")).rejects.toMatchObject({reason:{code:"agent_composition.unavailable",params:{module:"tools:no_such_tool"}}});
  });
});
