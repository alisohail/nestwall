import { expect, test } from '@playwright/test';
import { seedFreshFamily } from './support/seed';
import { signIn, watchForProblems } from './support/helpers';
import { PARENT } from './support/stack';

const WALL = { name: 'E2E Hall wall', exitCode: '246810', wrongCode: '111111' };

test.beforeAll(async () => {
  await seedFreshFamily();
});

test('parent creates a wall device, the display pairs with its key, and the exit PIN unlocks it', async ({ browser, page }) => {
  const wallContext = await browser.newContext();
  const wallPage = await wallContext.newPage();
  const problems: string[] = [];
  watchForProblems(page, 'parent', problems);
  watchForProblems(wallPage, 'wall', problems);

  try {
    // Parent: create the device with an exit PIN and read the one-time pairing key.
    await signIn(page, PARENT.userId, PARENT.password);
    await page.goto('/settings/admin/wall');
    await page.locator('button.btn.primary.sm.right').click();
    const modal = page.getByRole('dialog');
    await modal.locator('#wall-name').fill(WALL.name);
    await modal.locator('#wall-new-exit').fill(WALL.exitCode);
    await page.getByRole('button', { name: 'Create key', exact: true }).click();

    const key = page.locator('.pair-key');
    await expect(key).toHaveText(/^[A-Z0-9]{4}-[A-Z0-9]{4}$/);
    const pairingKey = ((await key.textContent()) ?? '').trim();
    await page.getByRole('button', { name: 'Done', exact: true }).click();

    // Wall display: enter the key on the pairing screen. Typing the key fills the slots in order.
    await wallPage.goto('/pair');
    await wallPage.getByTestId('pair-slot-0').focus();
    await wallPage.keyboard.type(pairingKey.replace('-', ''));
    await wallPage.getByTestId('pair-submit').click();
    await expect(wallPage.getByTestId('wall-display')).toBeVisible();

    // Exit: a wrong PIN is refused and the keypad stays closed to the wall.
    await wallPage.getByTestId('exit-x').click();
    await enterPin(wallPage, WALL.wrongCode);
    await wallPage.locator('[data-testid="exit-keypad"] button[data-key="\u2713"]').click();
    await expect(wallPage.getByTestId('exit-msg')).not.toHaveText('');
    await expect(wallPage.getByTestId('exit-unlocked')).toHaveCount(0);

    // The right PIN unlocks the exit options.
    await enterPin(wallPage, WALL.exitCode);
    await wallPage.locator('[data-testid="exit-keypad"] button[data-key="\u2713"]').click();
    await expect(wallPage.getByTestId('exit-unlocked')).toBeVisible();

    // The server refuses a wrong exit PIN with 403 exit_code_wrong, which the browser logs. That is expected here.
    expect(problems.filter((p) => !p.includes('status of 403'))).toEqual([]);
  } finally {
    await wallContext.close();
  }
});

async function enterPin(page: import('@playwright/test').Page, code: string): Promise<void> {
  for (const digit of code) {
    await page.locator(`[data-testid="exit-keypad"] button[data-key="${digit}"]`).click();
  }
}
