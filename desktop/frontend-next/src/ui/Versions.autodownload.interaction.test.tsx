// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import "./testkit";
import { Versions } from "./Versions";
import { resetUpdateWatch, startUpdateWatch } from "../state/updatewatch";
import type { UpdateProgress, VersionHub } from "../port/port";

beforeEach(() => vi.useFakeTimers());
afterEach(() => { cleanup(); resetUpdateWatch(); vi.useRealTimers(); });

const hub = (newer: boolean): VersionHub => ({
  current: "2.20.0", pinned: "", stalePin: false, latest: "2.21.0", newer,
  versions: [{ version: "2.21.0", tag: "studio-v2.21.0", publishedAt: "", hasNotes: false, current: false, older: false }],
});
const idle: UpdateProgress = { version: "", phase: "idle", received: 0, total: 0 };

// The window's background read found nothing a minute after it opened. The
// release is published afterwards; the person then opens the Versions page.
function pages(mode: "manual" | "notify" | "download") {
  const watchPort = {
    updateMode: () => Promise.resolve(mode),
    versions: vi.fn(() => Promise.resolve(hub(true))),
    goToVersion: vi.fn(() => Promise.resolve()),
    onUpdateProgress: (cb: (p: UpdateProgress) => void) => { queueMicrotask(() => cb(idle)); return () => {}; },
  };
  const pagePort = {
    versions: () => Promise.resolve(hub(true)),
    pinVersion: vi.fn(() => Promise.resolve()),
    goToVersion: vi.fn(() => Promise.resolve()),
    cancelVersion: vi.fn(() => Promise.resolve()),
    restartToVersion: vi.fn(() => Promise.resolve()),
    onUpdateProgress: (cb: (p: UpdateProgress) => void) => { queueMicrotask(() => cb(idle)); return () => {}; },
  };
  return { watchPort, pagePort };
}

describe("automatic download and the Versions page", () => {
  it("downloads nothing on opening the page unless the person chose automatic download", async () => {
    for (const mode of ["manual", "notify"] as const) {
      const { watchPort, pagePort } = pages(mode);
      const stop = startUpdateWatch(watchPort);
      render(<Versions port={pagePort} />);
      await act(async () => { await vi.advanceTimersByTimeAsync(1_000); });
      expect(watchPort.goToVersion).not.toHaveBeenCalled();
      expect(pagePort.goToVersion).not.toHaveBeenCalled();
      stop();
      cleanup();
      resetUpdateWatch();
    }
  });

  it("does not start a second download when the page is opened while one is under way", async () => {
    const { watchPort, pagePort } = pages("download");
    const stop = startUpdateWatch(watchPort);
    await vi.advanceTimersByTimeAsync(2 * 60_000);
    expect(watchPort.goToVersion).toHaveBeenCalledTimes(1);
    render(<Versions port={pagePort} />);
    await act(async () => { await vi.advanceTimersByTimeAsync(1_000); });
    expect(watchPort.goToVersion).toHaveBeenCalledTimes(1);
    stop();
  });

  it("starts the download when the person opens the page and a newer release is out, without a click", async () => {
    let published = false;
    const watchPort = {
      updateMode: () => Promise.resolve("download" as const),
      versions: vi.fn(() => Promise.resolve(hub(published))),
      goToVersion: vi.fn(() => Promise.resolve()),
      onUpdateProgress: (cb: (p: UpdateProgress) => void) => { queueMicrotask(() => cb(idle)); return () => {}; },
    };
    const stop = startUpdateWatch(watchPort);
    await vi.advanceTimersByTimeAsync(2 * 60_000);
    expect(watchPort.goToVersion).not.toHaveBeenCalled();

    published = true;
    const pagePort = {
      versions: () => Promise.resolve(hub(true)),
      pinVersion: vi.fn(() => Promise.resolve()),
      goToVersion: vi.fn(() => Promise.resolve()),
      cancelVersion: vi.fn(() => Promise.resolve()),
      restartToVersion: vi.fn(() => Promise.resolve()),
      onUpdateProgress: (cb: (p: UpdateProgress) => void) => { queueMicrotask(() => cb(idle)); return () => {}; },
    };
    render(<Versions port={pagePort} />);
    await act(async () => { await vi.advanceTimersByTimeAsync(1_000); });
    expect(watchPort.goToVersion).toHaveBeenCalledExactlyOnceWith("2.21.0");
    stop();
  });

  it("stops a background download from the same row the page shows", async () => {
    vi.useRealTimers();
    const cancel = vi.fn(() => Promise.resolve());
    const pagePort = {
      versions: () => Promise.resolve(hub(true)),
      pinVersion: vi.fn(() => Promise.resolve()),
      goToVersion: vi.fn(() => Promise.resolve()),
      cancelVersion: cancel,
      restartToVersion: vi.fn(() => Promise.resolve()),
      onUpdateProgress: (cb: (p: UpdateProgress) => void) => {
        queueMicrotask(() => cb({ version: "2.21.0", phase: "downloading", received: 3, total: 9 }));
        return () => {};
      },
    };
    render(<Versions port={pagePort} />);
    await userEvent.click(await screen.findByRole("button", { name: "取消下载" }));
    expect(cancel).toHaveBeenCalledExactlyOnceWith("2.21.0");
  });
});
