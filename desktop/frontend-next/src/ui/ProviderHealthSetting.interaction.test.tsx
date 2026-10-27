// @vitest-environment jsdom
import { act, cleanup, fireEvent, render, renderHook, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { ProviderHealthSetting } from "./ProviderHealthSetting";
import { useModelHealth } from "./modelhealth";
import { MockPort } from "../port/mock";

afterEach(() => { cleanup(); vi.restoreAllMocks(); });
const models = [{ ref: "one/alpha", provider: "one", model: "alpha" }];

it("requires opt-in, persists both switch directions and stops probes after disabling", async () => {
  const port = new MockPort(), changed = vi.fn(), failed = vi.fn();
  const probe = vi.spyOn(port, "providerHealth");
  const saved = vi.spyOn(port, "saveProviderHealthSettings");
  const { result, rerender } = renderHook(({ pulse }) => useModelHealth(port, pulse), { initialProps: { pulse: 0 } });
  render(<ProviderHealthSetting port={port} onChanged={changed} onFailed={failed} />);
  const toggle = screen.getByRole("switch");
  await waitFor(() => expect(toggle.hasAttribute("disabled")).toBe(false));
  expect(toggle.getAttribute("aria-checked")).toBe("false");
  await act(() => result.current.check(Promise.resolve(models)));
  expect(probe).not.toHaveBeenCalled();
  fireEvent.click(toggle);
  await waitFor(() => expect(changed).toHaveBeenCalledTimes(1));
  expect(saved).toHaveBeenLastCalledWith(true);
  await act(() => result.current.check(Promise.resolve(models)));
  expect(probe).toHaveBeenCalledTimes(1);
  fireEvent.click(toggle);
  await waitFor(() => expect(changed).toHaveBeenCalledTimes(2));
  expect(saved).toHaveBeenLastCalledWith(false);
  rerender({ pulse: 1 });
  await act(() => result.current.check(Promise.resolve(models)));
  expect(probe).toHaveBeenCalledTimes(1);
  expect(result.current.readings).toEqual({});
  expect(failed).not.toHaveBeenCalled();
});

it("fails closed on unavailable settings and ignores late consent after a runtime switch", async () => {
  const old = new MockPort(), port = new MockPort();
  let finish!: (value: { enabled: boolean }) => void;
  vi.spyOn(old, "providerHealthSettings").mockReturnValue(new Promise((resolve) => { finish = resolve; }));
  vi.spyOn(port, "providerHealthSettings").mockRejectedValue(new Error("unsupported"));
  const oldProbe = vi.spyOn(old, "providerHealth"), probe = vi.spyOn(port, "providerHealth");
  const { result, rerender } = renderHook(({ port }) => useModelHealth(port, 0), { initialProps: { port: old } });
  let pending!: Promise<void>;
  act(() => { pending = result.current.check(Promise.resolve(models)); });
  rerender({ port });
  await act(async () => { finish({ enabled: true }); await pending; await result.current.check(Promise.resolve(models)); });
  expect(oldProbe).not.toHaveBeenCalled();
  expect(probe).not.toHaveBeenCalled();
  expect(result.current.readings).toEqual({});
});
