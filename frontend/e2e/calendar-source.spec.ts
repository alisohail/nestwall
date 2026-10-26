import { expect, test, type Page } from '@playwright/test';
import { seedFreshFamily } from './support/seed';
import { signIn, watchForProblems } from './support/helpers';
import { PARENT } from './support/stack';

const CALENDAR = { name: 'E2E School calendar' };

const ICS = [
  'BEGIN:VCALENDAR',
  'VERSION:2.0',
  'PRODID:-//NestWall E2E//EN',
  'BEGIN:VEVENT',
  'UID:e2e-school-1@nestwall.example.test',
  'DTSTAMP:20250101T000000Z',
  'DTSTART:20250105T090000Z',
  'DTEND:20250105T100000Z',
  'SUMMARY:E2E assembly',
  'END:VEVENT',
  'END:VCALENDAR',
].join('\r\n');

type ImportList = { items: { name: string }[] };

test.beforeAll(async () => {
  await seedFreshFamily();
});

test('parent adds a manual calendar and deletes it while another calendar remains', async ({ page }) => {
  const problems: string[] = [];
  watchForProblems(page, 'parent', problems);
  const name = 'E2E Holidays';

  await signIn(page, PARENT.userId, PARENT.password);
  await page.goto('/settings/admin/calendars');

  await page.locator('button.btn.primary.sm.right').click();
  await page.locator('[data-testid="kind-manual"]').click();
  await page.locator('#imp-name').fill(name);
  await page.locator('.modal button.btn.primary').click();

  const row = page.locator('[data-testid="manual-calendar"]').filter({ hasText: name });
  await expect(row).toHaveCount(1);

  await row.locator('button.icon-btn.danger').click();
  await page.locator('.confirm').filter({ hasText: name }).locator('button.danger').click();

  await expect(page.locator('[data-testid="manual-calendar"]').filter({ hasText: name })).toHaveCount(0);
  expect(problems).toEqual([]);
});

test('parent removes a calendar source and it is gone from the list', async ({ page }) => {
  const problems: string[] = [];
  watchForProblems(page, 'parent', problems);

  await signIn(page, PARENT.userId, PARENT.password);
  await addIcsImport(page, CALENDAR.name);

  await page.goto('/settings/admin/calendars');
  const row = page.locator('.item').filter({ hasText: CALENDAR.name });
  await expect(row).toHaveCount(1);

  await row.locator('button.icon-btn.danger').click();
  await page.locator('.confirm').filter({ hasText: CALENDAR.name }).locator('button.danger').click();

  await expect(page.locator('.item').filter({ hasText: CALENDAR.name })).toHaveCount(0);

  const list = await page.request.get('/api/v1/calendar/imports');
  expect(list.ok()).toBeTruthy();
  const body = (await list.json()) as ImportList;
  expect(body.items.map((i) => i.name)).not.toContain(CALENDAR.name);

  expect(problems).toEqual([]);
});

/** Adds one ICS import through the API so the removal journey has a source to delete. */
async function addIcsImport(page: Page, name: string): Promise<void> {
  const cookies = await page.context().cookies();
  const csrf = cookies.find((c) => c.name === 'nestwall-csrf')?.value;
  if (!csrf) {
    throw new Error('no CSRF cookie for calendar import');
  }
  const res = await page.request.post('/api/v1/calendar/imports', {
    headers: { 'X-CSRF-Token': csrf },
    multipart: {
      file: { name: 'e2e-school.ics', mimeType: 'text/calendar', buffer: Buffer.from(ICS) },
      name,
      colour: '#4F8A6B',
      audience: 'everyone',
    },
  });
  expect(res.ok(), await res.text()).toBeTruthy();
}
