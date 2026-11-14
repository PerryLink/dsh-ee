#!/usr/bin/env node
// verify-package：SDK 发布前的包检查（元数据 / 文件清单 / tag 与版本一致性）。
//
// 用法：
//   node scripts/sdk/verify-package.mjs --package typescript
//   node scripts/sdk/verify-package.mjs --package typescript --dist dist/sdk-ts --tag sdk-ts-v0.1.0
//   node scripts/sdk/verify-package.mjs --package python --dist dist/sdk-python --tag sdk-py-v0.1.0
//
// 做两层检查：
//   1) 静态元数据（任何环境都能跑，preflight 用）：package.json / pyproject.toml /
//      版本在代码、CHANGELOG、COMPATIBILITY 里是否一致、tag 前缀是否匹配；
//   2) 产物检查（需要先构建，`--dist` 用）：npm tarball 的文件清单、wheel/sdist 的文件
//      清单与 METADATA/PKG-INFO 元数据，确认没有把 src/test/node_modules 发出去。
//
// 只依赖 Node 内置模块；读 tarball/sdist 用系统 `tar`，读 wheel 用系统 `python3`
// 标准库（解 zip + 解析 METADATA）。失败时退出码 1，用法/环境错误退出码 2。

import { createHash } from 'node:crypto';
import { execFileSync } from 'node:child_process';
import { existsSync, readFileSync, readdirSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';

const HERE = dirname(fileURLToPath(import.meta.url));
export const REPO_ROOT = resolve(HERE, '..', '..');

const TS_DIR = 'sdk/typescript';
const TS_NAME = '@dsh-ee/harness-client';
const TS_TARBALL_BASENAME = 'dsh-ee-harness-client';
const TS_TAG_PREFIX = 'sdk-ts-v';

const PY_DIR = 'sdk/python';
const PY_NAME = 'dsh-ee-harness';
const PY_MODULE = 'dsh_ee_harness';
const PY_TAG_PREFIX = 'sdk-py-v';

const SEMVER = /^(?:0|[1-9]\d*)\.(?:0|[1-9]\d*)\.(?:0|[1-9]\d*)(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$/;
const PEP440 = /^(?:0|[1-9]\d*)(?:\.(?:0|[1-9]\d*))*(?:(?:a|b|rc)\d+)?(?:\.post\d+)?(?:\.dev\d+)?$/i;

const TS_REQUIRED_TARBALL = [
  'package/package.json',
  'package/dist/index.js',
  'package/dist/index.d.ts',
  'package/README.md',
  'package/LICENSE',
  'package/CHANGELOG.md',
  'package/COMPATIBILITY.md',
];
const TS_REQUIRED_REPO = ['package.json', 'README.md', 'LICENSE', 'CHANGELOG.md', 'COMPATIBILITY.md', 'tsconfig.json', 'src/index.ts'];
const TS_ALLOWED_FILES = ['dist', 'README.md', 'LICENSE', 'CHANGELOG.md', 'COMPATIBILITY.md'];

class VerifyError extends Error {
  constructor(message) {
    super(message);
    this.name = 'VerifyError';
  }
}

function newReport(kind) {
  return { kind, checks: [], warnings: [], errors: [], artifacts: [] };
}

function check(report, condition, okMessage, errorMessage) {
  if (condition) {
    report.checks.push(okMessage);
  } else {
    report.errors.push(errorMessage);
  }
  return Boolean(condition);
}

function warn(report, message) {
  report.warnings.push(message);
}

function readJsonFile(file, label) {
  try {
    return JSON.parse(readFileSync(file, 'utf8'));
  } catch (error) {
    throw new VerifyError(`无法解析 ${label}（${file}）：${error.message}`);
  }
}

function sha256(file) {
  return createHash('sha256').update(readFileSync(file)).digest('hex');
}

function normalizeLooseVersion(version) {
  return String(version)
    .trim()
    .toLowerCase()
    .replace(/^v/, '')
    .replace(/[-_]/g, '');
}

function expectDocs(report, dir, files, label) {
  for (const file of files) {
    check(report, existsSync(join(dir, file)), `${label} 存在 ${file}`, `${label} 缺少 ${file}`);
  }
}

// ---------- 版本 / tag 一致性 ----------

function checkVersionConsistency(report, { packageVersion, tagPrefix, label }) {
  if (!tagPrefix) return;
  const tag = report.options.tag ?? '';
  if (tag === '') return;
  if (!tag.startsWith(tagPrefix)) {
    report.errors.push(`${label} 的 tag 应以 ${tagPrefix} 开头，收到：${tag}`);
    return;
  }
  const tagVersion = tag.slice(tagPrefix.length);
  report.checks.push(`${label} tag 前缀正确（${tagPrefix}）`);
  check(
    report,
    tagVersion === packageVersion,
    `${label} tag 版本与包版本一致：${packageVersion}`,
    `${label} tag 版本 ${tagVersion} 与包版本 ${packageVersion} 不一致（先改版本再打 tag）`,
  );
  if (report.options.expectedVersion && report.options.expectedVersion !== packageVersion) {
    report.errors.push(`${label} --expected-version ${report.options.expectedVersion} 与包版本 ${packageVersion} 不一致`);
  }
}

function checkChangelogAndCompatibility(report, { dir, version, label }) {
  const changelogFile = join(dir, 'CHANGELOG.md');
  const compatibilityFile = join(dir, 'COMPATIBILITY.md');
  if (!existsSync(changelogFile) || !existsSync(compatibilityFile)) {
    report.errors.push(`${label} 缺少 CHANGELOG.md 或 COMPATIBILITY.md，无法校验版本记录`);
    return;
  }
  const changelog = readFileSync(changelogFile, 'utf8');
  const compatibility = readFileSync(compatibilityFile, 'utf8');
  check(
    report,
    changelog.includes(`## [${version}]`),
    `${label} CHANGELOG.md 有 ${version} 的版本小节`,
    `${label} CHANGELOG.md 缺少 ## [${version}]（发布前把 Unreleased 改成该版本）`,
  );
  check(
    report,
    compatibility.includes(version),
    `${label} COMPATIBILITY.md 已标注 SDK 版本 ${version}`,
    `${label} COMPATIBILITY.md 未出现版本 ${version}（兼容矩阵会与包版本脱节）`,
  );
}

// ---------- TypeScript ----------

export function verifyTypescriptPackage(options = {}) {
  const root = options.root ?? REPO_ROOT;
  const dir = join(root, TS_DIR);
  const report = newReport('typescript');
  report.options = options;

  const pkgFile = join(dir, 'package.json');
  if (!existsSync(pkgFile)) {
    report.errors.push(`找不到 ${pkgFile}`);
    return report;
  }
  let pkg;
  try {
    pkg = readJsonFile(pkgFile, 'sdk/typescript/package.json');
  } catch (error) {
    report.errors.push(error.message);
    return report;
  }

  check(report, typeof pkg.name === 'string' && pkg.name === TS_NAME, `包名 ${TS_NAME}`, `包名应为 ${TS_NAME}，实际 ${pkg.name}`);
  check(report, typeof pkg.version === 'string' && SEMVER.test(pkg.version), `版本 ${pkg.version} 是合法 semver`, `版本不是合法 semver：${pkg.version}`);
  check(report, pkg.license === 'Apache-2.0', 'license 为 Apache-2.0', `license 应为 Apache-2.0，实际 ${pkg.license}`);
  check(report, pkg.private !== true, '未标记 private', 'package.json 标记了 private，不能发布');
  check(report, pkg.type === 'module', '是 ESM（type=module）', '应为 ESM 包（type=module）');
  check(report, pkg.publishConfig?.access === 'public', 'publishConfig.access=public', '缺少 publishConfig.access=public（scoped 包需要显式 public）');
  check(report, typeof pkg.description === 'string' && pkg.description.length > 0, '有 description', '缺少 description');
  check(report, Array.isArray(pkg.keywords) && pkg.keywords.length > 0, '有 keywords', '缺少 keywords');
  check(report, pkg.engines?.node === '>=18', 'engines.node =>=18', `engines.node 应为 >=18，实际 ${pkg.engines?.node}`);
  check(report, pkg.repository?.directory === TS_DIR, `repository.directory=${TS_DIR}`, 'repository.directory 应为 sdk/typescript');
  check(report, typeof pkg.repository?.url === 'string' && pkg.repository.url.includes('dsh-ee'), 'repository.url 指向 dsh-ee', 'repository.url 缺少 dsh-ee');
  check(report, typeof pkg.bugs?.url === 'string' && pkg.bugs.url.includes('dsh-ee'), '有 bugs.url', '缺少 bugs.url');
  check(report, pkg.main === './dist/index.js' && pkg.types === './dist/index.d.ts', 'main/types 指向 dist', 'main/types 应指向 dist/index.js 与 dist/index.d.ts');
  check(report, pkg.exports?.['.']?.import === './dist/index.js', 'exports.import 指向 dist/index.js', 'exports["."].import 应指向 dist/index.js');
  check(report, pkg.exports?.['.']?.types === './dist/index.d.ts', 'exports.types 指向 dist/index.d.ts', 'exports["."].types 应指向 dist/index.d.ts');
  check(report, typeof pkg.scripts?.build === 'string', '有 build 脚本', '缺少 build 脚本');
  check(report, typeof pkg.scripts?.prepack === 'string' && pkg.scripts.prepack.includes('build'), 'prepack 会先构建 dist', 'prepack 必须先构建 dist，避免发布空包');

  const filesList = Array.isArray(pkg.files) ? pkg.files : [];
  for (const entry of TS_ALLOWED_FILES) {
    check(report, filesList.includes(entry), `files 白名单包含 ${entry}`, `files 白名单缺少 ${entry}`);
  }

  const rawPkg = readFileSync(pkgFile, 'utf8');
  check(
    report,
    !/npm_[A-Za-z0-9]{20,}|_authToken|pypi-[A-Za-z0-9]{10,}/.test(rawPkg),
    'package.json 没有硬编码 npm token',
    'package.json 疑似硬编码了 npm token / registry 凭据',
  );

  expectDocs(report, dir, TS_REQUIRED_REPO, 'sdk/typescript');

  const tsconfigFile = join(dir, 'tsconfig.json');
  if (existsSync(tsconfigFile)) {
    try {
      const tsconfig = readJsonFile(tsconfigFile, 'sdk/typescript/tsconfig.json');
      check(report, tsconfig.compilerOptions?.outDir === 'dist', 'tsconfig outDir=dist', 'tsconfig.compilerOptions.outDir 应为 dist');
      check(report, tsconfig.compilerOptions?.declaration === true, 'tsconfig 生成 .d.ts', 'tsconfig 需要 declaration: true');
    } catch (error) {
      report.errors.push(error.message);
    }
  }

  const version = typeof pkg.version === 'string' ? pkg.version : '<unknown>';
  const indexFile = join(dir, 'src/index.ts');
  if (existsSync(indexFile)) {
    const index = readFileSync(indexFile, 'utf8');
    const match = /export const SDK_VERSION = '([^']+)'/.exec(index);
    check(report, Boolean(match), 'src/index.ts 导出了 SDK_VERSION', 'src/index.ts 缺少 SDK_VERSION 导出');
    if (match) {
      check(report, match[1] === version, `SDK_VERSION=${version} 与 package.json 一致`, `SDK_VERSION=${match[1]} 与 package.json 版本 ${version} 不一致`);
    }
  }
  const mcpFile = join(dir, 'src/mcp.ts');
  if (existsSync(mcpFile)) {
    const mcp = readFileSync(mcpFile, 'utf8');
    const match = /DEFAULT_CLIENT_INFO[^}]*version:\s*'([^']+)'/s.exec(mcp);
    check(report, Boolean(match), 'src/mcp.ts 有默认 client info 版本', 'src/mcp.ts 缺少 DEFAULT_CLIENT_INFO.version');
    if (match) {
      check(report, match[1] === version, `MCP client info 版本=${version}`, `src/mcp.ts 的 DEFAULT_CLIENT_INFO.version=${match[1]} 与 package.json 版本 ${version} 不一致`);
    }
  }
  const testFile = join(dir, 'test/client.test.mjs');
  if (existsSync(testFile)) {
    const test = readFileSync(testFile, 'utf8');
    check(
      report,
      test.includes(`SDK_VERSION 是 ${version}`),
      `测试中的 SDK_VERSION 版本号=${version}`,
      `test/client.test.mjs 仍断言旧版本号（应改为 ${version}）`,
    );
  }

  checkVersionConsistency(report, { packageVersion: version, tagPrefix: TS_TAG_PREFIX, label: 'TypeScript SDK' });
  checkChangelogAndCompatibility(report, { dir, version, label: 'TypeScript SDK' });

  if (options.distDir) verifyTypescriptTarball(report, { dir, distDir: resolve(options.distDir ?? ''), version });
  return report;
}

