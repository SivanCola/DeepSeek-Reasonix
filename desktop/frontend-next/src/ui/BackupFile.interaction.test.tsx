// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from "vitest";
import { act, cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import "./testkit";
import { BackupFile } from "./BackupFile";
import { MockPort } from "../port/mock";
import { HttpError, type AgentPort, type BackupFileCatalog } from "../port/port";

afterEach(cleanup);

const passwordFields = () => document.querySelectorAll<HTMLInputElement>('input[type="password"]');

describe("backup to a file", () => {
  it("leaves API keys unticked, and sends the ticked categories with the passphrase to be sealed", async () => {
    const port = new MockPort() as unknown as AgentPort;
    const exporter = vi.spyOn(port, "exportBackupFile");
    const save = vi.spyOn(port, "saveText");
    render(<BackupFile port={port} />);

    const secrets = await screen.findByRole("checkbox", { name: /API 密钥/ });
    expect((secrets as HTMLInputElement).checked).toBe(false);
    const [pass, again] = passwordFields();
    await userEvent.type(pass!, "a long passphrase");
    await userEvent.type(again!, "a long passphrase");
    await userEvent.click(screen.getByRole("button", { name: "导出到文件…" }));

    await waitFor(() => expect(exporter).toHaveBeenCalledOnce());
    const [categories, passphrase] = exporter.mock.calls[0]!;
    expect(categories).not.toContain("secrets");
    expect(passphrase).toBe("a long passphrase");
    await waitFor(() => expect(save).toHaveBeenCalledWith("reasonix-config-demo.rxcb", "UlhDQgAAAA=="));
    expect(passwordFields()[0]!.value).toBe("");
  });

  it("holds the button back, and says why, until the passphrase is long enough and repeated", async () => {
    const port = new MockPort() as unknown as AgentPort;
    render(<BackupFile port={port} />);
    const button = await screen.findByRole<HTMLButtonElement>("button", { name: "导出到文件…" });
    expect(button.disabled).toBe(true);
    const [pass, again] = passwordFields();
    await userEvent.type(pass!, "abcd");
    expect(document.querySelector('[data-action="backup.wait"]')!.textContent).toBe("口令还差 6 个字符");
    await userEvent.type(pass!, "efghij");
    await userEvent.type(again!, "abcdefghiX");
    expect(button.disabled).toBe(true);
    expect(screen.getByText("两次输入的口令不一致")).toBeTruthy();
  });

  it("warns that ticked keys travel in the file", async () => {
    const port = new MockPort() as unknown as AgentPort;
    render(<BackupFile port={port} />);
    const secrets = await screen.findByRole("checkbox", { name: /API 密钥/ });
    await userEvent.click(secrets);
    expect(secrets.closest("label")!.hasAttribute("data-warn")).toBe(true);
  });

  it("opens a chosen file with its passphrase, previews it, and applies through the file route", async () => {
    const port = new MockPort() as unknown as AgentPort;
    const preview = vi.spyOn(port, "previewBackupFile");
    const apply = vi.spyOn(port, "applyBackupFile");
    render(<BackupFile port={port} />);
    await screen.findByRole("button", { name: "导出到文件…" });

    const input = document.querySelector<HTMLInputElement>('input[type="file"]')!;
    await userEvent.upload(input, new File(["  U0VBTEVE\n"], "laptop.rxcb"));
    await userEvent.type(await screen.findByLabelText("加密口令"), "a long passphrase");
    await userEvent.click(screen.getByRole("button", { name: "预览差异" }));

    await waitFor(() => expect(preview).toHaveBeenCalledWith("U0VBTEVE", "a long passphrase"));
    await userEvent.click(await screen.findByRole("checkbox", { name: /docs\/REASONIX\.md/ }));
    await userEvent.click(await screen.findByRole("button", { name: /恢复所选 1 项/ }));
    await waitFor(() => expect(apply).toHaveBeenCalledWith("demo", ["memory:docs/REASONIX.md"], []));
  });

  it("only identifies a host-only refusal when the producer declares it", async () => {
    const port = new MockPort() as unknown as AgentPort;
    vi.spyOn(port, "backupFileCatalog").mockRejectedValue(new HttpError(404, "device", { code: "device.host_only" }));
    const view = render(<BackupFile port={port} />);
    await waitFor(() => expect(port.backupFileCatalog).toHaveBeenCalled());
    await waitFor(() => expect(view.container.textContent).toContain("本机窗口"));
    expect(screen.queryByRole("button", { name: "导出到文件…" })).toBeNull();
  });

  it.each([new Error("catalog unavailable"), new HttpError(404, "catalog unavailable")])("shows a real load failure and lets a retry recover the catalogue: %s", async (failure) => {
    const port = new MockPort();
    const catalog = await port.backupFileCatalog();
    vi.spyOn(port, "backupFileCatalog").mockRejectedValueOnce(failure).mockResolvedValue(catalog);
    render(<BackupFile port={port} />);
    expect(await screen.findByRole("alert")).toHaveProperty("textContent", expect.stringContaining("catalog unavailable"));
    expect(screen.queryByText("配置文件的导出与导入只在本机窗口可用。")).toBeNull();
    await userEvent.click(screen.getByRole("button", { name: "重试" }));
    expect(await screen.findByRole("button", { name: "导出到文件…" })).toBeTruthy();
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it.each(["success", "failure"])("ignores an old port's late catalogue %s", async (result) => {
    const a = new MockPort(), b = new MockPort();
    const catalog = await a.backupFileCatalog();
    let resolve!: (value: BackupFileCatalog) => void;
    let reject!: (error: unknown) => void;
    vi.spyOn(a, "backupFileCatalog").mockReturnValue(new Promise((yes, no) => { resolve = yes; reject = no; }));
    vi.spyOn(b, "backupFileCatalog").mockResolvedValue({ ...catalog, minPassphrase: 18 });
    const view = render(<BackupFile port={a} />);
    view.rerender(<BackupFile port={b} />);
    await screen.findByLabelText("加密口令（至少 18 个字符）");
    await act(async () => result === "success" ? resolve(catalog) : reject(new Error("A late error")));
    expect(screen.getByLabelText("加密口令（至少 18 个字符）")).toBeTruthy();
    expect(screen.queryByText(/A late error|只在本机窗口可用/)).toBeNull();
  });

  it("does not save an old owner's export after the selected port changes", async () => {
    const a = new MockPort(), b = new MockPort();
    let finish!: (value: Awaited<ReturnType<typeof a.exportBackupFile>>) => void;
    vi.spyOn(a, "exportBackupFile").mockReturnValue(new Promise((resolve) => { finish = resolve; }));
    const save = vi.spyOn(a, "saveText");
    const view = render(<BackupFile port={a} />);
    await screen.findByRole("button", { name: "导出到文件…" });
    const [pass, again] = passwordFields();
    await userEvent.type(pass!, "long fixture passphrase");
    await userEvent.type(again!, "long fixture passphrase");
    await userEvent.click(screen.getByRole("button", { name: "导出到文件…" }));
    expect(a.exportBackupFile).toHaveBeenCalledOnce();
    view.rerender(<BackupFile port={b} />);
    await screen.findByRole("button", { name: "导出到文件…" });
    await act(async () => finish({ name: "old.rxcb", file: "QUJD" }));
    expect(save).not.toHaveBeenCalled();
    expect(passwordFields()[0]!.value).toBe("");
    expect(screen.queryByText("已导出。")).toBeNull();
  });

  it("starts an import by ticking only what this machine lacks, never what it would replace", async () => {
    const port = new MockPort() as unknown as AgentPort;
    vi.spyOn(port, "previewBackupFile").mockResolvedValue({
      planId: "p", createdAt: "2026-10-14T00:00:00Z", platform: "darwin/arm64", samePlatform: true, categories: ["settings"],
      items: [
        { id: "provider:new", category: "settings", kind: "provider", name: "brand-new", status: "new", recommended: true },
        { id: "provider:work", category: "settings", kind: "provider", name: "same-name", status: "changed", recommended: true },
      ],
    });
    render(<BackupFile port={port} />);
    await screen.findByRole("button", { name: "导出到文件…" });
    await userEvent.upload(document.querySelector<HTMLInputElement>('input[type="file"]')!, new File(["QQ=="], "x.rxcb"));
    await userEvent.type(await screen.findByLabelText("加密口令"), "a long passphrase");
    await userEvent.click(screen.getByRole("button", { name: "预览差异" }));

    const picks = await screen.findAllByRole("checkbox", { name: /brand-new|same-name/ });
    expect(picks.map((c) => (c as HTMLInputElement).checked)).toEqual([true, false]);
  });
});
