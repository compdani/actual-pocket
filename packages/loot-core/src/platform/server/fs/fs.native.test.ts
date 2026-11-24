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

import * as sqlite from '#platform/server/sqlite';
import { setSqliteStore } from '#platform/server/sqlite/storage/index';
import { createNativeStore } from '#platform/server/sqlite/storage/native';
import { installFakeHost } from '#platform/server/sqlite/storage/native-host-fake';
import type { FakeHost } from '#platform/server/sqlite/storage/native-host-fake';
import {
  initWasmEngine,
  resetIndexedDb,
} from '#platform/server/sqlite/storage/test-helpers';

import {
  exists,
  getModifiedTime,
  init,
  listDir,
  mkdir,
  readFile,
  removeDir,
  removeDirRecursively,
  writeFile,
} from './index';

vi.mock('#platform/server/connection', () => ({ send: vi.fn() }));

const ROOT = '/documents/Actual';
const BUDGET = `${ROOT}/My-Budget-abc`;

let host: FakeHost;

beforeAll(() => initWasmEngine('native'));

beforeEach(async () => {
  await resetIndexedDb();
  host = installFakeHost();
  setSqliteStore(createNativeStore({ debounceMs: 10, maxDirtyMs: 50 }));
});

afterEach(() => {
  vi.unstubAllGlobals();
});

async function databaseBytes(): Promise<Uint8Array> {
  const db = await sqlite.openDatabase();
  sqlite.execQuery(db, "CREATE TABLE t (v TEXT); INSERT INTO t VALUES ('x');");
  const bytes = await sqlite.exportDatabase(db);
  sqlite.closeDatabase(db);
  return bytes;
}

describe('web filesystem (desktop folder on disk)', () => {
  it('lists the budgets already in the folder', async () => {
    host.files.set(`${BUDGET}/metadata.json`, new TextEncoder().encode('{}'));
    host.files.set(`${BUDGET}/db.sqlite`, await databaseBytes());
    host.dirs.add(BUDGET);
    await init({ storage: 'native' });

    expect(await listDir(ROOT)).toEqual(['My-Budget-abc']);
    expect((await listDir(BUDGET)).sort()).toEqual([
      'db.sqlite',
      'metadata.json',
    ]);
    expect(await readFile(`${BUDGET}/metadata.json`)).toBe('{}');
  });

  it('writes budget files to the folder, not to IndexedDB', async () => {
    await init({ storage: 'native' });
    await writeFile(`${BUDGET}/metadata.json`, '{"budgetName":"Mine"}');
    expect(host.text(`${BUDGET}/metadata.json`)).toBe('{"budgetName":"Mine"}');
    expect(await readFile(`${BUDGET}/metadata.json`)).toBe(
      '{"budgetName":"Mine"}',
    );

    const zip = new Uint8Array([80, 75, 3, 4]);
    await writeFile(`${BUDGET}/backups/2026-01-01_00-00-00.zip`, zip);
    expect(host.files.get(`${BUDGET}/backups/2026-01-01_00-00-00.zip`)).toEqual(
      zip,
    );
    expect(
      await readFile(`${BUDGET}/backups/2026-01-01_00-00-00.zip`, 'binary'),
    ).toEqual(zip);
  });

  it('reports modified times, which backups need', async () => {
    await init({ storage: 'native' });
    await writeFile(`${BUDGET}/metadata.json`, '{}');
    host.mtimes.set(`${BUDGET}/metadata.json`, Date.UTC(2026, 0, 2));
    expect(await getModifiedTime(`${BUDGET}/metadata.json`)).toEqual(
      new Date(Date.UTC(2026, 0, 2)),
    );
  });

  it('creates and removes directories on disk', async () => {
    // The directory tree is module state, so this test uses its own folder
    const dir = `${ROOT}/Empty-Budget-xyz`;
    await init({ storage: 'native' });
    await mkdir(dir);
    expect(host.dirs.has(dir)).toBe(true);
    expect(await exists(dir)).toBe(true);
    await removeDir(dir);
    expect(host.dirs.has(dir)).toBe(false);
  });

  it('deletes a whole budget, database included', async () => {
    host.files.set(`${BUDGET}/metadata.json`, new TextEncoder().encode('{}'));
    host.files.set(`${BUDGET}/db.sqlite`, await databaseBytes());
    host.files.set(`${BUDGET}/backups/1.zip`, new Uint8Array([1]));
    host.dirs.add(BUDGET);
    host.dirs.add(`${BUDGET}/backups`);
    await init({ storage: 'native' });

    await removeDirRecursively(BUDGET);
    expect([...host.files.keys()]).toEqual([]);
    expect(host.dirs.has(BUDGET)).toBe(false);
    expect(await exists(BUDGET)).toBe(false);
  });

  it('reports an unreachable folder with its real path', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () =>
        Response.json(
          {
            message: 'permission denied',
            path: '/Users/me/Documents/Actual',
            code: 'EACCES',
          },
          { status: 503 },
        ),
      ),
    );
    await expect(init({ storage: 'native' })).rejects.toMatchObject({
      name: 'NativeFolderError',
      path: '/Users/me/Documents/Actual',
      code: 'EACCES',
    });
  });
});
