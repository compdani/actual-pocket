import * as connection from '#platform/server/connection';
import { logger } from '#platform/server/log';
import * as host from '#platform/server/native-host';
import {
  exportBytes,
  getSqlite3,
  openMemoryDatabase,
} from '#platform/server/sqlite/wasm';
import type { WasmDatabase } from '#platform/server/sqlite/wasm';

import { BudgetInUseError } from './errors';
import {
  acquireWebLock,
  BUDGET_LOCK_TIMEOUT_MS,
  budgetLockName,
} from './locks';
import { diffPages, encodePatch } from './page-diff';
import type { SqliteFileStore } from './types';

// The desktop app's store: each database runs in memory and is saved to the
// file on disk through the desktop host after writes, debounced, plus on
// close and on request. A save sends only the pages that changed since the
// last one, which the host applies all or nothing through a journal; the
// first save, a large change or a file changed by something else is written
// whole, atomically. Either way a crash leaves the previous save or the new
// one, never a torn file.
// The desktop app runs a single worker, but the budget's Web Lock is still
// taken so a reload never has two workers writing one file.

const DEFAULT_DEBOUNCE_MS = 1000;
const DEFAULT_MAX_DIRTY_MS = 5000;

// Past this share of the file a whole-file write is simpler and no slower
const MAX_PATCH_SHARE = 0.5;

export type NativeStoreOptions = {
  debounceMs?: number;
  maxDirtyMs?: number;
  lockTimeoutMs?: number;
};

type Entry = {
  path: string;
  db: WasmDatabase;
  // The file's bytes and version on disk as of the last save, to diff the
  // next one against; null forces a whole-file write
  saved: Uint8Array | null;
  version: string | null;
  dirty: boolean;
  firstDirtyAt: number | null;
  timer: ReturnType<typeof setTimeout> | null;
  pending: Promise<void> | null;
  // Whether the current run of failed writes has had its retry
  retried: boolean;
  // Whether the last write failed, for `flush` to report
  failed: boolean;
  releaseLock: () => void;
};

