// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render } from "@testing-library/react";
import "../testkit";
import { RememberChoice, RememberWhere } from "./RememberChoice";
import { ApprovalCard } from "./ApprovalCard";
import { t } from "../../i18n";
import type { Item } from "../../state/session";

afterEach(cleanup);

describe("RememberChoice", () => {
  it("draws only what the kernel will honour and reports the choice", () => {
    const choose = vi.fn();
    const box = render(<RememberChoice options={["always"]} target="a1" busy="" disabled={false} onChoose={choose} />).container;
    expect(box.querySelectorAll("button")).toHaveLength(1);
    const button = box.querySelector("button")!;
    expect(button.dataset.value).toBe("always");
    expect(button.dataset.target).toBe("a1");
    fireEvent.click(button);
    expect(choose).toHaveBeenCalledWith("always");
  });

  it("draws nothing when there is nothing to remember", () => {
    expect(render(<RememberChoice options={[]} target="a1" busy="" disabled={false} onChoose={() => {}} />).container.innerHTML).toBe("");
    expect(render(<RememberWhere options={[]} target="a1" />).container.innerHTML).toBe("");
  });

  it("says where each remembered choice is kept", () => {
    const both = render(<RememberWhere options={["session", "always"]} target="a1" />).container.textContent ?? "";
    expect(both).toContain("不落盘");
    expect(both).toContain("保存规则");
    expect(render(<RememberWhere options={["session"]} target="a2" />).container.textContent).not.toContain("保存规则");
  });
});

type Approval = Extract<Item, { t: "approval" }>;
const draw = (a: Approval["a"], onApprove = vi.fn(async () => {})) =>
  render(<ApprovalCard item={{ t: "approval", id: "i1", a }} onApprove={onApprove} onFullAccess={async () => {}} onPlan={async () => {}} />).container;

describe("the approval card's footer", () => {
  it("offers the session and always choices only where the kernel grants them", () => {
    const none = draw({ id: "a1", tool: "bash", subject: "ls" });
    expect(none.querySelector('[data-value="session"]')).toBeNull();
    expect(none.querySelector('[data-value="always"]')).toBeNull();
    expect(none.querySelector(".apv-note")).toBeNull();
    cleanup();
    const both = draw({ id: "a2", tool: "bash", subject: "ls", allowsSession: true, allowsPersist: true });
    expect(both.querySelector('[data-value="session"]')).not.toBeNull();
    expect(both.querySelector('[data-value="always"]')).not.toBeNull();
    expect(both.querySelector(".apv-note")?.textContent).toBe(t("会话授权不落盘；始终允许将保存规则，可在设置 → 工具与权限中撤销。"));
    expect(both.querySelector(".apv-note")!.compareDocumentPosition(both.querySelector(".apv-ft")!)).toBe(Node.DOCUMENT_POSITION_FOLLOWING);
  });

  it("answers with the remembered verdict", () => {
    const approve = vi.fn(async () => {});
    const box = draw({ id: "a3", tool: "bash", subject: "ls", allowsSession: true, allowsPersist: true }, approve);
    fireEvent.click(box.querySelector('[data-value="session"]')!);
    expect(approve).toHaveBeenCalledWith("i1", "a3", "session");
  });

  it.each(["session", "always", "deny", "once"] as const)("keeps the %s decision's original callback", (verdict) => {
    const approve = vi.fn(async () => {});
    const box = draw({ id: "a4", tool: "bash", subject: "ls", allowsSession: true, allowsPersist: true }, approve);
    expect([...box.querySelectorAll(".apv-ft button")].map((b) => b.getAttribute("data-value"))).toEqual(["session", "always", "deny", "once"]);
    fireEvent.click(box.querySelector(`[data-value="${verdict}"]`)!);
    expect(approve).toHaveBeenCalledExactlyOnceWith("i1", "a4", verdict);
  });
});
