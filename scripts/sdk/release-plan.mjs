#!/usr/bin/env node
// release-plan：根据触发方式（tag push / workflow_dispatch）计算 SDK 发布计划。
//
// 输入全部来自环境变量（与 .github/workflows/release-sdk.yml 的 preflight 步骤一致）：
//   EVENT_NAME       workflow_dispatch | push
//   REF_NAME         触发 ref；tag push 时为 tag 名
//   INPUT_PACKAGE    dispatch 输入：both | typescript | python
//   INPUT_DRY_RUN    dispatch 输入："true" | "false"（缺省按 true 处理，偏安全）
//   INPUT_DIST_TAG   dispatch 输入：latest | next
//   NPM_TOKEN        只做“是否配置”的判断，不输出、不落盘
//
// 输出写到 $GITHUB_OUTPUT（本地没有该变量时打印 key=value 到 stdout）：
//   typescript python dry_run publish npm_token tag version npm_tag
//
// 退出码：计划合法 0；输入非法 1（preflight 失败，后续 build/publish 不会执行）。

import { appendFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { pathToFileURL } from 'node:url';

export const TYPESCRIPT_TAG_PREFIX = 'sdk-ts-v';
export const PYTHON_TAG_PREFIX = 'sdk-py-v';

// npm semver（允许 prerelease/build 后缀；与 package.json 的 version 字段对齐）
const NPM_VERSION = /^(?:0|[1-9]\d*)\.(?:0|[1-9]\d*)\.(?:0|[1-9]\d*)(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$/;
// PEP 440 的常见子集（0.1.0 / 0.2.0rc1 / 0.2.0.post1 / 0.2.0.dev1）
const PEP440_VERSION = /^(?:0|[1-9]\d*)(?:\.(?:0|[1-9]\d*))*(?:(?:a|b|rc)\d+)?(?:\.post\d+)?(?:\.dev\d+)?$/i;

const PACKAGE_CHOICES = new Set(['both', 'typescript', 'python']);
const DIST_TAG_CHOICES = new Set(['latest', 'next']);
const OUTPUT_KEYS = [
  'typescript',
  'python',
  'dry_run',
  'publish',
  'npm_token',
  'tag',
  'version',
  'npm_tag',
];

function fail(errors, message) {
  errors.push(message);
}

/**
 * 纯函数：由环境变量对象算出发布计划，便于单测和本地 dry run。
 * 不读取 process.env，也不会打印任何 secret。
 */
export function computePlan(env = {}) {
  const errors = [];
  const warnings = [];
  const notes = [];
  const eventName = String(env.EVENT_NAME ?? '').trim();
  const refName = String(env.REF_NAME ?? '').trim();
  const rawPackage = String(env.INPUT_PACKAGE ?? '').trim();
  const rawDryRun = String(env.INPUT_DRY_RUN ?? '').trim().toLowerCase();
  const rawDistTag = String(env.INPUT_DIST_TAG ?? '').trim();
  const hasNpmToken = String(env.NPM_TOKEN ?? '').trim() !== '';

  let typescript = false;
  let python = false;
  let dryRun = true;
  let tag = '';
  let version = '';
  let npmTag = 'latest';

  if (eventName === 'push') {
    // tag push 是唯一的常规发布入口；普通 branch push 不会触发本 workflow。
    dryRun = false;
    if (refName.startsWith(TYPESCRIPT_TAG_PREFIX)) {
      typescript = true;
      tag = refName;
      version = refName.slice(TYPESCRIPT_TAG_PREFIX.length);
      if (!NPM_VERSION.test(version)) {
        fail(errors, `tag ${refName} 的版本部分不是合法 semver：${version || '<empty>'}`);
      }
      npmTag = version.includes('-') ? 'next' : 'latest';
    } else if (refName.startsWith(PYTHON_TAG_PREFIX)) {
      python = true;
      tag = refName;
      version = refName.slice(PYTHON_TAG_PREFIX.length);
      if (!PEP440_VERSION.test(version)) {
        fail(errors, `tag ${refName} 的版本部分不是合法 PEP 440 版本：${version || '<empty>'}`);
      }
    } else {
      fail(errors, `push 的 ref ${refName || '<empty>'} 不是 SDK 发布 tag（${TYPESCRIPT_TAG_PREFIX}* / ${PYTHON_TAG_PREFIX}*）`);
    }
    if (!errors.length) {
      warnings.push(`tag push ${tag}：工作流不是 dry-run，会尝试发布对应 SDK 包。`);
    }
  } else if (eventName === 'workflow_dispatch') {
    // 手动触发默认 dry_run=true；显式 false 才允许发布。
    if (rawDryRun === '') {
      dryRun = true;
    } else if (rawDryRun === 'true' || rawDryRun === 'false') {
      dryRun = rawDryRun === 'true';
    } else {
      fail(errors, `dry_run 输入只接受 true/false，收到：${rawDryRun}`);
    }
    const pkg = rawPackage === '' ? 'both' : rawPackage;
    if (!PACKAGE_CHOICES.has(pkg)) {
      fail(errors, `package 输入只接受 ${[...PACKAGE_CHOICES].join('/')}，收到：${pkg}`);
    } else {
      typescript = pkg !== 'python';
      python = pkg !== 'typescript';
    }
    npmTag = rawDistTag === '' ? 'latest' : rawDistTag;
    if (!DIST_TAG_CHOICES.has(npmTag)) {
      fail(errors, `dist_tag 输入只接受 ${[...DIST_TAG_CHOICES].join('/')}，收到：${npmTag}`);
    }
    if (dryRun) {
      notes.push('dry-run：只构建、测试、打包并上传 artifact，不执行 publish。');
    } else {
      warnings.push('手动 workflow_dispatch 且 dry_run=false：会真正发布；发布任务受 environment 审批和凭据配置约束。');
    }
  } else {
    fail(errors, `不支持的事件类型：${eventName || '<empty>'}（只接受 workflow_dispatch 或 push tag）`);
  }

  const publish = !dryRun && (typescript || python);
  if (publish && typescript && !hasNpmToken) {
    warnings.push('已请求发布 TypeScript 包，但没有配置 NPM_TOKEN：publish-npm 任务会被跳过（不会硬编码或伪造凭据）。');
  }
  if (publish && typescript && hasNpmToken) {
    notes.push('npm publish 通过 repository/environment secret 注入 NODE_AUTH_TOKEN。');
  }
  if (publish && python) {
    notes.push('PyPI 发布使用 trusted publisher（OIDC，id-token: write），不使用 PyPI token。');
  }

  const outputs = {
    typescript: String(typescript),
    python: String(python),
    dry_run: String(dryRun),
    publish: String(publish),
    npm_token: String(hasNpmToken),
    tag,
    version,
    npm_tag: npmTag,
  };

  return {
    ok: errors.length === 0,
    errors,
    warnings,
    notes,
    outputs,
    summary: renderSummary({ eventName, refName, outputs, warnings, notes }),
  };
}

export function renderSummary({ eventName, refName, outputs, warnings = [], notes = [] }) {
  const row = (label, value) => `| ${label} | ${value} |`;
  const lines = [
    '## SDK 发布计划',
    '',
    '| 项 | 值 |',
    '| --- | --- |',
    row('事件', eventName || '<empty>'),
    row('ref', refName || '<empty>'),
    row('TypeScript 包', outputs.typescript === 'true' ? '是' : '否'),
    row('Python 包', outputs.python === 'true' ? '是' : '否'),
    row('dry_run', outputs.dry_run === 'true' ? '是' : '否'),
    row('publish', outputs.publish === 'true' ? '是' : '否'),
    row('NPM_TOKEN 已配置', outputs.npm_token === 'true' ? '是' : '否'),
    row('tag', outputs.tag || '—'),
    row('version', outputs.version || '—'),
    row('npm dist-tag', outputs.npm_tag || '—'),
  ];
  if (warnings.length > 0) {
    lines.push('', '### 注意', '', ...warnings.map((item) => `- ${item}`));
  }
  if (notes.length > 0) {
    lines.push('', '### 说明', '', ...notes.map((item) => `- ${item}`));
  }
  return `${lines.join('\n')}\n`;
}

function hasArg(argv, name) {
  return argv.includes(name);
}

function writeOutputs(outputs, env) {
  const lines = OUTPUT_KEYS.map((key) => `${key}=${outputs[key] ?? ''}`).join('\n');
  if (env.GITHUB_OUTPUT) {
    appendFileSync(env.GITHUB_OUTPUT, `${lines}\n`);
  } else {
    process.stdout.write(`${lines}\n`);
  }
}

function main() {
  const argv = process.argv.slice(2);
  const plan = computePlan(process.env);
  for (const warning of plan.warnings) console.error(`::warning::${warning}`);
  for (const note of plan.notes) console.error(`note: ${note}`);
  if (!plan.ok) {
    for (const error of plan.errors) console.error(`::error::${error}`);
    process.exitCode = 1;
    return;
  }
  writeOutputs(plan.outputs, process.env);
  if (hasArg(argv, '--summary')) {
    process.stdout.write(plan.summary);
  } else if (process.env.GITHUB_STEP_SUMMARY) {
    appendFileSync(process.env.GITHUB_STEP_SUMMARY, plan.summary);
  }
}

const invokedPath = process.argv[1] ? pathToFileURL(resolve(process.argv[1])).href : '';
if (import.meta.url === invokedPath) {
  main();
}
