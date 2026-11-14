// release-plan 的单元测试 + CLI dry run：验证不同触发方式下的发布门是否算对。
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { existsSync, mkdtempSync, readFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

import { computePlan, renderSummary } from './release-plan.mjs';

const HERE = dirname(fileURLToPath(import.meta.url));
const SCRIPT = resolve(HERE, 'release-plan.mjs');

function plan(env) {
  return computePlan(env);
}

test('tag push：sdk-ts-v0.1.0 只发 TypeScript，非 dry-run', () => {
  const result = plan({ EVENT_NAME: 'push', REF_NAME: 'sdk-ts-v0.1.0' });
  assert.equal(result.ok, true, result.errors.join('\n'));
  assert.deepEqual(result.outputs, {
    typescript: 'true',
    python: 'false',
    dry_run: 'false',
    publish: 'true',
    npm_token: 'false',
    tag: 'sdk-ts-v0.1.0',
    version: '0.1.0',
    npm_tag: 'latest',
  });
  assert.ok(result.warnings.some((line) => line.includes('NPM_TOKEN')));
});

test('tag push：预发布版本自动用 next dist-tag', () => {
  const result = plan({ EVENT_NAME: 'push', REF_NAME: 'sdk-ts-v0.2.0-rc.1' });
  assert.equal(result.ok, true, result.errors.join('\n'));
  assert.equal(result.outputs.npm_tag, 'next');
});

test('tag push：sdk-py-v0.2.0 只发 Python，release 版本可用 PEP 440 后缀', () => {
  const result = plan({ EVENT_NAME: 'push', REF_NAME: 'sdk-py-v0.2.0rc1' });
  assert.equal(result.ok, true, result.errors.join('\n'));
  assert.equal(result.outputs.python, 'true');
  assert.equal(result.outputs.typescript, 'false');
  assert.equal(result.outputs.version, '0.2.0rc1');
  assert.equal(result.outputs.publish, 'true');
});

test('workflow_dispatch 缺省参数：dry-run，构建两个包但不发布', () => {
  const result = plan({ EVENT_NAME: 'workflow_dispatch' });
  assert.equal(result.ok, true, result.errors.join('\n'));
  assert.equal(result.outputs.dry_run, 'true');
  assert.equal(result.outputs.publish, 'false');
  assert.equal(result.outputs.typescript, 'true');
  assert.equal(result.outputs.python, 'true');
  assert.equal(result.outputs.tag, '');
  assert.ok(result.notes.some((line) => line.includes('dry-run')));
});

test('workflow_dispatch + dry_run=false + 有 token：允许 TypeScript 发布', () => {
  const result = plan({
    EVENT_NAME: 'workflow_dispatch',
    INPUT_PACKAGE: 'typescript',
    INPUT_DRY_RUN: 'false',
    INPUT_DIST_TAG: 'next',
    NPM_TOKEN: 'placeholder-not-a-real-token',
  });
  assert.equal(result.ok, true, result.errors.join('\n'));
  assert.equal(result.outputs.publish, 'true');
  assert.equal(result.outputs.npm_token, 'true');
  assert.equal(result.outputs.typescript, 'true');
  assert.equal(result.outputs.python, 'false');
  assert.equal(result.outputs.npm_tag, 'next');
});

test('workflow_dispatch + dry_run=false + 无 token：发布计划要求 npm 跳过', () => {
  const result = plan({ EVENT_NAME: 'workflow_dispatch', INPUT_DRY_RUN: 'false', INPUT_PACKAGE: 'both' });
  assert.equal(result.ok, true, result.errors.join('\n'));
  assert.equal(result.outputs.publish, 'true');
  assert.equal(result.outputs.npm_token, 'false');
  assert.ok(result.warnings.some((line) => line.includes('publish-npm 任务会被跳过')));
});

test('非法输入：错误 tag / branch push / 非法 package / 非法 dry_run', () => {
  assert.equal(plan({ EVENT_NAME: 'push', REF_NAME: 'sdk-ts-v0.1' }).ok, false);
  assert.equal(plan({ EVENT_NAME: 'push', REF_NAME: 'main' }).ok, false);
  assert.equal(plan({ EVENT_NAME: 'workflow_dispatch', INPUT_PACKAGE: 'wheel' }).ok, false);
  assert.equal(plan({ EVENT_NAME: 'workflow_dispatch', INPUT_DRY_RUN: 'maybe' }).ok, false);
  assert.equal(plan({ EVENT_NAME: 'schedule' }).ok, false);
});

test('计划摘要不包含 secret 内容', () => {
  const result = plan({
    EVENT_NAME: 'workflow_dispatch',
    INPUT_DRY_RUN: 'false',
    INPUT_PACKAGE: 'typescript',
    NPM_TOKEN: 'token-that-must-not-leak',
  });
  const summary = renderSummary({ eventName: 'workflow_dispatch', refName: 'main', ...result });
  assert.ok(!summary.includes('token-that-must-not-leak'));
  assert.ok(!JSON.stringify(result.outputs).includes('token-that-must-not-leak'));
  assert.ok(summary.includes('NPM_TOKEN 已配置 | 是'));
});

function runCli(env) {
  const dir = mkdtempSync(join(tmpdir(), 'dsh-release-plan-'));
  const outputFile = join(dir, 'github_output');
  const result = spawnSync(process.execPath, [SCRIPT], {
    encoding: 'utf8',
    env: {
      PATH: process.env.PATH,
      GITHUB_OUTPUT: outputFile,
      ...env,
    },
  });
  const output = existsSync(outputFile) ? readFileSync(outputFile, 'utf8') : '';
  rmSync(dir, { recursive: true, force: true });
  return { ...result, output };
}

test('CLI：把计划写入 $GITHUB_OUTPUT，且不打印 secret', () => {
  const token = 'npm_secret_cli_must_not_leak_1234567890';
  const result = runCli({
    EVENT_NAME: 'workflow_dispatch',
    INPUT_DRY_RUN: 'false',
    INPUT_PACKAGE: 'typescript',
    NPM_TOKEN: token,
    GITHUB_STEP_SUMMARY: '',
  });
  assert.equal(result.status, 0, result.stderr);
  assert.match(result.output, /^typescript=true$/m);
  assert.match(result.output, /^publish=true$/m);
  assert.match(result.output, /^npm_token=true$/m);
  assert.doesNotMatch(`${result.stdout}${result.stderr}${result.output}`, new RegExp(token));
});

test('CLI：非法计划退出码 1 且不写 outputs', () => {
  const result = runCli({ EVENT_NAME: 'push', REF_NAME: 'main' });
  assert.equal(result.status, 1);
  assert.equal(result.output, '');
  assert.match(result.stderr, /::error::/);
});
