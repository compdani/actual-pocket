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

import {
  closeDatabase,
  execQuery,
  openDatabase,
  runQuery,
} from '#platform/server/sqlite';
import { exportBytes, openMemoryDatabase } from '#platform/server/sqlite/wasm';

import { createNativeStore } from './native';
import { installFakeHost } from './native-host-fake';
import type { FakeHost } from './native-host-fake';
import { initWasmEngine, sleep } from './test-helpers';

import { getSqliteStore, setSqliteStore } from './index';

const sendMock = vi.hoisted(() => vi.fn());
vi.mock('#platform/server/connection', () => ({ send: sendMock }));

const PATH = '/documents/Actual/budget-one/db.sqlite';
const SCHEMA = 'CREATE TABLE t (id INTEGER PRIMARY KEY, v TEXT);';

let host: FakeHost;

beforeAll(() => initWasmEngine('native'));

beforeEach(() => {
  host = installFakeHost();
  sendMock.mockClear();
  setSqliteStore(createNativeStore({ debounceMs: 20, maxDirtyMs: 100 }));
});

afterEach(async () => {
  await getSqliteStore().releaseAll();
  vi.unstubAllGlobals();
});

function valuesIn(bytes: Uint8Array | undefined): string[] {
  if (!bytes) {
    return [];
  }
  const db = openMemoryDatabase(bytes);
  try {
    return db
      .exec({ sql: 'SELECT v FROM t ORDER BY id', returnValue: 'resultRows' })
      .map(row => String(row[0]));
  } finally {
    db.close();
  }
}

