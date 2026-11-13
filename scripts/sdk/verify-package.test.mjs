// verify-package 的测试：真实仓库的静态检查必须通过，tag/产物不一致必须报错。
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { execFileSync, spawnSync } from 'node:child_process';
import { mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

import { REPO_ROOT, parsePyproject, verifyPythonPackage, verifyTypescriptPackage } from './verify-package.mjs';

const HERE = dirname(fileURLToPath(import.meta.url));
const SCRIPT = resolve(HERE, 'verify-package.mjs');
const TS_PKG = JSON.parse(readFileSync(join(REPO_ROOT, 'sdk/typescript/package.json'), 'utf8'));
const PY_VERSION = /version\s*=\s*"([^"]+)"/.exec(readFileSync(join(REPO_ROOT, 'sdk/python/pyproject.toml'), 'utf8'))[1];
const hasPython3 = spawnSync('python3', ['--version'], { encoding: 'utf8' }).status === 0;

function withTempDir(fn) {
  const dir = mkdtempSync(join(tmpdir(), 'dsh-verify-package-'));
  try {
    return fn(dir);
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
}

test('真实仓库：两个 SDK 的静态元数据检查通过', () => {
  const tsReport = verifyTypescriptPackage();
  const pyReport = verifyPythonPackage();
  assert.deepEqual(tsReport.errors, []);
  assert.deepEqual(pyReport.errors, []);
  assert.ok(tsReport.checks.length > 20);
  assert.ok(pyReport.checks.length > 15);
});

test('--tag 与包版本不一致时报错', () => {
  const tsReport = verifyTypescriptPackage({ tag: 'sdk-ts-v9.9.9' });
  assert.ok(tsReport.errors.some((line) => line.includes('tag 版本')));
  const pyReport = verifyPythonPackage({ tag: 'sdk-py-v9.9.9' });
  assert.ok(pyReport.errors.some((line) => line.includes('tag 版本')));
});

test('tag 前缀与包不匹配时报错', () => {
  const report = verifyTypescriptPackage({ tag: 'sdk-py-v0.1.0' });
  assert.ok(report.errors.some((line) => line.includes('sdk-ts-v')));
});

test('parsePyproject 能取到发布检查需要的键（含跨行数组与带空格的键）', () => {
  const values = parsePyproject(readFileSync(join(REPO_ROOT, 'sdk/python/pyproject.toml'), 'utf8'));
  assert.equal(values.get('project.name'), '"dsh-ee-harness"');
  assert.equal(values.get('project.version'), `"${PY_VERSION}"`);
  assert.ok(values.get('build-system.requires').includes('setuptools'));
  assert.ok(values.has('project.urls.Bug Tracker'));
});

function buildTsFixture(dir, { omit = [] } = {}) {
  const tree = join(dir, 'tree', 'package');
  mkdirSync(join(tree, 'dist'), { recursive: true });
  const files = {
    'package.json': JSON.stringify(TS_PKG, null, 2),
    'dist/index.js': 'export const SDK_VERSION = "0.1.0";\n',
    'dist/index.d.ts': 'export declare const SDK_VERSION = "0.1.0";\n',
    'README.md': '# fixture\n',
    'LICENSE': 'Apache License 2.0\n',
    'CHANGELOG.md': '## [Unreleased]\n\n## [0.1.0]\n',
    'COMPATIBILITY.md': '| SDK 版本 | `0.1.0` |\n',
  };
  for (const [name, content] of Object.entries(files)) {
    if (omit.includes(name)) continue;
    writeFileSync(join(tree, name), content);
  }
  const distDir = join(dir, 'dist');
  mkdirSync(distDir, { recursive: true });
  const tarball = join(distDir, `dsh-ee-harness-client-${TS_PKG.version}.tgz`);
  execFileSync('tar', ['-czf', tarball, '-C', join(dir, 'tree'), 'package']);
  return distDir;
}

test('TypeScript：合法 npm tarball 通过产物检查', () => {
  withTempDir((dir) => {
    const distDir = buildTsFixture(dir);
    const report = verifyTypescriptPackage({ root: REPO_ROOT, distDir });
    assert.deepEqual(report.errors, []);
    assert.ok(report.artifacts.some((item) => item.kind === 'npm tarball' && item.entries > 0));
  });
});

test('TypeScript：tarball 缺少 README 或混入 test/ 时报错', () => {
  withTempDir((dir) => {
    const distDir = buildTsFixture(dir, { omit: ['README.md'] });
    const report = verifyTypescriptPackage({ root: REPO_ROOT, distDir });
    assert.ok(report.errors.some((line) => line.includes('tarball 缺少 package/README.md')));
  });
  withTempDir((dir) => {
    const distDir = buildTsFixture(dir);
    const tree = join(dir, 'tree', 'package');
    mkdirSync(join(tree, 'test'), { recursive: true });
    writeFileSync(join(tree, 'test', 'client.test.mjs'), '// test\n');
    execFileSync('tar', ['-czf', join(distDir, `dsh-ee-harness-client-${TS_PKG.version}.tgz`), '-C', join(dir, 'tree'), 'package']);
    const report = verifyTypescriptPackage({ root: REPO_ROOT, distDir });
    assert.ok(report.errors.some((line) => line.includes('不应包含开发文件') || line.includes('不应包含开发配置')));
  });
});

function createWheel(file, entries) {
  const script = [
    'import json, sys, zipfile',
    'path, entries = sys.argv[1], json.loads(sys.argv[2])',
    'with zipfile.ZipFile(path, "w") as archive:',
    '    for name, content in entries.items():',
    '        archive.writestr(name, content)',
  ].join('\n');
  execFileSync('python3', ['-c', script, file, JSON.stringify(entries)]);
}

function buildPyFixture(dir, { omitClient = false } = {}) {
  const distDir = join(dir, 'dist');
  mkdirSync(distDir, { recursive: true });
  const distInfo = `dsh_ee_harness-${PY_VERSION}.dist-info`;
  const entries = {
    'dsh_ee_harness/__init__.py': `__version__ = "${PY_VERSION}"\n`,
    'dsh_ee_harness/client.py': 'DEFAULT_MCP_CLIENT_INFO = {"name": "fixture", "version": "0.1.0"}\n',
    [`${distInfo}/METADATA`]: [
      'Metadata-Version: 2.1',
      'Name: dsh-ee-harness',
      `Version: ${PY_VERSION}`,
      'Summary: fixture',
      'License: Apache-2.0',
      'Requires-Python: >=3.9',
      '',
    ].join('\n'),
    [`${distInfo}/WHEEL`]: 'Wheel-Version: 1.0\n',
  };
  if (omitClient) delete entries['dsh_ee_harness/client.py'];
  createWheel(join(distDir, `dsh_ee_harness-${PY_VERSION}-py3-none-any.whl`), entries);

  const sdistRoot = join(dir, 'sdist-tree', `dsh_ee_harness-${PY_VERSION}`);
  mkdirSync(join(sdistRoot, 'dsh_ee_harness'), { recursive: true });
  writeFileSync(join(sdistRoot, 'pyproject.toml'), readFileSync(join(REPO_ROOT, 'sdk/python/pyproject.toml')));
  writeFileSync(join(sdistRoot, 'README.md'), '# fixture\n');
  writeFileSync(join(sdistRoot, 'PKG-INFO'), `Metadata-Version: 2.1\nName: dsh-ee-harness\nVersion: ${PY_VERSION}\n\n`);
  writeFileSync(join(sdistRoot, 'dsh_ee_harness', '__init__.py'), `__version__ = "${PY_VERSION}"\n`);
  writeFileSync(join(sdistRoot, 'dsh_ee_harness', 'client.py'), 'x = 1\n');
  execFileSync('tar', ['-czf', join(distDir, `dsh_ee_harness-${PY_VERSION}.tar.gz`), '-C', join(dir, 'sdist-tree'), `dsh_ee_harness-${PY_VERSION}`]);
  return distDir;
}

test('Python：合法 wheel + sdist 通过产物检查', { skip: !hasPython3 && '需要系统 python3' }, () => {
  withTempDir((dir) => {
    const distDir = buildPyFixture(dir);
    const report = verifyPythonPackage({ root: REPO_ROOT, distDir });
    assert.deepEqual(report.errors, []);
    assert.equal(report.artifacts.length, 2);
  });
});

test('Python：wheel 缺模块文件时报错', { skip: !hasPython3 && '需要系统 python3' }, () => {
  withTempDir((dir) => {
    const distDir = buildPyFixture(dir, { omitClient: true });
    const report = verifyPythonPackage({ root: REPO_ROOT, distDir });
    assert.ok(report.errors.some((line) => line.includes('wheel 缺少 dsh_ee_harness/client.py')));
  });
});

test('CLI：真实仓库检查退出码 0；非法参数退出码 2', () => {
  const ok = spawnSync(process.execPath, [SCRIPT, '--package', 'typescript'], { encoding: 'utf8', cwd: REPO_ROOT });
  assert.equal(ok.status, 0, ok.stderr);
  assert.match(ok.stdout, /verify-package: typescript OK/);

  const bad = spawnSync(process.execPath, [SCRIPT, '--package', 'wheel'], { encoding: 'utf8', cwd: REPO_ROOT });
  assert.equal(bad.status, 2);

  const help = spawnSync(process.execPath, [SCRIPT, '--help'], { encoding: 'utf8', cwd: REPO_ROOT });
  assert.equal(help.status, 0);
  assert.match(help.stdout, /--package/);
});
