import { expect, test, type Page } from '@playwright/test';
import { seedFreshFamily } from './support/seed';
import { signIn, watchForProblems } from './support/helpers';
import { CHILD, PARENT } from './support/stack';

const JOB = { title: 'E2E Big job', points: 20 };
const REWARD = { name: 'E2E Movie night', icon: '🎬', cost: 15 };

type Balance = { balance: number; reserved: number };
type Redemption = { id: string; reward_id: string; status: string; version: number };
type LedgerEntry = { amount: number };

test.beforeAll(async () => {
  await seedFreshFamily();
});

test('child earns 20 points, redeems a reward, replays change nothing', async ({ browser, page }) => {
  const childContext = await browser.newContext();
  const childPage = await childContext.newPage();
  const problems: string[] = [];
  watchForProblems(page, 'parent', problems);
  watchForProblems(childPage, 'child', problems);

  try {
    // Parent: create a reward that needs approval and a 20-point job for the child.
    // Balances are read as baselines because the other journey on this stack also awards points.
    await signIn(page, PARENT.userId, PARENT.password);
    const childId = await childMemberId(page);
    const reward = await createReward(page);
    await createJob(page, childId);

    // Child: finish the job.
    await signIn(childPage, CHILD.userId, CHILD.password);
    const start = await myPoints(childPage);
    // A job due today is listed on Today, not on the My tasks "coming up" tab.
    await childPage.goto('/today');
    await childPage.getByTestId('today-task').filter({ hasText: JOB.title }).first().getByTestId('today-task-title').click();
    await childPage.getByTestId('task-finish').click();
    await childPage.getByTestId('task-confirm-ok').click();
    await expect(childPage.getByTestId('task-submitted')).toBeVisible();

    // Parent: approve the job. The child now holds 20 more points.
    await page.goto('/approvals');
    const jobRow = page.getByTestId('task-list').locator('li', { hasText: JOB.title });
    await expect(jobRow).toHaveCount(1);
    await jobRow.getByTestId('task-approve').click();
    await expect(jobRow).toHaveCount(0);

    await childPage.goto('/rewards');
    await expect(childPage.getByTestId('rewards-balance')).toHaveText(String(start.balance + JOB.points));
    expect(await myPoints(childPage)).toEqual({ balance: start.balance + JOB.points, reserved: 0 });

    // Child: request the reward from the catalogue. Capture the key the UI sends so the replay reuses it.
    const redeemSent = childPage.waitForRequest(
      (req) => req.method() === 'POST' && req.url().endsWith(`/api/v1/rewards/${reward.id}/redeem`),
    );
    await childPage.getByTestId(`reward-${reward.id}`).getByTestId('reward-request').click();
    const idempotencyKey = (await redeemSent).headers()['idempotency-key'];
    expect(idempotencyKey).toBeTruthy();

    await expect(childPage.getByTestId('rewards-toast')).toBeVisible();
    const afterRequest = { balance: start.balance + JOB.points - REWARD.cost, reserved: REWARD.cost };
    await expect(childPage.getByTestId('rewards-balance')).toHaveText(String(afterRequest.balance));
    expect(await myPoints(childPage)).toEqual(afterRequest);
    const childEntries = await ledgerTotal(page, childId);

    // Replay the same request with the same key: the API returns the stored request and writes nothing.
    const replay = await apiPost(childPage, `/api/v1/rewards/${reward.id}/redeem`, {
      'Idempotency-Key': idempotencyKey,
    });
    expect(replay.status()).toBe(200);
    expect(((await replay.json()) as { replayed: boolean }).replayed).toBe(true);
    expect(await myPoints(childPage)).toEqual(afterRequest);
    expect(await ledgerTotal(page, childId)).toEqual(childEntries);
    const pending = await redemptionFor(page, reward.id);
    expect(pending.status).toBe('pending');

    // Parent: approve the request in Approvals.
    await page.goto('/approvals');
    await page.getByTestId('tab-requests').click();
    const requestRow = page.getByTestId(`request-${pending.id}`);
    await expect(requestRow).toBeVisible();
    await requestRow.getByTestId('request-approve').click();
    await expect(requestRow).toHaveCount(0);

    // Approval spends the held points: balance stays the same and the hold is released.
    const afterApprove = { balance: afterRequest.balance, reserved: 0 };
    const approved = await redemptionFor(page, reward.id);
    expect(approved.status).toBe('approved');
    expect(await myPoints(childPage)).toEqual(afterApprove);
    expect(await ledgerTotal(page, childId)).toEqual(childEntries);

    // Replay the approval: a stale If-Match is refused, and the current version gets 409 not_pending.
    const staleApprove = await apiPost(page, `/api/v1/redemptions/${pending.id}/approve`, {
      'If-Match': `"${pending.version}"`,
    });
    expect(staleApprove.ok()).toBeFalsy();
    const repeatApprove = await apiPost(page, `/api/v1/redemptions/${pending.id}/approve`, {
      'If-Match': `"${approved.version}"`,
    });
    expect(repeatApprove.status()).toBe(409);
    expect(((await repeatApprove.json()) as { error: { code: string } }).error.code).toBe('not_pending');
    expect(await myPoints(childPage)).toEqual(afterApprove);
    expect(await ledgerTotal(page, childId)).toEqual(childEntries);

    // Parent: the ledger shows the award and the spend.
    await page.goto('/points');
    const ledger = page.getByTestId('ledger');
    await expect(ledger).toContainText('+20');
    await expect(ledger).toContainText('-15');

    // Child: the request is shown as approved.
    await childPage.goto('/rewards');
    await expect(childPage.getByTestId(`request-${pending.id}`).locator('.badge.ok')).toBeVisible();

    expect(problems).toEqual([]);
  } finally {
    await childContext.close();
  }
});

