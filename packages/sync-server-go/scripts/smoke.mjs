// End-to-end smoke test: empty data dir -> bootstrap -> upload a budget ->
// two clients sync -> download and compare -> backup during syncs -> restore
// into a fresh data dir -> log in, download and sync again.
import { execFile, execFileSync, spawn } from 'node:child_process';
import fs from 'node:fs';
import net from 'node:net';
import os from 'node:os';
import path from 'node:path';
import { promisify } from 'node:util';

const execFileAsync = promisify(execFile);
const resultPrefix = 'SMOKE_RESULT ';
const ownerEmail = 'smoke-owner@example.com';
const ownerPassword = 'smoke-owner-password';
const accountName = 'Smoke Checking';
const serverBin = path.resolve('actual-server');
const clientScript = path.resolve('scripts/smoke-client.mjs');
const serverURLBase = 'http://127.0.0.1';
const root = fs.mkdtempSync(path.join(os.tmpdir(), 'actual-smoke-'));
const keep = process.env.SMOKE_KEEP === '1';

let server;
let restoredServer;

function sleep(ms) {
  return new Promise(resolve => setTimeout(resolve, ms));
}

async function freePort() {
  const listener = net.createServer();
  await new Promise((resolve, reject) => {
    listener.once('error', reject);
    listener.listen(0, '127.0.0.1', resolve);
  });
  const port = listener.address().port;
  await new Promise(resolve => listener.close(resolve));
  return port;
}

async function waitForHealth(url, logs) {
  for (let attempt = 0; attempt < 160; attempt += 1) {
    try {
      const response = await fetch(`${url}/health`);
      if (response.ok) {
        return;
      }
    } catch {
      // not up yet
    }
    await sleep(250);
  }
  throw new Error(`server did not become healthy:\n${logs()}`);
}

async function startServer(dataDir, port) {
  let output = '';
  const child = spawn(
    serverBin,
    ['serve', `--http=127.0.0.1:${port}`],
    {
      env: {
        ...process.env,
        ACTUAL_DATA_DIR: dataDir,
        ACTUAL_HOSTNAME: '127.0.0.1',
        ACTUAL_PORT: String(port),
        NODE_ENV: 'production',
      },
      stdio: ['ignore', 'pipe', 'pipe'],
    },
  );
  child.stdout.on('data', chunk => (output += chunk.toString()));
  child.stderr.on('data', chunk => (output += chunk.toString()));

  const url = `${serverURLBase}:${port}`;
  try {
    await waitForHealth(url, () => output);
  } catch (error) {
    child.kill('SIGKILL');
    throw error;
  }

  return { child, url, output: () => output };
}

async function stopServer(instance) {
  if (!instance) {
    return;
  }
  const { child } = instance;
  if (child.exitCode !== null) {
    return;
  }
  child.kill('SIGTERM');
  for (let attempt = 0; attempt < 40; attempt += 1) {
    if (child.exitCode !== null) {
      return;
    }
    await sleep(100);
  }
  child.kill('SIGKILL');
}

async function postJSON(url, body, token) {
  const response = await fetch(url, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      ...(token ? { Authorization: token } : {}),
    },
    body: JSON.stringify(body),
  });
  const text = await response.text();
  if (!response.ok) {
    throw new Error(`POST ${url} -> ${response.status}: ${text}`);
  }
  return text ? JSON.parse(text) : null;
}

function runClient(options) {
  return new Promise((resolve, reject) => {
    execFileAsync(process.execPath, [clientScript], {
      env: {
        ...process.env,
        SMOKE_CLIENT: JSON.stringify(options),
      },
      maxBuffer: 16 * 1024 * 1024,
    })
      .then(({ stdout }) => {
        const line = stdout
          .split('\n')
          .reverse()
          .find(entry => entry.startsWith(resultPrefix));
        if (!line) {
          reject(new Error(`client produced no result:\n${stdout}`));
          return;
        }
        resolve(JSON.parse(line.slice(resultPrefix.length)));
      })
      .catch(error => {
        reject(
          new Error(
            `client ${options.action} failed:\n${error.stderr || error.message}`,
          ),
        );
      });
  });
}

function transactionKey(row) {
  return `${row.id}|${row.date}|${row.amount}`;
}

function assertSameTransactions(label, first, second) {
  const left = first.map(transactionKey);
  const right = second.map(transactionKey);
  if (JSON.stringify(left) !== JSON.stringify(right)) {
    throw new Error(
      `${label}: transaction lists differ:\n${JSON.stringify(left)}\n${JSON.stringify(right)}`,
    );
  }
}

function findFile(directory, name) {
  for (const entry of fs.readdirSync(directory, { withFileTypes: true })) {
    const full = path.join(directory, entry.name);
    if (entry.isDirectory()) {
      const found = findFile(full, name);
      if (found) {
        return found;
      }
    } else if (entry.name === name) {
      return full;
    }
  }
  return null;
}

function normalizeRestore(restorePB) {
  if (fs.existsSync(path.join(restorePB, 'data.db'))) {
    return;
  }

  const dataDB = findFile(restorePB, 'data.db');
  if (!dataDB) {
    throw new Error(`restored backup has no data.db under ${restorePB}`);
  }

  const source = path.dirname(dataDB);
  for (const entry of fs.readdirSync(source)) {
    fs.cpSync(path.join(source, entry), path.join(restorePB, entry), {
      recursive: true,
    });
  }
}

