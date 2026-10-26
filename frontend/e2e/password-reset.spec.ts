import { expect, test } from '@playwright/test';
import { seedFreshFamily } from './support/seed';
import { watchForProblems } from './support/helpers';
import { PARENT } from './support/stack';

test.beforeAll(async () => {
  await seedFreshFamily();
});

// The e2e stack has no SMTP server, so the full email round trip (reset link delivered and used)
// cannot run here. These journeys cover what the UI does without mail: the not-set-up message and
// an invalid reset link.
test('parent asks for an email reset and is told email is not set up', async ({ page }) => {
  const problems: string[] = [];
  watchForProblems(page, 'parent', problems);

  await page.goto('/login');
  await page.getByRole('button', { name: 'Forgot password?', exact: true }).click();
  await page.getByTestId('forgot-parent-email').click();
  await page.locator('#forgot-email').fill(PARENT.email);
  await page.locator('form.forgot-form button[type="submit"]').click();

  await expect(page.getByRole('heading', { name: 'Email is not set up yet', exact: true })).toBeVisible();
  await expect(page.getByRole('heading', { name: 'Check your email', exact: true })).toHaveCount(0);

  expect(problems).toEqual([]);
});

test('an invalid reset link shows an error instead of a form', async ({ page }) => {
  const problems: string[] = [];
  watchForProblems(page, 'parent', problems);

  await page.goto('/reset-password/not-a-real-token');
  await page.locator('#rp-password').fill('a-new-password-1');
  await page.locator('#rp-repeat').fill('a-new-password-1');
  await page.locator('form.rp-form button[type="submit"]').click();
  await expect(page.locator('form.rp-form')).toHaveCount(0);
  await expect(page.getByRole('heading', { level: 2 })).toBeVisible();

  // The invalid token makes the API answer 400 by design, so that console message is expected here.
  expect(problems.filter((p) => !p.includes('status of 400'))).toEqual([]);
});