function findSingle(distDir, predicate, pattern, label) {
  if (!existsSync(distDir)) {
    throw new VerifyError(`--dist 目录不存在：${distDir}`);
  }
  const matches = readdirSync(distDir)
    .filter((name) => predicate(name))
    .sort();
  if (matches.length !== 1) {
    throw new VerifyError(`--dist ${distDir} 里应恰好有 1 个 ${label}（${pattern}），实际 ${matches.length} 个：${matches.join(', ') || '无'}`);
  }
  return join(distDir, matches[0]);
}

function tarList(file) {
  try {
    return execFileSync('tar', ['-tzf', file], { encoding: 'utf8' })
      .split('\n')
      .filter((line) => line !== '');
  } catch (error) {
    throw new VerifyError(`无法读取 tar 包 ${file}（需要系统 tar）：${error.message}`);
  }
}

function tarRead(file, entry) {
  try {
    return execFileSync('tar', ['-xOzf', file, entry], { encoding: 'utf8' });
  } catch (error) {
    throw new VerifyError(`无法从 ${file} 读取 ${entry}：${error.message}`);
  }
}

function verifyTypescriptTarball(report, { distDir, version }) {
  let tarball;
  try {
    tarball = findSingle(distDir, (name) => name.endsWith('.tgz'), '*.tgz', 'npm tarball');
  } catch (error) {
    report.errors.push(error.message);
    return;
  }
  const basename = tarball.split(/[\\/]/).pop();
  check(
    report,
    basename === `${TS_TARBALL_BASENAME}-${version}.tgz`,
    `tarball 文件名 ${basename} 符合 ${TS_TARBALL_BASENAME}-<version>.tgz`,
    `tarball 文件名 ${basename} 与包名/版本不符（期望 ${TS_TARBALL_BASENAME}-${version}.tgz）`,
  );

  let entries;
  try {
    entries = tarList(tarball);
  } catch (error) {
    report.errors.push(error.message);
    return;
  }
  const fileSet = new Set(entries);
  for (const required of TS_REQUIRED_TARBALL) {
    check(report, fileSet.has(required), `tarball 包含 ${required}`, `tarball 缺少 ${required}`);
  }
  for (const entry of entries) {
    if (/^package\/(?:src|test|node_modules)\//.test(entry)) {
      report.errors.push(`tarball 不应包含开发文件：${entry}`);
    }
    if (/^package\/tsconfig\.json$/.test(entry) || /^package\/package-lock\.json$/.test(entry)) {
      report.errors.push(`tarball 不应包含开发配置：${entry}`);
    }
    if (/^package\/.+\.ts$/.test(entry) && !/\.d\.ts$/.test(entry)) {
      report.errors.push(`tarball 不应包含未编译的 TypeScript：${entry}`);
    }
  }
  check(report, entries.every((entry) => entry.startsWith('package/')), 'tarball 顶层都是 package/', 'tarball 出现了 package/ 之外的文件');

  let tarballPkg;
  try {
    tarballPkg = JSON.parse(tarRead(tarball, 'package/package.json'));
  } catch (error) {
    report.errors.push(error.message);
    return;
  }
  check(report, tarballPkg.name === TS_NAME, `tarball package.json name=${TS_NAME}`, `tarball package.json name 应为 ${TS_NAME}`);
  check(
    report,
    tarballPkg.version === version,
    `tarball package.json version=${version}`,
    `tarball package.json version=${tarballPkg.version} 与源码版本 ${version} 不一致`,
  );
  check(report, tarballPkg.license === 'Apache-2.0', 'tarball license=Apache-2.0', 'tarball package.json license 不是 Apache-2.0');
  check(report, tarballPkg.publishConfig?.access === 'public', 'tarball publishConfig.access=public', 'tarball package.json 缺少 publishConfig.access=public');
  check(report, Array.isArray(tarballPkg.files), 'tarball package.json 保留 files 白名单', 'tarball package.json 缺少 files 字段');

  const bytes = readFileSync(tarball).length;
  if (bytes > 1024 * 1024) {
    warn(report, `npm tarball 大小为 ${(bytes / 1024).toFixed(1)} KiB，超过 1 MiB，请确认没有误打包大文件`);
  }
  report.artifacts.push({ file: basename, kind: 'npm tarball', bytes, sha256: sha256(tarball), entries: entries.length });
  report.checks.push(`tarball 文件清单 ${entries.length} 项，大小 ${(bytes / 1024).toFixed(1)} KiB`);
}

