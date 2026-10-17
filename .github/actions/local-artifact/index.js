const fs = require('node:fs');
const path = require('node:path');
const { pipeline } = require('node:stream/promises');

// main 按仓库、运行与重试编号隔离本机产物，跨重试读取同一次运行最近成功上传的文件。
async function main() {
  const input = (name) => (process.env[`INPUT_${name.toUpperCase()}`] || '').trim();
  const shared = process.env.CI_ARTIFACTS_DIR;
  if (!shared || !path.isAbsolute(shared) || !fs.statSync(shared, { throwIfNoEntry: false })?.isDirectory()) {
    throw new Error('CI_ARTIFACTS_DIR must point to the mounted shared artifact directory');
  }
  for (const key of ['GITHUB_REPOSITORY_ID', 'GITHUB_RUN_ID', 'GITHUB_RUN_ATTEMPT']) {
    if (!/^\d+$/.test(process.env[key] || '')) throw new Error(`Missing ${key}`);
  }
  const root = path.join(shared, process.env.GITHUB_REPOSITORY_ID, process.env.GITHUB_RUN_ID);
  const attempt = Number(process.env.GITHUB_RUN_ATTEMPT);
  let destination;
  if (input('operation') === 'upload') {
    const name = input('name');
    if (!/^[\w.-]+$/.test(name) || name === '.' || name === '..') throw new Error('Invalid artifact name');
    destination = path.join(root, String(attempt), name);
    // 同一任务的产物为扁平文件；仅在全部文件复制完成后登记完成标记。
    const files = input('path').split(/\r?\n/).filter(Boolean).flatMap((pattern) =>
      [...fs.globSync(pattern.replaceAll('\\', '/'))].filter((file) => fs.statSync(file).isFile()));
    if (!files.length) throw new Error('No artifact files found');
    const names = files.map((file) => path.basename(file));
    if (new Set(names).size !== names.length || names.includes('.complete')) throw new Error('Duplicate artifact filenames');
    fs.mkdirSync(destination, { recursive: true });
    const marker = path.join(destination, '.complete');
    fs.rmSync(marker, { force: true });
    for (const file of files) await pipeline(fs.createReadStream(file), fs.createWriteStream(path.join(destination, path.basename(file))));
    fs.writeFileSync(marker, JSON.stringify(names));
    console.log(`Saved ${files.length} files to ${destination}`);
  } else if (input('operation') === 'download') {
    const pattern = input('name') || input('pattern');
    if (!pattern) throw new Error('Artifact name or pattern is required');
    const matches = new RegExp(`^${pattern.split('*').map((part) => part.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')).join('.*')}$`);
    const artifacts = new Map();
    // 重跑失败任务时沿用本次运行先前成功任务的产物。
    for (let version = attempt; version >= 1; version--) {
      const folder = path.join(root, String(version));
      if (!fs.existsSync(folder)) continue;
      for (const name of fs.readdirSync(folder)) {
        const source = path.join(folder, name);
        if (matches.test(name) && !artifacts.has(name) && fs.existsSync(path.join(source, '.complete'))) artifacts.set(name, source);
      }
    }
    if (!artifacts.size) throw new Error(`No local artifacts match ${pattern}`);
    destination = path.resolve(input('path'));
    fs.mkdirSync(destination, { recursive: true });
    const copied = new Set();
    for (const source of artifacts.values()) {
      for (const file of JSON.parse(fs.readFileSync(path.join(source, '.complete'), 'utf8'))) {
        if (path.basename(file) !== file || copied.has(file)) throw new Error(`Invalid or duplicate artifact file: ${file}`);
        copied.add(file);
        await pipeline(fs.createReadStream(path.join(source, file)), fs.createWriteStream(path.join(destination, file)));
      }
    }
    console.log(`Downloaded ${copied.size} files to ${destination}`);
  } else {
    throw new Error('operation must be upload or download');
  }
  fs.appendFileSync(process.env.GITHUB_OUTPUT, `artifact-path=${destination}\n`);
  if (process.env.GITHUB_STEP_SUMMARY) fs.appendFileSync(process.env.GITHUB_STEP_SUMMARY, `本机产物目录：\`${destination}\`\n`);
}

main().catch((error) => {
  console.error(error.message);
  process.exitCode = 1;
});
