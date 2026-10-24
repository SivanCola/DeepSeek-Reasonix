// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { Item } from "../../state/session";
import { clearAskDraft } from "../../state/askdraft";
import { AskCard } from "./AskCard";

afterEach(() => {
  cleanup();
  clearAskDraft("draft-ask");
});

const ask = (answered?: string[][]) =>
  ({
    t: "ask",
    id: "row",
    ask: {
      id: "draft-ask",
      questions: [
        { id: "q1", header: "Q1", prompt: "first", multi: true, options: [{ label: "a1" }, { label: "b1" }] },
        { id: "q2", header: "Q2", prompt: "second", multi: false, options: [{ label: "a2" }, { label: "b2" }] },
      ],
    },
    answered,
  }) as Extract<Item, { t: "ask" }>;

const click = (name: string | RegExp) => userEvent.click(screen.getByRole("button", { name }));

describe("an unanswered ask keeps what was picked when its block is unmounted", () => {
  it("restores picks and the current question after a remount", async () => {
    const first = render(<AskCard item={ask()} onAnswer={vi.fn()} />);
    await click(/a1/);
    await click("下一题（1/2）");
    await click(/b2/);
    first.unmount();

    render(<AskCard item={ask()} onAnswer={vi.fn()} />);
    expect(screen.getByRole("tab", { name: /Q2/ }).getAttribute("aria-selected")).toBe("true");
    await userEvent.click(screen.getByRole("tab", { name: /Q1/ }));
    expect(screen.getByRole("button", { name: /a1/ }).getAttribute("aria-pressed")).toBe("true");
    await userEvent.click(screen.getByRole("tab", { name: /Q2/ }));
    expect(screen.getByRole("button", { name: /b2/ }).getAttribute("aria-pressed")).toBe("true");
  });

  it("restores a note typed on a single-choice pick and free text on a multi-choice question", async () => {
    const first = render(<AskCard item={ask()} onAnswer={vi.fn()} />);
    await userEvent.type(screen.getAllByRole("textbox", { name: "其他 —— 自行填写" })[0], "mine");
    await click("下一题（1/2）");
    await click(/a2/);
    await userEvent.type(screen.getByRole("textbox", { name: "补充说明（可选）" }), "why");
    first.unmount();

    render(<AskCard item={ask()} onAnswer={vi.fn()} />);
    expect((screen.getByRole("textbox", { name: "补充说明（可选）" }) as HTMLInputElement).value).toBe("why");
    await userEvent.click(screen.getByRole("tab", { name: /Q1/ }));
    expect((screen.getAllByRole("textbox", { name: "其他 —— 自行填写" })[0] as HTMLInputElement).value).toBe("mine");
  });

  it("forgets the draft once the ask is answered, so a later card starts empty", async () => {
    const first = render(<AskCard item={ask()} onAnswer={vi.fn()} />);
    await click(/a1/);
    first.rerender(<AskCard item={ask([["a1"], ["b2"]])} onAnswer={vi.fn()} />);
    first.unmount();
    render(<AskCard item={ask()} onAnswer={vi.fn()} />);
    expect(screen.getByRole("tab", { name: /Q1/ }).getAttribute("aria-selected")).toBe("true");
    expect(screen.getByRole("button", { name: /a1/ }).getAttribute("aria-pressed")).not.toBe("true");
  });
});
