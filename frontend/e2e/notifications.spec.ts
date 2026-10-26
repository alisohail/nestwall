import { expect, test, type Page } from '@playwright/test';
import { seedFreshFamily } from './support/seed';
import { signIn, watchForProblems } from './support/helpers';
import { CHILD, PARENT } from './support/stack';

const BONUS = { title: 'E2E Notify wash the car', points: 5 };
const LIVE_BONUS = { title: 'E2E Live wash the windows', points: 4 };

test.beforeAll(async () => {
  await seedFreshFamily();
});

/** Sets the family browser switch and saves only when it needs to change. */
async function setBrowserSwitch(page: Page, on: boolean): Promise<void> {
  const browser = page.getByTestId('notify-browser');
  if ((await browser.isChecked()) === on) return;
  await page.locator('label.switch').filter({ has: browser }).click();
  const saved = page.waitForResponse((r) => r.url().includes('/api/v1/notifications/settings') && r.request().method() === 'PUT');
  await page.getByTestId('notify-save').click();
  expect((await saved).ok()).toBeTruthy();
  await expect(page.getByTestId('notify-saved')).toBeVisible();
}

test('parent changes the family notification settings and previews the template', async ({ page }) => {
  const problems: string[] = [];
  watchForProblems(page, 'parent', problems);

  await signIn(page, PARENT.userId, PARENT.password);
  await page.goto('/settings/admin/notifications');
  await expect(page.getByTestId('notify-switches')).toBeVisible();

  // Start from browser on (the default), since an earlier run may have left it off.
  await setBrowserSwitch(page, true);
  // The browser switch is on by default and the digest is off. Turning browser off and saving should persist.
  const browser = page.getByTestId('notify-browser');
  await expect(browser).toBeChecked();
  await expect(page.getByTestId('notify-digest')).not.toBeChecked();
  await setBrowserSwitch(page, false);

  await expect(page.getByTestId('notify-browser')).not.toBeChecked();

  // The digest interval defaults to 15 minutes and stays locked while the digest is off.
  // The e2e stack has no SMTP server, so email (and therefore the digest) cannot be turned on here;
  // the save path is covered by the component specs.
  await expect(page.getByTestId('notify-digest-minutes')).toBeDisabled();
  await expect(page.getByTestId('notify-digest-minutes')).toHaveValue('15');

  // Preview renders the sample notice inside the sandboxed frame.
  await page.getByTestId('notify-preview').click();
  await expect(page.getByTestId('notify-preview-frame')).toBeVisible();
  const previewLogo = page.frameLocator('[data-testid="notify-preview-frame"]').locator('img[alt="NestWall"]');
  await expect(previewLogo).toHaveAttribute('src', /^data:image\/png;base64,/);
  await expect.poll(() => previewLogo.evaluate((img: HTMLImageElement) => img.naturalWidth)).toBeGreaterThan(0);

  expect(problems).toEqual([]);
});

test('child bell updates live when a bonus is approved, without a page reload', async ({ browser, page }) => {
  const childContext = await browser.newContext();
  const childPage = await childContext.newPage();
  const problems: string[] = [];
  watchForProblems(page, 'parent', problems);
  watchForProblems(childPage, 'child', problems);

  try {
    await signIn(page, PARENT.userId, PARENT.password);
    await page.goto('/bonus/new');
    await expect(page.getByRole('button', { name: 'Today', exact: true })).toBeEnabled();
    await page.getByTestId('bonus-title').fill(LIVE_BONUS.title);
    await page.getByTestId('bonus-points').fill(String(LIVE_BONUS.points));
    const childChip = page.locator('[data-testid^="bonus-kid-"]').filter({ hasText: CHILD.displayName }).first();
    if ((await childChip.getAttribute('aria-pressed')) !== 'true') {
      await childChip.click();
    }
    const approval = page.getByTestId('bonus-approval');
    if (!(await approval.isChecked())) {
      await approval.check();
    }
    await page.getByTestId('bonus-publish').click();
    await expect(page).toHaveURL(/\/bonus(\?.*)?$/);

    // The child opens the page once and keeps it open. The approval must reach the bell without a reload.
    await signIn(childPage, CHILD.userId, CHILD.password);
    await childPage.goto('/my-tasks');
    await expect(childPage.getByTestId('bell-button')).toBeVisible();
    await childPage.getByTestId('tab-bonus').click();
    const card = childPage.getByTestId('kid-bonus-card').filter({ hasText: LIVE_BONUS.title });
    await card.getByTestId('kid-bonus-grab').click();
    await card.getByTestId('kid-bonus-finish').click();

    await page.goto('/bonus');
    await page.getByTestId('bonus-approve').first().click();

    await expect(childPage.getByTestId('bell-count')).toBeVisible({ timeout: 15_000 });
    await childPage.getByTestId('bell-button').click();
    const newest = childPage.getByTestId('bell-panel').locator('.bell-list li').first();
    await expect(newest.locator('.bell-new')).toBeVisible();
    await expect(newest.locator('.bell-icon')).toHaveClass(/tone-success/);

    await childPage.getByTestId('bell-clear').click();
    await expect(childPage.getByTestId('bell-count')).toHaveCount(0);

    expect(problems).toEqual([]);
  } finally {
    await childContext.close();
  }
});

test('child sees a bonus approval in the bell and clears it', async ({ browser, page }) => {
  const childContext = await browser.newContext({ permissions: ['notifications'] });
  const childPage = await childContext.newPage();
  const problems: string[] = [];
  watchForProblems(page, 'parent', problems);
  watchForProblems(childPage, 'child', problems);

  try {
    // Parent publishes a bonus that needs approval, the child finishes it, and the parent approves it.
    await signIn(page, PARENT.userId, PARENT.password);
    await page.goto('/bonus/new');
    await expect(page.getByRole('button', { name: 'Today', exact: true })).toBeEnabled();
    await page.getByTestId('bonus-title').fill(BONUS.title);
    await page.getByTestId('bonus-points').fill(String(BONUS.points));
    const childChip = page.locator('[data-testid^="bonus-kid-"]').filter({ hasText: CHILD.displayName }).first();
    if ((await childChip.getAttribute('aria-pressed')) !== 'true') {
      await childChip.click();
    }
    const approval = page.getByTestId('bonus-approval');
    if (!(await approval.isChecked())) {
      await approval.check();
    }
    await page.getByTestId('bonus-publish').click();
    await expect(page).toHaveURL(/\/bonus(\?.*)?$/);

    await signIn(childPage, CHILD.userId, CHILD.password);
    await childPage.goto('/my-tasks');
    await childPage.getByTestId('tab-bonus').click();
    const card = childPage.getByTestId('kid-bonus-card').filter({ hasText: BONUS.title });
    await card.getByTestId('kid-bonus-grab').click();
    await card.getByTestId('kid-bonus-finish').click();

    await page.goto('/bonus');
    await page.getByTestId('bonus-approve').first().click();

    // The child's bell picks up the approval on its next refresh. Open it to refresh now.
    await childPage.goto('/my-tasks');
    await childPage.getByTestId('bell-button').click();
    await expect(childPage.getByTestId('bell-count')).toBeVisible();
    await expect(childPage.getByTestId('bell-panel').locator('.bell-list li').first()).toBeVisible();
    // Permission is granted for this context, so the enable-alerts prompt must stay hidden.
    await expect(childPage.getByTestId('bell-enable-alerts')).toHaveCount(0);

    await childPage.getByTestId('bell-clear').click();
    await expect(childPage.getByTestId('bell-count')).toHaveCount(0);

    expect(problems).toEqual([]);
  } finally {
    await childContext.close();
  }
});
