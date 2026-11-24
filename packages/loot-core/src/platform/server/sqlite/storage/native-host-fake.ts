import { vi } from 'vitest';

import { crc32 } from './page-diff';

// An in-memory stand-in for the desktop host's file API (packages/
// desktop-wails fsapi.go), installed as the global fetch. Tests use it to
// check what the engine reads and writes on "disk".

export type FakeHost = {
  files: Map<string, Uint8Array>;
  dirs: Set<string>;
  mtimes: Map<string, number>;
  // Bytes received per request kind, to check what a save sent
  sent: { put: number; patch: number };
  // Changes a file as another program would, giving it a new version
  changeOutside: (path: string, bytes: Uint8Array) => void;
  // Rejects the next writes of a path with a 503, once per entry
  failWrites: (path: string, times?: number) => void;
  requests: string[];
  text: (path: string) => string | undefined;
};

const ROOT = '/documents/Actual';

function parentDirs(path: string): string[] {
  const parts = path.split('/');
  const dirs: string[] = [];
  for (let i = 2; i < parts.length; i++) {
    dirs.push(parts.slice(0, i).join('/'));
  }
  return dirs.filter(dir => dir === ROOT || dir.startsWith(ROOT + '/'));
}

export function installFakeHost(): FakeHost {
  const files = new Map<string, Uint8Array>();
  const dirs = new Set<string>([ROOT]);
  const mtimes = new Map<string, number>();
  const failures = new Map<string, number>();
  const requests: string[] = [];
  const versions = new Map<string, number>();
  let nextVersion = 1;
  const sent = { put: 0, patch: 0 };

  function store(path: string, bytes: Uint8Array) {
    files.set(path, bytes);
    mtimes.set(path, Date.now());
    versions.set(path, nextVersion++);
    parentDirs(path).forEach(dir => dirs.add(dir));
  }

  function versionHeaders(path: string): HeadersInit {
    return { 'X-Actual-Version': String(versions.get(path)) };
  }

  // Like the real host: base64 text whose decoded length must match,
  // because WKWebView drops binary bodies on the app's URL scheme
  function decodeBody(init: RequestInit, headers: Headers): Uint8Array | null {
    if (
      typeof init.body !== 'string' ||
      headers.get('X-Actual-Body-Encoding') !== 'base64'
    ) {
      throw new Error('the engine must send bodies as base64 text');
    }
    const decoded = Uint8Array.from(atob(init.body), char =>
      char.charCodeAt(0),
    );
    return String(decoded.length) === headers.get('X-Actual-Body-Length')
      ? decoded
      : null;
  }

  // Applies a page patch as patch.go does, or returns null to refuse it
  function applyPatch(old: Uint8Array, patch: Uint8Array): Uint8Array | null {
    const view = new DataView(patch.buffer, patch.byteOffset, patch.byteLength);
    const size = Number(view.getBigUint64(0, true));
    const count = view.getUint32(8, true);
    const next = new Uint8Array(size);
    next.set(old.subarray(0, Math.min(old.length, size)));
    let position = 12;
    for (let i = 0; i < count; i++) {
      const offset = Number(view.getBigUint64(position, true));
      const length = view.getUint32(position + 8, true);
      const oldCrc = view.getUint32(position + 12, true);
      const replaced =
        offset < old.length
          ? old.subarray(offset, Math.min(offset + length, old.length))
          : new Uint8Array(0);
      if (crc32(replaced) !== oldCrc) {
        return null;
      }
      next.set(patch.subarray(position + 16, position + 16 + length), offset);
      position += 16 + length;
    }
    return next;
  }

  const host: FakeHost = {
    files,
    dirs,
    mtimes,
    requests,
    sent,
    changeOutside(path, bytes) {
      store(path, bytes);
    },
    failWrites(path, times = 1) {
      failures.set(path, times);
    },
    text(path) {
      const bytes = files.get(path);
      return bytes && new TextDecoder().decode(bytes);
    },
  };

  const fetchMock = vi.fn(
    async (input: RequestInfo | URL, init: RequestInit = {}) => {
      const url = new URL(String(input), 'http://wails.localhost');
      const method = init.method ?? 'GET';
      const endpoint = url.pathname.replace('/__actual/fs/', '');
      const path = url.searchParams.get('path') ?? '';
      requests.push(`${method} ${endpoint} ${path}`.trim());
      const headers = new Headers(init.headers);
      if (headers.get('X-Actual-Desktop') !== '1') {
        return new Response('forbidden', { status: 403 });
      }
      if (endpoint === 'list') {
        return Response.json({ files: [...files.keys()], dirs: [...dirs] });
      }
      switch (`${method} ${endpoint}`) {
        case 'GET file': {
          const bytes = files.get(path);
          return bytes
            ? new Response(bytes.slice(), { headers: versionHeaders(path) })
            : new Response('not found', { status: 404 });
        }
        case 'PUT file': {
          const remaining = failures.get(path) ?? 0;
          if (remaining > 0) {
            failures.set(path, remaining - 1);
            return Response.json(
              { message: 'disk full', path, code: 'ENOSPC' },
              { status: 503 },
            );
          }
          const decoded = decodeBody(init, headers);
          if (!decoded) {
            return new Response('incomplete', { status: 400 });
          }
          sent.put += decoded.length;
          store(path, decoded);
          return new Response(null, {
            status: 204,
            headers: versionHeaders(path),
          });
        }
        case 'POST patch': {
          const remaining = failures.get(path) ?? 0;
          if (remaining > 0) {
            failures.set(path, remaining - 1);
            return Response.json(
              { message: 'disk full', path, code: 'ENOSPC' },
              { status: 503 },
            );
          }
          const decoded = decodeBody(init, headers);
          if (!decoded) {
            return new Response('incomplete', { status: 400 });
          }
          const old = files.get(path);
          if (
            !old ||
            headers.get('X-Actual-Base-Version') !== String(versions.get(path))
          ) {
            return new Response('conflict', { status: 412 });
          }
          const next = applyPatch(old, decoded);
          if (!next) {
            return new Response('conflict', { status: 412 });
          }
          sent.patch += decoded.length;
          store(path, next);
          return new Response(null, {
            status: 204,
            headers: versionHeaders(path),
          });
        }
        case 'DELETE file':
          files.delete(path);
          return new Response(null, { status: 204 });
        case 'GET stat': {
          if (files.has(path)) {
            return Response.json({
              mtimeMs: mtimes.get(path) ?? 0,
              isDir: false,
            });
          }
          if (dirs.has(path)) {
            return Response.json({ mtimeMs: 0, isDir: true });
          }
          return new Response('not found', { status: 404 });
        }
        case 'POST mkdir':
          [...parentDirs(path), path].forEach(dir => dirs.add(dir));
          return new Response(null, { status: 204 });
        case 'POST rmdir': {
          const isEmpty = ![...files.keys(), ...dirs].some(
            entry => entry !== path && entry.startsWith(path + '/'),
          );
          if (isEmpty && path !== ROOT) {
            dirs.delete(path);
          }
          return new Response(null, { status: 204 });
        }
        default:
          return new Response('not found', { status: 404 });
      }
    },
  );
  vi.stubGlobal('fetch', fetchMock);
  return host;
}
