/** Shared production page navigation used by behavior and memory fixtures. */
export async function chooseAppLayout(page, label, className) {
  await page.locator('button:has(svg.lucide-settings)').last().click();
  await page.locator('.settings-screen').waitFor();
  await page.locator('.settings-screen .set-seg__btn').filter({ hasText: new RegExp(`^${label}$`) }).click();
  await page.locator(`.app.${className}`).waitFor();
  await page.locator('.settings-screen .management-screen__back').click();
  await page.locator('.settings-screen').waitFor({ state: 'detached' });
}

/**
 * Locate a local session through the active layout's canonical navigation.
 * Workbench owns SessionRef rows; Creation retains the legacy ProjectTree for
 * this release. Keeping the compatibility choice here prevents benchmarks
 * from encoding either sidebar implementation as the product identity.
 */
export function sessionButton(page, label) {
  return page.locator(".workspace-browser__session-open, .project-tree__topic-main").filter({ hasText: label }).first();
}

export async function revealSession(page, label) {
  // Composer readiness and sidebar data readiness are independent. Wait for
  // the first projected row before deciding whether the target is truncated.
  await page.locator(".workspace-browser__session-open, .project-tree__topic-main").first().waitFor({ state: "visible" });
  let button = sessionButton(page, label);
  if (await button.count() === 0) {
    const expanders = page.locator(".workspace-browser__show-more");
    await expanders.first().waitFor({ state: "visible" });
    for (let index = 0; index < await expanders.count(); index += 1) {
      const expander = expanders.nth(index);
      if (await expander.isVisible()) await expander.click();
    }
    button = sessionButton(page, label);
  }
  await button.waitFor({ state: "visible" });
  return button;
}

export async function selectSession(page, label) {
  const button = await revealSession(page, label);
  await button.click();
}

/** Return the visible new-session action without coupling a fixture to layout. */
export function newSessionButton(page) {
  return page.locator(".sidebar__quick-action:visible, .sidebar__new:visible").first();
}

export function activeSessionLabel(page) {
  return page.locator([
    '.workspace-browser__session-open[aria-current="page"] strong',
    ".project-tree__topic--active .project-tree__topic-label",
  ].join(", ")).first();
}

export async function readActiveSessionLabel(page) {
  const label = activeSessionLabel(page);
  return await label.count() > 0 ? await label.textContent() ?? "" : "";
}
