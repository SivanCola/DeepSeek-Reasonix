// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import "./testkit";
import { Feedback } from "./Feedback";
import { MockPort } from "../port/mock";
import type { AgentPort } from "../port/port";

afterEach(cleanup);
const png = (n: string) => new File([new Uint8Array(64)], n, { type: "image/png" });

describe("a paste in the frame after the form appears is taken", () => {
  // The listener must be in place in the commit that draws the form. Dropped
  // pastes were a race, so the check repeats; it failed in about one round in ten.
  for (let i = 0; i < 40; i++) {
    it(`round ${i}`, async () => {
      const port = new MockPort() as unknown as AgentPort;
      port.openExternal = vi.fn(async () => {});
      render(<Feedback port={port} tab="send" onClose={vi.fn()} onError={vi.fn()} />);
      await screen.findByText("随反馈一起发送的环境信息");
      const e = new Event("paste", { bubbles: true, cancelable: true }) as Event & { clipboardData: unknown };
      e.clipboardData = { files: [png("race.png")], items: [], getData: () => "" };
      document.querySelector("form")!.dispatchEvent(e);
      expect(e.defaultPrevented).toBe(true);
    });
  }
});
