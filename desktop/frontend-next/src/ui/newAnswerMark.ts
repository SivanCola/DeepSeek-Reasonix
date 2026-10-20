import { useMemo, useRef } from "react";
import type { Item } from "../state/session";

/** newAnswerAnchor names the item the "new answers start here" line sits above:
 *  the first turn the person sent after they last looked, else, when the last
 *  turn was sent before that, the first thing said in answer to it. Null when
 *  nothing precedes it (everything is new) or nothing can be placed. */
export function newAnswerAnchor(items: Item[], since: number): string | null {
  const sent = items.filter((i): i is Extract<Item, { t: "user" }> => i.t === "user" && !i.pending && !i.steer && i.at !== undefined);
  const later = sent.find((i) => i.at! > since);
  let id: string | undefined = later?.id;
  if (!id) {
    let last = items.length - 1;
    while (last >= 0 && !(items[last].t === "user" && !(items[last] as { pending?: boolean }).pending && !(items[last] as { steer?: boolean }).steer)) last--;
    id = items.slice(last + 1).find((i) => i.t === "say" && (i.text.trim() || i.reasoning?.trim()))?.id;
  }
  if (!id) return null;
  return items.findIndex((i) => i.id === id) > 0 ? id : null;
}

/** useFrozenNewAnswerAnchor holds the line where it was once a turn has run in
 *  this window: a message sent after looking must not pull it down to itself.
 *  The held value is idempotent for a given render, so a repeated render is safe. */
export function useFrozenNewAnswerAnchor(items: Item[], since: number | undefined, running: boolean): string | null {
  const live = useMemo(() => (since === undefined ? null : newAnswerAnchor(items, since)), [items, since]);
  const held = useRef<{ since: number | undefined; id: string | null; frozen: boolean }>({ since: undefined, id: null, frozen: false });
  const h = held.current;
  if (h.since !== since) {
    h.since = since;
    h.id = live;
    h.frozen = running;
  } else if (!h.frozen) {
    if (running) h.frozen = true;
    else h.id = live;
  }
  return since === undefined ? null : h.frozen ? h.id : live;
}
