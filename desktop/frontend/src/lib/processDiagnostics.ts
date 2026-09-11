export interface ProcessDiagnosticsSnapshot {
  scope: "electron";
  samples: {
    ageMs: number;
    intervalMs: number | null;
    processes: { pid: number; type: string; cpuPercent: number | null; workingSetMb: number | null; privateMb: number | null }[];
  }[];
}

// Reporting must still work with an older shell, a rejected IPC or a hung host.
export async function boundedDiagnostics<T>(read: () => Promise<T>, timeoutMs = 750): Promise<T | undefined> {
  let cancel = () => {};
  try {
    return await Promise.race([
      Promise.resolve().then(read).catch(() => undefined),
      new Promise<undefined>((resolve) => { const timer = setTimeout(resolve, timeoutMs); cancel = () => clearTimeout(timer); }),
    ]);
  } finally { cancel(); }
}
