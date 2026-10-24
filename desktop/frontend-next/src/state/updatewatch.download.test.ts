import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { UpdateMode, UpdateProgress, VersionHub } from "../port/version";
import { readUpdateAvailable, readUpdateReady, resetUpdateWatch, startUpdateWatch } from "./updatewatch";

const hub = (over: Partial<VersionHub> = {}): VersionHub => ({
  current: "2.20.0", pinned: "", stalePin: false, latest: "2.21.0", newer: true, versions: [], ...over,
});
const progress = (phase: UpdateProgress["phase"], version = "2.21.0"): UpdateProgress => ({ version, phase, received: 0, total: 0 });

// The kernel's own answer to "what is the move doing", pulled by the window.
function kernel(mode: UpdateMode, over: { read?: () => Promise<VersionHub>; goTo?: (v: string) => Promise<void> } = {}) {
  const feeds = new Set<(p: UpdateProgress) => void>();
  let now: UpdateProgress = progress("idle", "");
  const p = {
    updateMode: vi.fn(() => Promise.resolve(mode)),
    versions: vi.fn(over.read ?? (() => Promise.resolve(hub()))),
    goToVersion: vi.fn(over.goTo ?? (() => Promise.resolve())),
    onUpdateProgress: vi.fn((cb: (p: UpdateProgress) => void) => {
      feeds.add(cb);
      queueMicrotask(() => feeds.has(cb) && cb(now));
      return () => void feeds.delete(cb);
    }),
  };
  const say = (next: UpdateProgress) => {
    now = next;
    for (const cb of [...feeds]) cb(next);
  };
  return { p, say, listening: () => feeds.size };
}

const MINUTE = 60_000;
const DAY = 24 * 60 * MINUTE;

beforeEach(() => vi.useFakeTimers());
afterEach(() => {
  resetUpdateWatch();
  vi.useRealTimers();
});

