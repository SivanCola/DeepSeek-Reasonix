// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import "./testkit";
import { Versions } from "./Versions";
import { HttpError, type UpdateProgress, type VersionHub } from "../port/port";

afterEach(cleanup);

const hub: VersionHub = {
  current: "2.20.0",
  pinned: "",
  stalePin: false,
  latest: "2.21.0",
  newer: true,
  versions: [
    { version: "2.21.0", tag: "studio-v2.21.0", publishedAt: "", hasNotes: false, current: false, older: false },
    { version: "2.20.0", tag: "studio-v2.20.0", publishedAt: "", hasNotes: false, current: true, older: false },
  ],
};

function portAt(progress: UpdateProgress, cancel = vi.fn((_v: string) => Promise.resolve())) {
  return {
    versions: () => Promise.resolve(hub),
    pinVersion: vi.fn(() => Promise.resolve()),
    goToVersion: vi.fn(() => Promise.resolve()),
    cancelVersion: cancel,
    restartToVersion: vi.fn(() => Promise.resolve()),
    onUpdateProgress: (cb: (p: UpdateProgress) => void) => {
      cb(progress);
      return () => {};
    },
  };
}

describe("cancelling a download", () => {
  it("stops the download from the row that is downloading", async () => {
    const cancel = vi.fn((_v: string) => Promise.resolve());
    render(<Versions port={portAt({ version: "2.21.0", phase: "downloading", received: 1024, total: 4096 }, cancel)} />);
    await userEvent.click(await screen.findByRole("button", { name: "取消下载" }));
    expect(cancel).toHaveBeenCalledExactlyOnceWith("2.21.0");
    expect(screen.queryByRole("button", { name: "安装这个版本" })).toBeNull();
  });

  it("also stops while the artifact is being verified", async () => {
    const cancel = vi.fn((_v: string) => Promise.resolve());
    render(<Versions port={portAt({ version: "2.21.0", phase: "verifying", received: 4096, total: 4096 }, cancel)} />);
    await userEvent.click(await screen.findByRole("button", { name: "取消下载" }));
    expect(cancel).toHaveBeenCalledExactlyOnceWith("2.21.0");
  });

  it("keeps a second click out while the cancellation is under way", async () => {
    let release: () => void = () => {};
    const cancel = vi.fn(() => new Promise<void>((resolve) => { release = resolve; }));
    render(<Versions port={portAt({ version: "2.21.0", phase: "downloading", received: 1, total: 2 }, cancel)} />);
    await userEvent.click(await screen.findByRole("button", { name: "取消下载" }));
    const pending = await screen.findByRole("button", { name: "取消中…" });
    expect(pending.hasAttribute("disabled")).toBe(true);
    release();
  });

  it("returns the row to install once the kernel reports the download cancelled", async () => {
    render(<Versions port={portAt({ version: "2.21.0", phase: "cancelled", received: 0, total: 0 })} />);
    expect(await screen.findByRole("button", { name: "安装这个版本" })).toBeTruthy();
    expect(screen.queryByRole("button", { name: "取消下载" })).toBeNull();
    expect(screen.queryByText("安装失败")).toBeNull();
  });

  it("says why a cancellation was refused", async () => {
    const cancel = vi.fn(() => Promise.reject(new HttpError(409, "nothing to cancel", { code: "update.nothing_to_cancel" }, true)));
    render(<Versions port={portAt({ version: "2.21.0", phase: "downloading", received: 1, total: 2 }, cancel)} />);
    await userEvent.click(await screen.findByRole("button", { name: "取消下载" }));
    expect(await screen.findByText("操作未完成")).toBeTruthy();
    expect(await screen.findByText("没有正在下载的版本可供取消，可能已经完成或已停止")).toBeTruthy();
  });
});
