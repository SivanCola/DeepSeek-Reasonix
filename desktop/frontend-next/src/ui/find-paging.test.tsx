// @vitest-environment jsdom
import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { Find } from "./Find";
import { useFind } from "./usefind";

vi.mock("../i18n", () => ({ t: (text: string) => text }));
afterEach(cleanup);

function Search({ pending, failed = false }: { pending: boolean; failed?: boolean }) {
  const find = useFind([], 0, true, () => {});
  return <Find find={{ ...find, open: true, query: "earlier message" }} pending={pending} failed={failed} />;
}

it("does not declare no results until all history has been searched", () => {
  const { rerender } = render(<Search pending />);
  expect(screen.getByText("正在查找较早消息…")).toBeTruthy();
  expect(screen.queryByText("无结果")).toBeNull();
  rerender(<Search pending failed />);
  expect(screen.getByText("部分历史尚未读取")).toBeTruthy();
  expect(screen.queryByText("无结果")).toBeNull();
  rerender(<Search pending={false} />);
  expect(screen.getByText("无结果")).toBeTruthy();
});
