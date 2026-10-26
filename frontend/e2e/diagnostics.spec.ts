import { expect, test } from '@playwright/test';
import { seedFreshFamily } from './support/seed';
import { signIn, watchForProblems } from './support/helpers';
import { PARENT } from './support/stack';

test.beforeAll(async () => {
  await seedFreshFamily();
});

test('parent sees the recent events card, with Clear disabled when there is nothing to clear', async ({ page }) => {
  const problems: string[] = [];
  watchForProblems(page, 'parent', problems);

  await signIn(page, PARENT.userId, PARENT.password);
  await page.goto('/settings/admin/diagnostics');
  await expect(page.getByTestId('diagnostics-app')).toBeVisible();
  await expect(page.getByTestId('diagnostics-errors-see-events')).toBeVisible();

  const events = page.getByTestId('diagnostics-events');
  await expect(events).toBeVisible();
  const clear = page.getByTestId('diagnostics-events-clear');
  // A fresh stack may have no events; if it has some, Clear is enabled and the list is shown.
  if (await page.getByTestId('diagnostics-events-empty').isVisible()) {
    await expect(clear).toBeDisabled();
  } else {
    await expect(page.getByTestId('diagnostics-events-list')).toBeVisible();
    await expect(clear).toBeEnabled();
  }

  expect(problems).toEqual([]);
});
