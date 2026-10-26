import { expect, test } from '@playwright/test';
import { seedFreshFamily } from './support/seed';
import { signIn, watchForProblems } from './support/helpers';
import { PARENT } from './support/stack';

test.beforeAll(async () => {
  await seedFreshFamily();
});

test('wrong password is refused, correct password signs in, sign out ends the session', async ({ page }) => {
  const problems: string[] = [];
  watchForProblems(page, 'parent', problems);

  // A wrong password stays on the login page and shows the error. One failure stays well
  // under the per-user login limit, so later journeys can still sign in.
  await page.goto('/login');
  await page.locator('#login-user-id').fill(PARENT.userId);
  await page.locator('#login-password').fill('not-the-password');
  await page.getByRole('button', { name: 'Sign In', exact: true }).click();
  await expect(page.getByTestId('login-error')).toBeVisible();
  await expect(page).toHaveURL(/\/login/);

  await signIn(page, PARENT.userId, PARENT.password);
  await expect(page.getByTestId('dash-quick-add')).toBeVisible();
  // The navigation shows the server build version under the parent's name.
  await expect(page.getByTestId('app-version')).toHaveText(/^v\S+/);

  // Sign out from the shell, then a protected page must send the user back to login.
  await page.locator('.logout-btn').click();
  await expect(page).toHaveURL(/\/login/);
  await page.goto('/dashboard');
  await expect(page).toHaveURL(/\/login/);

  // The wrong-password attempt and the logged-out session check log 401 responses by design.
  expect(problems.filter((p) => !p.includes('status of 401'))).toEqual([]);
});
