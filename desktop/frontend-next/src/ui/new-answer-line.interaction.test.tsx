// @vitest-environment jsdom
import { afterEach, describe, expect, it } from "vitest";
import { cleanup, render } from "@testing-library/react";
import "./testkit";
import { Transcript } from "./Transcript";
import { NewAnswerAnchor } from "./NewAnswerLine";
import type { Item } from "../state/session";

afterEach(cleanup);

const noop = async () => undefined as never;
const same = { entering: [] as string[], onEntered: () => {}, waiting: {}, scroll: { current: null }, onExtInvoke: () => {}, checkpoints: new Map(), onOpenProject: () => {}, onKeepHere: () => {}, takeovers: {} };
const items: Item[] = [
  { t: "user", id: "u1", text: "first question", authoredTurn: 1, msgIndex: 0, at: 100 },
  { t: "say", id: "s1", text: "first answer", done: true },
  { t: "user", id: "u2", text: "second question", authoredTurn: 2, msgIndex: 2, at: 300 },
  { t: "say", id: "s2", text: "second answer", done: true },
];
const draw = (anchor: string | null) => render(
  <NewAnswerAnchor.Provider value={anchor}>
    <Transcript items={items} {...same} revision={1} hidden={false} focus={null} onApprove={noop} onFullAccess={noop} onPlan={noop} onAnswer={noop} onForget={noop}
      onExtSubmit={noop} onPrepareRewind={noop} onCommitRewind={noop} onPrepareFileRevert={noop} onCommitFileRevert={noop} needsProject={false} />
  </NewAnswerAnchor.Provider>,
);

describe("the new-answer line in the transcript", () => {
  it("is drawn once, just above the anchored turn, and says nothing about how many", () => {
    const { container } = draw("u2");
    const lines = container.querySelectorAll("[data-new-answer]");
    expect(lines.length).toBe(1);
    const line = lines[0];
    expect(line.textContent).toBe("从这里开始有新回答");
    expect(container.textContent!.indexOf("first answer")).toBeLessThan(container.textContent!.indexOf("从这里开始有新回答"));
    expect(container.textContent!.indexOf("从这里开始有新回答")).toBeLessThan(container.textContent!.indexOf("second question"));
    expect(line.textContent).not.toMatch(/\d/);
  });

  it("takes the place of the rule that opens the turn instead of doubling it", () => {
    const { container } = draw("u2");
    expect(container.querySelectorAll("hr.turn-rule").length).toBe(1);
    cleanup();
    const plain = draw(null);
    expect(plain.container.querySelectorAll("hr.turn-rule").length).toBe(2);
  });

  it("is absent without an anchor", () => {
    const { container } = draw(null);
    expect(container.querySelector("[data-new-answer]")).toBeNull();
  });
});
