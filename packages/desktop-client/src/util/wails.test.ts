import { sendCatch } from '@actual-app/core/platform/client/connection';
import { describe, expect, it, vi } from 'vitest';

import { decodeFile, encodeFile, flushWailsStorage } from './wails';

vi.mock('@actual-app/core/platform/client/connection', () => ({
  sendCatch: vi.fn(),
}));

describe('Wails file transfers', () => {
  it('preserves Unicode text in CSV exports', () => {
    const csv = 'Payee,Amount\nCafé 日本語 🍎,12.34\n';
    expect(new TextDecoder().decode(decodeFile(encodeFile(csv)))).toBe(csv);
  });

  it('round trips binary budget exports larger than the JS argument limit', () => {
    const bytes = Uint8Array.from(
      { length: 400_000 },
      (_, index) => index % 256,
    );
    expect(decodeFile(encodeFile(bytes))).toEqual(bytes);
  });
});

describe('Wails shutdown persistence', () => {
  it('blocks shutdown when the worker could not flush its pending writes', async () => {
    vi.mocked(sendCatch).mockResolvedValueOnce({
      data: undefined,
      error: { type: 'ServerError', message: 'Quota exceeded' },
    });
    await expect(flushWailsStorage()).rejects.toThrow('Quota exceeded');
  });

  it('allows shutdown after the worker confirms persistence', async () => {
    vi.mocked(sendCatch).mockResolvedValueOnce({
      data: 'ok',
      error: undefined,
    });
    await expect(flushWailsStorage()).resolves.toBeUndefined();
  });
});
