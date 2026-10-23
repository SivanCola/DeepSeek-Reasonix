import { describe, expect, it } from "vitest";
import { mockUsage } from "./mock_usage";

describe("mock usage windows", () => {
  it("returns the requested explicit calendar range", () => {
    const report = mockUsage({ from: "2026-08-03", to: "2026-08-05" });

    expect(report.from).toBe("2026-08-03");
    expect(report.to).toBe("2026-08-05");
    expect(report.daily.map((day) => day.day)).toEqual(["2026-08-03", "2026-08-04", "2026-08-05"]);
  });

  it("rejects a range longer than the kernel accepts", () => {
    expect(() => mockUsage({ from: "2025-01-01", to: "2026-01-01" })).toThrow("range must be between 1 and 365 days");
  });

  it("returns a full day of hour buckets for a single-day window", () => {
    const report = mockUsage({ from: "2026-08-03", to: "2026-08-03" });

    expect(report.hourly).toHaveLength(24);
    expect(report.hourly?.[9].hour).toBe("09:00");
    const total = (report.hourly ?? []).reduce((a, hour) => a + hour.total, 0);
    expect(total).toBe(report.daily[0].total);
  });
});
