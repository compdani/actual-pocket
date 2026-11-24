import { describe, expect, it } from 'vitest';

import { crc32, diffPages, encodePatch, sqlitePageSize } from './page-diff';

const PAGE = 4096;

// A fake sqlite file: a header naming the page size, then `pages` pages
// each filled with its own byte
function file(pageBytes: number[], pageSize = PAGE): Uint8Array {
  const bytes = new Uint8Array(pageBytes.length * pageSize);
  pageBytes.forEach((value, index) =>
    bytes.fill(value, index * pageSize, (index + 1) * pageSize),
  );
  bytes.set(new TextEncoder().encode('SQLite format 3\0'), 0);
  const raw = pageSize === 65536 ? 1 : pageSize;
  bytes[16] = raw >> 8;
  bytes[17] = raw & 0xff;
  return bytes;
}

describe('sqlite page diffs', () => {
  it('computes CRC-32 as Go does', () => {
    expect(crc32(new TextEncoder().encode('123456789'))).toBe(0xcbf43926);
    expect(crc32(new Uint8Array(0))).toBe(0);
  });

  it('reads the page size from the header', () => {
    expect(sqlitePageSize(file([1], 4096))).toBe(4096);
    expect(sqlitePageSize(file([1], 65536))).toBe(65536);
    expect(sqlitePageSize(new Uint8Array(10))).toBeNull();
    expect(sqlitePageSize(new Uint8Array(4096))).toBeNull();
  });

  it('finds nothing to save in an unchanged file', () => {
    expect(diffPages(file([1, 2, 3]), file([1, 2, 3]))).toEqual({
      ranges: [],
      changedBytes: 0,
    });
  });

  it('sends only changed pages, merging neighbours', () => {
    const saved = file([1, 2, 3, 4, 5]);
    const next = file([1, 9, 9, 4, 8]);
    const diff = diffPages(saved, next);
    expect(diff?.ranges.map(r => [r.offset, r.data.length])).toEqual([
      [PAGE, 2 * PAGE],
      [4 * PAGE, PAGE],
    ]);
    expect(diff?.changedBytes).toBe(3 * PAGE);
    expect(diff?.ranges[0].oldCrc).toBe(crc32(saved.subarray(PAGE, 3 * PAGE)));
  });

  it('handles a file that grows or shrinks', () => {
    const grown = diffPages(file([1, 2]), file([1, 2, 3]));
    expect(grown?.ranges.map(r => r.offset)).toEqual([2 * PAGE]);
    // The added page replaces nothing
    expect(grown?.ranges[0].oldCrc).toBe(0);

    const shrunk = diffPages(file([1, 2, 3]), file([1, 2]));
    expect(shrunk?.ranges).toEqual([]);
  });

  it('refuses to diff across a page size change', () => {
    expect(diffPages(file([1, 2], 4096), file([1], 8192))).toBeNull();
  });

  it('encodes the patch the host expects', () => {
    const next = file([1, 9]);
    const diff = diffPages(file([1, 2]), next);
    const patch = encodePatch(next.length, diff?.ranges ?? []);
    const view = new DataView(patch.buffer);
    expect(Number(view.getBigUint64(0, true))).toBe(2 * PAGE);
    expect(view.getUint32(8, true)).toBe(1);
    expect(Number(view.getBigUint64(12, true))).toBe(PAGE);
    expect(view.getUint32(20, true)).toBe(PAGE);
    expect(patch.length).toBe(12 + 16 + PAGE);
    expect(patch.subarray(28)).toEqual(next.subarray(PAGE));
  });
});