// ---------- Python ----------

function stripTomlComment(line) {
  let quoted = null;
  for (let i = 0; i < line.length; i += 1) {
    const ch = line[i];
    if (quoted) {
      if (ch === '\\' && quoted === '"') i += 1;
      else if (ch === quoted) quoted = null;
    } else if (ch === '"' || ch === "'") {
      quoted = ch;
    } else if (ch === '#') {
      return line.slice(0, i);
    }
  }
  return line;
}

// 只解析 pyproject.toml 里发布检查需要的小子集：分节、`key = value`、跨行数组、
// 引号字符串与 inline table。复杂 TOML（嵌套 table、多行字符串）不支持。
export function parsePyproject(text) {
  const values = new Map();
  let section = '';
  const lines = String(text).split(/\r?\n/);
  for (let index = 0; index < lines.length; index += 1) {
    const line = stripTomlComment(lines[index]).trim();
    if (line === '') continue;
    const sectionMatch = /^\[\[?([^\]]+)\]\]?$/.exec(line);
    if (sectionMatch) {
      section = sectionMatch[1].trim();
      continue;
    }
    const entry = /^("(?:[^"\\]|\\.)*"|'(?:[^']*)'|[A-Za-z0-9_.-]+)\s*=\s*(.*)$/.exec(line);
    if (!entry) continue;
    let key = entry[1];
    if ((key.startsWith('"') && key.endsWith('"')) || (key.startsWith("'") && key.endsWith("'"))) {
      key = key.slice(1, -1);
    }
    let value = entry[2].trim();
    if (value.startsWith('[') && !value.includes(']')) {
      while (index + 1 < lines.length) {
        index += 1;
        value += `\n${stripTomlComment(lines[index]).trim()}`;
        if (value.includes(']')) break;
      }
    }
    values.set(section === '' ? key : `${section}.${key}`, value);
  }
  return values;
}

