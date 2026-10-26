import { expect, test } from '@playwright/test';
import { seedFreshFamily } from './support/seed';
import { signIn, watchForProblems } from './support/helpers';
import { CHILD, PARENT } from './support/stack';

test.beforeAll(async () => {
  await seedFreshFamily();
});

test('child cannot open parent-only pages by URL and sees no task add controls', async ({ browser }) => {
  const context = await browser.newContext();
  const page = await context.newPage();
  const problems: string[] = [];
  watchForProblems(page, 'child', problems);

  try {
    await signIn(page, CHILD.userId, CHILD.password);

    for (const path of ['/approvals', '/bonus/new']) {
      await page.goto(path);
      await expect(page).not.toHaveURL(new RegExp(path.replace('/', '\\/')));
    }

    await page.goto('/tasks');
    await expect(page.getByRole('heading').first()).toBeVisible();
    await expect(page.locator('a[href="/tasks/new"]')).toHaveCount(0);
  } finally {
    await context.close();
  }
  expect(problems).toEqual([]);
});

test('parent sees the task add controls and the approvals page', async ({ page }) => {
  const problems: string[] = [];
  watchForProblems(page, 'parent', problems);

  await signIn(page, PARENT.userId, PARENT.password);
  await page.goto('/tasks');
  await expect(page.locator('a[href="/tasks/new"]').first()).toBeVisible();

  await page.goto('/approvals');
  await expect(page).toHaveURL(/\/approvals/);
  expect(problems).toEqual([]);
});
