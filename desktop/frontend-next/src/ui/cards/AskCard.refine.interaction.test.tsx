// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import "../testkit";
import type { Item } from "../../state/session";
import { AskCard } from "./AskCard";

afterEach(cleanup);

const item = (answered?: string[][]) =>
  ({
    t: "ask",
    id: "row",
    ask: { id: "ask", questions: [{ id: "q1", header: "Q1", prompt: "which one", multi: false, options: [{ label: "a" }, { label: "b" }] }] },
    answered,
  }) as Extract<Item, { t: "ask" }>;

const box = () => screen.getByRole("textbox", { name: "其他 —— 自行填写" }) as HTMLInputElement;
const refineButton = () => screen.getByRole("button", { name: "优化提示词" });

describe("optimising the free-text answer of a question card", () => {
  it("offers the rewrite and applies it only when taken", async () => {
    const refine = vi.fn(async () => "请直接删除全部，之后我会单独重装。\n不需要再确认。");
    const answer = vi.fn(async () => {});
    render(<AskCard item={item()} onAnswer={answer} refine={refine} />);
    await userEvent.type(box(), "很难拆分就删完");
    await userEvent.click(refineButton());
    expect(refine).toHaveBeenCalledWith("很难拆分就删完", expect.any(AbortSignal));
    expect(await screen.findByText(/请直接删除全部/)).toBeTruthy();
    expect(box().value).toBe("很难拆分就删完");
    await userEvent.click(screen.getByRole("button", { name: "采用" }));
    expect(box().value).toBe("请直接删除全部，之后我会单独重装。 不需要再确认。");
    expect(screen.queryByLabelText("优化后的提示词")).toBeNull();
    await userEvent.click(screen.getByRole("button", { name: "确认" }));
    expect(answer).toHaveBeenCalledWith("row", "ask", [{ questionId: "q1", selected: ["请直接删除全部，之后我会单独重装。 不需要再确认。"] }]);
  });

  it("cancelling a rewrite in progress leaves the answer as typed and aborts the request", async () => {
    let signal: AbortSignal | undefined;
    const refine = vi.fn((_draft: string, s?: AbortSignal) => { signal = s; return new Promise<string>(() => {}); });
    render(<AskCard item={item()} onAnswer={async () => {}} refine={refine} />);
    await userEvent.type(box(), "draft");
    await userEvent.click(refineButton());
    expect(await screen.findByText("正在优化…")).toBeTruthy();
    await userEvent.click(screen.getByRole("button", { name: "取消" }));
    expect(signal?.aborted).toBe(true);
    expect(screen.queryByLabelText("优化后的提示词")).toBeNull();
    expect(box().value).toBe("draft");
  });

  it("says why a rewrite failed, can try again, and never touches the answer", async () => {
    const refine = vi.fn().mockRejectedValueOnce(new Error("provider unavailable")).mockResolvedValueOnce("better");
    render(<AskCard item={item()} onAnswer={async () => {}} refine={refine} />);
    await userEvent.type(box(), "draft");
    await userEvent.click(refineButton());
    expect((await screen.findByRole("alert")).textContent).toContain("provider unavailable");
    expect(box().value).toBe("draft");
    await userEvent.click(screen.getByRole("button", { name: "重试" }));
    expect(await screen.findByText("better")).toBeTruthy();
    expect(refine).toHaveBeenCalledTimes(2);
  });

  it("asks from the keyboard with Ctrl+Shift+E and puts the card away with Escape, as the composer does", async () => {
    const refine = vi.fn(async () => "rewritten");
    render(<AskCard item={item()} onAnswer={async () => {}} refine={refine} />);
    await userEvent.type(box(), "draft");
    await userEvent.keyboard("{Control>}{Shift>}e{/Shift}{/Control}");
    expect(await screen.findByText("rewritten")).toBeTruthy();
    await userEvent.keyboard("{Escape}");
    expect(screen.queryByLabelText("优化后的提示词")).toBeNull();
    expect(box().value).toBe("draft");
  });

  it("does nothing for an empty box, and Enter still moves the card on", async () => {
    const refine = vi.fn(async () => "x");
    const answer = vi.fn(async () => {});
    render(<AskCard item={item()} onAnswer={answer} refine={refine} />);
    await userEvent.click(refineButton());
    expect(refine).not.toHaveBeenCalled();
    await userEvent.type(box(), "mine{Enter}");
    expect(answer).toHaveBeenCalledWith("row", "ask", [{ questionId: "q1", selected: ["mine"] }]);
  });

  it("is not offered on a settled card or where the window has no rewrite", () => {
    const { unmount } = render(<AskCard item={item([["a"]])} onAnswer={async () => {}} refine={async () => "x"} />);
    expect(screen.queryByRole("button", { name: "优化提示词" })).toBeNull();
    unmount();
    render(<AskCard item={item()} onAnswer={async () => {}} />);
    expect(screen.queryByRole("button", { name: "优化提示词" })).toBeNull();
  });

  it("a card mounted again keeps the typed answer but starts without a stale rewrite, and leaving aborts the one in flight", async () => {
    let signal: AbortSignal | undefined;
    const refine = vi.fn((_d: string, s?: AbortSignal) => { signal = s; return new Promise<string>(() => {}); });
    const first = render(<AskCard item={item()} onAnswer={async () => {}} refine={refine} />);
    await userEvent.type(box(), "draft");
    await userEvent.click(refineButton());
    await screen.findByText("正在优化…");
    first.unmount();
    await waitFor(() => expect(signal?.aborted).toBe(true));
    render(<AskCard item={item()} onAnswer={async () => {}} refine={refine} />);
    expect(screen.queryByLabelText("优化后的提示词")).toBeNull();
    expect(box().value).toBe("draft");
  });
});