function tomlString(raw) {
  const text = String(raw ?? '').trim();
  const match = /^(["'])([\s\S]*)\1$/.exec(text);
  if (!match) return null;
  const quote = match[1];
  const body = match[2];
  if (quote === "'") return body;
  return body.replaceAll('\\\\', '\\').replaceAll('\\"', '"').replaceAll('\\n', '\n');
}

function tomlStringArray(raw) {
  const values = [];
  const pattern = /("(?:[^"\\]|\\.)*"|'(?:[^']*)')/g;
  let match;
  while ((match = pattern.exec(String(raw ?? ''))) !== null) {
    const value = tomlString(match[1]);
    if (value !== null) values.push(value);
  }
  return values;
}

function tomlStringMap(raw) {
  const result = {};
  const pattern = /([A-Za-z0-9_-]+)\s*=\s*("(?:[^"\\]|\\.)*"|'(?:[^']*)')/g;
  let match;
  while ((match = pattern.exec(String(raw ?? ''))) !== null) {
    result[match[1]] = tomlString(match[2]);
  }
  return result;
}

function findPython(distDir) {
  const files = readdirSync(distDir).sort();
  return {
    wheel: files.find((name) => name.endsWith('.whl')) ?? '',
    sdist: files.find((name) => name.endsWith('.tar.gz')) ?? '',
  };
}

