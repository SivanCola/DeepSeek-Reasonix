// @vitest-environment jsdom
import { useState } from "react";
import { act, cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import "./testkit";
import { WorkbenchPanel } from "./WorkbenchPanel";
import { useConversationFiles } from "./useConversationFiles";
import { MockPort } from "../port/mock";
import type { AgentPort } from "../port/port";

const onCloseManual = vi.fn();
const filesShown = () => document.querySelector(".workbench-body")!.hasAttribute("data-files");
const tabs = () => [...document.querySelectorAll("[role=tab]")].map((e) => e.textContent?.trim());

function Harness({ port, root = "/w", panel = true }: { port: AgentPort; root?: string; panel?: boolean }) {
  const [presses, setPresses] = useState(0);
  const files = useConversationFiles(port, root, false, true, () => {}, () => {});
  return (
    <>
      <button onClick={() => files.refs.link?.("docs/notes.md")?.()}>link</button>
      <button onClick={() => setPresses((n) => n + 1)}>files</button>
      {panel && <WorkbenchPanel scheme="dark" shown changes={[]} onSurfaces={vi.fn()} onCloseManual={onCloseManual} onExternal={vi.fn()} manual tabs={[]}
        port={port} fileRequest={files.request} toggleFiles={presses} />}
    </>
  );
}

const closeTab = () => userEvent.click(document.querySelector<HTMLElement>(".workbench-tab-close")!);
afterEach(() => { cleanup(); onCloseManual.mockClear(); });

describe("a file linked from the conversation opens once", () => {
  it("shows the file tree, not the old file, when Files is pressed after that file was closed", async () => {
    render(<Harness port={new MockPort()} />);
    await userEvent.click(screen.getByText("link"));
    expect(tabs()).toEqual(["notes.md"]);
    await closeTab();
    expect(tabs()).toEqual([]);
    expect(onCloseManual).toHaveBeenCalledTimes(1);
    await userEvent.click(screen.getByText("files"));
    expect(filesShown()).toBe(true);
    expect(tabs()).toEqual([]);
  });

  it("opens the same link again when it is clicked again", async () => {
    render(<Harness port={new MockPort()} />);
    await userEvent.click(screen.getByText("link"));
    await closeTab();
    await userEvent.click(screen.getByText("link"));
    expect(tabs()).toEqual(["notes.md"]);
  });

  it("returns to the file tree when a file picked from the explorer is closed", async () => {
    render(<Harness port={new MockPort()} />);
    await userEvent.click(screen.getByText("files"));
    expect(filesShown()).toBe(true);
    const row = await screen.findByText("README.md");
    await userEvent.click(row);
    expect(tabs()).toEqual(["README.md"]);
    await closeTab();
    expect(filesShown()).toBe(true);
    expect(onCloseManual).not.toHaveBeenCalled();
  });

  it("leaves nothing to replay after the pane moves to another session", async () => {
    const first = new MockPort();
    const { rerender } = render(<Harness port={first} panel={false} />);
    await userEvent.click(screen.getByText("link"));
    rerender(<Harness port={new MockPort()} root="/other" panel={false} />);
    rerender(<Harness port={first} panel />);
    await act(async () => {});
    expect(tabs()).toEqual([]);
  });
});
