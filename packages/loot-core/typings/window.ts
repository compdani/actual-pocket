type FileDialogOptions = {
  properties?: Array<'openFile' | 'openDirectory'>;
  filters?: {
    name: string;
    extensions: string[];
  }[];
};

export type DesktopSyncServerStatus = {
  // False when this build ships without the server
  available: boolean;
  running: boolean;
  port: number;
  autoStart: boolean;
};

// Native features of the desktop app (packages/desktop-wails), present only
// there. Folder paths are real paths on this computer.
export type DesktopHost = {
  getDocumentDir: () => Promise<string>;
  // A folder the user picked, or null when the dialog was cancelled
  chooseFolder: (title?: string) => Promise<string | null>;
  // Use another folder for budgets; the app reloads afterwards
  setDocumentDir: (directory: string) => Promise<void>;
  // Move the budgets into another folder and use it; the app reloads
  // afterwards
  moveBudgetDirectory: (directory: string) => Promise<void>;
  revealDocumentDir: () => Promise<void>;
  openInFileManager: (filepath: string) => Promise<void>;
  getSyncServerStatus: () => Promise<DesktopSyncServerStatus>;
  startSyncServer: (port: number) => Promise<void>;
  stopSyncServer: () => Promise<void>;
  // Adds a server certificate the user picks to the system's trust store.
  // Resolves to a message for the user, or '' when cancelled.
  trustCertificate: () => Promise<string>;
};

type Actual = {
  IS_DEV: boolean;
  IS_WAILS?: boolean;
  desktop?: DesktopHost;
  ACTUAL_VERSION: string;
  openURLInBrowser: (url: string) => void;
  saveFile: (
    contents: string | Buffer,
    filename: string,
    dialogTitle?: string,
  ) => Promise<void>;
  openFileDialog: (options: FileDialogOptions) => Promise<string[]>;
  relaunch: () => void;
  reload: (() => Promise<void>) | undefined;
  applyAppUpdate: () => Promise<void>;
  getServerSocket: () => Promise<Worker | null>;
  setTheme: (theme: string) => void;
  logToTerminal: (...args: unknown[]) => void;
  isUpdateReadyForDownload: () => boolean;
  waitForUpdateReadyForDownload: () => Promise<void>;
  startOAuthServer: () => Promise<string>;
};

declare global {
  var Actual: Actual;

  // oxlint-disable-next-line typescript/consistent-type-definitions -- global Window augmentation requires interface
  interface Window {
    Actual: Actual;
  }

  var IS_TESTING: boolean;

  var currentMonth: string | null;

  var emptyDatabase: (avoidUpdate?: boolean) => () => Promise<void>;
}