function inspectWheel(wheelPath) {
  const script = [
    'import json, sys, zipfile, email.parser, email.policy',
    'path = sys.argv[1]',
    'zip_file = zipfile.ZipFile(path)',
    'names = zip_file.namelist()',
    'metadata_names = [name for name in names if name.endswith(".dist-info/METADATA")]',
    'if len(metadata_names) != 1:',
    '    json.dump({"error": f"expected one METADATA, got {len(metadata_names)}"}, sys.stdout)',
    '    sys.exit(0)',
    'raw = zip_file.read(metadata_names[0]).decode("utf-8", "replace")',
    'message = email.parser.Parser(policy=email.policy.compat32).parsestr(raw)',
    'keys = ("Name", "Version", "Requires-Python", "License", "Summary")',
    'json.dump({"names": names, "metadata": {key: message.get(key) for key in keys}}, sys.stdout)',
  ].join('\n');
  let output;
  try {
    output = execFileSync('python3', ['-c', script, wheelPath], { encoding: 'utf8' });
  } catch (error) {
    throw new VerifyError(`无法用 python3 读取 wheel（需要系统 Python 3）：${error.message}`);
  }
  try {
    return JSON.parse(output);
  } catch (error) {
    throw new VerifyError(`python3 wheel 检查输出不是 JSON：${error.message}`);
  }
}

