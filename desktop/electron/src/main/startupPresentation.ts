export type StartupPresentReason = "boot" | "second-instance" | "activate";

// A healthy handshake replaces the provisional window in ~0.5s, so first boot
// must stay hidden. Second clicks still need the diagnostic page.
export function shouldShowStartupDiagnostic(reason: StartupPresentReason, serviceReady: boolean, hasWindow: boolean): boolean {
  if (serviceReady) return false;
  if (reason === "second-instance") return true;
  if (reason === "activate") return hasWindow;
  return false;
}
