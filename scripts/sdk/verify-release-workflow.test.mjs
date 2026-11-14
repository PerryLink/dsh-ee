// verify-release-workflow 的测试：当前工作流必须通过，并且对“坏改动”确实会报错。
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

import { verifyReleaseWorkflow, verifyReleaseWorkflowFile, WORKFLOW_PATH, REPO_ROOT } from './verify-release-workflow.mjs';

const HERE = dirname(fileURLToPath(import.meta.url));
const WORKFLOW_TEXT = readFileSync(resolve(REPO_ROOT, WORKFLOW_PATH), 'utf8');

function mutate(from, to) {
  assert.ok(WORKFLOW_TEXT.includes(from), `测试夹具失效：工作流里找不到 ${JSON.stringify(from)}`);
  return WORKFLOW_TEXT.replace(from, to);
}

test('仓库里的 release-sdk.yml 通过全部发布约束', () => {
  const report = verifyReleaseWorkflowFile();
  assert.deepEqual(report.errors, []);
  assert.ok(report.checks.length > 50);
});

test('拒绝 pull_request 触发', () => {
  const text = mutate('  push:\n    tags:', '  pull_request:\n    branches: [main]\n  push:\n    tags:');
  const report = verifyReleaseWorkflow(text);
  assert.ok(report.errors.some((line) => line.includes('pull_request')));
});

test('拒绝按 branch push 触发', () => {
  const text = mutate('  push:\n    tags:', '  push:\n    branches: [main]\n    tags:');
  const report = verifyReleaseWorkflow(text);
  assert.ok(report.errors.some((line) => line.includes('branches')));
});

test('dry_run 默认值必须是 true', () => {
  const text = mutate('        default: true', '        default: false');
  const report = verifyReleaseWorkflow(text);
  assert.ok(report.errors.some((line) => line.includes('dry_run 默认值')));
});

test('publish-npm 必须要求 NPM_TOKEN 已配置', () => {
  const text = mutate("      needs.preflight.outputs.npm_token == 'true'", "      needs.preflight.outputs.npm_token == 'false'");
  const report = verifyReleaseWorkflow(text);
  assert.ok(report.errors.some((line) => line.includes('NPM_TOKEN 已配置')));
});

test('publish job 不允许绕开 publish=true 条件', () => {
  const text = mutate("      needs.preflight.outputs.publish == 'true' &&\n      needs.preflight.outputs.typescript == 'true'", "      needs.preflight.outputs.typescript == 'true'");
  const report = verifyReleaseWorkflow(text);
  assert.ok(report.errors.some((line) => line.includes('publish == true')));
});

test('拒绝硬编码 token', () => {
  const text = mutate(
    '        run: npm test',
    '        run: |\n          npm test\n          echo ghp_abcdefghijklmnopqrstuvwxyz1234',
  );
  const report = verifyReleaseWorkflow(text);
  assert.ok(report.errors.some((line) => line.includes('硬编码凭据')));
});

test('secret 只允许出现在 plan env 和 npm publish env', () => {
  const text = mutate(
    '      - name: 类型检查\n        run: npm run typecheck',
    '      - name: 类型检查\n        env:\n          LEAK: ${{ secrets.NPM_TOKEN }}\n        run: npm run typecheck',
  );
  const report = verifyReleaseWorkflow(text);
  assert.ok(report.errors.some((line) => line.includes('secret 出现在非预期位置')));
});

test('build job 不允许出现 npm publish', () => {
  const text = mutate(
    '      - name: 类型检查\n        run: npm run typecheck',
    '      - name: 类型检查\n        run: npm run typecheck && npm publish',
  );
  const report = verifyReleaseWorkflow(text);
  assert.ok(report.errors.some((line) => line.includes('不应执行 npm publish') || line.includes('没有 npm publish')));
});

test('YAML 语法错误会报解析失败而不是静默通过', () => {
  const report = verifyReleaseWorkflow('name: [unclosed\n');
  assert.equal(report.errors.length > 0, true);
  assert.ok(report.errors.some((line) => line.includes('解析失败')));
});

test('CLI 入口能跑通当前工作流', () => {
  const file = resolve(REPO_ROOT, WORKFLOW_PATH);
  const started = Date.now();
  const report = verifyReleaseWorkflowFile(file);
  assert.deepEqual(report.errors, []);
  assert.ok(Date.now() - started < 5000);
});
