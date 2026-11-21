#!/usr/bin/env node
// Enforce that all workspaces use the same version range of any shared
// dependency. Workspace protocol references (workspace:*) and
// peerDependencies are excluded since they intentionally use different or
// wider ranges.
import fs from 'node:fs';
import path from 'node:path';

const root = path.resolve(import.meta.dirname, '..');
const packagesDir = path.join(root, 'packages');

const manifests = [path.join(root, 'package.json')];
for (const entry of fs.readdirSync(packagesDir, { withFileTypes: true })) {
  const manifest = path.join(packagesDir, entry.name, 'package.json');
  if (entry.isDirectory() && fs.existsSync(manifest)) {
    manifests.push(manifest);
  }
}

/** @type {Map<string, Map<string, string[]>>} */
const dependencyVersions = new Map();

for (const manifest of manifests) {
  const pkg = JSON.parse(fs.readFileSync(manifest, 'utf8'));
  const workspace = pkg.name ?? path.relative(root, manifest);
  for (const type of ['dependencies', 'devDependencies']) {
    for (const [name, range] of Object.entries(pkg[type] ?? {})) {
      if (range.startsWith('workspace:')) {
        continue;
      }
      const versions = dependencyVersions.get(name) ?? new Map();
      dependencyVersions.set(name, versions);
      versions.set(range, [...(versions.get(range) ?? []), workspace]);
    }
  }
}

let errors = 0;
for (const [name, versions] of dependencyVersions) {
  if (versions.size <= 1) {
    continue;
  }
  const rangeList = [...versions.keys()].join(', ');
  for (const workspaces of versions.values()) {
    for (const workspace of workspaces) {
      errors++;
      console.error(
        `${workspace}: Package "${name}" has inconsistent versions across ` +
          `workspaces (${rangeList}). All workspaces must use the same version range.`,
      );
    }
  }
}

if (errors > 0) {
  process.exit(1);
}