export function createNativeStore({
  debounceMs = DEFAULT_DEBOUNCE_MS,
  maxDirtyMs = DEFAULT_MAX_DIRTY_MS,
  lockTimeoutMs = BUDGET_LOCK_TIMEOUT_MS,
}: NativeStoreOptions = {}): SqliteFileStore {
  const entries = new Map<string, Entry>();
  const byDb = new Map<WasmDatabase, Entry>();
  // Writes still in flight for closed databases, keyed by path
  const pendingWrites = new Map<string, Promise<void>>();
  // Snapshots whose write on close failed, kept so the next open of the
  // path continues from them instead of the older file, and writes again
  const unsaved = new Map<string, Uint8Array>();
  // Concurrent opens of one path share the whole operation
  const opening = new Map<string, Promise<WasmDatabase>>();

  function inTransaction(db: WasmDatabase): boolean {
    const { capi } = getSqlite3();
    return db.pointer != null && capi.sqlite3_get_autocommit(db.pointer) === 0;
  }

  // Saves bytes as a page patch against the last save when that is small,
  // and whole otherwise. Resolves to the file's new version.
  async function save(
    path: string,
    bytes: Uint8Array,
    saved: Uint8Array | null,
    version: string | null,
  ): Promise<string | null> {
    if (saved && version) {
      const diff = diffPages(saved, bytes);
      if (diff && diff.changedBytes <= bytes.length * MAX_PATCH_SHARE) {
        if (diff.ranges.length === 0 && saved.length === bytes.length) {
          return version;
        }
        const result = await host.patchFile(
          path,
          encodePatch(bytes.length, diff.ranges),
          version,
        );
        if (result !== 'conflict') {
          return result;
        }
        logger.warn('sqlite storage: changed on disk, saving it whole', path);
      }
    }
    return host.writeFile(path, bytes);
  }

  async function persist(entry: Entry, bytes: Uint8Array): Promise<void> {
    entry.version = await save(entry.path, bytes, entry.saved, entry.version);
    entry.saved = bytes;
    entry.retried = false;
    entry.failed = false;
  }

  function schedule(entry: Entry) {
    if (entry.timer) {
      clearTimeout(entry.timer);
    }
    const waited =
      entry.firstDirtyAt == null ? 0 : Date.now() - entry.firstDirtyAt;
    const delay = Math.max(0, Math.min(debounceMs, maxDirtyMs - waited));
    entry.timer = setTimeout(() => {
      entry.timer = null;
      void flushEntry(entry);
    }, delay);
  }

  function flushEntry(entry: Entry): Promise<void> {
    if (!entry.dirty) {
      return entry.pending ?? Promise.resolve();
    }
    if (inTransaction(entry.db)) {
      // Never save half a transaction. The cap restarts too, so a long
      // transaction is polled once per debounce rather than continuously.
      entry.firstDirtyAt = Date.now();
      schedule(entry);
      return entry.pending ?? Promise.resolve();
    }
    const bytes = exportBytes(entry.db);
    entry.dirty = false;
    entry.firstDirtyAt = null;
    const previous = entry.pending ?? Promise.resolve();
    const pending = previous
      .then(() => persist(entry, bytes))
      .catch(error => {
        logger.error('sqlite storage: failed to save', entry.path, error);
        entry.dirty = true;
        entry.failed = true;
        if (byDb.get(entry.db) !== entry) {
          // Closed meanwhile: the close's own write reports for it
          return;
        }
        // One retry after the usual delay, then wait for the next write
        if (!entry.retried) {
          entry.retried = true;
          entry.firstDirtyAt = Date.now();
          schedule(entry);
        } else {
          connection.send('storage-write-failed', { path: entry.path });
        }
      })
      .finally(() => {
        if (entry.pending === pending) {
          entry.pending = null;
        }
      });
    entry.pending = pending;
    return pending;
  }

  function lockBudget(path: string): Promise<() => void> {
    return acquireWebLock(budgetLockName(path), lockTimeoutMs).catch(() => {
      throw new BudgetInUseError(path, 'the storage lock could not be taken');
    });
  }

  function open(path: string): Promise<WasmDatabase> {
    const inFlight = opening.get(path);
    if (inFlight) {
      return inFlight;
    }
    const promise = openOnce(path).finally(() => {
      if (opening.get(path) === promise) {
        opening.delete(path);
      }
    });
    opening.set(path, promise);
    return promise;
  }

  async function openOnce(path: string): Promise<WasmDatabase> {
    const existing = entries.get(path);
    if (existing) {
      return existing.db;
    }
    await pendingWrites.get(path);
    const releaseLock = await lockBudget(path);
    try {
      const stranded = unsaved.get(path);
      const onDisk = stranded ? null : await host.readFileVersioned(path);
      const bytes = stranded ?? onDisk?.bytes ?? null;
      const db = openMemoryDatabase(bytes ?? undefined);
      const entry: Entry = {
        path,
        db,
        // A stranded snapshot is not what is on disk: write it whole
        saved: onDisk?.bytes ?? null,
        version: onDisk?.version ?? null,
        dirty: false,
        firstDirtyAt: null,
        timer: null,
        pending: null,
        retried: false,
        failed: false,
        releaseLock,
      };
      if (bytes == null) {
        // A brand new file: write it now so the folder lists it
        try {
          const initial = exportBytes(db);
          entry.version = await host.writeFile(path, initial);
          entry.saved = initial;
        } catch (error) {
          db.close();
          throw error;
        }
      }
      if (stranded) {
        unsaved.delete(path);
        entry.dirty = true;
        entry.firstDirtyAt = Date.now();
        schedule(entry);
      }
      entries.set(path, entry);
      byDb.set(db, entry);
      return db;
    } catch (error) {
      releaseLock();
      throw error;
    }
  }

  const store: SqliteFileStore = {
    kind: 'native',

    open,

    close(path, db) {
      const entry = entries.get(path);
      if (!entry || entry.db !== db) {
        db.close();
        return;
      }
      if (entry.timer) {
        clearTimeout(entry.timer);
        entry.timer = null;
      }
      // Closing discards an open transaction, as sqlite would
      if (inTransaction(db)) {
        db.exec('ROLLBACK');
      }
      const bytes =
        entry.dirty || entry.pending != null ? exportBytes(db) : null;
      db.close();
      entries.delete(path);
      byDb.delete(db);
      const previous = entry.pending ?? Promise.resolve();
      entry.pending = previous
        .then(() => (bytes && entry.dirty ? persist(entry, bytes) : undefined))
        .catch(error => {
          logger.error('sqlite storage: failed to save on close', path, error);
          if (bytes) {
            unsaved.set(path, bytes);
            connection.send('storage-write-failed', { path });
          }
        })
        .finally(() => entry.releaseLock());
      pendingWrites.set(path, entry.pending);
      void entry.pending.finally(() => {
        if (pendingWrites.get(path) === entry.pending) {
          pendingWrites.delete(path);
        }
      });
    },

    async readFile(path) {
      const inFlight = opening.get(path);
      if (inFlight) {
        return exportBytes(await inFlight);
      }
      await pendingWrites.get(path);
      const entry = entries.get(path);
      if (entry) {
        return exportBytes(entry.db);
      }
      const stranded = unsaved.get(path);
      if (stranded) {
        return stranded;
      }
      const bytes = await host.readFile(path);
      if (bytes == null) {
        throw new Error('File does not exist: ' + path);
      }
      return bytes;
    },

    async writeFile(path, bytes) {
      await opening.get(path)?.catch(() => undefined);
      await pendingWrites.get(path);
      if (entries.has(path)) {
        throw new Error(`cannot overwrite an open database: ${path}`);
      }
      const releaseLock = await lockBudget(path);
      try {
        await host.writeFile(path, bytes);
        unsaved.delete(path);
      } finally {
        releaseLock();
      }
    },

    removeFile(path) {
      return store.removeFiles([path]);
    },

    // Every budget involved is locked before the first file goes, so a
    // budget held elsewhere refuses the whole removal
    async removeFiles(paths) {
      for (const path of paths) {
        await opening.get(path)?.catch(() => undefined);
        await pendingWrites.get(path);
        const entry = entries.get(path);
        if (entry) {
          store.close(path, entry.db);
          await pendingWrites.get(path);
        }
      }
      const releases: (() => void)[] = [];
      try {
        const locked = new Set<string>();
        for (const path of paths) {
          const name = budgetLockName(path);
          if (!locked.has(name)) {
            locked.add(name);
            releases.push(await lockBudget(path));
          }
        }
        for (const path of paths) {
          await host.removeFile(path);
          unsaved.delete(path);
        }
      } finally {
        for (const release of releases) {
          release();
        }
      }
    },

    async revertFile(path) {
      throw new Error(
        `the desktop storage has no legacy layout to revert to: ${path}`,
      );
    },

    async exists(path) {
      if (entries.has(path) || unsaved.has(path)) {
        return true;
      }
      return (await host.stat(path)) != null;
    },

    // Rejects when a file could not be written, so callers never take a
    // flush for durability it did not deliver
    async flush() {
      await Promise.all([
        ...[...entries.values()].map(flushEntry),
        ...pendingWrites.values(),
      ]);
      const notWritten = [...entries.values()]
        .filter(entry => entry.failed)
        .map(entry => entry.path)
        .concat([...unsaved.keys()]);
      if (notWritten.length > 0) {
        throw new Error(
          'sqlite storage: not written: ' + notWritten.join(', '),
        );
      }
    },

    async releaseAll() {
      for (const entry of [...entries.values()]) {
        store.close(entry.path, entry.db);
      }
      await Promise.all([...pendingWrites.values()]);
    },

    onWrite(db) {
      const entry = byDb.get(db);
      if (!entry) {
        return;
      }
      entry.dirty = true;
      entry.firstDirtyAt ??= Date.now();
      schedule(entry);
    },
  };

  return store;
}
