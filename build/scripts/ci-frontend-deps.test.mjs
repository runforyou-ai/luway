import assert from 'node:assert/strict';
import { execFileSync, spawnSync } from 'node:child_process';
import { existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import test from 'node:test';

const script = fileURLToPath(new URL('./ci-frontend-deps.mjs', import.meta.url));

test('依赖复用、失效与安装失败恢复', () => {
  const directory = mkdtempSync(join(tmpdir(), 'ci-deps-check-'));
  const run = (...args) => spawnSync(process.execPath, [script, directory, ...args], { encoding: 'utf8' });
  try {
    mkdirSync(join(directory, 'dep'));
    writeFileSync(join(directory, 'dep/package.json'), JSON.stringify({ name: 'cache-test-dependency', version: '1.0.0' }));
    const manifest = { name: 'cache-test', version: '1.0.0', dependencies: { 'cache-test-dependency': 'file:dep' } };
    writeFileSync(join(directory, 'package.json'), JSON.stringify(manifest));
    const args = ['install', '--package-lock-only', '--ignore-scripts', '--no-audit', '--no-fund'];
    if (process.platform === 'win32') {
      execFileSync(process.execPath, [join(dirname(process.execPath), 'node_modules/npm/bin/npm-cli.js'), ...args], { cwd: directory, stdio: 'pipe' });
    } else {
      execFileSync('npm', args, { cwd: directory, stdio: 'pipe' });
    }
    assert.equal(run('--check').status, 1);
    let result = run();
    assert.equal(result.status, 0, result.stderr);
    assert.match(result.stdout, /安装前端依赖/);
    assert.equal(run('--check').status, 0);
    result = run();
    assert.equal(result.status, 0, result.stderr);
    assert.match(result.stdout, /复用前端依赖/);

    const lock = join(directory, 'package-lock.json');
    const originalLock = readFileSync(lock, 'utf8');
    writeFileSync(lock, `${originalLock}\n`);
    assert.equal(run('--check').status, 1, '锁文件变化必须失效');
    writeFileSync(lock, originalLock);
    writeFileSync(join(directory, '.npmrc'), 'ignore-scripts=true\n');
    assert.equal(run('--check').status, 1, '安装配置变化必须失效');
    rmSync(join(directory, '.npmrc'));
    writeFileSync(join(directory, 'node_modules/.package-lock.json'), '{}');
    assert.equal(run('--check').status, 1, '安装树被改动必须失效');

    manifest.dependencies['missing-local-dependency'] = 'file:missing';
    writeFileSync(join(directory, 'package.json'), JSON.stringify(manifest));
    assert.notEqual(run().status, 0, '安装失败必须向 CI 报错');
    assert.equal(existsSync(join(directory, 'node_modules/.ci-dependencies.json')), false, '失败后不能保留成功标记');
  } finally {
    rmSync(directory, { recursive: true, force: true });
  }
});
