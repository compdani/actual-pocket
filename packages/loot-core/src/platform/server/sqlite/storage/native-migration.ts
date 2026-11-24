import { dirname, isUnderRoot } from '#platform/server/fs/web-paths';
import * as idb from '#platform/server/indexeddb';
import type { Data } from '#platform/server/indexeddb';
import { logger } from '#platform/server/log';
import * as host from '#platform/server/native-host';

import { detectOpfsSyncAccess } from './config';
import { createMemoryIdbStore } from './idb-memory';
import { createOpfsStore } from './opfs-sahpool';
import type { SqliteFileStore } from './types';

// The first desktop builds kept budgets in the WebView's own storage (OPFS,
// or IndexedDB without it), as the browser does. Once per budget folder,
// copy each of those budgets into it, unless the folder already has a budget
// of that id. The WebView's copies are left as they were. The marker lives
// in the folder, so it follows the data rather than the WebView profile.

const MIGRATED_MARKER = `${host.NATIVE_DOCUMENT_ROOT}/.actual-desktop-migrated`;

async function markMigrated() {
  await host.writeFile(MIGRATED_MARKER, new Date().toISOString());
}

function isSqliteFile(path: string): boolean {
  return path.endsWith('.sqlite');
}

async function listRows(): Promise<Pick<Data, 'filepath' | 'storage'>[]> {
  const { store } = idb.getStore(await idb.openDatabase(), 'files');
  return new Promise((resolve, reject) => {
    const rows: Pick<Data, 'filepath' | 'storage'>[] = [];
    const request = store.openCursor();
    request.onsuccess = () => {
      const cursor = request.result;
      if (!cursor) {
        resolve(rows);
        return;
      }
      const { filepath, storage } = cursor.value as Data;
      rows.push({ filepath, storage });
      cursor.continue();
    };
    request.onerror = () => reject(request.error);
  });
}

async function readRowContents(path: string): Promise<string | Uint8Array> {
  const { store } = idb.getStore(await idb.openDatabase(), 'files');
  const row = await idb.get(store, path);
  if (row == null) {
    throw new Error('File does not exist: ' + path);
  }
  return row.contents;
}

export async function migrateBrowserBudgetsToNative(): Promise<void> {
  if ((await host.stat(MIGRATED_MARKER)) != null) {
    return;
  }

  const rows = (await listRows()).filter(row =>
    isUnderRoot(row.filepath, host.NATIVE_DOCUMENT_ROOT),
  );
  const budgetDirs = new Set(
    rows
      .filter(row => isSqliteFile(row.filepath))
      .map(row => dirname(row.filepath)),
  );
  if (budgetDirs.size === 0) {
    await markMigrated();
    return;
  }

  const onDisk = await host.listFiles();
  const occupied = new Set(onDisk.files.map(path => dirname(path)));

  const sources: { opfs?: SqliteFileStore; idb?: SqliteFileStore } = {};
  async function sourceFor(
    row: Pick<Data, 'storage'>,
  ): Promise<SqliteFileStore> {
    if (row.storage === 'opfs') {
      if (!(await detectOpfsSyncAccess())) {
        throw new Error('the budget is in OPFS, which is unavailable');
      }
      sources.opfs ??= createOpfsStore();
      return sources.opfs;
    }
    // The fallback store also reads budgets still in absurd-sql's layout
    sources.idb ??= createMemoryIdbStore();
    return sources.idb;
  }

  let failed = false;
  try {
    for (const dir of budgetDirs) {
      if (occupied.has(dir)) {
        logger.info(`desktop storage: ${dir} is already on disk, kept as is`);
        continue;
      }
      const files = rows.filter(row => dirname(row.filepath) === dir);
      const written: string[] = [];
      try {
        // The database goes last, so an interrupted copy never looks like
        // a budget whose data is missing
        const ordered = [
          ...files.filter(row => !isSqliteFile(row.filepath)),
          ...files.filter(row => isSqliteFile(row.filepath)),
        ];
        for (const row of ordered) {
          const contents = isSqliteFile(row.filepath)
            ? await (await sourceFor(row)).readFile(row.filepath)
            : await readRowContents(row.filepath);
          await host.writeFile(row.filepath, contents);
          written.push(row.filepath);
        }
        logger.info(`desktop storage: copied ${dir} to the budget folder`);
      } catch (error) {
        failed = true;
        logger.error(`desktop storage: could not copy ${dir}`, error);
        for (const path of written) {
          await host.removeFile(path).catch(() => undefined);
        }
        await host.removeDir(dir).catch(() => undefined);
      }
    }
  } finally {
    await sources.opfs?.releaseAll().catch(() => undefined);
    await sources.idb?.releaseAll().catch(() => undefined);
  }

  // A budget that failed to copy is tried again on the next start
  if (!failed) {
    await markMigrated();
  }
}
