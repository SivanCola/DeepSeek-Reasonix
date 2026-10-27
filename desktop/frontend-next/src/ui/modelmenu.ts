// modelmenu.ts — the model picker's rows, grouped the way the account is.
import type { ModelEntry } from "../port/port";
import type { MenuItem } from "./Menu";
import { groupVendors } from "./Models";
import { t } from "../i18n";
import { KIND_LABEL } from "./vendors";
import { orderAccounts } from "../state/providerorder";
import { modelHealthStatus, type ModelHealth } from "./modelhealth";

type ModelMenuItem = MenuItem & { account?: { heading: MenuItem; order: number } };

// modelTooltip names the active model with the picker's canonical account label.
export function modelTooltip(models: ModelEntry[], current: string | undefined, label: string): string {
  const account = groupVendors(models).find((a) =>
    a.kinds.some((kind) => a.byKind[kind].some((model) => model.ref === current)));
  return account ? `${account.label} / ${label}` : current ?? label;
}

// Every account gets a heading with its endpoint: the provider name is the
// config's word for an entry, not the user's for an endpoint, and two doors onto
// one account share it. Rows carry the wire format beside the model name.
export function modelMenu(models: ModelEntry[], order: readonly string[] = [], current?: string, health: ModelHealth = {}): ModelMenuItem[] {
  const accounts = orderAccounts(groupVendors(models), order);
  const out: ModelMenuItem[] = [];
  for (const [i, a] of accounts.entries()) {
    const mono = monogram(a.label, a.key);
    const mark = a.kinds.flatMap((k) => a.byKind[k]).map((m) => m.serviceMark ?? m.mark).find(Boolean);
    const heading = { value: `__account:${a.key}`, label: a.label, right: a.host, header: true, divide: i > 0, mono, mark };
    out.push(heading);
    for (const kind of a.kinds) {
      for (const m of a.byKind[kind]) {
        out.push({
          value: m.ref,
          label: m.modelAlias?.trim() || m.model,
          desc: m.modelAlias?.trim() ? m.model : undefined,
          account: { heading, order: i },
          status: modelHealthStatus(health[m.provider]),
          meta: t(KIND_LABEL[kind] ?? kind),
          badge: m.vision ? t("读图") : undefined,
          mono,
          mark: m.mark,
        });
      }
    }
  }
  const held = out.find((item) => item.value === current);
  if (held) {
    out.splice(out.indexOf(held), 1);
    for (let i = out.length - 1; i >= 0; i--) {
      if (out[i].header && (!out[i + 1] || out[i + 1].header)) out.splice(i, 1);
    }
    for (const item of out) if (item.header) item.divide = true;
    out.unshift(held);
  }
  out.push({
    value: "__manage-models",
    label: t("管理模型与连接"),
    desc: t("服务商、API 与可用模型"),
    plain: true,
    divide: out.length > 0,
  });
  return out;
}

const TONES = 5;

// A letter chip stands in for a vendor mark: the same provider always draws the
// same letter and tone, where no declared vendor has artwork.
export function monogram(label: string, key: string): { letter: string; tone: number } {
  const letter = [...label.trim()].find((c) => /[\p{L}\p{N}]/u.test(c))?.toUpperCase() ?? "?";
  let h = 0;
  for (const c of key) h = (h * 31 + c.charCodeAt(0)) >>> 0;
  return { letter, tone: h % TONES };
}

/** The headings a menu offers to narrow by, in the order they appear. */
export function groupHeadings(items: ModelMenuItem[]): MenuItem[] {
  const accounts = new Map(items.filter((it) => it.account).map((it) => [it.account!.heading.value, it.account!]));
  return [...accounts.values()].sort((a, b) => a.order - b.order).map((a) => a.heading);
}

/** The remembered choices that are still on offer. A choice for an account no
 *  longer listed must not empty the menu, so it simply stops counting. */
export function chosenGroups(items: ModelMenuItem[], selected: readonly string[]): string[] {
  const offered = new Set(groupHeadings(items).map((h) => h.value));
  return selected.filter((key) => offered.has(key));
}

/** The rows of the chosen groups. What belongs to no group (the current model,
 *  pinned before the first heading) and every action stay. */
export function narrowToGroups(items: ModelMenuItem[], chosen: readonly string[]): ModelMenuItem[] {
  if (chosen.length === 0) return items;
  let inGroup = false;
  let keep = true;
  return items.filter((it) => {
    if (it.header) {
      inGroup = true;
      keep = chosen.includes(it.value);
      return keep;
    }
    if (it.plain) return true;
    return inGroup ? keep : true;
  });
}

export function compactModelMenu(items: ModelMenuItem[], current?: string): MenuItem[] {
  const rows = items.filter((it) => !it.header && !it.plain).map((it) => ({
    ...it, selectionMark: true, meta: [it.account?.heading.label, it.meta].filter(Boolean).join(" · "),
    searchText: it.account?.heading.right,
  }));
  const held = rows.find((it) => it.value === current);
  const others = rows.filter((it) => it !== held);
  return [
    ...(held ? [{ value: "__current", label: t("当前选择"), header: true }, held] : []),
    ...(others.length ? [{ value: "__others", label: t("其他模型"), header: true }, ...others] : []),
    ...items.filter((it) => it.plain).map((it) => ({ ...it, desc: undefined, note: t("连接检测不代表模型调用可用") })),
  ];
}
