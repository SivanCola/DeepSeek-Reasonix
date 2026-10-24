import { useSyncExternalStore } from "react";
import type { UpdateMode, UpdateProgress, VersionHub } from "../port/version";

// In notify mode the window reads the version catalog in the background and
// remembers whether a release newer than this build is published. It installs
// nothing and shows nothing but a mark. The catalog read is the version panel's
// own call; a failure is silent and backs off, so an offline or proxied machine
// is not retried in a storm. In download mode it also asks the kernel for the
// same download the version panel starts and watches that move until the release
// is verified and waiting. It never asks for the restart: that is the person's.
const FIRST_DELAY = 60_000;
const DAY = 24 * 60 * 60_000;
const RETRY_FIRST = 15 * 60_000;

export interface UpdateWatchPort {
  updateMode(): Promise<UpdateMode>;
  versions(): Promise<VersionHub>;
  goToVersion?(version: string): Promise<void>;
  onUpdateProgress?(cb: (p: UpdateProgress) => void): () => void;
}

let available = "";
let ready = "";
const listeners = new Set<() => void>();

const publish = (next: string) => {
  if (next === available) return;
  available = next;
  for (const listener of listeners) listener();
};

const publishReady = (next: string) => {
  if (next === ready) return;
  ready = next;
  for (const listener of listeners) listener();
};

const subscribe = (listener: () => void) => {
  listeners.add(listener);
  return () => void listeners.delete(listener);
};

/** The newer release the last background read found, or "". */
export const readUpdateAvailable = (): string => available;

export function useUpdateAvailable(): string {
  return useSyncExternalStore(subscribe, readUpdateAvailable, readUpdateAvailable);
}

/** The release downloaded and verified, waiting for the person's restart, or "". */
export const readUpdateReady = (): string => ready;

export function useUpdateReady(): string {
  return useSyncExternalStore(subscribe, readUpdateReady, readUpdateReady);
}

export function resetUpdateWatch(): void {
  publish("");
  publishReady("");
}

let active: UpdateWatch | null = null;

/** Tells the running check the mode changed, so the choice takes effect now. */
export function refreshUpdateWatch(): Promise<void> {
  return active ? active.refresh() : Promise.resolve();
}

export interface UpdateWatch {
  (): void;
  /** Reads the mode again and, in notify mode, checks now. */
  refresh(): Promise<void>;
}

export function startUpdateWatch(port: UpdateWatchPort): UpdateWatch {
  let timer: ReturnType<typeof setTimeout> | undefined;
  let stopped = false;
  let failures = 0;
  let tracking: (() => void) | null = null;

  const backoff = () => {
    failures++;
    later(Math.min(DAY, RETRY_FIRST * 2 ** (failures - 1)));
  };

  const untrack = () => {
    tracking?.();
    tracking = null;
  };

  // Follows the kernel's answer to "what is the move doing" until it ends. The
  // first answer decides whether a download is owed: nothing under way, or an
  // earlier attempt that failed. A download the person cancelled is not
  // retried, but one cancelled for an older release does not block the one now
  // offered. Once started, an error ends the watch and the next daily check
  // tries again.
  const track = (version: string) => {
    if (tracking || !port.onUpdateProgress || !port.goToVersion) return;
    const goTo = port.goToVersion.bind(port);
    let started = false;
    let off: (() => void) | undefined;
    const done = () => {
      off?.();
      if (tracking === done) tracking = null;
    };
    const start = () => {
      started = true;
      goTo(version).catch(() => {
        done();
        backoff();
      });
    };
    tracking = done;
    off = port.onUpdateProgress((p) => {
      if (stopped || tracking !== done) return;
      if (p.phase === "ready") {
        publishReady(p.version);
        done();
      } else if (p.phase === "cancelled") {
        if (p.version === version) done();
        else if (!started) start();
        else done();
      } else if ((p.phase === "idle" || p.phase === "error") && !started) {
        start();
      } else if (p.phase === "error") {
        done();
      } else if (p.phase !== "idle" && p.version !== version) {
        done();
      }
    });
    if (tracking !== done) off();
  };

  const later = (ms: number) => {
    if (stopped) return;
    clearTimeout(timer);
    timer = setTimeout(() => void check(), ms);
  };

  const check = async () => {
    let mode: UpdateMode;
    try {
      mode = await port.updateMode();
    } catch {
      failures++;
      later(Math.min(DAY, RETRY_FIRST * 2 ** (failures - 1)));
      return;
    }
    if (stopped) return;
    if (mode !== "notify" && mode !== "download") {
      publish("");
      publishReady("");
      untrack();
      failures = 0;
      later(DAY);
      return;
    }
    try {
      const hub = await port.versions();
      if (stopped) return;
      failures = 0;
      const newer = hub.newer && !hub.pinned && !hub.withdrawn && hub.latest ? hub.latest : "";
      publish(newer);
      later(DAY);
      if (mode === "download" && newer) track(newer);
    } catch {
      failures++;
      later(Math.min(DAY, RETRY_FIRST * 2 ** (failures - 1)));
    }
  };

  later(FIRST_DELAY);
  const stop = (() => {
    stopped = true;
    clearTimeout(timer);
    untrack();
    if (active === stop) active = null;
  }) as UpdateWatch;
  stop.refresh = async () => {
    failures = 0;
    clearTimeout(timer);
    await check();
  };
  active = stop;
  return stop;
}
