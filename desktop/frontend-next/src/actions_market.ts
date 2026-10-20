import type { UIAction } from "./actions";

export const MARKET_ACTIONS: UIAction[] = [
  // The community market: reads are views, and the one write is the install
  // its confirmation page answers with the plan the person just read.
  { id: "market.search", kind: "view", target: "none", proof: "interaction" },
  { id: "market.kind", kind: "view", target: "none", proof: "interaction" },
  { id: "market.sort", kind: "view", target: "none", proof: "interaction" },
  { id: "market.pinned", kind: "view", target: "none", proof: "interaction" },
  { id: "market.show-all", kind: "view", target: "none", proof: "interaction" },
  { id: "market.retry", kind: "view", target: "none", proof: "interaction" },
  { id: "market.detail-retry", kind: "view", target: "none", proof: "interaction" },
  { id: "market.more", kind: "view", target: "none", proof: "interaction" },
  { id: "market.open", kind: "navigation", target: "none", proof: "interaction" },
  { id: "market.back", kind: "navigation", target: "none", proof: "interaction" },
  { id: "market.cancel", kind: "navigation", target: "none", proof: "interaction" },
  { id: "market.inspect", kind: "kernel-mutation", target: "none", proof: "interaction" },
  { id: "market.trust", kind: "kernel-mutation", target: "none", proof: "interaction" },
  { id: "market.confirm-many", kind: "view", target: "none", proof: "interaction" },
  { id: "market.install", kind: "kernel-mutation", target: "none", proof: "authority-effect" },
  { id: "market.view-installed", kind: "navigation", target: "none", proof: "interaction" },
  // Publishing: the draft is on screen only, and the submission is the one
  // write, spending the account session on the registry.
  { id: "market.view", kind: "view", target: "none", proof: "interaction" },
  { id: "market.signin", kind: "navigation", target: "none", proof: "interaction" },
  { id: "market.draft", kind: "view", target: "none", proof: "interaction" },
  { id: "market.publish", kind: "kernel-mutation", target: "none", proof: "interaction" },
  { id: "market.prepare-version", kind: "view", target: "none", proof: "interaction" },
  { id: "market.publish-again", kind: "view", target: "none", proof: "interaction" },
  // The account's own packages: a preview, an install pinned to that preview's
  // digest, and sending a private one to review.
  { id: "market.own-inspect", kind: "kernel-mutation", target: "none", proof: "interaction" },
  { id: "market.own-retry", kind: "kernel-mutation", target: "none", proof: "interaction" },
  { id: "market.mine-retry", kind: "view", target: "none", proof: "interaction" },
  { id: "market.submit", kind: "kernel-mutation", target: "none", proof: "interaction" },
  { id: "market.vote", kind: "kernel-mutation", target: "none", proof: "interaction" },
  { id: "market.vote-retry", kind: "view", target: "none", proof: "interaction" },
];
