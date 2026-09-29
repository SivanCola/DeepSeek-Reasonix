// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import "./testkit";
import { Market } from "./Market";
import { MockPort } from "../port/mock";
import { HttpError } from "../port/http_error";
import type { AgentPort, MarketList, MarketPackage, MarketQuery } from "../port/port";

afterEach(cleanup);

const row = (slug: string, pinned?: boolean): MarketPackage => ({
  kind: "skill", handle: slug.split("/")[0], name: slug.split("/")[1], slug, summary: "s", description: "", homepage: "",
  repoUrl: "", tags: [], latestVersion: "1.0.0", installCount: 3, starCount: 0, upCount: 0, downCount: 0, approvalRate: null, score: 0, verified: false, status: "active", updatedAt: "", pinned,
});

describe("market list", () => {
  it("asks the registry for installable packages instead of filtering a page", async () => {
    const port = new MockPort() as unknown as AgentPort;
    const asked: MarketQuery[] = [];
    port.marketList = vi.fn(async (q: MarketQuery) => {
      asked.push(q);
      return { packages: q.pinned ? [row("a/kit", true)] : [row("a/kit", true), row("b/raw", false), row("c/old")], limit: 24, offset: 0 };
    });
    render(<Market port={port} onInstalled={() => {}} />);

    await screen.findByText("kit");
    expect(asked[0]).toMatchObject({ pinned: true, offset: 0 });
    expect(screen.queryByText("raw")).toBeNull();
    await userEvent.click(screen.getByRole("checkbox", { name: "只看已固定" }));
    await screen.findByText("raw");
    expect(screen.getByText("已固定")).toBeTruthy();
    expect(screen.getByText("未固定")).toBeTruthy();
    expect(screen.getByText("固定状态未知")).toBeTruthy();
    expect(asked.at(-1)).toMatchObject({ pinned: false, offset: 0 });
  });

  it("offers all packages when the registry cannot filter", async () => {
    const port = new MockPort() as unknown as AgentPort;
    port.marketList = vi.fn(async (q: MarketQuery) => {
      if (q.pinned) throw new HttpError(502, "unsupported", { code: "market.filter_unsupported" });
      return { packages: [row("b/raw", false)], limit: 24, offset: 0 };
    });
    render(<Market port={port} onInstalled={() => {}} />);
    await userEvent.click(await screen.findByRole("button", { name: "查看全部包" }));
    await screen.findByText("raw");
    expect(screen.getByRole<HTMLInputElement>("checkbox", { name: "只看已固定" }).checked).toBe(false);
  });

  it("ignores a previous filtered page after switching to all packages", async () => {
    const port = new MockPort() as unknown as AgentPort;
    let finish!: (value: MarketList) => void;
    port.marketList = vi.fn((q: MarketQuery) => q.pinned
      ? new Promise<MarketList>((resolve) => { finish = resolve; })
      : Promise.resolve({ packages: [row("b/raw", false)], limit: 24, offset: 0 }));
    render(<Market port={port} onInstalled={() => {}} />);
    await waitFor(() => expect(finish).toBeDefined());
    await userEvent.click(screen.getByRole("checkbox", { name: "只看已固定" }));
    await screen.findByText("raw");
    finish({ packages: [row("a/kit", true)], limit: 24, offset: 0 });
    await waitFor(() => expect(screen.queryByText("kit")).toBeNull());
  });

  it("opens an entry from the keyboard", async () => {
    const port = new MockPort() as unknown as AgentPort;
    port.marketList = async () => ({ packages: [row("a/kit", true)], limit: 24, offset: 0 });
    port.marketDetail = vi.fn(async () => ({ package: row("a/kit", true), pinned: true }));
    render(<Market port={port} onInstalled={() => {}} />);

    (await screen.findByRole("button", { name: /kit/ })).focus();
    await userEvent.keyboard("{Enter}");
    await waitFor(() => expect(port.marketDetail).toHaveBeenCalledWith("a/kit"));
  });

  it("lists each applied capability and opens its installed location", async () => {
    const port = new MockPort() as unknown as AgentPort;
    const onViewInstalled = vi.fn();
    const install = port.installMarket.bind(port);
    port.installMarket = async (req) => {
      const out = await install(req);
      return { ...out, actions: [{ kind: "plugin", action: "install_plugin_package", status: "done", riskLevel: "high", name: "manifest-kit" }, ...(out.actions ?? [])] };
    };
    render(<Market port={port} onInstalled={() => {}} onViewInstalled={onViewInstalled} />);
    await userEvent.click(await screen.findByRole("button", { name: /review-kit/ }));
    await userEvent.click(await screen.findByRole("button", { name: "查看将安装的内容" }));
    await userEvent.click(screen.getByRole("checkbox", { name: "我已看过这 3 个技能，全部安装" }));
    await userEvent.click(screen.getByRole("button", { name: "安装" }));
    await screen.findByRole("button", { name: "查看已安装能力" });
    expect(document.querySelector(".mkt-installed")?.textContent).toContain("review");
    expect(document.querySelector(".mkt-installed")?.textContent).toContain("pr-notes");
    await userEvent.click(screen.getByRole("button", { name: "查看已安装能力" }));
    expect(onViewInstalled).toHaveBeenCalledWith("plugin", "manifest-kit");
  });

  it("installs an unpinned package only on trust, through the same preview", async () => {
    const port = new MockPort() as unknown as AgentPort;
    const plan = vi.spyOn(port, "planMarket");
    const install = vi.spyOn(port, "installMarket");
    render(<Market port={port} onInstalled={() => {}} />);
    await userEvent.click(await screen.findByRole("checkbox", { name: "只看已固定" }));
    await userEvent.click(await screen.findByRole("button", { name: /lm-studio-vision-bridge/ }));

    await screen.findByText("未固定——审核时没有记录内容摘要");
    expect(screen.getByText("内容未经审核固定")).toBeTruthy();
    expect(screen.queryByRole("button", { name: "查看将安装的内容" })).toBeNull();
    await userEvent.click(screen.getByRole("button", { name: "信任并安装" }));

    await screen.findByText("你选择了信任这个发布者");
    expect(plan).toHaveBeenCalledWith(expect.objectContaining({ slug: "1574022644/lm-studio-vision-bridge", trust: true }));
    expect(install).not.toHaveBeenCalled();
    await userEvent.click(screen.getByRole("button", { name: "安装" }));

    await waitFor(() => expect(install).toHaveBeenCalledTimes(1));
    const req = install.mock.calls[0]![0];
    const shown = await plan.mock.results[0]!.value;
    expect(req).toMatchObject({ trust: true, digest: shown.contentDigest, planId: shown.planId, version: "2.0.0" });
  });

  it("does not ask for trust on a pinned package", async () => {
    const port = new MockPort() as unknown as AgentPort;
    const plan = vi.spyOn(port, "planMarket");
    render(<Market port={port} onInstalled={() => {}} />);
    await userEvent.click(await screen.findByRole("button", { name: /make-ui-not-ai/ }));
    expect(screen.queryByRole("button", { name: "信任并安装" })).toBeNull();
    await userEvent.click(await screen.findByRole("button", { name: "查看将安装的内容" }));
    await screen.findByText("已按内容摘要核对：与审核时固定的版本一致。");
    expect(plan.mock.calls[0]![0].trust).toBeUndefined();
  });
});
