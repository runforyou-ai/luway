const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { spawnSync } = require('node:child_process');

test('共享产物支持跨重试合并，并隔离仓库与未完成上传', (t) => {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'ci-artifacts-'));
  t.after(() => fs.rmSync(root, { recursive: true, force: true }));
  const shared = path.join(root, 'shared');
  fs.mkdirSync(shared);
  const run = (repository, attempt, operation, name, inputPath) => spawnSync(process.execPath, [path.join(__dirname, 'index.js')], {
    cwd: root, encoding: 'utf8', env: { ...process.env, CI_ARTIFACTS_DIR: shared,
      GITHUB_REPOSITORY_ID: repository, GITHUB_RUN_ID: '100', GITHUB_RUN_ATTEMPT: attempt,
      GITHUB_OUTPUT: path.join(root, 'output'), GITHUB_STEP_SUMMARY: '',
      INPUT_OPERATION: operation, INPUT_NAME: name, INPUT_PATH: inputPath,
    },
  });
  fs.writeFileSync(path.join(root, 'windows.exe'), 'signed');
  fs.writeFileSync(path.join(root, 'linux.deb'), 'old');
  for (const [name,file] of [['windows-assets','windows.exe'],['linux-assets','linux.deb']]) {
    const result=run('1','1','upload',name,file); assert.equal(result.status,0,result.stderr);
  }
  fs.writeFileSync(path.join(root, 'linux.deb'), 'new');
  assert.equal(run('1','2','upload','linux-assets','linux.deb').status,0);
  fs.mkdirSync(path.join(shared,'1','100','2','incomplete-assets'));
  const result=run('1','2','download','*-assets','download');
  assert.equal(result.status,0,result.stderr);
  assert.equal(fs.readFileSync(path.join(root,'download','windows.exe'),'utf8'),'signed');
  assert.equal(fs.readFileSync(path.join(root,'download','linux.deb'),'utf8'),'new');
  assert.notEqual(run('2','2','download','*-assets','wrong-repository').status,0);
  assert.notEqual(run('1','2','upload','../escape','linux.deb').status,0);
});