export function verifyPythonPackage(options = {}) {
  const root = options.root ?? REPO_ROOT;
  const dir = join(root, PY_DIR);
  const report = newReport('python');
  report.options = options;

  const pyprojectFile = join(dir, 'pyproject.toml');
  if (!existsSync(pyprojectFile)) {
    report.errors.push(`找不到 ${pyprojectFile}`);
    return report;
  }
  const toml = parsePyproject(readFileSync(pyprojectFile, 'utf8'));

  const name = tomlString(toml.get('project.name'));
  const version = tomlString(toml.get('project.version')) ?? '<unknown>';
  check(report, name === PY_NAME, `包名 ${PY_NAME}`, `project.name 应为 ${PY_NAME}，实际 ${name}`);
  check(report, PEP440.test(version), `版本 ${version} 是合法 PEP 440 版本`, `project.version 不是合法 PEP 440 版本：${version}`);
  check(report, tomlString(toml.get('build-system.build-backend')) === 'setuptools.build_meta', 'build-backend=setuptools.build_meta', 'build-system.build-backend 应为 setuptools.build_meta');
  const buildRequires = tomlStringArray(toml.get('build-system.requires'));
  check(report, buildRequires.some((item) => item.startsWith('setuptools')), 'build-system.requires 包含 setuptools', 'build-system.requires 缺少 setuptools');

  const requiresPython = tomlString(toml.get('project.requires-python')) ?? '';
  const pythonFloor = /^>=\s*(\d+)\.(\d+)$/.exec(requiresPython);
  check(
    report,
    Boolean(pythonFloor) && (Number(pythonFloor[1]) > 3 || (Number(pythonFloor[1]) === 3 && Number(pythonFloor[2]) >= 9)),
    `requires-python=${requiresPython}（>=3.9）`,
    `project.requires-python 应声明 >=3.9，实际 ${requiresPython || '<empty>'}`,
  );

  const licenseRaw = String(toml.get('project.license') ?? '');
  check(report, licenseRaw.includes('Apache-2.0'), 'license=Apache-2.0', `project.license 应声明 Apache-2.0，实际 ${licenseRaw || '<empty>'}`);
  const readme = tomlString(toml.get('project.readme')) ?? '';
  check(report, readme === 'README.md', 'readme=README.md', `project.readme 应为 README.md，实际 ${readme || '<empty>'}`);
  check(report, existsSync(join(dir, readme || 'README.md')), 'README.md 存在', 'pyproject 声明的 readme 文件不存在');

  const dependencies = toml.get('project.dependencies');
  check(
    report,
    dependencies === undefined || tomlStringArray(dependencies).length === 0,
    '没有运行时依赖（仅标准库）',
    'project.dependencies 应为空数组（SDK 只依赖标准库）',
  );

  const packages = tomlStringArray(toml.get('tool.setuptools.packages'));
  check(report, packages.includes(PY_MODULE), `setuptools.packages 包含 ${PY_MODULE}`, `tool.setuptools.packages 缺少 ${PY_MODULE}`);
  check(report, toml.has('project.urls.Homepage'), '[project.urls] Homepage', 'project.urls 缺少 Homepage');
  check(report, toml.has('project.urls.Repository'), '[project.urls] Repository', 'project.urls 缺少 Repository');
  check(report, toml.has('project.urls.Bug Tracker'), '[project.urls] Bug Tracker', 'project.urls 缺少 Bug Tracker');
  check(report, toml.has('project.urls.Changelog'), '[project.urls] Changelog', 'project.urls 缺少 Changelog');
  check(report, toml.has('project.urls.Compatibility'), '[project.urls] Compatibility', 'project.urls 缺少 Compatibility');

  expectDocs(report, dir, ['README.md', 'CHANGELOG.md', 'COMPATIBILITY.md'], 'sdk/python');
  expectDocs(report, dir, [`${PY_MODULE}/__init__.py`, `${PY_MODULE}/client.py`, 'tests/test_client.py'], 'sdk/python');

  const initFile = join(dir, PY_MODULE, '__init__.py');
  if (existsSync(initFile)) {
    const init = readFileSync(initFile, 'utf8');
    const match = /__version__\s*=\s*["']([^"']+)["']/.exec(init);
    check(report, Boolean(match), '__init__.py 有 __version__', '__init__.py 缺少 __version__');
    if (match) {
      check(report, match[1] === version, `__version__=${version} 与 pyproject 一致`, `__init__.py 的 __version__=${match[1]} 与 pyproject 版本 ${version} 不一致`);
    }
  }
  const clientFile = join(dir, PY_MODULE, 'client.py');
  if (existsSync(clientFile)) {
    const client = readFileSync(clientFile, 'utf8');
    const match = /DEFAULT_MCP_CLIENT_INFO\s*=\s*\{[^}]*"version":\s*"([^"]+)"/s.exec(client);
    check(report, Boolean(match), 'client.py 有默认 client info 版本', 'client.py 缺少 DEFAULT_MCP_CLIENT_INFO.version');
    if (match) {
      check(report, match[1] === version, `MCP client info 版本=${version}`, `client.py 的 DEFAULT_MCP_CLIENT_INFO.version=${match[1]} 与 pyproject 版本 ${version} 不一致`);
    }
  }

  checkVersionConsistency(report, { packageVersion: version, tagPrefix: PY_TAG_PREFIX, label: 'Python SDK' });
  checkChangelogAndCompatibility(report, { dir, version, label: 'Python SDK' });

  if (options.distDir) verifyPythonDist(report, { dir, distDir: resolve(options.distDir ?? ''), version });
  return report;
}

