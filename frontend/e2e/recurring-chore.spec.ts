import { expect, test, type Page } from '@playwright/test';
import { seedFreshFamily } from './support/seed';
import { signIn, watchForProblems } from './support/helpers';
import { CHILD, PARENT, TEMPLATE } from './support/stack';

type TaskCopy = { id: string; title: string; member_id: string; status: string };

test.beforeAll(async () => {
  await seedFreshFamily();
});

test('parent creates a recurring chore, child completes it, parent approves it', async ({ browser, page }) => {
  const childContext = await browser.newContext();
  const childPage = await childContext.newPage();
  const problems: string[] = [];
  watchForProblems(page, 'parent', problems);
  watchForProblems(childPage, 'child', problems);

  try {
    // Parent: create a daily chore from the template and assign it to the child.
    await signIn(page, PARENT.userId, PARENT.password);
    await page.getByTestId('dash-quick-add').click();
    await expect(page).toHaveURL(/\/tasks\/new$/);

    await page.locator('button.chip', { hasText: TEMPLATE.name }).click();
    const childChip = page.locator('button.chip', { hasText: CHILD.displayName });
    if ((await childChip.getAttribute('aria-pressed')) !== 'true') {
      await childChip.click();
    }
    await expect(childChip).toHaveAttribute('aria-pressed', 'true');

    await page.getByRole('button', { name: 'Daily', exact: true }).click();
    await page.getByRole('button', { name: 'Create task', exact: true }).click();
    await expect(page).toHaveURL(/\/tasks$/);

    // A daily series generates one copy per day for the child, not a single task.
    const copies = (await listTaskCopies(page)).filter((c) => c.title === TEMPLATE.title);
    expect(copies.length).toBeGreaterThan(1);

    // Child: finish today's copy.
    await signIn(childPage, CHILD.userId, CHILD.password);
    await childPage.goto('/my-tasks');
    const row = childPage.getByTestId('today-task').filter({ hasText: TEMPLATE.title }).first();
    await expect(row).toBeVisible();
    await row.getByTestId('today-task-title').click();
    await expect(childPage).toHaveURL(/\/my-tasks\/[0-9a-f-]{36}(\?.*)?$/);
    const taskPath = new URL(childPage.url()).pathname;

    await childPage.getByTestId('task-finish').click();
    await childPage.getByTestId('task-confirm-ok').click();
    await expect(childPage.getByTestId('task-submitted')).toBeVisible();

    // Parent: approve the submitted copy from the dashboard.
    await page.goto('/dashboard');
    const approval = page.getByTestId('approval-row').filter({ hasText: TEMPLATE.title });
    await expect(approval).toHaveCount(1);
    await approval.getByTestId('approve-btn').click();
    await expect(page.getByTestId('approvals-empty')).toBeVisible();

    // Child: the approved state is shown on reload.
    await childPage.goto(taskPath);
    await expect(childPage.getByTestId('task-approved')).toBeVisible();

    expect(problems).toEqual([]);
  } finally {
    await childContext.close();
  }
});

async function listTaskCopies(page: Page): Promise<TaskCopy[]> {
  const res = await page.request.get('/api/v1/tasks', { params: { limit: '200' } });
  expect(res.ok()).toBeTruthy();
  const body = (await res.json()) as { tasks: TaskCopy[] };
  return body.tasks;
}
