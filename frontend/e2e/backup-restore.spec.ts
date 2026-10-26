import { expect, test } from '@playwright/test';
import { seedFreshFamily } from './support/seed';
import { signIn, watchForProblems } from './support/helpers';
import { PARENT } from './support/stack';

const RESTORE_WORD = 'RESTORE';

test.beforeAll(async () => {
  await seedFreshFamily();
});

// The restore replaces the database and then ends the API process so Docker can restart it.
// This harness has no restarter, so this journey runs alone in the "restore" project after
// every other journey (see playwright.config.ts) and stops at the restarting overlay.
test('parent runs a backup and restores it, and the app shows it is restarting', async ({ page }) => {
  const problems: string[] = [];
  watchForProblems(page, 'parent', problems);

  await signIn(page, PARENT.userId, PARENT.password);
  await page.goto('/settings/admin/data');
  const card = page.getByTestId('backups-card');
  await expect(card).toBeVisible();

  await card.getByTestId('backups-run').click();
  await expect(card.getByTestId('backups-run-ok')).toBeVisible();

  // Restore the newest history row.
  const restoreButton = page.getByTestId('backups-history').locator('[data-testid^="backup-restore-"]').first();
  await expect(restoreButton).toBeEnabled({ timeout: 30_000 });
  await restoreButton.click();
  await expect(page.getByTestId('backups-restore-dialog')).toBeVisible();
  await page.getByTestId('backups-restore-password').fill(PARENT.password);
  await page.getByTestId('backups-restore-typed').fill(RESTORE_WORD);

  const restored = page.waitForResponse(
    (r) => r.request().method() === 'POST' && r.url().includes('/settings/backups/') && r.url().endsWith('/restore'),
  );
  await page.getByTestId('backups-restore-ok').click();
  const res = await restored;
  expect(res.status(), await res.text()).toBe(202);
  expect(await res.json()).toMatchObject({ restarting: true });

  await expect(page.getByTestId('backups-restore-overlay')).toBeVisible();
  // The restore replaces the database, so the open notifications stream reconnects with a 401.
  // Any other console error is still a failure.
  expect(problems.filter((p) => !p.includes('status of 401'))).toEqual([]);
});
