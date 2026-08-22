import { mkdirSync, rmSync, cpSync, readFileSync } from 'node:fs';
import { spawnSync } from 'node:child_process';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { execSync } from 'node:child_process';

const root = fileURLToPath(new URL('../', import.meta.url));
const artifactDirectory = join(root, 'artifacts');
const buildCacheDirectory = join(root, '.build-cache');
rmSync(artifactDirectory, { recursive: true, force: true });
mkdirSync(artifactDirectory, { recursive: true });
mkdirSync(buildCacheDirectory, { recursive: true });

const npmCacheDirectory = join(root, '.npm-cache');
mkdirSync(npmCacheDirectory, { recursive: true });

const packageManager = process.execPath;
const npmCli = join(dirname(process.execPath), 'node_modules', 'npm', 'bin', 'npm-cli.js');

const version = JSON.parse(readFileSync(join(root, 'package.json'), 'utf8')).version;

function run(args, cwd = root) {
  const result = spawnSync(packageManager, [npmCli, ...args], {
    cwd,
    stdio: 'inherit',
    shell: false,
    env: { ...process.env, npm_config_cache: npmCacheDirectory },
  });
  if (result.error) {
    console.error(`Error running npm: ${result.error.message}`);
    throw result.error;
  }
  if (result.status !== 0) {
    console.error(`npm command failed with status ${result.status}`);
    process.exit(result.status ?? 1);
  }
}

function runShell(command, cwd = root) {
  const result = spawnSync(command, {
    cwd,
    stdio: 'inherit',
    shell: true,
    env: { ...process.env, PATH: process.env.PATH },
  });
  if (result.error) throw result.error;
  if (result.status !== 0) process.exit(result.status ?? 1);
}

function runNode(args, cwd = root) {
  const result = spawnSync(process.execPath, args, { cwd, stdio: 'inherit', shell: false });
  if (result.error) throw result.error;
  if (result.status !== 0) process.exit(result.status ?? 1);
}

// Platform-specific configuration
const platforms = [
  { name: 'windows', goos: 'windows', goarch: 'amd64', ext: '.exe', distName: 'win32-x64' },
  { name: 'linux', goos: 'linux', goarch: 'amd64', ext: '', distName: 'linux-x64' },
  { name: 'darwin-arm64', goos: 'darwin', goarch: 'arm64', ext: '', distName: 'darwin-arm64' },
];

console.log('=== Building Maatgen Release ===\n');

// Step 1: Build Web UI
console.log('Building the protocol package...');
run(['run', 'build', '--prefix', 'packages/protocol']);

console.log('Building the Web version...');
run(['run', 'build', '--prefix', 'apps/web']);

// Step 2: Build Agent Manager for each platform
console.log('\nBuilding Agent Manager for each platform...');
const agentManagerDir = join(root, 'apps', 'agent-manager');
const binariesDir = join(buildCacheDirectory, 'bin');
mkdirSync(binariesDir, { recursive: true });

for (const platform of platforms) {
  const outputName = `agent-manager${platform.ext}`;
  const outputPath = join(binariesDir, platform.distName, outputName);
  mkdirSync(dirname(outputPath), { recursive: true });

  console.log(`  Building for ${platform.name} (${platform.goos}/${platform.goarch})...`);

  if (process.platform === 'win32' || process.platform === 'darwin' || process.platform === 'linux') {
    const env = {
      ...process.env,
      GOOS: platform.goos,
      GOARCH: platform.goarch,
      CGO_ENABLED: '0',
    };

    const result = spawnSync(
      'go',
      ['build', '-o', outputPath, './cmd/agent-manager'],
      {
        cwd: agentManagerDir,
        stdio: 'inherit',
        env,
      }
    );
    if (result.error) {
      console.error(`Error running go build for ${platform.name}: ${result.error.message}`);
      throw result.error;
    }
    if (result.status !== 0) {
      console.error(`Failed to build for ${platform.name} (status ${result.status})`);
      process.exit(result.status ?? 1);
    }
    console.log(`    ✓ Built ${outputPath}`);
  }
}

