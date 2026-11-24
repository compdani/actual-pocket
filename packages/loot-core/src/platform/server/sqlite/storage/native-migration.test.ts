import 'fake-indexeddb/auto';
import {
  afterEach,
  beforeAll,
  beforeEach,
  describe,
  expect,
  it,
  vi,
} from 'vitest';

import * as idb from '#platform/server/indexeddb';
import {
  closeDatabase,
  execQuery,
  exportDatabase,
  openDatabase,
} from '#platform/server/sqlite';
import { openMemoryDatabase } from '#platform/server/sqlite/wasm';

import { putSqliteFileRow } from './files-store';
import { installFakeHost } from './native-host-fake';
import type { FakeHost } from './native-host-fake';
import { migrateBrowserBudgetsToNative } from './native-migration';
import { initWasmEngine, resetIndexedDb } from './test-helpers';

vi.mock('#platform/server/connection', () => ({ send: vi.fn() }));

const ROOT = '/documents/Actual';
const BUDGET = `${ROOT}/Preview-Budget-abc`;
const MARKER = `${ROOT}/.actual-desktop-migrated`;

let host: FakeHost;

beforeAll(() => initWasmEngine('native'));

beforeEach(async () => {
  await resetIndexedDb();
  await idb.openDatabase();
  host = installFakeHost();
});

afterEach(() => {
  vi.unstubAllGlobals();
});

async function budgetBytes(value: string): Promise<Uint8Array> {
  const db = await openDatabase();
  execQuery(db, `CREATE TABLE t (v TEXT); INSERT INTO t VALUES ('${value}');`);
  const bytes = await exportDatabase(db);
  closeDatabase(db);
  return bytes;
}

// A budget as the first desktop builds stored it: the database in the
// IndexedDB fallback's row, metadata in the `files` store
async function seedPreviewBudget(dir: string, value: string) {
  await putSqliteFileRow({
    filepath: `${dir}/db.sqlite`,
    contents: await budgetBytes(value),
    storage: 'memory-idb',
    rev: 1,
  });
  const { store } = idb.getStore(await idb.openDatabase(), 'files');
  await idb.set(store, {
    filepath: `${dir}/metadata.json`,
    contents: `{"id":"${value}"}`,
  });
}

function valueIn(bytes: Uint8Array | undefined): string | undefined {
  if (!bytes) {
    return undefined;
  }
  const db = openMemoryDatabase(bytes);
  try {
    return String(
      db.exec({ sql: 'SELECT v FROM t', returnValue: 'resultRows' })[0][0],
    );
  } finally {
    db.close();
  }
}

describe('copying budgets from WebView storage to the desktop folder', () => {
  it('copies each budget once', async () => {
    await seedPreviewBudget(BUDGET, 'preview');
    await migrateBrowserBudgetsToNative();

    expect(valueIn(host.files.get(`${BUDGET}/db.sqlite`))).toBe('preview');
    expect(host.text(`${BUDGET}/metadata.json`)).toBe('{"id":"preview"}');
    expect(host.files.has(MARKER)).toBe(true);

    // Deleted on disk afterwards: it must not come back
    host.files.delete(`${BUDGET}/db.sqlite`);
    host.files.delete(`${BUDGET}/metadata.json`);
    await migrateBrowserBudgetsToNative();
    expect([...host.files.keys()]).toEqual([MARKER]);
  });

  it('keeps a budget already in the folder as it is', async () => {
    await seedPreviewBudget(BUDGET, 'preview');
    host.files.set(`${BUDGET}/db.sqlite`, await budgetBytes('on-disk'));
    await migrateBrowserBudgetsToNative();
    expect(valueIn(host.files.get(`${BUDGET}/db.sqlite`))).toBe('on-disk');
    expect(host.files.has(`${BUDGET}/metadata.json`)).toBe(false);
  });

  it('undoes a partial copy and tries again on the next start', async () => {
    await seedPreviewBudget(BUDGET, 'preview');
    host.failWrites(`${BUDGET}/db.sqlite`, 1);
    await migrateBrowserBudgetsToNative();
    expect(host.files.size).toBe(0);

    await migrateBrowserBudgetsToNative();
    expect(valueIn(host.files.get(`${BUDGET}/db.sqlite`))).toBe('preview');
  });

  it('only marks the folder without budgets in WebView storage', async () => {
    await migrateBrowserBudgetsToNative();
    expect([...host.files.keys()]).toEqual([MARKER]);
  });

  it('copies into another folder that has no marker yet', async () => {
    await seedPreviewBudget(BUDGET, 'preview');
    await migrateBrowserBudgetsToNative();
    // The user switches to an empty folder: it gets the budgets too
    host.files.clear();
    await migrateBrowserBudgetsToNative();
    expect(valueIn(host.files.get(`${BUDGET}/db.sqlite`))).toBe('preview');
  });
});
