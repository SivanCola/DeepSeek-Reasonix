// @vitest-environment jsdom
import { afterEach, beforeEach, expect, it } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import "./testkit";
import { OpenSessionLimitSection } from "./OpenSessionLimitSection";
import { openSessionLimit, setOpenSessionLimit } from "../state/prefs";

beforeEach(() => localStorage.clear());
afterEach(cleanup);

const box = () => screen.getByRole("textbox", { name: "会话标签数上限" }) as HTMLInputElement;

it("starts at zero, which the row says means no limit", () => {
  render(<OpenSessionLimitSection />);
  expect(box().value).toBe("0");
  expect(screen.getByText(/0 表示不限制/)).toBeTruthy();
});

it("keeps only digits as they are typed and saves the number once it is committed", async () => {
  render(<OpenSessionLimitSection />);
  await userEvent.clear(box());
  await userEvent.type(box(), "1a2-");
  expect(box().value).toBe("12");
  expect(openSessionLimit()).toBe(0);
  await userEvent.type(box(), "{Enter}");
  expect(openSessionLimit()).toBe(12);
  expect(localStorage.getItem("rx-open-session-limit")).toBe("12");
});

it("returns to never folding when the box is emptied or set to zero", async () => {
  setOpenSessionLimit(8);
  render(<OpenSessionLimitSection />);
  expect(box().value).toBe("8");
  await userEvent.clear(box());
  await userEvent.tab();
  expect(openSessionLimit()).toBe(0);
  expect(box().value).toBe("0");
  expect(localStorage.getItem("rx-open-session-limit")).toBeNull();
});

it("shows a choice made elsewhere, such as another window", () => {
  localStorage.setItem("rx-open-session-limit", "6");
  render(<OpenSessionLimitSection />);
  expect(box().value).toBe("6");
});
