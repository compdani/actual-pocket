import assert from 'node:assert/strict';
import { generateKeyPairSync } from 'node:crypto';
import fs from 'node:fs';
import path from 'node:path';

import * as api from '@actual-app/api';
import { chromium } from 'playwright-core';

const base = process.env.BASE_URL || 'http://127.0.0.1:5091';
const email = process.env.ACTUAL_EMAIL || 'e2e@example.com';
const password = process.env.ACTUAL_PASSWORD || 'e2e-password';
const budgetName = process.env.ACTUAL_BUDGET || 'Smoke Budget';
const groupId = process.env.ACTUAL_GROUP_ID;
const results = {};

assert(groupId, "Set ACTUAL_GROUP_ID to the seeded budget's sync ID");
const browser = await chromium.launch({
  executablePath: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE,
});
const page = await browser.newPage();
const consoleErrors = [];
page.on('console', m => {
  if (m.type() === 'error') consoleErrors.push(m.text());
});
page.on('pageerror', e => consoleErrors.push(e.message));

async function setup(cardId, fill) {
  const card = page.getByTestId(`bank-sync-provider-${cardId}`);
  await card.getByRole('button', { name: 'Set up' }).click();
  await fill();
  await page.getByRole('button', { name: 'Save and continue' }).click();
  await card
    .getByRole('button', { name: 'Edit setup' })
    .waitFor({ timeout: 15000 });
}

async function selectNewAccount(provider) {
  const dialog = page.getByTestId('select-linked-accounts-modal');
  await dialog
    .getByRole('button', { name: 'Set up bank sync' })
    .first()
    .click();
  const account = dialog.getByRole('combobox').locator('input');
  await account.fill('Create new account');
  await account.press('Enter');
  const [transactions] = await Promise.all([
    page.waitForResponse(
      response =>
        new URL(response.url()).pathname === `/${provider}/transactions`,
    ),
    dialog.getByRole('button', { name: 'Link accounts', exact: true }).click(),
  ]);
  assert(transactions.ok(), `Transaction request failed for ${provider}`);
  await dialog.waitFor({ state: 'hidden' });
}

async function link(cardId) {
  const card = page.getByTestId(`bank-sync-provider-${cardId}`);
  await card.getByRole('button', { name: 'Link bank account' }).click();
  await selectNewAccount(cardId.toLowerCase());
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
  await page
    .getByTestId('bank-sync-provider-simpleFin')
    .waitFor({ timeout: 30000 });

  await setup('simpleFin', async () => {
    await page
      .locator('#token-field')
      .fill(Buffer.from('http://bridge.example/claim').toString('base64'));
  });
  await link('simpleFin');

  await setup('pluggyai', async () => {
    await page.locator('#client-id-field').fill('client-id');
    await page.locator('#client-secret-field').fill('client-secret');
    await page.locator('#item-ids-field').fill('item-1');
  });
  await link('pluggyai');

  await setup('akahu', async () => {
    await page.locator('#appToken-field').fill('app-token');
    await page.locator('#userToken-field').fill('user-token');
  });
  await link('akahu');

  await setup('enableBanking', async () => {
    await page.locator('#application-id-field').fill('app-id');
    const { privateKey } = generateKeyPairSync('rsa', {
      modulusLength: 2048,
      privateKeyEncoding: { type: 'pkcs1', format: 'pem' },
    });
    await page.locator('#secret-key-field').setInputFiles({
      name: 'enable-banking-test.pem',
      mimeType: 'application/x-pem-file',
      buffer: Buffer.from(privateKey),
    });
  });
  const card = page.getByTestId('bank-sync-provider-enableBanking');
  await card.getByRole('button', { name: 'Link bank account' }).click();
  await page.locator('#country-field').fill('Finland');
  await page.keyboard.press('Enter');
  await page.locator('#bank-field').waitFor({ timeout: 15000 });
  await page.locator('#bank-field').fill('Nordea');
  await page.keyboard.press('Enter');
  const popupPromise = page
    .waitForEvent('popup', { timeout: 15000 })
    .catch(() => null);
  await page.getByRole('button', { name: /Link bank in browser/ }).click();
  const popup = await popupPromise;
  if (popup) {
    await popup.waitForLoadState('domcontentloaded').catch(() => {
      // The provider may already have disconnected during cleanup.
    });
    await popup.close().catch(() => {
      // The provider may already have disconnected during cleanup.
    });
  }
  await selectNewAccount('enablebanking');
  await page
    .getByText('Enable Banking Checking', { exact: true })
    .first()
    .waitFor();
  await Promise.all([
    page.waitForResponse(
      response =>
        new URL(response.url()).pathname === '/sync/sync' && response.ok(),
    ),
    page.getByRole('button', { name: 'Server Sync' }).click(),
  ]);

  // Sync through the public API against the same budget and verify each
  // provider contributed a transaction.
  const dataDir = fs.mkdtempSync(path.join(import.meta.dirname, '.bank-sync-'));
  try {
    const internal = await api.init({
      dataDir,
      serverURL: base,
      email,
      password,
    });
    await api.downloadBudget(groupId);
    await api.runBankSync();
    const accounts = await internal.send('accounts-get');
    for (const provider of [
      'simpleFin',
      'pluggyai',
      'akahu',
      'enableBanking',
    ]) {
      const account = accounts.find(
        item => item.account_sync_source === provider,
      );
      assert(account, `Missing linked account for ${provider}`);
      const transactions = await api.getTransactions(
        account.id,
        '2020-01-01',
        new Date().toISOString().slice(0, 10),
      );
      const imported = transactions.filter(
        transaction => transaction.imported_id,
      );
      assert(imported.length > 0, `No transactions imported for ${provider}`);
      results[provider] = `${imported.length} transaction(s)`;
    }
  } finally {
    try {
      await api.shutdown();
    } finally {
      fs.rmSync(dataDir, { recursive: true, force: true });
    }
  }
  console.log(JSON.stringify({ ok: true, results, consoleErrors }, null, 2));
} finally {
  await browser.close();
}
