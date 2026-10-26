import { expect, type Page } from '@playwright/test';

/** Signs in through the login form and waits until the app leaves /login. */
export async function signIn(page: Page, userId: string, password: string): Promise<void> {
  await page.goto('/login');
  await page.locator('#login-user-id').fill(userId);
  await page.locator('#login-password').fill(password);
  await page.getByRole('button', { name: 'Sign In', exact: true }).click();
  await expect(page).not.toHaveURL(/\/login/);
}

/** Collects uncaught errors and console errors so a journey can fail on them. */
export function watchForProblems(page: Page, label: string, problems: string[]): void {
  page.on('pageerror', (err) => problems.push(`${label} page error: ${err.message}`));
  page.on('console', (msg) => {
    // The template preview is a sandboxed frame without scripts. Chrome reports each blocked script
    // attempt, which is the sandbox doing its job, so those messages are not app problems.
    if (msg.type() === 'error' && !msg.text().includes("frame is sandboxed and the 'allow-scripts' permission is not set")) {
      problems.push(`${label} console error: ${msg.text()}`);
    }
  });
}