function verifyPythonDist(report, { distDir, version }) {
  if (!existsSync(distDir)) {
    report.errors.push(`--dist 目录不存在：${distDir}`);
    return;
  }
  const { wheel, sdist } = findPython(distDir);
  if (!wheel) report.errors.push(`--dist ${distDir} 里没有 wheel（*.whl）`);
  if (!sdist) report.errors.push(`--dist ${distDir} 里没有 sdist（*.tar.gz）`);
  if (!wheel || !sdist) return;

  check(
    report,
    normalizeLooseVersion(wheel.slice(wheel.indexOf('-') + 1).split('-')[0]) === normalizeLooseVersion(version),
    `wheel 文件名包含版本 ${version}`,
    `wheel 文件名 ${wheel} 与 project.version ${version} 不一致`,
  );
  check(
    report,
    normalizeLooseVersion(sdist.slice(sdist.indexOf('-') + 1, -'.tar.gz'.length)) === normalizeLooseVersion(version),
    `sdist 文件名包含版本 ${version}`,
    `sdist 文件名 ${sdist} 与 project.version ${version} 不一致`,
  );

  // wheel：文件清单 + METADATA
  let wheelInfo;
  try {
    wheelInfo = inspectWheel(join(distDir, wheel));
  } catch (error) {
    report.errors.push(error.message);
    report.artifacts.push({ file: wheel, kind: 'wheel', bytes: readFileSync(join(distDir, wheel)).length, sha256: sha256(join(distDir, wheel)) });
    return;
  }
  if (wheelInfo.error) {
    report.errors.push(`wheel ${wheel} 结构异常：${wheelInfo.error}`);
    return;
  }
  const wheelNames = new Set(wheelInfo.names);
  for (const required of [
    `${PY_MODULE}/__init__.py`,
    `${PY_MODULE}/client.py`,
    `${PY_MODULE}-${version}.dist-info/METADATA`,
    `${PY_MODULE}-${version}.dist-info/WHEEL`,
  ]) {
    check(report, wheelNames.has(required), `wheel 包含 ${required}`, `wheel 缺少 ${required}`);
  }
  if ([...wheelNames].some((name) => name.startsWith('tests/'))) {
    report.errors.push('wheel 不应包含 tests/');
  }
  if ([...wheelNames].some((name) => name.endsWith('.pyc'))) {
    report.errors.push('wheel 不应包含 .pyc');
  }
  const metadata = wheelInfo.metadata ?? {};
  check(report, String(metadata.Name ?? '').replaceAll('_', '-') === PY_NAME, `wheel METADATA Name=${PY_NAME}`, `wheel METADATA Name 应为 ${PY_NAME}，实际 ${metadata.Name}`);
  check(report, String(metadata.Version ?? '') === version, `wheel METADATA Version=${version}`, `wheel METADATA Version=${metadata.Version} 与 ${version} 不一致`);
  check(report, metadata['Requires-Python'] === '>=3.9', 'wheel METADATA Requires-Python=>=3.9', `wheel METADATA Requires-Python 应为 >=3.9，实际 ${metadata['Requires-Python']}`);
  check(report, String(metadata.License ?? '').includes('Apache-2.0'), 'wheel METADATA License=Apache-2.0', 'wheel METADATA License 缺少 Apache-2.0');
  check(report, typeof metadata.Summary === 'string' && metadata.Summary !== 'None', 'wheel METADATA 有 Summary', 'wheel METADATA 缺少 Summary');

  // sdist：解包前缀 + 文件清单 + PKG-INFO
  let entries;
  try {
    entries = tarList(join(distDir, sdist));
  } catch (error) {
    report.errors.push(error.message);
    return;
  }
  const prefix = entries.find((entry) => entry.endsWith('/pyproject.toml'));
  if (!prefix) {
    report.errors.push('sdist 里找不到 pyproject.toml');
    return;
  }
  const rootPrefix = prefix.slice(0, -'pyproject.toml'.length);
  check(report, rootPrefix === `${PY_MODULE}-${version}/`, `sdist 顶层目录 ${rootPrefix}`, `sdist 顶层目录应为 ${PY_MODULE}-${version}/，实际 ${rootPrefix}`);
  for (const required of ['pyproject.toml', 'README.md', 'PKG-INFO', `${PY_MODULE}/__init__.py`, `${PY_MODULE}/client.py`]) {
    check(report, entries.includes(`${rootPrefix}${required}`), `sdist 包含 ${required}`, `sdist 缺少 ${required}`);
  }
  if (entries.some((entry) => entry.includes('node_modules/') || entry.endsWith('.pyc'))) {
    report.errors.push('sdist 里混入了 node_modules 或 .pyc');
  }
  try {
    const pkgInfo = tarRead(join(distDir, sdist), `${rootPrefix}PKG-INFO`);
    const infoName = /^Name:\s*(.+)$/m.exec(pkgInfo)?.[1]?.trim() ?? '';
    const infoVersion = /^Version:\s*(.+)$/m.exec(pkgInfo)?.[1]?.trim() ?? '';
    check(report, infoName.replaceAll('_', '-') === PY_NAME, `sdist PKG-INFO Name=${PY_NAME}`, `sdist PKG-INFO Name 应为 ${PY_NAME}，实际 ${infoName}`);
    check(report, infoVersion === version, `sdist PKG-INFO Version=${version}`, `sdist PKG-INFO Version=${infoVersion} 与 ${version} 不一致`);
  } catch (error) {
    report.errors.push(error.message);
  }

  for (const [file, kind] of [
    [wheel, 'wheel'],
    [sdist, 'sdist'],
  ]) {
    const path = join(distDir, file);
    report.artifacts.push({ file, kind, bytes: readFileSync(path).length, sha256: sha256(path) });
  }
}