async function csrfToken(page: Page): Promise<string> {
  const cookie = (await page.context().cookies()).find((c) => c.name === 'nestwall-csrf');
  if (!cookie) {
    throw new Error('no CSRF cookie in the browser context');
  }
  return cookie.value;
}

async function apiPost(page: Page, path: string, headers: Record<string, string> = {}) {
  return page.request.post(path, { headers: { 'X-CSRF-Token': await csrfToken(page), ...headers } });
}

async function apiGetJson<T>(page: Page, path: string, params?: Record<string, string>): Promise<T> {
  const res = await page.request.get(path, { params });
  expect(res.ok(), `GET ${path}`).toBeTruthy();
  return (await res.json()) as T;
}

async function childMemberId(page: Page): Promise<string> {
  const body = await apiGetJson<{ members: { id: string; user_id: string }[] }>(page, '/api/v1/family/members');
  const child = body.members.find((m) => m.user_id === CHILD.userId);
  if (!child) {
    throw new Error('child member not found');
  }
  return child.id;
}

async function createReward(page: Page): Promise<{ id: string }> {
  const res = await page.request.post('/api/v1/rewards', {
    data: { name: REWARD.name, icon: REWARD.icon, cost: REWARD.cost, status: 'active', approval_required: true },
    headers: { 'X-CSRF-Token': await csrfToken(page) },
  });
  expect(res.status()).toBe(201);
  return (await res.json()) as { id: string };
}

async function createJob(page: Page, childId: string): Promise<void> {
  const res = await page.request.post('/api/v1/tasks', {
    data: {
      title: JOB.title,
      points: JOB.points,
      due_date: todayInFamilyZone(),
      approval_required: true,
      assignee_ids: [childId],
    },
    headers: { 'X-CSRF-Token': await csrfToken(page) },
  });
  expect(res.status()).toBe(201);
}

async function myPoints(page: Page): Promise<Balance> {
  const body = await apiGetJson<Balance>(page, '/api/v1/me/points');
  return { balance: body.balance, reserved: body.reserved };
}

/** Sum of every ledger amount for the member. The ledger includes pending holds, so this equals the balance. */
async function ledgerTotal(page: Page, memberId: string): Promise<number> {
  const body = await apiGetJson<{ entries: LedgerEntry[] }>(page, '/api/v1/points/entries', {
    member_id: memberId,
    limit: '200',
  });
  return body.entries.reduce((sum, e) => sum + e.amount, 0);
}

async function redemptionFor(page: Page, rewardId: string): Promise<Redemption> {
  const body = await apiGetJson<{ redemptions: Redemption[] }>(page, '/api/v1/redemptions');
  const match = body.redemptions.filter((r) => r.reward_id === rewardId);
  expect(match.length).toBe(1);
  return match[0];
}

function todayInFamilyZone(): string {
  // en-CA formats dates as YYYY-MM-DD. The family zone is the same one the seed uses.
  return new Intl.DateTimeFormat('en-CA', {
    timeZone: Intl.DateTimeFormat().resolvedOptions().timeZone,
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
  }).format(new Date());
}