async function main() {
  const originalData = path.join(root, 'original');
  const restoreData = path.join(root, 'restored');
  const port = await freePort();
  const restoredPort = await freePort();

  console.log(`smoke: data dir ${root}`);
  server = await startServer(originalData, port);
  console.log(`smoke: server ${server.url}`);

  await postJSON(`${server.url}/account/bootstrap`, {
    email: ownerEmail,
    password: ownerPassword,
  });

  const created = await runClient({
    action: 'create',
    dataDir: path.join(root, 'client-a'),
    serverURL: server.url,
    email: ownerEmail,
    password: ownerPassword,
    budgetName: 'Smoke Budget',
    accountName,
    firstAmount: 111_00,
  });
  console.log(`smoke: uploaded budget ${created.syncId}`);

  const clientB = await runClient({
    action: 'download',
    dataDir: path.join(root, 'client-b'),
    serverURL: server.url,
    email: ownerEmail,
    password: ownerPassword,
    syncId: created.syncId,
    accountName,
    addAmount: 222_00,
    payeeName: 'Client B Payee',
  });
  if (clientB.transactions.length !== created.transactions.length + 1) {
    throw new Error(
      `client B has ${clientB.transactions.length} transactions, want ${
        created.transactions.length + 1
      }`,
    );
  }
  const clientBIds = new Set(clientB.transactions.map(row => row.id));
  for (const row of created.transactions) {
    if (!clientBIds.has(row.id)) {
      throw new Error(`client B is missing transaction ${row.id}`);
    }
  }

  const clientA2 = await runClient({
    action: 'download',
    dataDir: path.join(root, 'client-a'),
    serverURL: server.url,
    email: ownerEmail,
    password: ownerPassword,
    syncId: created.syncId,
    accountName,
  });
  assertSameTransactions(
    'client A after B synced',
    clientA2.transactions,
    clientB.transactions,
  );
  console.log(`smoke: two clients synced (${clientA2.transactions.length} transactions)`);

  const superUserEmail = 'smoke-admin@example.com';
  const superUserPassword = 'smoke-admin-password';
  execFileSync(
    serverBin,
    ['superuser', 'upsert', superUserEmail, superUserPassword],
    {
      env: {
        ...process.env,
        ACTUAL_DATA_DIR: originalData,
        NODE_ENV: 'production',
      },
      stdio: 'pipe',
    },
  );
  const superUser = await postJSON(
    `${server.url}/api/collections/_superusers/auth-with-password`,
    { identity: superUserEmail, password: superUserPassword },
  );

  // Keep syncing while the backup runs; the backup hooks pause syncs.
  const syncLoop = runClient({
    action: 'loop',
    dataDir: path.join(root, 'client-a'),
    serverURL: server.url,
    email: ownerEmail,
    password: ownerPassword,
    syncId: created.syncId,
    accountName,
    durationMs: 2000,
    intervalMs: 10,
  });
  await sleep(300);

  await postJSON(`${server.url}/api/backups`, { name: 'smoke-backup.zip' }, superUser.token);
  const loop = await syncLoop;
  if (loop.syncs < 1) {
    throw new Error('the sync loop made no syncs during the backup');
  }
  console.log(`smoke: backup created during ${loop.syncs} syncs`);

  const backupPath = findFile(
    path.join(originalData, 'server-files', 'pb_data', 'backups'),
    'smoke-backup.zip',
  );
  if (!backupPath) {
    throw new Error('backup file not found');
  }
  execFileSync('unzip', ['-t', backupPath], { stdio: 'pipe' });

  await stopServer(server);
  server = null;

  const restorePB = path.join(restoreData, 'server-files', 'pb_data');
  fs.mkdirSync(restorePB, { recursive: true });
  execFileSync('unzip', ['-q', backupPath, '-d', restorePB], { stdio: 'pipe' });
  normalizeRestore(restorePB);

  restoredServer = await startServer(restoreData, restoredPort);
  console.log(`smoke: restored server ${restoredServer.url}`);

  const restoredDownload = await runClient({
    action: 'download',
    dataDir: path.join(root, 'client-c'),
    serverURL: restoredServer.url,
    email: ownerEmail,
    password: ownerPassword,
    syncId: created.syncId,
    accountName,
  });
  assertSameTransactions(
    'restored client after download',
    clientA2.transactions,
    restoredDownload.transactions,
  );

  const restoredSync = await runClient({
    action: 'download',
    dataDir: path.join(root, 'client-c'),
    serverURL: restoredServer.url,
    email: ownerEmail,
    password: ownerPassword,
    syncId: created.syncId,
    accountName,
    addAmount: 333_00,
    payeeName: 'Restored Payee',
  });
  const restoredReader = await runClient({
    action: 'download',
    dataDir: path.join(root, 'client-d'),
    serverURL: restoredServer.url,
    email: ownerEmail,
    password: ownerPassword,
    syncId: created.syncId,
    accountName,
  });
  assertSameTransactions(
    'restored second client',
    restoredSync.transactions,
    restoredReader.transactions,
  );
  if (restoredReader.transactions.length !== clientA2.transactions.length + 1) {
    throw new Error(
      `restored transaction count = ${restoredReader.transactions.length}, want ${
        clientA2.transactions.length + 1
      }`,
    );
  }

  console.log(
    JSON.stringify(
      {
        ok: true,
        syncId: created.syncId,
        transactionsBeforeBackup: clientA2.transactions.length,
        transactionsAfterRestore: restoredReader.transactions.length,
        syncsDuringBackup: loop.syncs,
      },
      null,
      2,
    ),
  );
}

main()
  .catch(error => {
    console.error(error && error.stack ? error.stack : String(error));
    process.exitCode = 1;
  })
  .finally(async () => {
    await stopServer(restoredServer);
    await stopServer(server);
    if (process.exitCode) {
      console.error(`smoke: data kept at ${root}`);
    } else if (!keep) {
      fs.rmSync(root, { recursive: true, force: true });
    }
  });