// ---------- CLI ----------

const USAGE = `用法：
  node scripts/sdk/verify-package.mjs --package typescript [--dist DIR] [--tag sdk-ts-vX.Y.Z]
  node scripts/sdk/verify-package.mjs --package python [--dist DIR] [--tag sdk-py-vX.Y.Z]
选项：
  --package <typescript|python>  必填，要检查的 SDK
  --root <dir>                   仓库根目录（默认脚本所在仓库）
  --dist <dir>                   检查构建产物（npm tarball / wheel + sdist）
  --tag <tag>                    发布 tag，校验前缀与版本一致
  --expected-version <version>   额外断言包版本
  -h, --help                     显示帮助`;

function parseArgs(argv) {
  const options = { root: REPO_ROOT, package: '', distDir: '', tag: '', expectedVersion: '', help: false };
  for (let index = 0; index < argv.length; index += 1) {
    const arg = argv[index];
    if (arg === '-h' || arg === '--help') {
      options.help = true;
    } else if (arg === '--package') {
      options.package = argv[++index] ?? '';
    } else if (arg.startsWith('--package=')) {
      options.package = arg.slice('--package='.length);
    } else if (arg === '--root') {
      options.root = resolve(argv[++index] ?? '');
    } else if (arg === '--dist') {
      options.distDir = argv[++index] ?? '';
    } else if (arg === '--tag') {
      options.tag = argv[++index] ?? '';
    } else if (arg === '--expected-version') {
      options.expectedVersion = argv[++index] ?? '';
    } else {
      throw new VerifyError(`未知参数：${arg}\n${USAGE}`);
    }
  }
  if (!options.help && !['typescript', 'python'].includes(options.package)) {
    throw new VerifyError(`--package 必须是 typescript 或 python\n${USAGE}`);
  }
  return options;
}

function printReport(report) {
  console.log(`verify-package: ${report.kind}`);
  for (const line of report.checks) console.log(`  ✓ ${line}`);
  for (const line of report.warnings) console.log(`  ! ${line}`);
  for (const line of report.errors) console.error(`  ✗ ${line}`);
  for (const artifact of report.artifacts) {
    console.log(`  - ${artifact.kind}: ${artifact.file} (${(artifact.bytes / 1024).toFixed(1)} KiB, sha256 ${artifact.sha256.slice(0, 16)}…)`);
  }
  if (report.errors.length > 0) {
    console.error(`verify-package: ${report.kind} FAILED（${report.errors.length} 个错误）`);
    return 1;
  }
  console.log(`verify-package: ${report.kind} OK（${report.checks.length} 项检查通过）`);
  return 0;
}

function main() {
  let options;
  try {
    options = parseArgs(process.argv.slice(2));
  } catch (error) {
    console.error(error.message);
    process.exitCode = 2;
    return;
  }
  if (options.help) {
    console.log(USAGE);
    return;
  }
  try {
    const report = options.package === 'typescript' ? verifyTypescriptPackage(options) : verifyPythonPackage(options);
    process.exitCode = printReport(report);
  } catch (error) {
    console.error(`verify-package: ${error.message}`);
    process.exitCode = 2;
  }
}

const invokedPath = process.argv[1] ? pathToFileURL(resolve(process.argv[1])).href : '';
if (import.meta.url === invokedPath) {
  main();
}
