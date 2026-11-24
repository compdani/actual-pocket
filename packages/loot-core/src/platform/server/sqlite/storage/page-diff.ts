// Page diffs for the desktop app's storage: the pages of a sqlite file that
// changed since its last save, encoded as the patch the desktop host applies
// (packages/desktop-wails patch.go).

export type PageRange = {
  offset: number;
  data: Uint8Array;
  // CRC-32 of the bytes the range replaces, clipped to the old file, so the
  // host can tell a file that changed under it
  oldCrc: number;
};

export type PageDiff = {
  ranges: PageRange[];
  changedBytes: number;
};

const DEFAULT_PAGE_SIZE = 4096;

// The page size in a sqlite header (bytes 16-17, big endian; 1 means
// 65536), or null for anything that is not a valid sqlite header
export function sqlitePageSize(bytes: Uint8Array): number | null {
  if (bytes.length < 100) {
    return null;
  }
  const raw = (bytes[16] << 8) | bytes[17];
  const size = raw === 1 ? 65536 : raw;
  const isPowerOfTwo =
    size >= 512 && size <= 65536 && (size & (size - 1)) === 0;
  return isPowerOfTwo ? size : null;
}

function pagesEqual(
  a: Uint8Array,
  b: Uint8Array,
  offset: number,
  length: number,
): boolean {
  // Word-wise where both views allow it: much faster on large budgets
  if (
    (a.byteOffset + offset) % 4 === 0 &&
    (b.byteOffset + offset) % 4 === 0 &&
    length % 4 === 0
  ) {
    const wordsA = new Uint32Array(a.buffer, a.byteOffset + offset, length / 4);
    const wordsB = new Uint32Array(b.buffer, b.byteOffset + offset, length / 4);
    for (let i = 0; i < wordsA.length; i++) {
      if (wordsA[i] !== wordsB[i]) {
        return false;
      }
    }
    return true;
  }
  for (let i = offset; i < offset + length; i++) {
    if (a[i] !== b[i]) {
      return false;
    }
  }
  return true;
}

// The ranges of `next` that differ from `saved`, adjacent changed pages
// merged. Null when a diff cannot be trusted (not sqlite, or the page size
// changed, as a VACUUM can do), which calls for a whole-file write.
export function diffPages(
  saved: Uint8Array,
  next: Uint8Array,
): PageDiff | null {
  const pageSize = sqlitePageSize(next) ?? DEFAULT_PAGE_SIZE;
  if (sqlitePageSize(saved) !== sqlitePageSize(next)) {
    return null;
  }
  const ranges: PageRange[] = [];
  let changedBytes = 0;
  let start = -1;

  function close(end: number) {
    if (start < 0) {
      return;
    }
    const data = next.subarray(start, end);
    const oldEnd = Math.min(end, saved.length);
    const old = start < saved.length ? saved.subarray(start, oldEnd) : null;
    ranges.push({
      offset: start,
      data,
      oldCrc: crc32(old ?? new Uint8Array(0)),
    });
    changedBytes += data.length;
    start = -1;
  }

  for (let offset = 0; offset < next.length; offset += pageSize) {
    const length = Math.min(pageSize, next.length - offset);
    const changed =
      offset + length > saved.length ||
      !pagesEqual(saved, next, offset, length);
    if (changed && start < 0) {
      start = offset;
    } else if (!changed) {
      close(offset);
    }
  }
  close(next.length);
  return { ranges, changedBytes };
}

// The patch body: new size u64, range count u32, then per range offset u64,
// length u32, old CRC u32 and the bytes, all little endian
export function encodePatch(size: number, ranges: PageRange[]): Uint8Array {
  const total = ranges.reduce((sum, range) => sum + 16 + range.data.length, 12);
  const bytes = new Uint8Array(total);
  const view = new DataView(bytes.buffer);
  view.setBigUint64(0, BigInt(size), true);
  view.setUint32(8, ranges.length, true);
  let position = 12;
  for (const range of ranges) {
    view.setBigUint64(position, BigInt(range.offset), true);
    view.setUint32(position + 8, range.data.length, true);
    view.setUint32(position + 12, range.oldCrc, true);
    bytes.set(range.data, position + 16);
    position += 16 + range.data.length;
  }
  return bytes;
}

let crcTable: Uint32Array | null = null;

// CRC-32 (IEEE), as Go's hash/crc32.ChecksumIEEE
export function crc32(bytes: Uint8Array): number {
  if (!crcTable) {
    crcTable = new Uint32Array(256);
    for (let n = 0; n < 256; n++) {
      let c = n;
      for (let k = 0; k < 8; k++) {
        c = c & 1 ? 0xedb88320 ^ (c >>> 1) : c >>> 1;
      }
      crcTable[n] = c >>> 0;
    }
  }
  let crc = 0xffffffff;
  for (let i = 0; i < bytes.length; i++) {
    crc = crcTable[(crc ^ bytes[i]) & 0xff] ^ (crc >>> 8);
  }
  return (crc ^ 0xffffffff) >>> 0;
}
