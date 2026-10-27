import { expect, it } from "vitest";
import { compactModelMenu, groupHeadings, modelMenu, narrowToGroups } from "./modelmenu";
import { modelHealthStatus } from "./modelhealth";
import { t } from "../i18n";

it("keeps the pinned model's provider in filters and gives every compact row its account", () => {
  const items = modelMenu([
    { ref: "a/x", provider: "a", model: "x", displayName: "Account A", kind: "openai" },
    { ref: "b/y", provider: "b", model: "y", displayName: "Account B", kind: "openai" },
  ], [], "a/x");
  expect(groupHeadings(items).map((g) => g.label)).toContain("Account A");
  const compact = compactModelMenu(items, "a/x");
  expect(compact.filter((i) => i.header).map((i) => i.label)).toEqual([t("当前选择"), t("其他模型")]);
  expect(compact.find((i) => i.value === "a/x")?.meta).toContain("Account A");
  const b = groupHeadings(items).find((g) => g.label === "Account B")!;
  expect(compactModelMenu(narrowToGroups(items, [b.value]), "a/x").filter((i) => !i.header && !i.plain).map((i) => i.value)).toEqual(["a/x", "b/y"]);
});

it("shows only the most actionable short status and keeps the full explanation separately", () => {
  const status = modelHealthStatus({connection:"reachable",wallet:"insufficient",checkedAt:1});
  expect(status).toMatchObject({ label:t("余额不足"), tone:"warning" });
  expect(status?.detail).toContain(t("余额不足，请充值后重试"));
  expect(modelHealthStatus({connection:"rate_limited",wallet:"unknown",checkedAt:1})?.label).toBe(t("暂时限流"));
  expect(modelHealthStatus({connection:"reachable",wallet:"unsupported",checkedAt:1})?.label).toBe(t("连接正常"));
  expect(modelHealthStatus({connection:"unknown",wallet:"unknown",checkedAt:0})?.tone).toBe("neutral");
});
