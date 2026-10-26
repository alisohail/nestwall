import { expect, test, type Page } from '@playwright/test';
import { seedFreshFamily } from './support/seed';
import { signIn, watchForProblems } from './support/helpers';
import { CHILD, PARENT } from './support/stack';

const BONUS = { title: 'E2E Wash the car', points: 7 };

test.beforeAll(async () => {
  await seedFreshFamily();
});

test('parent publishes a bonus, child grabs and finishes it, parent approves it', async ({ browser, page }) => {
  const childContext = await browser.newContext();
  const childPage = await childContext.newPage();
  const problems: string[] = [];
  watchForProblems(page, 'parent', problems);
  watchForProblems(childPage, 'child', problems);

  try {
    // Parent: publish a first-come bonus for today that needs approval.
    await signIn(page, PARENT.userId, PARENT.password);
    await page.goto('/bonus/new');
    await expect(page.getByTestId('bonus-new-page')).toBeVisible();
    // Publish needs the family time zone, which loads after the page. The Today chip is enabled once it has loaded.
    await expect(page.getByRole('button', { name: 'Today', exact: true })).toBeEnabled();

    await page.getByTestId('bonus-title').fill(BONUS.title);
    await page.getByTestId('bonus-points').fill(String(BONUS.points));

    const childChip = page.getByTestId(await childChipTestId(page));
    if ((await childChip.getAttribute('aria-pressed')) !== 'true') {
      await childChip.click();
    }
    await expect(childChip).toHaveAttribute('aria-pressed', 'true');

    const approval = page.getByTestId('bonus-approval');
    if (!(await approval.isChecked())) {
      await approval.check();
    }

    const published = page.waitForResponse((r) => r.url().includes('/api/v1/bonus-tasks') && r.request().method() === 'POST');
    await page.getByTestId('bonus-publish').click();
    const publishRes = await published;
    expect(publishRes.ok(), await publishRes.text()).toBeTruthy();
    await expect(page).toHaveURL(/\/bonus(\?.*)?$/);
    await expect(page.getByRole('heading', { name: BONUS.title, exact: true })).toBeVisible();

    // Child: grab the bonus from My tasks, then finish it.
    await signIn(childPage, CHILD.userId, CHILD.password);
    await childPage.goto('/my-tasks');
    await childPage.getByTestId('tab-bonus').click();
    const card = childPage.getByTestId('kid-bonus-card').filter({ hasText: BONUS.title });
    await expect(card).toHaveCount(1);
    await card.getByTestId('kid-bonus-grab').click();
    const completed = childPage.waitForResponse((r) => r.url().includes('/complete') && r.request().method() === 'POST');
    await card.getByTestId('kid-bonus-finish').click();
    expect((await completed).ok()).toBeTruthy();

    // Parent: the finished bonus waits for approval on the Bonus page.
    await page.goto('/bonus');
    const row = page.getByTestId('bonus-approve').first();
    await expect(row).toBeVisible();
    await row.click();
    await expect(page.getByTestId('bonus-approve')).toHaveCount(0);

    expect(problems).toEqual([]);
  } finally {
    await childContext.close();
  }
});

/** The child's chip uses the member id, which the test reads from the page, not from a fixed value. */
async function childChipTestId(page: Page): Promise<string> {
  const chip = page.locator('[data-testid^="bonus-kid-"]').filter({ hasText: CHILD.displayName }).first();
  const testId = await chip.getAttribute('data-testid');
  if (!testId) {
    throw new Error(`no bonus kid chip for ${CHILD.displayName}`);
  }
  return testId;
}
