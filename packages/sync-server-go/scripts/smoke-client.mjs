// One @actual-app/api client for the smoke test. Each invocation is a fresh
// process, so two "clients" really are two sessions with separate data dirs.
import fs from 'node:fs';
import * as api from '@actual-app/api';

const resultPrefix = 'SMOKE_RESULT ';
const options = JSON.parse(process.env.SMOKE_CLIENT || '{}');

function logResult(value) {
  console.log(resultPrefix + JSON.stringify(value));
}

async function main() {
  fs.mkdirSync(options.dataDir, { recursive: true });

  const internal = await api.init({
    dataDir: options.dataDir,
    serverURL: options.serverURL,
    email: options.email,
    password: options.password,
  });

  const action = options.action;

  if (action === 'create') {
    await internal.send('create-budget', { budgetName: options.budgetName });
    await internal.send('upload-budget');

    let budgets = await internal.send('api/get-budgets');
    let budget = budgets.find(entry => entry.cloudFileId);
    if (!budget) {
      await internal.send('api/sync');
      budgets = await internal.send('api/get-budgets');
      budget = budgets.find(entry => entry.cloudFileId);
    }
    if (!budget) {
      throw new Error('budget was not uploaded: ' + JSON.stringify(budgets));
    }

    const accountId = await internal.send('api/account-create', {
      account: { name: options.accountName },
      initialBalance: 0,
    });
    await internal.send('api/transactions-add', {
      accountId,
      transactions: [
        {
          date: '2026-01-15',
          amount: options.firstAmount,
          payee_name: 'Client A Payee',
        },
      ],
    });
    await internal.send('api/sync');

    logResult({
      syncId: budget.groupId,
      accountId,
      transactions: await transactions(internal, accountId),
    });
    return;
  }

  if (action === 'download' || action === 'loop') {
    await internal.send('api/download-budget', { syncId: options.syncId });
    const accounts = await internal.send('api/accounts-get');
    const account = accounts.find(entry => entry.name === options.accountName);
    if (!account) {
      throw new Error(
        'account not found after download: ' + JSON.stringify(accounts),
      );
    }

    if (action === 'download' && options.addAmount) {
      await internal.send('api/transactions-add', {
        accountId: account.id,
        transactions: [
          {
            date: '2026-02-01',
            amount: options.addAmount,
            payee_name: options.payeeName,
          },
        ],
      });
      await internal.send('api/sync');
    }

    if (action === 'loop') {
      const deadline = Date.now() + options.durationMs;
      let syncs = 0;
      while (Date.now() < deadline) {
        await internal.send('api/sync');
        syncs += 1;
        await new Promise(resolve => setTimeout(resolve, options.intervalMs));
      }
      logResult({ syncs, transactions: await transactions(internal, account.id) });
      return;
    }

    logResult({
      accountId: account.id,
      transactions: await transactions(internal, account.id),
    });
    return;
  }

  throw new Error('unknown action ' + action);
}

async function transactions(internal, accountId) {
  const rows = await internal.send('api/transactions-get', { accountId });
  return rows
    .map(row => ({
      id: row.id,
      date: row.date,
      amount: row.amount,
    }))
    .sort((a, b) =>
      `${a.date}|${a.amount}|${a.id}`.localeCompare(
        `${b.date}|${b.amount}|${b.id}`,
      ),
    );
}

main()
  .then(async () => {
    await api.shutdown();
    process.exit(0);
  })
  .catch(async error => {
    console.error(error && error.stack ? error.stack : String(error));
    try {
      await api.shutdown();
    } catch {}
    process.exit(1);
  });
