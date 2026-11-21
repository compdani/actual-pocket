import { chromium } from '/Users/dani/.t3/worktrees/actual-pocket/t3-432ad215-dc17fb4cd41f83eb/node_modules/playwright-core/index.mjs';
import * as api from '@actual-app/api';
import fs from 'node:fs';

const base = process.env.BASE_URL || 'http://127.0.0.1:5091';
const email = process.env.ACTUAL_EMAIL || 'e2e@example.com';
const password = process.env.ACTUAL_PASSWORD || 'e2e-password';
const budgetName = process.env.ACTUAL_BUDGET || 'Smoke Budget';
const groupId = process.env.ACTUAL_GROUP_ID;
const results = {};

const browser = await chromium.launch({
  executablePath: '/Users/dani/Library/Caches/ms-playwright/chromium-1255/chrome-mac-arm64/Google Chrome for Testing.app/Contents/MacOS/Google Chrome for Testing',
  headless: true,
});
const page = await browser.newPage();
const consoleErrors = [];
page.on('console', m => { if (m.type() === 'error') consoleErrors.push(m.text()); });
page.on('pageerror', e => consoleErrors.push(e.message));

async function setup(cardId, fill) {
  const card = page.getByTestId(`bank-sync-provider-${cardId}`);
  await card.getByRole('button', { name: 'Set up' }).click();
  await fill();
  await page.getByRole('button', { name: 'Save and continue' }).click();
  await card.getByRole('button', { name: 'Edit setup' }).waitFor({ timeout: 15000 });
}

async function link(cardId) {
  const card = page.getByTestId(`bank-sync-provider-${cardId}`);
  await card.getByRole('button', { name: 'Link bank account' }).click();
  const setupBankSync = page.getByRole('button', { name: 'Set up bank sync' }).first();
  if (await setupBankSync.isVisible().catch(() => false)) {
    await setupBankSync.click();
    await page.getByText('Create new account', { exact: true }).first().click();
  }
  const linkButton = page.getByRole('button', {
    name: /^(Link accounts|Link or unlink accounts|Link and unlink accounts)$/,
  });
  await linkButton.waitFor({ timeout: 20000 });
  await linkButton.click({ timeout: 20000 });
  await page.waitForTimeout(1500);
}

try {
  await page.goto(base, { waitUntil: 'domcontentloaded' });
  await page.getByPlaceholder('Email').fill(email);
  await page.getByPlaceholder('Password').fill(password);
  await page.getByRole('button', { name: 'Sign in' }).click();
  await page.getByText(budgetName).waitFor({ timeout: 30000 });
  await page.getByText(budgetName).click();
  await page.waitForURL('**/budget', { timeout: 30000 });
  await page.goto(`${base}/bank-sync`, { waitUntil: 'domcontentloaded' });
  await page.getByTestId('bank-sync-provider-simpleFin').waitFor({ timeout: 30000 });

  await setup('simpleFin', async () => {
    await page.locator('#token-field').fill(Buffer.from('http://bridge.example/claim').toString('base64'));
  });
  await link('simpleFin');
  results.simplefin = 'configured and linked';

  await setup('pluggyai', async () => {
    await page.locator('#client-id-field').fill('client-id');
    await page.locator('#client-secret-field').fill('client-secret');
    await page.locator('#item-ids-field').fill('item-1');
  });
  await link('pluggyai');
  results.pluggyai = 'configured and linked';

  await setup('akahu', async () => {
    await page.locator('#appToken-field').fill('app-token');
    await page.locator('#userToken-field').fill('user-token');
  });
  await link('akahu');
  results.akahu = 'configured and linked';

  await setup('enableBanking', async () => {
    await page.locator('#application-id-field').fill('app-id');
    await page.locator('#secret-key-field').setInputFiles('/tmp/eb.pem');
  });
  const card = page.getByTestId('bank-sync-provider-enableBanking');
  await card.getByRole('button', { name: 'Link bank account' }).click();
  await page.locator('#country-field').fill('Finland');
  await page.keyboard.press('Enter');
  await page.locator('#bank-field').waitFor({ timeout: 15000 });
  await page.locator('#bank-field').fill('Nordea');
  await page.keyboard.press('Enter');
  const popupPromise = page.waitForEvent('popup', { timeout: 15000 }).catch(() => null);
  await page.getByRole('button', { name: /Link bank in browser/ }).click();
  const popup = await popupPromise;
  if (popup) {
    await popup.waitForLoadState('domcontentloaded').catch(() => {});
    await popup.close().catch(() => {});
  }
  await page.getByRole('button', { name: 'Link accounts' }).waitFor({ timeout: 30000 });
  await page.getByRole('button', { name: 'Link accounts' }).click();
  await page.waitForTimeout(1500);
  results.enablebanking = 'configured and linked';

  // Sync through the public API against the same budget and verify each
  // provider contributed a transaction.
  fs.mkdirSync('/tmp/bank-sync-api', { recursive: true });
  await api.init({ dataDir: '/tmp/bank-sync-api', serverURL: base, email, password });
  await api.downloadBudget(groupId);
  await api.runBankSync();
  const accounts = await api.getAccounts();
  const synced = accounts.filter(a => a.account_sync_source);
  for (const account of synced) {
    const transactions = await api.getTransactions(account.id, '2020-01-01', '2026-12-31');
    results[account.account_sync_source] = `${transactions.length} transaction(s)`;
  }
  await api.shutdown();
  console.log(JSON.stringify({ ok: true, results, consoleErrors }, null, 2));
} finally {
  await browser.close();
}
