// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import "./testkit";
import { DefaultMode } from "./DefaultMode";
import { MockPort } from "../port/mock";
import type { AgentPort } from "../port/port";

afterEach(cleanup);

const toggle = () => screen.findByRole("switch", { name: "新对话默认进入计划模式" });

describe("the default-mode switch", () => {
  it("starts off, says what it changes and what it leaves alone, and saves the opposite of what it shows", async () => {
    const p = new MockPort() as unknown as AgentPort;
    const save = vi.spyOn(p, "saveDefaultMode");
    render(<DefaultMode port={p} />);
    const sw = await toggle();
    expect(sw.getAttribute("aria-checked")).toBe("false");
    expect(screen.getByText(/已有会话不变/)).toBeTruthy();
    expect(screen.getByText(/项目不能更改/)).toBeTruthy();
    await userEvent.click(sw);
    expect(save).toHaveBeenCalledWith(true);
    await waitFor(() => expect(sw.getAttribute("aria-checked")).toBe("true"));
    await userEvent.click(sw);
    expect(save).toHaveBeenLastCalledWith(false);
  });

  it("keeps the switch where it was when the save is refused", async () => {
    const p = new MockPort() as unknown as AgentPort;
    p.saveDefaultMode = async () => {
      throw new Error("disk full");
    };
    render(<DefaultMode port={p} />);
    const sw = await toggle();
    await userEvent.click(sw);
    await screen.findByText(/disk full/);
    expect(sw.getAttribute("aria-checked")).toBe("false");
  });

  it("says so when the setting cannot be read", async () => {
    const p = new MockPort() as unknown as AgentPort;
    p.defaultMode = async () => {
      throw new Error("offline");
    };
    render(<DefaultMode port={p} />);
    expect(await screen.findByText("无法读取新对话的起始模式设置。")).toBeTruthy();
  });
});
