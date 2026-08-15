import { mkdirSync, rmSync } from 'node:fs';
import { spawnSync } from 'node:child_process';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

const root = fileURLToPath(new URL('../', import.meta.url));
const artifactDirectory = join(root, 'artifacts');
rmSync(artifactDirectory, { recursive: true, force: true });
mkdirSync(artifactDirectory, { recursive: true });
const npmCacheDirectory = join(root, '.npm-cache');
mkdirSync(npmCacheDirectory, { recursive: true });

const packageManager = process.execPath;
const npmCli = join(dirname(process.execPath), 'node_modules', 'npm', 'bin', 'npm-cli.js');

function run(args, cwd = root) {
  const result = spawnSync(packageManager, [npmCli, ...args], {
    cwd,
    stdio: 'inherit',
    shell: false,
    env: { ...process.env, npm_config_cache: npmCacheDirectory },
  });
  if (result.error) throw result.error;
  if (result.status !== 0) process.exit(result.status ?? 1);
}

function runNode(args, cwd = root) {
  const result = spawnSync(process.execPath, args, { cwd, stdio: 'inherit', shell: false });
  if (result.error) throw result.error;
  if (result.status !== 0) process.exit(result.status ?? 1);
}

console.log('Building the Web version...');
run(['run', 'build', '--prefix', 'apps/web']);

console.log('Packing the Web version...');
run(['pack', '--pack-destination', '../../artifacts'], join(root, 'apps', 'web'));

console.log('Building the VS Code version...');
const extensionDirectory = join(root, 'apps', 'vscode-extension');
run(['run', 'build'], extensionDirectory);

console.log('Packaging the VS Code extension...');
runNode([
  join(root, 'scripts', 'package-vsix.mjs'),
  '--extension-dir', extensionDirectory,
  '--output', join(artifactDirectory, 'maatgen-0.1.0.vsix'),
], root);

console.log('Frontend build completed. Web package and VSIX: artifacts/');