describe('desktop (native) sqlite storage', () => {
  it('creates a new database on disk as soon as it is opened', async () => {
    const db = await openDatabase(PATH);
    expect(host.files.has(PATH)).toBe(true);
    closeDatabase(db);
  });

  it('saves writes to disk after a short delay and reads them back', async () => {
    const db = await openDatabase(PATH);
    execQuery(db, SCHEMA);
    runQuery(db, 'INSERT INTO t (v) VALUES (?)', ['first']);
    await sleep(80);
    expect(valuesIn(host.files.get(PATH))).toEqual(['first']);

    runQuery(db, 'INSERT INTO t (v) VALUES (?)', ['second']);
    closeDatabase(db);
    await getSqliteStore().flush();
    expect(valuesIn(host.files.get(PATH))).toEqual(['first', 'second']);

    const reopened = await openDatabase(PATH);
    expect(runQuery(reopened, 'SELECT v FROM t ORDER BY id', [], true)).toEqual(
      [{ v: 'first' }, { v: 'second' }],
    );
    closeDatabase(reopened);
  });

  it('reads an open database from memory, including unsaved changes', async () => {
    const db = await openDatabase(PATH);
    execQuery(db, SCHEMA);
    runQuery(db, 'INSERT INTO t (v) VALUES (?)', ['pending']);
    expect(valuesIn(await getSqliteStore().readFile(PATH))).toEqual([
      'pending',
    ]);
    closeDatabase(db);
  });

  it('retries a failed save once, then reports it and fails a flush', async () => {
    const db = await openDatabase(PATH);
    execQuery(db, SCHEMA);
    host.failWrites(PATH, 2);
    runQuery(db, 'INSERT INTO t (v) VALUES (?)', ['x']);
    await sleep(150);
    expect(sendMock).toHaveBeenCalledWith('storage-write-failed', {
      path: PATH,
    });
    // The disk recovers: the next flush writes and succeeds
    await getSqliteStore().flush();
    expect(valuesIn(host.files.get(PATH))).toEqual(['x']);
    closeDatabase(db);
  });

  it('keeps a snapshot whose save on close failed and continues from it', async () => {
    const db = await openDatabase(PATH);
    execQuery(db, SCHEMA);
    runQuery(db, 'INSERT INTO t (v) VALUES (?)', ['kept']);
    host.failWrites(PATH, 1);
    closeDatabase(db);
    await expect(getSqliteStore().flush()).rejects.toThrow(/not written/);

    const reopened = await openDatabase(PATH);
    expect(runQuery(reopened, 'SELECT v FROM t', [], true)).toEqual([
      { v: 'kept' },
    ]);
    closeDatabase(reopened);
    await getSqliteStore().flush();
    expect(valuesIn(host.files.get(PATH))).toEqual(['kept']);
  });

  it('refuses to overwrite an open database and removes files from disk', async () => {
    const db = await openDatabase(PATH);
    await expect(
      getSqliteStore().writeFile(PATH, new Uint8Array([1])),
    ).rejects.toThrow(/open database/);
    expect(await getSqliteStore().exists(PATH)).toBe(true);
    await getSqliteStore().removeFiles([PATH]);
    expect(host.files.has(PATH)).toBe(false);
    expect(await getSqliteStore().exists(PATH)).toBe(false);
    void db;
  });

  it('saves a small change to a large budget as a few pages', async () => {
    const db = await openDatabase(PATH);
    execQuery(db, SCHEMA);
    for (let i = 0; i < 400; i++) {
      runQuery(db, 'INSERT INTO t (v) VALUES (?)', ['x'.repeat(2000)]);
    }
    await getSqliteStore().flush();
    const size = host.files.get(PATH)?.length ?? 0;
    expect(size).toBeGreaterThan(500_000);

    host.sent.put = 0;
    host.sent.patch = 0;
    runQuery(db, 'UPDATE t SET v = ? WHERE id = 200', ['changed']);
    await getSqliteStore().flush();

    expect(host.sent.put).toBe(0);
    expect(host.sent.patch).toBeGreaterThan(0);
    expect(host.sent.patch).toBeLessThan(size / 20);
    expect(host.files.get(PATH)).toEqual(await getSqliteStore().readFile(PATH));
    expect(valuesIn(host.files.get(PATH))[199]).toBe('changed');
    closeDatabase(db);
  });

  it('saves the whole file when it changed on disk meanwhile', async () => {
    const db = await openDatabase(PATH);
    execQuery(db, SCHEMA);
    runQuery(db, 'INSERT INTO t (v) VALUES (?)', ['mine']);
    await getSqliteStore().flush();

    // Another program replaces the file (a sync client, a restored copy)
    const other = openMemoryDatabase();
    other.exec(SCHEMA + " INSERT INTO t (v) VALUES ('theirs');");
    host.changeOutside(PATH, exportBytes(other));
    other.close();

    host.sent.put = 0;
    runQuery(db, 'INSERT INTO t (v) VALUES (?)', ['again']);
    await getSqliteStore().flush();
    expect(host.sent.put).toBeGreaterThan(0);
    expect(valuesIn(host.files.get(PATH))).toEqual(['mine', 'again']);
    closeDatabase(db);
  });

  it('keeps the file on disk identical to the database through many saves', async () => {
    const db = await openDatabase(PATH);
    execQuery(db, SCHEMA);
    let seed = 42;
    const random = () => {
      seed = (seed * 1103515245 + 12345) % 2 ** 31;
      return seed / 2 ** 31;
    };
    for (let round = 0; round < 25; round++) {
      const action = random();
      if (action < 0.5) {
        runQuery(db, 'INSERT INTO t (v) VALUES (?)', [
          'r'.repeat(Math.floor(random() * 5000)),
        ]);
      } else if (action < 0.8) {
        runQuery(db, 'UPDATE t SET v = ? WHERE id % 3 = ?', [
          String(round),
          Math.floor(random() * 3),
        ]);
      } else if (action < 0.95) {
        runQuery(db, 'DELETE FROM t WHERE id % 4 = ?', [
          Math.floor(random() * 4),
        ]);
      } else {
        execQuery(db, 'VACUUM');
      }
      await getSqliteStore().flush();
      expect(host.files.get(PATH)).toEqual(
        await getSqliteStore().readFile(PATH),
      );
    }
    closeDatabase(db);
  });

  it('sends every request with the desktop header', async () => {
    const db = await openDatabase(PATH);
    closeDatabase(db);
    await getSqliteStore().flush();
    expect(host.requests.length).toBeGreaterThan(0);
    // The fake host answers 403 without the header, which would have failed
    // the open above
  });
});
