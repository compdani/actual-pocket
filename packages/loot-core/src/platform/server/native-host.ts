// Client for the desktop host's file API, used by the `native` storage. The
// desktop app (packages/desktop-wails) serves it from its internal asset
// server, which only the app's own WebView can reach, and maps the virtual
// budget folder below onto the folder on disk the user chose. Every path
// sent is a virtual path under NATIVE_DOCUMENT_ROOT.

// The engine's budget folder in the browser build (ACTUAL_DOCUMENT_DIR plus
// 'Actual'); the host maps exactly this directory
export const NATIVE_DOCUMENT_ROOT = '/documents/Actual';

const API_ROOT = '/__actual/fs';
// The host refuses requests without it, so a plain link or image pointing
// at the API from inside the app cannot read or change budget files
const HEADERS = { 'X-Actual-Desktop': '1' };

export type NativeListing = { files: string[]; dirs: string[] };
export type NativeStat = { mtimeMs: number; isDir: boolean };

// A failure the host traced to the folder on disk: `path` is the real path
// and `code` the filesystem error (EACCES, ENOENT, ...), for the startup
// error screen to show
export class NativeFolderError extends Error {
  path: string;
  code: string | undefined;

  constructor(message: string, path: string, code: string | undefined) {
    super(message);
    this.name = 'NativeFolderError';
    this.path = path;
    this.code = code;
  }
}

type HostErrorBody = { message?: string; path?: string; code?: string };

function url(endpoint: string, path?: string): string {
  const query = path == null ? '' : `?path=${encodeURIComponent(path)}`;
  return `${API_ROOT}/${endpoint}${query}`;
}

async function request(
  endpoint: string,
  path: string | undefined,
  init: RequestInit = {},
  // Statuses the caller handles itself, besides success
  handled: number[] = [404],
): Promise<Response> {
  const response = await fetch(url(endpoint, path), {
    ...init,
    headers: { ...HEADERS, ...init.headers },
    cache: 'no-store',
  });
  if (!response.ok && !handled.includes(response.status)) {
    const text = await response.text().catch(() => '');
    let body: HostErrorBody = {};
    try {
      body = JSON.parse(text) as HostErrorBody;
    } catch {
      // Not a structured error; the text is the message
    }
    const message =
      `desktop file ${endpoint} ${path ?? ''} failed: ${response.status} ${body.message ?? text}`.trim();
    if (body.path) {
      throw new NativeFolderError(message, body.path, body.code);
    }
    throw new Error(message);
  }
  return response;
}

export async function listFiles(): Promise<NativeListing> {
  const response = await request('list', undefined);
  return (await response.json()) as NativeListing;
}

// The file's bytes, or null when it does not exist
export async function readFile(path: string): Promise<Uint8Array | null> {
  return (await readFileVersioned(path))?.bytes ?? null;
}

// The file's bytes with the version the host gave them (see patchFile), or
// null when it does not exist
export async function readFileVersioned(
  path: string,
): Promise<{ bytes: Uint8Array; version: string | null } | null> {
  const response = await request('file', path);
  if (response.status === 404) {
    return null;
  }
  return {
    bytes: new Uint8Array(await response.arrayBuffer()),
    version: response.headers.get('X-Actual-Version'),
  };
}

function toBase64(bytes: Uint8Array): string {
  let binary = '';
  // Chunked: spreading a whole budget would exceed the call stack
  for (let offset = 0; offset < bytes.length; offset += 0x8000) {
    binary += String.fromCharCode(...bytes.subarray(offset, offset + 0x8000));
  }
  return btoa(binary);
}

// Replaces the file atomically on disk, creating its directories.
//
// The bytes travel as base64 text: WKWebView hands the app's URL scheme no
// body at all for binary bodies (ArrayBuffer, typed arrays, Blob), only for
// strings, and Wails' own workaround is installed on pages, not in workers.
// The decoded length goes along, and the host refuses a write that does
// not match it, so a body lost on the way can never become an empty file.
//
// Resolves to the file's new version.
export async function writeFile(
  path: string,
  contents: Uint8Array | string,
): Promise<string | null> {
  const bytes =
    typeof contents === 'string'
      ? new TextEncoder().encode(contents)
      : contents;
  const response = await request('file', path, {
    method: 'PUT',
    headers: bodyHeaders(bytes),
    body: toBase64(bytes),
  });
  return response.headers.get('X-Actual-Version');
}

function bodyHeaders(bytes: Uint8Array): Record<string, string> {
  return {
    'Content-Type': 'text/plain',
    'X-Actual-Body-Encoding': 'base64',
    'X-Actual-Body-Length': String(bytes.length),
  };
}

// Applies a page patch (see sqlite/storage/page-diff.ts) to a file that is
// still at `baseVersion`. The host applies it all or nothing, through a
// journal. Resolves to the new version, or 'conflict' when the file changed
// since that version, which calls for a whole-file write instead.
export async function patchFile(
  path: string,
  patch: Uint8Array,
  baseVersion: string,
): Promise<string | 'conflict'> {
  const response = await request(
    'patch',
    path,
    {
      method: 'POST',
      headers: { ...bodyHeaders(patch), 'X-Actual-Base-Version': baseVersion },
      body: toBase64(patch),
    },
    [412],
  );
  if (response.status === 412) {
    return 'conflict';
  }
  return response.headers.get('X-Actual-Version') ?? 'conflict';
}

// Removing a file that is already gone succeeds
export async function removeFile(path: string): Promise<void> {
  await request('file', path, { method: 'DELETE' });
}

export async function stat(path: string): Promise<NativeStat | null> {
  const response = await request('stat', path);
  if (response.status === 404) {
    return null;
  }
  return (await response.json()) as NativeStat;
}

export async function mkdir(path: string): Promise<void> {
  await request('mkdir', path, { method: 'POST' });
}

// Removes the directory when it is empty, and leaves it otherwise
export async function removeDir(path: string): Promise<void> {
  await request('rmdir', path, { method: 'POST' });
}
