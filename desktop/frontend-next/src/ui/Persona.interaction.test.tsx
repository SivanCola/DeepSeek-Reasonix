// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import "./testkit";
import { Persona } from "./Persona";
import { HttpError, type AgentPort } from "../port/port";
import type { PersonaDraft, PersonaSettings } from "../port/boundary";

afterEach(cleanup);

const base: PersonaSettings = {
  prefix: "", suffix: "", include_runtime_context: true,
  path: "/u/config.toml",
  presets: [
    { id: "engineer", prefix: "Rigorous engineer prefix.", suffix: "", include_runtime_context: true },
    { id: "concise", prefix: "", suffix: "Be terse.", include_runtime_context: false },
  ],
};

function portWith(initial: PersonaSettings = base) {
  return {
    persona: vi.fn(async () => initial),
    savePersonaSettings: vi.fn(async (s: PersonaDraft) => ({ ...initial, ...s })),
  } as unknown as AgentPort & { savePersonaSettings: ReturnType<typeof vi.fn> };
}

const field = (name: string) => screen.findByRole<HTMLTextAreaElement>("textbox", { name });
const saveButton = () => screen.getByRole<HTMLButtonElement>("button", { name: "保存" });
const now = (container: HTMLElement) => container.querySelector(".grp .grp-hd .now")?.textContent ?? null;

describe("the system persona page", () => {
  it("renders the presets the kernel serves and fills both boxes from one", async () => {
    const { container } = render(<Persona port={portWith()} />);
    expect(await screen.findByRole("button", { name: "严谨工程师" })).toBeTruthy();
    expect(screen.getByRole("button", { name: "简洁" })).toBeTruthy();

    await userEvent.click(screen.getByRole("button", { name: "严谨工程师" }));
    expect((await field("系统提示词开头")).value).toBe("Rigorous engineer prefix.");
    expect((await field("补充要求")).value).toBe("");
    expect(screen.queryByRole("switch", { name: /complete/ })).toBeNull();
    expect(now(container)).toBe("严谨工程师");

    await userEvent.click(screen.getByRole("button", { name: "简洁" }));
    expect((await field("系统提示词开头")).value).toBe("");
    expect((await field("补充要求")).value).toBe("Be terse.");
    expect(screen.getByRole("switch", { name: "注入可选运行时上下文" }).getAttribute("aria-checked")).toBe("false");
  });

  it("reads 自定义 once the applied preset is edited", async () => {
    const { container } = render(<Persona port={portWith()} />);
    await userEvent.click(await screen.findByRole("button", { name: "严谨工程师" }));
    expect(now(container)).toBe("严谨工程师");
    await userEvent.type(await field("系统提示词开头"), " Also be kind.");
    expect(now(container)).toBe("自定义");
  });

  it("saves supported fields and declares the next session build", async () => {
    const port = portWith();
    const { container } = render(<Persona port={port} />);
    const prefix = await field("系统提示词开头");
    expect(saveButton().disabled).toBe(true);

    await userEvent.type(prefix, "Lead with evidence.");
    expect(container.querySelector(".apply")?.textContent).toContain("未保存");
    expect(container.querySelector(".apply")?.textContent).not.toContain("已保存");
    await userEvent.type(await field("补充要求"), "Stay terse.");
    await userEvent.click(screen.getByRole("switch", { name: "注入可选运行时上下文" }));

    expect(saveButton().disabled).toBe(false);
    await userEvent.click(saveButton());
    expect(port.savePersonaSettings).toHaveBeenCalledWith({
      prefix: "Lead with evidence.",
      suffix: "Stay terse.",
      include_runtime_context: false,
    });
    expect(container.querySelector(".apply")?.textContent).toContain("已保存 · 下次会话构建时生效");
    expect(screen.getByText("/u/config.toml")).toBeTruthy();
    // Clean again: the same bytes would be a second no-op save.
    expect(saveButton().disabled).toBe(true);
    await userEvent.type(prefix, " More.");
    expect(container.querySelector(".apply")?.textContent).toContain("未保存");
    expect(container.querySelector(".apply")?.textContent).not.toContain("已保存");
  });

  it("shows the kernel's size refusal", async () => {
    const port = portWith();
    render(<Persona port={port} />);
    await userEvent.type(await field("系统提示词开头"), "Something.");

    port.savePersonaSettings.mockImplementationOnce(async () => {
      throw new HttpError(400, "persona.too_large", {
        code: "persona.too_large", params: { detail: "70 KiB" },
      });
    });
    await userEvent.click(saveButton());
    expect(await screen.findByText(/人设内容超出长度上限/)).toBeTruthy();
  });

  it("renders the empty state when the settings cannot be read", async () => {
    const port = { persona: vi.fn(async () => { throw new Error("offline"); }), savePersonaSettings: vi.fn() } as unknown as AgentPort;
    render(<Persona port={port} />);
    expect(await screen.findByText("无法读取人设设置。")).toBeTruthy();
  });
});