// Step 3: Package Web UI with binaries for each platform
console.log('\nPackaging Web UI with Agent Manager...');
const webDistDir = join(root, 'apps', 'web', 'dist');
const configFile = join(agentManagerDir, 'config', 'providers.json');
const deployReadme = join(root, 'docs', 'deploy.md');

for (const platform of platforms) {
  console.log(`  Packaging for ${platform.distName}...`);

  const stageDir = join(buildCacheDirectory, 'stage', platform.distName);
  const webSubDir = join(stageDir, 'web', 'dist');
  const configSubDir = join(stageDir, 'config');

  rmSync(stageDir, { recursive: true, force: true });
  mkdirSync(webSubDir, { recursive: true });
  mkdirSync(configSubDir, { recursive: true });

  try {
    // Copy static files
    cpSync(webDistDir, webSubDir, { recursive: true });

    // Copy binary
    const binaryName = `agent-manager${platform.ext}`;
    const binaryPath = join(binariesDir, platform.distName, binaryName);
    console.log(`    Copying binary from ${binaryPath}...`);
    cpSync(binaryPath, join(stageDir, binaryName));

    // Copy config
    cpSync(configFile, join(configSubDir, 'providers.json'));

    // Create ZIP
    let archiveName = `maatgen-web-${version}-${platform.distName}.zip`;
    let archivePath = join(artifactDirectory, archiveName);

    if (process.platform === 'win32') {
      // Use PowerShell for Windows - pass paths as arguments to avoid escaping issues
      const psCommand = `
        $ProgressPreference = 'SilentlyContinue'
        try {
          Compress-Archive -Path "$ENV:STAGE_DIR\\*" -DestinationPath "$ENV:ARCHIVE_PATH" -Force -ErrorAction Stop
          Write-Host "Archive created successfully"
        } catch {
          Write-Host "ERROR: $_" -ForegroundColor Red
          exit 1
        }
      `.trim();

      const result = spawnSync('powershell', ['-NoProfile', '-Command', psCommand], {
        stdio: 'inherit',
        shell: false,
        env: {
          ...process.env,
          STAGE_DIR: stageDir,
          ARCHIVE_PATH: archivePath,
        },
      });
      if (result.error) throw result.error;
      if (result.status !== 0) {
        throw new Error(`Failed to create ZIP for ${platform.distName}`);
      }
    } else {
      // Use tar for Unix (more reliable than zip command)
      archiveName = `maatgen-web-${version}-${platform.distName}.tar.gz`;
      archivePath = join(artifactDirectory, archiveName);
      const result = spawnSync('tar', ['-czf', archivePath, '-C', buildCacheDirectory, `stage/${platform.distName}`], {
        stdio: 'inherit',
      });
      if (result.error) throw result.error;
      if (result.status !== 0) {
        throw new Error(`Failed to create TAR for ${platform.distName}`);
      }
    }

    console.log(`    ✓ ${archiveName}`);
  } catch (error) {
    console.error(`  Error packaging ${platform.distName}: ${error.message}`);
    process.exit(1);
  }
}

// Step 4: Build VS Code Extension
console.log('\nBuilding the VS Code extension...');
const extensionDirectory = join(root, 'apps', 'vscode-extension');
run(['run', 'build'], extensionDirectory);

console.log('Packaging the VS Code extension...');
runNode([
  join(root, 'scripts', 'package-vsix.mjs'),
  '--extension-dir', extensionDirectory,
  '--output', join(artifactDirectory, `maatgen-${version}.vsix`),
], root);

// Cleanup
rmSync(buildCacheDirectory, { recursive: true, force: true });

console.log('\n=== Release Build Completed ===');
console.log(`\nArtifacts generated in ${artifactDirectory}:`);
console.log(`  - maatgen-web-${version}-win32-x64.zip`);
console.log(`  - maatgen-web-${version}-linux-x64.zip`);
console.log(`  - maatgen-web-${version}-darwin-arm64.zip`);
console.log(`  - maatgen-${version}.vsix`);
console.log('\nFor detailed deployment instructions, see docs/deploy.md');