describe("the download update mode", () => {
  it("downloads nothing in manual or notify mode", async () => {
    for (const mode of ["manual", "notify"] as const) {
      const k = kernel(mode);
      const stop = startUpdateWatch(k.p);
      await vi.advanceTimersByTimeAsync(2 * MINUTE);
      expect(k.p.goToVersion).not.toHaveBeenCalled();
      expect(readUpdateReady()).toBe("");
      stop();
      resetUpdateWatch();
    }
  });

  it("starts the download of the newest release once, and says it is ready when the kernel does", async () => {
    const k = kernel("download");
    const stop = startUpdateWatch(k.p);
    await vi.advanceTimersByTimeAsync(2 * MINUTE);
    expect(k.p.goToVersion).toHaveBeenCalledExactlyOnceWith("2.21.0");
    expect(readUpdateAvailable()).toBe("2.21.0");
    expect(readUpdateReady()).toBe("");
    k.say(progress("downloading"));
    expect(readUpdateReady()).toBe("");
    k.say(progress("ready"));
    expect(readUpdateReady()).toBe("2.21.0");
    expect(k.listening()).toBe(0);
    stop();
  });

  it("leaves a pinned build, a withdrawn one and a catalog with nothing newer alone", async () => {
    for (const over of [{ pinned: "2.20.0" }, { withdrawn: { version: "2.20.0", reason: "x" } }, { newer: false, latest: "2.20.0" }]) {
      const k = kernel("download", { read: () => Promise.resolve(hub(over)) });
      const stop = startUpdateWatch(k.p);
      await vi.advanceTimersByTimeAsync(2 * MINUTE);
      expect(k.p.goToVersion).not.toHaveBeenCalled();
      stop();
      resetUpdateWatch();
    }
  });

  it("does not start a second move while one is under way or already waiting for its restart", async () => {
    for (const phase of ["downloading", "verifying", "applying", "ready"] as const) {
      const k = kernel("download");
      k.say(progress(phase));
      const stop = startUpdateWatch(k.p);
      await vi.advanceTimersByTimeAsync(2 * MINUTE);
      expect(k.p.goToVersion).not.toHaveBeenCalled();
      stop();
      resetUpdateWatch();
    }
  });

  it("shows a release an earlier launch left waiting, without downloading it again", async () => {
    const k = kernel("download");
    k.say(progress("ready"));
    const stop = startUpdateWatch(k.p);
    await vi.advanceTimersByTimeAsync(2 * MINUTE);
    expect(readUpdateReady()).toBe("2.21.0");
    expect(k.p.goToVersion).not.toHaveBeenCalled();
    stop();
  });

  it("never asks the kernel to install or restart: it only downloads", async () => {
    const k = kernel("download");
    const stop = startUpdateWatch({ ...k.p, restartToVersion: vi.fn() } as never);
    await vi.advanceTimersByTimeAsync(2 * MINUTE);
    k.say(progress("ready"));
    await vi.advanceTimersByTimeAsync(2 * DAY);
    expect((k.p as never as { restartToVersion?: unknown }).restartToVersion).toBeUndefined();
    stop();
  });

  it("tries a failed download again at the next daily check, not in a storm", async () => {
    const k = kernel("download");
    const stop = startUpdateWatch(k.p);
    await vi.advanceTimersByTimeAsync(2 * MINUTE);
    k.say(progress("error"));
    await vi.advanceTimersByTimeAsync(2 * MINUTE);
    expect(k.p.goToVersion).toHaveBeenCalledTimes(1);
    await vi.advanceTimersByTimeAsync(DAY);
    k.say(progress("idle", ""));
    await vi.advanceTimersByTimeAsync(MINUTE);
    expect(k.p.goToVersion).toHaveBeenCalledTimes(2);
    stop();
  });

  it("stays silent when the download cannot be started, and backs off", async () => {
    const k = kernel("download", { goTo: () => Promise.reject(new Error("no")) });
    const stop = startUpdateWatch(k.p);
    await vi.advanceTimersByTimeAsync(2 * MINUTE);
    expect(k.p.goToVersion).toHaveBeenCalledTimes(1);
    expect(readUpdateReady()).toBe("");
    expect(k.listening()).toBe(0);
    await vi.advanceTimersByTimeAsync(10 * MINUTE);
    expect(k.p.goToVersion).toHaveBeenCalledTimes(1);
    stop();
  });

  it("forgets what it said and stops downloading once the user goes back to manual", async () => {
    let mode: UpdateMode = "download";
    const k = kernel("download");
    k.p.updateMode.mockImplementation(() => Promise.resolve(mode));
    const stop = startUpdateWatch(k.p);
    await vi.advanceTimersByTimeAsync(2 * MINUTE);
    k.say(progress("ready"));
    expect(readUpdateReady()).toBe("2.21.0");
    mode = "manual";
    await stop.refresh();
    expect(readUpdateReady()).toBe("");
    expect(readUpdateAvailable()).toBe("");
    await vi.advanceTimersByTimeAsync(2 * DAY);
    expect(k.p.goToVersion).toHaveBeenCalledTimes(1);
    stop();
  });

  it("falls back to notifying on a window whose port cannot download", async () => {
    const k = kernel("download");
    const { goToVersion: _g, onUpdateProgress: _o, ...bare } = k.p;
    const stop = startUpdateWatch(bare);
    await vi.advanceTimersByTimeAsync(2 * MINUTE);
    expect(readUpdateAvailable()).toBe("2.21.0");
    expect(readUpdateReady()).toBe("");
    stop();
  });

  it("stops watching a download the person cancelled and does not start it again", async () => {
    const k = kernel("download");
    const stop = startUpdateWatch(k.p);
    await vi.advanceTimersByTimeAsync(2 * MINUTE);
    expect(k.p.goToVersion).toHaveBeenCalledTimes(1);
    k.say(progress("cancelled"));
    expect(k.listening()).toBe(0);
    await vi.advanceTimersByTimeAsync(2 * DAY);
    expect(k.p.goToVersion).toHaveBeenCalledTimes(1);
    stop();
  });

  it("still downloads a newer release after an older one was cancelled", async () => {
    let latest = "2.21.0";
    const k = kernel("download", { read: () => Promise.resolve(hub({ latest })) });
    const stop = startUpdateWatch(k.p);
    await vi.advanceTimersByTimeAsync(2 * MINUTE);
    k.say(progress("cancelled", "2.21.0"));
    latest = "2.22.0";
    await stop.refresh();
    await vi.advanceTimersByTimeAsync(0);
    expect(k.p.goToVersion).toHaveBeenLastCalledWith("2.22.0");
    stop();
  });
});
