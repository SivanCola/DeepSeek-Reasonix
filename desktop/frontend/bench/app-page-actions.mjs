/** The desktop ships one workbench layout; this waits for its chrome. */
export async function chooseAppLayout() {
  return undefined;
}

/**
 * Locate a local session through the active layout's canonical navigation.
 * Workbench owns SessionRef rows; Creation retains the legacy ProjectTree for
 * this release. Keeping the compatibility choice here prevents benchmarks
 * from encoding either sidebar implementation as the product identity.
 */
export function sessionButton(page, label) {
  return page.locator(".project-tree__topic-main").filter({ hasText: label }).first();
}

export async function revealSession(page, label) {
  // Composer readiness and sidebar data readiness are independent. Wait for
  // the first projected row before deciding whether the target is truncated.
  await page.locator(".project-tree__topic-main").first().waitFor({ state: "visible" });
  let button = sessionButton(page, label);
  if (await button.count() === 0) {
    button = sessionButton(page, label);
  }
  await button.waitFor({ state: "visible" });
  return button;
}

export async function selectSession(page, label) {
  const button = await revealSession(page, label);
  await button.click();
}

/** Return the visible new-session action. */
export function newSessionButton(page) {
  return page.locator(".sidebar__quick-action:visible").first();
}

export function activeSessionLabel(page) {
  return page.locator('.project-tree__topic--active .project-tree__topic-label').first();
}

export async function readActiveSessionLabel(page) {
  const label = activeSessionLabel(page);
  return await label.count() > 0 ? await label.textContent() ?? "" : "";
}
