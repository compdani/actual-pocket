import { sendCatch } from '@actual-app/core/platform/client/connection';
import type {
  DesktopHost,
  DesktopSyncServerStatus,
} from '@actual-app/core/typings/window';

type FileFilter = { name: string; extensions: string[] };
type ImportedFile = { name: string; contents: string };

type DesktopMethods = {
  OpenFile: (filters: FileFilter[]) => ImportedFile | null;
  SaveFile: (contents: string, filename: string, title: string) => void;
  OpenURL: (url: string) => void;
  StartOAuthServer: () => string;
  Ready: () => void;
  ConfirmClose: () => void;
  CloseFailed: (message: string) => void;
  LogError: (message: string) => void;
  GetDocumentDir: () => string;
  ChooseFolder: (title: string) => string;
  SetDocumentDir: (directory: string) => void;
  MoveBudgetDirectory: (directory: string) => void;
  RevealDocumentDir: () => void;
  OpenInFileManager: (filepath: string) => void;
  GetSyncServerStatus: () => DesktopSyncServerStatus;
  StartSyncServer: (port: number) => void;
  StopSyncServer: () => void;
  TrustCertificate: () => string;
};

// This is the small subset of Wails' bundled runtime used by the bridge.
export type WailsRuntime = {
  Call: {
    ByName: <T>(method: string, ...args: unknown[]) => Promise<T>;
  };
  Events: { On: (event: string, callback: () => void) => () => void };
};

export async function flushWailsStorage(): Promise<void> {
  const result = await sendCatch('storage-flush');
  if (result.error) {
    throw new Error(result.error.message ?? 'Unable to persist budget storage');
  }
}

export function encodeFile(contents: string | Uint8Array): string {
  const bytes =
    typeof contents === 'string'
      ? new TextEncoder().encode(contents)
      : contents;
  // Do not spread the whole file: large budget exports exceed the JS call
  // stack limit. Go's []byte JSON representation is a base64 string.
  let binary = '';
  for (let offset = 0; offset < bytes.length; offset += 32768) {
    binary += String.fromCharCode(...bytes.subarray(offset, offset + 32768));
  }
  return btoa(binary);
}

export function decodeFile(contents: string): Uint8Array<ArrayBuffer> {
  return Uint8Array.from(atob(contents), char => char.charCodeAt(0));
}

export function installWailsBridge(runtime: Promise<WailsRuntime>): void {
  async function invoke<Method extends keyof DesktopMethods>(
    method: Method,
    ...args: Parameters<DesktopMethods[Method]>
  ): Promise<ReturnType<DesktopMethods[Method]>> {
    const { Call } = await runtime;
    return Call.ByName(`main.Desktop.${method}`, ...args);
  }

  window.Actual.IS_WAILS = true;
  // Keep backend failures visible in the terminal even when the platform's
  // Web Inspector is unavailable. Do not forward successful request payloads.
  void window.Actual.getServerSocket().then(worker => {
    worker?.addEventListener('message', event => {
      const error = event.data?.error ?? event.data?.result?.error;
      if (error && (error.message || error.stack || error.cause?.stack)) {
        void invoke(
          'LogError',
          `${error.message ?? ''}\n${error.cause?.stack ?? error.stack ?? ''}`,
        ).catch(console.error);
      }
    });
  });
  const desktop: DesktopHost = {
    getDocumentDir: () => invoke('GetDocumentDir'),
    chooseFolder: async (title = '') =>
      (await invoke('ChooseFolder', title)) || null,
    setDocumentDir: async directory => {
      // Nothing may be written to the old folder after the switch
      await flushWailsStorage();
      await invoke('SetDocumentDir', directory);
    },
    moveBudgetDirectory: async directory => {
      await flushWailsStorage();
      await invoke('MoveBudgetDirectory', directory);
    },
    revealDocumentDir: () => invoke('RevealDocumentDir'),
    openInFileManager: filepath => invoke('OpenInFileManager', filepath),
    getSyncServerStatus: () => invoke('GetSyncServerStatus'),
    startSyncServer: port => invoke('StartSyncServer', port),
    stopSyncServer: () => invoke('StopSyncServer'),
    trustCertificate: () => invoke('TrustCertificate'),
  };
  window.Actual.desktop = desktop;
  window.Actual.openFileDialog = async ({ filters = [], properties = [] }) => {
    if (properties.includes('openDirectory')) {
      // Folders are used by the host (the budget folder), so their real
      // path is returned rather than a copy in the worker
      const folder = await desktop.chooseFolder();
      return folder ? [folder] : [];
    }
    const file = await invoke('OpenFile', filters);
    if (!file) {
      return [];
    }
    // Importers use the browser worker's virtual filesystem. Native absolute
    // paths must never be passed to it. The host returns only the chosen file.
    const extension = file.name.includes('.') ? file.name.split('.').pop() : '';
    const filename = `import-${crypto.randomUUID()}${extension ? `.${extension}` : ''}`;
    await window.__actionsForMenu.uploadFile(
      filename,
      decodeFile(file.contents).buffer,
    );
    return [`/uploads/${filename}`];
  };
  window.Actual.saveFile = async (contents, filename, title = '') => {
    await invoke('SaveFile', encodeFile(contents), filename, title);
  };
  window.Actual.openURLInBrowser = url => {
    void invoke('OpenURL', url).catch(console.error);
  };
  window.Actual.startOAuthServer = () => invoke('StartOAuthServer');
  window.Actual.isUpdateReadyForDownload = () => false;
  window.Actual.waitForUpdateReadyForDownload = () =>
    new Promise(() => {
      // Packaged desktop assets update when a new application is installed.
    });
  window.Actual.reload = async () => {
    await flushWailsStorage();
    window.location.reload();
  };
  window.Actual.relaunch = () => {
    void window.Actual.reload?.().catch(console.error);
  };

  // External anchors and programmatic popups should use the system browser,
  // keeping the application on its stable local origin (and its budget data).
  document.addEventListener('click', event => {
    const anchor =
      event.target instanceof Element ? event.target.closest('a[href]') : null;
    if (!(anchor instanceof HTMLAnchorElement) || event.defaultPrevented) {
      return;
    }
    const url = new URL(anchor.href, window.location.href);
    if (
      url.origin !== window.location.origin &&
      ['http:', 'https:', 'mailto:'].includes(url.protocol)
    ) {
      event.preventDefault();
      window.Actual.openURLInBrowser(url.href);
    }
  });
  window.open = url => {
    if (url) {
      const resolved = new URL(url, window.location.href);
      if (resolved.origin === window.location.origin) {
        window.location.assign(resolved.href);
      } else {
        window.Actual.openURLInBrowser(resolved.href);
      }
    }
    return null;
  };

  let isClosing = false;
  void runtime
    .then(async ({ Events }) => {
      Events.On('desktop:closing', () => {
        if (isClosing) {
          return;
        }
        isClosing = true;
        void flushWailsStorage()
          .then(() => invoke('ConfirmClose'))
          .catch(async error => {
            isClosing = false;
            await invoke('CloseFailed', String(error));
          });
      });
      // Until the worker is reachable, the native close button can quit
      // normally. Afterwards a close waits for pending IndexedDB writes.
      await flushWailsStorage();
      await invoke('Ready');
    })
    .catch(console.error);
}
