// @vitest-environment jsdom
import { describe, expect, it } from "vitest";
import { act, renderHook } from "@testing-library/react";
import type { Item } from "../state/session";
import { newAnswerAnchor, useFrozenNewAnswerAnchor } from "./newAnswerMark";

const user = (id: string, at?: number, over: Partial<Extract<Item, { t: "user" }>> = {}): Item => ({ t: "user", id, text: id, ...(at === undefined ? {} : { at }), ...over });
const say = (id: string, text = id): Item => ({ t: "say", id, text, done: true });

describe("where the new answers start", () => {
  it("is above the first turn sent after the person last looked", () => {
    const items = [user("u1", 100), say("s1"), user("u2", 300), say("s2"), user("u3", 400), say("s3")];
    expect(newAnswerAnchor(items, 200)).toBe("u2");
  });

  it("is above the answer when the last turn was sent before the person looked", () => {
    const items = [user("u1", 100), say("s1"), user("u2", 150), say("s2"), say("s3")];
    expect(newAnswerAnchor(items, 200)).toBe("s2");
  });

  it("skips a sentence that draws nothing, a queued line and guidance said into a running turn", () => {
    const items = [user("u1", 100), say("blank", " "), say("s1"), user("q", 900, { pending: true }), user("g", 900, { steer: true })];
    expect(newAnswerAnchor(items, 200)).toBe("s1");
  });

  it("falls back to the answer when a message has no time", () => {
    expect(newAnswerAnchor([user("u1"), say("s1"), say("s2")], 200)).toBe("s1");
  });

  it("puts no line where everything above it would be empty, or nothing can be placed", () => {
    expect(newAnswerAnchor([user("u1", 300), say("s1")], 200)).toBeNull();
    expect(newAnswerAnchor([user("u1", 100)], 200)).toBeNull();
    expect(newAnswerAnchor([], 200)).toBeNull();
  });
});

describe("holding the line while the person talks on", () => {
  const before = [user("u1", 100), say("s1"), user("u2", 150), say("s2")];
  const after = [...before, user("u3", 500), say("s3")];

  it("does not follow the person's own new message once a turn is running", () => {
    const { result, rerender } = renderHook((p: { items: Item[]; running: boolean }) => useFrozenNewAnswerAnchor(p.items, 200, p.running), { initialProps: { items: before, running: false } });
    expect(result.current).toBe("s2");
    act(() => rerender({ items: after, running: true }));
    act(() => rerender({ items: after, running: false }));
    expect(result.current).toBe("s2");
  });

  it("moves when the person's own message is not holding it: nothing was running", () => {
    const { result, rerender } = renderHook((p: { items: Item[]; running: boolean }) => useFrozenNewAnswerAnchor(p.items, 200, p.running), { initialProps: { items: before, running: false } });
    rerender({ items: after, running: false });
    expect(result.current).toBe("u3");
  });

  it("takes the next unread span's start, not the old one", () => {
    const { result, rerender } = renderHook((p: { since: number | undefined; running: boolean }) => useFrozenNewAnswerAnchor(after, p.since, p.running), { initialProps: { since: 100 as number | undefined, running: true } });
    act(() => rerender({ since: 100, running: false }));
    expect(result.current).toBe("u2");
    act(() => rerender({ since: 400, running: false }));
    expect(result.current).toBe("u3");
    act(() => rerender({ since: undefined, running: false }));
    expect(result.current).toBeNull();
  });
});
