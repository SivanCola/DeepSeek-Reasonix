import type { ProcessMetric } from "electron";

// Only Electron-managed processes are covered; the Go service is not included.
// Do not forward ProcessMetric wholesale: names can contain user content.
export class ProcessDiagnostics {
  private samples: { atMs: number; intervalMs: number | null; processes: { pid: number; type: string; cpuPercent: number | null; workingSetMb: number | null; privateMb: number | null }[] }[] = [];
  private previousAt: number | undefined;
  private previousPids = new Set<number>();
  constructor(private readonly read: () => ProcessMetric[], private readonly now = () => performance.now()) {}

  sample(): void {
    const atMs = this.now();
    if (this.previousAt !== undefined && atMs - this.previousAt < 1000) return;
    try {
      const metrics = this.read();
      const intervalMs = this.previousAt === undefined ? null : atMs - this.previousAt;
      const finite = (n: number | undefined) => typeof n === "number" && Number.isFinite(n) && n >= 0 ? n : null;
      const mb = (n: number | undefined) => { const value = finite(n); return value === null ? null : value / 1024; };
      const types = new Set(["Browser", "Tab", "GPU", "Utility", "Zygote", "Sandbox helper", "Pepper Plugin", "Pepper Plugin Broker"]);
      const processes = metrics.slice(0, 128).map((metric) => ({
        pid: metric.pid,
        type: types.has(metric.type) ? metric.type : "Other",
        cpuPercent: intervalMs === null || !this.previousPids.has(metric.pid) ? null : finite(metric.cpu?.percentCPUUsage),
        workingSetMb: mb(metric.memory?.workingSetSize),
        privateMb: mb(metric.memory?.privateBytes),
      }));
      this.previousAt = atMs;
      this.previousPids = new Set(metrics.map((metric) => metric.pid));
      this.samples = [...this.samples.filter((s) => atMs - s.atMs <= 60_000), { atMs, intervalMs, processes }].slice(-12);
    } catch { /* A failed diagnostic sample must never affect the app. */ }
  }

  snapshot() {
    this.sample();
    const now = this.now();
    return { scope: "electron" as const, samples: this.samples.filter((s) => now - s.atMs <= 60_000).map(({ atMs, ...sample }) => ({ ageMs: now - atMs, ...sample })) };
  }
}
