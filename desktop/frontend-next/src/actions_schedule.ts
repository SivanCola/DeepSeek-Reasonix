import type { UIAction } from "./actions";

export const SCHEDULE_ACTIONS: UIAction[] = [
  { id: "schedule.resume-all", kind: "kernel-mutation", target: "none", proof: "authority-effect" },
  { id: "schedule.pause-all", kind: "kernel-mutation", target: "none", proof: "authority-effect" },
  { id: "schedule.pause", kind: "kernel-mutation", target: "entity", proof: "authority-effect" },
  { id: "schedule.resume", kind: "kernel-mutation", target: "entity", proof: "authority-effect" },
  { id: "schedule.renew", kind: "kernel-mutation", target: "entity", proof: "authority-effect" },
  { id: "schedule.delete", kind: "destructive", target: "entity", proof: "authority-effect" },
  { id: "schedule.delete-ask", kind: "view", target: "entity", proof: "interaction" },
  { id: "schedule.result", kind: "view", target: "entity", proof: "interaction" },
  { id: "schedule.result-close", kind: "view", target: "none", proof: "interaction" },
  { id: "schedule.retry", kind: "view", target: "none", proof: "interaction" },
  { id: "schedule.new", kind: "view", target: "none", proof: "interaction" },
  { id: "schedule.folder", kind: "view", target: "none", proof: "interaction" },
  { id: "schedule.prompt", kind: "view", target: "none", proof: "interaction" },
  { id: "schedule.label", kind: "view", target: "none", proof: "interaction" },
  { id: "schedule.every", kind: "view", target: "none", proof: "interaction" },
  { id: "schedule.cancel", kind: "view", target: "none", proof: "interaction" },
  { id: "schedule.preview", kind: "kernel-mutation", target: "none", proof: "authority-effect" },
  { id: "schedule.back", kind: "view", target: "none", proof: "interaction" },
  { id: "schedule.create", kind: "kernel-mutation", target: "none", proof: "authority-effect" },
];
