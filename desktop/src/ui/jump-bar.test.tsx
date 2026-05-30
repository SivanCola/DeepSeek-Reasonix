// @vitest-environment jsdom

import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeAll, describe, expect, it, vi } from "vitest";
import { JumpBar, type JumpBarItem } from "./jump-bar";

const items: JumpBarItem[] = [
  { index: 0, turn: 1, text: "First question" },
  { index: 2, turn: 2, text: "Second question" },
  { index: 4, turn: 3, text: "Third question" },
];

beforeAll(() => {
  HTMLElement.prototype.scrollIntoView = vi.fn();
});

afterEach(cleanup);

describe("JumpBar", () => {
  it("hides when there is only one user turn", () => {
    const { container } = render(
      <JumpBar activeTurn={1} items={items.slice(0, 1)} onJump={() => {}} />,
    );

    expect(container.querySelector(".jump-bar")).toBeNull();
  });

  it("calls onJump with the selected item", () => {
    const onJump = vi.fn();
    render(<JumpBar activeTurn={2} items={items} onJump={onJump} />);

    fireEvent.click(screen.getByRole("button", { name: /Jump to turn 1/ }));

    expect(onJump).toHaveBeenCalledTimes(1);
    expect(onJump).toHaveBeenCalledWith(items[0]);
  });

  it("marks the active turn", () => {
    render(<JumpBar activeTurn={2} items={items} onJump={() => {}} />);

    const activeDot = screen
      .getByRole("button", { name: /Jump to turn 2/ })
      .querySelector(".jump-dot") as HTMLElement;

    expect(activeDot.style.width).toBe("18px");
    expect(activeDot.style.background).toBe("var(--accent)");
  });
});
