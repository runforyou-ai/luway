import { createHash } from 'node:crypto';
import { execFileSync } from 'node:child_process';
import { existsSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';

const directory = resolve(process.argv[2] ?? 'frontend');
const checkOnly = process.argv.includes('--check');
const modules = join(directory, 'node_modules');
const marker = join(modules, '.ci-dependencies.json');
const installedLock = join(modules, '.package-lock.json');
const npm = (args, options = {}) => process.platform === 'win32'
  ? execFileSync(process.execPath, [join(dirname(process.execPath), 'node_modules/npm/bin/npm-cli.js'), ...args], { cwd: directory, ...options })
  : execFileSync('npm', args, { cwd: directory, ...options });
const digest = (value) => createHash('sha256').update(value).digest('hex');
const config = JSON.parse(npm(['config', 'list', '--json'], { encoding: 'utf8' }));

// 完整工具版本、安装配置和锁文件共同决定复用条件；不跨平台共享 node_modules。
const hash = createHash('sha256').update(JSON.stringify({
  schema: 1, node: process.version, npm: npm(['--version'], { encoding: 'utf8' }).trim(),
  platform: process.platform, arch: process.arch,
  nodeEnv: process.env.NODE_ENV ?? '',
  installConfig: Object.fromEntries(['omit', 'include', 'ignore-scripts', 'legacy-peer-deps', 'install-links', 'install-strategy'].map((name) => [name, config[name]])),
}));
for (const name of ['package.json', 'package-lock.json', '.npmrc', '../.npmrc']) {
  const file = join(directory, name);
  hash.update(name).update('\0');
  if (existsSync(file)) hash.update(readFileSync(file));
  else if (name === 'package.json' || name === 'package-lock.json') throw new Error(`缺少 ${file}`);
  hash.update('\0');
}
const key = hash.digest('hex');
let hit = false;
try {
  const saved = JSON.parse(readFileSync(marker, 'utf8'));
  hit = saved.key === key && saved.installedLock === digest(readFileSync(installedLock));
} catch {
  // 首次安装、中断的安装或手工变更均重新安装。
}
if (hit) {
  console.log(`复用前端依赖：${key.slice(0, 12)}`);
} else if (checkOnly) {
  console.error('前端依赖缓存失效，请先运行 CI 依赖准备步骤。');
  process.exitCode = 1;
} else {
  rmSync(marker, { force: true });
  console.log(`安装前端依赖：${key.slice(0, 12)}`);
  npm(['ci', '--prefer-offline', '--no-audit', '--no-fund'], { stdio: 'inherit' });
  writeFileSync(marker, JSON.stringify({ key, installedLock: digest(readFileSync(installedLock)) }));
}
