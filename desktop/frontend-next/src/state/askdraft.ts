// What a person has picked on an ask that is still waiting for them. The card
// is unmounted whenever its block scrolls out of the virtualized transcript, so
// its choices cannot live in the component; they live here, keyed by the ask id,
// until the ask is answered or refused.
export interface AskDraft {
  tab: number;
  picks: string[][];
  other: string[];
  otherOn: boolean[];
  note: string[];
}

const drafts = new Map<string, AskDraft>();

export function readAskDraft(id: string, questions: number): AskDraft {
  const saved = drafts.get(id);
  if (saved && saved.picks.length === questions) return saved;
  return {
    tab: 0,
    picks: Array.from({ length: questions }, () => []),
    other: Array.from({ length: questions }, () => ""),
    otherOn: Array.from({ length: questions }, () => false),
    note: Array.from({ length: questions }, () => ""),
  };
}

export function writeAskDraft(id: string, draft: AskDraft): void {
  drafts.set(id, draft);
}

export function clearAskDraft(id: string): void {
  drafts.delete(id);
}

/** Drops every draft; the test setup calls it so one test's picks never reach another's card. */
export function forgetAskDrafts(): void {
  drafts.clear();
}
