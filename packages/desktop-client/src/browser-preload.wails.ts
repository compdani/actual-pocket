// Wails runs the web worker, not the Electron/Node budgeting engine.
import './browser-preload.js';
import { installWailsBridge } from './util/wails';
import type { WailsRuntime } from './util/wails';

// Wails serves the runtime from its embedded asset server. Keeping this an
// external import avoids adding another copy of the runtime to the web build.
const runtimeUrl = '/wails/runtime.js';
const runtime: Promise<WailsRuntime> = import(/* @vite-ignore */ runtimeUrl);
installWailsBridge(runtime);
