#!/usr/bin/env node
import { spawn } from 'node:child_process';
import { cp, mkdir, readFile, rm, writeFile } from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const host = path.join(root, 'packages/desktop-wails');
const args = process.argv.slice(2);
const supportedArgs = ['--run', '--debug'];
if (args.some(arg => !supportedArgs.includes(arg))) {
  throw new Error(`Supported arguments: ${supportedArgs.join(', ')}`);
}

async function run(command, commandArgs, options = {}) {
  await new Promise((resolve, reject) => {
    const child = spawn(command, commandArgs, {
      cwd: root,
      stdio: 'inherit',
      ...options,
    });
    child.on('error', reject);
    child.on('exit', (code, signal) => {
      if (code === 0) resolve();
      else reject(new Error(`${command} exited with ${signal ?? code}`));
    });
  });
}

// Invoke lage's JS entry through Node so this also works on Windows, where
// package-manager and tool shims are `.cmd` files that `spawn` cannot run.
const lage = path.join(root, 'node_modules/lage/dist/lage.js');
// Browser translations are shared with the other native builds. Do not update
// a developer's translations checkout here; an existing one is used as-is.
await run(process.execPath, [lage, 'build:wails', '--to=@actual-app/web']);
const webBuild = path.join(root, 'packages/desktop-client/build-wails');
const staged = path.join(host, 'frontend/dist');
await rm(staged, { recursive: true, force: true });
await cp(webBuild, staged, {
  recursive: true,
  filter: source => !source.endsWith('.map') && !source.endsWith('stats.json'),
});

const { version } = JSON.parse(
  await readFile(
    path.join(root, 'packages/desktop-client/package.json'),
    'utf8',
  ),
);
const dist = path.join(host, 'dist');
await mkdir(dist, { recursive: true });
let executable;
const ldflags = [`-s -w -X main.version=${version}`];
if (process.platform === 'darwin') {
  const bundle = path.join(dist, 'Actual Pocket.app');
  const contents = path.join(bundle, 'Contents');
  await mkdir(path.join(contents, 'MacOS'), { recursive: true });
  await mkdir(path.join(contents, 'Resources'), { recursive: true });
  executable = path.join(contents, 'MacOS/actual-pocket');
  await cp(
    path.join(root, 'packages/desktop-electron/icons/icon.icns'),
    path.join(contents, 'Resources/icon.icns'),
  );
  await writeFile(
    path.join(contents, 'Info.plist'),
    `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>CFBundleExecutable</key><string>actual-pocket</string>
<key>CFBundleIdentifier</key><string>com.actualpocket.desktop</string>
<key>CFBundleName</key><string>Actual Pocket</string>
<key>CFBundleDisplayName</key><string>Actual Pocket</string>
<key>CFBundlePackageType</key><string>APPL</string>
<key>CFBundleShortVersionString</key><string>${version}</string>
<key>CFBundleVersion</key><string>${version}</string>
<key>CFBundleIconFile</key><string>icon.icns</string>
<key>NSHighResolutionCapable</key><true/>
<key>NSAppTransportSecurity</key><dict><key>NSAllowsLocalNetworking</key><true/></dict>
</dict></plist>
`,
  );
} else {
  executable = path.join(
    dist,
    process.platform === 'win32' ? 'actual-pocket.exe' : 'actual-pocket',
  );
  if (process.platform === 'win32') ldflags[0] += ' -H windowsgui';
}
await run('go', [
  '-C',
  host,
  'build',
  '-trimpath',
  '-tags',
  args.includes('--debug') ? 'devtools,private_mac_apis' : 'production',
  '-ldflags',
  ldflags[0],
  '-o',
  executable,
  '.',
]);
if (process.platform === 'darwin') {
  await run('codesign', [
    '--force',
    '--sign',
    '-',
    path.join(dist, 'Actual Pocket.app'),
  ]);
}
console.log(`Built ${executable}`);
if (args.includes('--run')) {
  await run(executable, [], {
    env: {
      ...process.env,
      ACTUAL_WAILS_DEBUG: args.includes('--debug') ? '1' : '',
    },
  });
}
