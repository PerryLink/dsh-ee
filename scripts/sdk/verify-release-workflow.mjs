#!/usr/bin/env node
// verify-release-workflow：用仓库内的零依赖 YAML 子集解析器校验
// `.github/workflows/release-sdk.yml` 的发布约束。
//
// 这个脚本本身就是发布工作流 preflight 的一步：它读取并解析 YAML（不是正则扫文本），
// 再断言安全/流程约束，防止后续改动意外让普通 push 触发发布、把 publish 挪出条件门、
// 或把凭据写进仓库文件：
//
//   - 触发面只有 workflow_dispatch + SDK tag push，没有 branch push / PR / schedule；
//   - workflow_dispatch 的 dry_run 默认 true，package/dist_tag 有明确默认值；
//   - build job 只构建/测试/打包并上传 artifact，不引用 secrets、不发布；
//   - publish 只在 preflight 判定 publish=true（非 dry-run）且目标包匹配时执行；
//     npm 还要求 npm_token=true；PyPI 用 trusted publisher（id-token: write）而非 token；
//   - secrets 只在允许的 env 位置出现，且没有明显的硬编码 token。
//
// 用法：node scripts/sdk/verify-release-workflow.mjs [--file <path>]
// 退出码：0 通过；1 约束不满足；2 文件不存在/无法读取。

import { readFileSync } from 'node:fs';
import { dirname, resolve } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';

import { parseYaml, YamlParseError } from './yaml-lite.mjs';
import { PYTHON_TAG_PREFIX, TYPESCRIPT_TAG_PREFIX } from './release-plan.mjs';

const HERE = dirname(fileURLToPath(import.meta.url));
export const REPO_ROOT = resolve(HERE, '..', '..');
export const WORKFLOW_PATH = '.github/workflows/release-sdk.yml';

const REQUIRED_JOBS = ['preflight', 'typescript-sdk', 'python-sdk', 'publish-npm', 'publish-pypi'];
const PLAN_OUTPUTS = ['typescript', 'python', 'dry_run', 'publish', 'npm_token', 'tag', 'version', 'npm_tag'];

// 常见凭据的形状：命中就认为有硬编码 secret 的嫌疑，必须人工确认/移除。
const SECRET_PATTERNS = [
  [/npm_[A-Za-z0-9]{20,}/, 'npm token（npm_ 开头）'],
  [/pypi-[A-Za-z0-9_-]{20,}/, 'PyPI API token（pypi- 开头）'],
  [/ghp_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,}/, 'GitHub token'],
  [/-----BEGIN [A-Z ]*PRIVATE KEY-----/, '私钥'],
];

function collectStrings(value, path, out) {
  if (typeof value === 'string') {
    out.push({ path, value });
    return out;
  }
  if (Array.isArray(value)) {
    value.forEach((item, index) => collectStrings(item, `${path}[${index}]`, out));
    return out;
  }
  if (value && typeof value === 'object') {
    for (const [key, child] of Object.entries(value)) {
      collectStrings(child, path === '' ? key : `${path}.${key}`, out);
    }
  }
  return out;
}

function stepRuns(job) {
  return (job?.steps ?? []).filter((step) => typeof step?.run === 'string').map((step) => step.run);
}

function stepUses(job) {
  return (job?.steps ?? []).filter((step) => typeof step?.uses === 'string').map((step) => step.uses);
}

function jobUsesAction(job, prefix) {
  return stepUses(job).some((uses) => uses.startsWith(prefix));
}

function findStep(job, predicate) {
  return (job?.steps ?? []).find((step) => predicate(step));
}

function createReport() {
  return { checks: [], warnings: [], errors: [] };
}

function expect(report, condition, okMessage, errorMessage) {
  if (condition) report.checks.push(okMessage);
  else report.errors.push(errorMessage);
  return Boolean(condition);
}

function asArray(value) {
  if (Array.isArray(value)) return value;
  if (value === undefined || value === null) return [];
  return [value];
}

function asString(value) {
  return typeof value === 'string' ? value : '';
}

/**
 * 纯函数：传入工作流 YAML 文本，返回 { checks, warnings, errors }。
 * 便于单测里对“坏工作流”做变异测试。
 */
export function verifyReleaseWorkflow(text) {
  const report = createReport();
  let doc;
  try {
    doc = parseYaml(text);
  } catch (error) {
    if (error instanceof YamlParseError) {
      report.errors.push(`工作流 YAML 解析失败：${error.message}`);
      return report;
    }
    throw error;
  }
  if (!doc || typeof doc !== 'object' || Array.isArray(doc)) {
    report.errors.push('工作流 YAML 顶层必须是映射');
    return report;
  }

  expect(report, doc.name === 'release-sdk', 'name 是 release-sdk', `name 应为 release-sdk，实际 ${doc.name}`);

  // ---------- 触发面 ----------
  const triggers = doc.on;
  if (!triggers || typeof triggers !== 'object' || Array.isArray(triggers)) {
    report.errors.push('缺少 on: 触发配置');
    return report;
  }
  const triggerKeys = Object.keys(triggers).sort();
  expect(
    report,
    triggerKeys.length === 2 && triggerKeys[0] === 'push' && triggerKeys[1] === 'workflow_dispatch',
    '触发面只有 push(tags) + workflow_dispatch',
    `触发面应只有 push + workflow_dispatch，实际：${triggerKeys.join(', ') || '<empty>'}`,
  );
  for (const forbidden of ['pull_request', 'pull_request_target', 'schedule', 'workflow_call', 'workflow_run', 'release']) {
    expect(report, !(forbidden in triggers), `没有 ${forbidden} 触发`, `不允许 ${forbidden} 触发（普通 CI/活动不能进发布流程）`);
  }
  const push = triggers.push ?? {};
  expect(report, Array.isArray(push.tags), 'push 只按 tags 触发', 'push 必须配置 tags 过滤');
  const expectedTags = [`${TYPESCRIPT_TAG_PREFIX}*`, `${PYTHON_TAG_PREFIX}*`];
  expect(
    report,
    JSON.stringify(push.tags) === JSON.stringify(expectedTags),
    `tag 模式是 ${expectedTags.join(' / ')}（与 release-plan 的 tag 前缀一致）`,
    `push.tags 应为 ${JSON.stringify(expectedTags)}，实际 ${JSON.stringify(push.tags)}`,
  );
  expect(report, !('branches' in push), 'push 不按 branch 触发', 'push 不能按 branches 触发（发布只走 tag/手动）');
  expect(
    report,
    Object.keys(push).length === 1 && 'tags' in push,
    'push 只有 tags 关键字',
    `push 下不应有其它关键字，实际：${Object.keys(push).join(', ')}`,
  );

  const dispatch = triggers.workflow_dispatch ?? {};
  expect(report, typeof dispatch === 'object' && !Array.isArray(dispatch), 'workflow_dispatch 是映射', 'workflow_dispatch 配置缺失');
  const inputs = dispatch.inputs ?? {};
  const dryRun = inputs.dry_run ?? {};
  expect(report, dryRun.type === 'boolean', 'dry_run 是 boolean 输入', 'dry_run 输入类型应为 boolean');
  expect(report, dryRun.default === true, 'dry_run 默认 true', `dry_run 默认值必须为 true，实际 ${JSON.stringify(dryRun.default)}`);
  expect(report, Array.isArray(inputs.package?.options) && inputs.package.default === 'both', 'package 输入默认 both', 'package 输入缺少 options 或默认值不是 both');
  expect(report, Array.isArray(inputs.dist_tag?.options) && inputs.dist_tag.default === 'latest', 'dist_tag 输入默认 latest', 'dist_tag 输入缺少 options 或默认值不是 latest');

  // ---------- 权限 / 并发 ----------
  expect(report, doc.permissions?.contents === 'read', '顶层 contents 只读', '顶层 permissions.contents 应为 read');
  expect(report, !('write-all' in (doc.permissions ?? {})), '顶层没有 write-all', '顶层 permissions 不能是 write-all');
  expect(report, Boolean(doc.concurrency?.group), '配置了 concurrency', '缺少 concurrency，重复发布可能并发执行');

  // ---------- job 结构 ----------
  const jobs = doc.jobs ?? {};
  const jobIds = Object.keys(jobs);
  expect(
    report,
    REQUIRED_JOBS.every((id) => jobIds.includes(id)),
    `job 齐全：${REQUIRED_JOBS.join(', ')}`,
    `缺少 job：${REQUIRED_JOBS.filter((id) => !jobIds.includes(id)).join(', ') || '<none>'}`,
  );
  for (const id of jobIds) {
    expect(report, jobs[id]?.['runs-on'] === 'ubuntu-latest', `${id} 使用 ubuntu-latest`, `${id} 应使用 ubuntu-latest`);
  }

  // preflight：用脚本解析/校验，并算出发布计划；NPM_TOKEN 只用于存在性判断。
  const preflight = jobs.preflight ?? {};
  expect(report, Array.isArray(preflight.steps) && preflight.steps.length > 0, 'preflight 有 steps', 'preflight 缺少 steps');
  const preflightRuns = stepRuns(preflight).join('\n');
  expect(report, preflightRuns.includes('verify-release-workflow.mjs'), 'preflight 会自检工作流 YAML', 'preflight 没有运行 verify-release-workflow.mjs');
  expect(report, preflightRuns.includes('verify-package.mjs'), 'preflight 会校验包元数据', 'preflight 没有运行 verify-package.mjs');
  expect(report, preflightRuns.includes('release-plan.mjs'), 'preflight 会计算发布计划', 'preflight 没有运行 release-plan.mjs');
  const planStep = findStep(preflight, (step) => asString(step?.run).includes('release-plan.mjs'));
  expect(report, planStep?.id === 'plan', '发布计划步骤 id=plan', '负责 release-plan.mjs 的步骤必须 id: plan');
  expect(
    report,
    planStep?.env?.NPM_TOKEN === '${{ secrets.NPM_TOKEN }}',
    'NPM_TOKEN 通过 env 注入给发布计划步骤',
    '发布计划步骤需要用 env 注入 secrets.NPM_TOKEN（只判断是否存在）',
  );
  for (const output of PLAN_OUTPUTS) {
    expect(report, Boolean(preflight.outputs?.[output]), `preflight 输出 ${output}`, `preflight.outputs 缺少 ${output}`);
  }

  // build job：只 build/test/package + 上传 artifact；不引用 secret、不 publish。
  const typescript = jobs['typescript-sdk'] ?? {};
  const python = jobs['python-sdk'] ?? {};
  for (const [id, job, target] of [
    ['typescript-sdk', typescript, 'typescript'],
    ['python-sdk', python, 'python'],
  ]) {
    expect(report, asArray(job.needs).includes('preflight'), `${id} 依赖 preflight`, `${id} 必须 needs: preflight`);
    const condition = asString(job.if);
    expect(
      report,
      condition.includes(`needs.preflight.outputs.${target} == 'true'`),
      `${id} 只在目标是 ${target} 时执行`,
      `${id}.if 必须检查 needs.preflight.outputs.${target} == 'true'`,
    );
    expect(report, Array.isArray(job.steps), `${id} 有 steps`, `${id} 缺少 steps`);
    expect(report, jobUsesAction(job, 'actions/upload-artifact@'), `${id} 上传 artifact`, `${id} 缺少 actions/upload-artifact`);
    const runs = stepRuns(job).join('\n');
    expect(report, runs.includes('verify-package.mjs'), `${id} 校验构建产物`, `${id} 没有运行 verify-package.mjs`);
    expect(report, !runs.includes('npm publish'), `${id} 不执行 npm publish`, `${id} 不应执行 npm publish（publish 只能在 publish job）`);
    expect(report, !JSON.stringify(job).includes('secrets.'), `${id} 不引用 secrets`, `${id} 不应引用 secrets`);
  }
  const tsRuns = stepRuns(typescript).join('\n');
  expect(report, tsRuns.includes('npm test'), 'TypeScript job 跑测试', 'TypeScript job 缺少 npm test');
  expect(report, tsRuns.includes('npm pack'), 'TypeScript job 跑 npm pack', 'TypeScript job 缺少 npm pack');
  const pyRuns = stepRuns(python).join('\n');
  expect(report, pyRuns.includes('unittest'), 'Python job 跑 unittest', 'Python job 缺少 unittest');
  expect(report, pyRuns.includes('python3 -m build'), 'Python job 跑 python3 -m build', 'Python job 缺少 python3 -m build');

  // publish-npm：非 dry-run + 目标匹配 + NPM_TOKEN 三重门；发布走 token env。
  const publishNpm = jobs['publish-npm'] ?? {};
  const npmCondition = asString(publishNpm.if);
  expect(
    report,
    npmCondition.includes("needs.preflight.outputs.publish == 'true'"),
    'publish-npm 需要 preflight publish=true（非 dry-run）',
    'publish-npm.if 必须要求 needs.preflight.outputs.publish == true',
  );
  expect(
    report,
    npmCondition.includes("needs.preflight.outputs.typescript == 'true'"),
    'publish-npm 只发布 TypeScript 目标',
    'publish-npm.if 必须要求 needs.preflight.outputs.typescript == true',
  );
  expect(
    report,
    npmCondition.includes("needs.preflight.outputs.npm_token == 'true'"),
    'publish-npm 要求 NPM_TOKEN 已配置',
    'publish-npm.if 必须要求 NPM_TOKEN 已配置（needs.preflight.outputs.npm_token == true）：没有 token 就跳过，而不是硬编码',
  );
  expect(report, asArray(publishNpm.needs).includes('preflight') && asArray(publishNpm.needs).includes('typescript-sdk'), 'publish-npm 依赖 preflight + typescript-sdk', 'publish-npm.needs 必须包含 preflight 和 typescript-sdk');
  expect(report, publishNpm.environment === 'npm', 'publish-npm 使用 npm environment（可加审批）', 'publish-npm 应设置 environment: npm');
  expect(report, publishNpm.permissions?.['id-token'] === 'write', 'publish-npm 有 id-token: write（provenance）', 'publish-npm 需要 id-token: write 生成 provenance');
  const npmPublishStep = findStep(publishNpm, (step) => asString(step?.run).includes('npm publish'));
  expect(report, Boolean(npmPublishStep), '存在 npm publish 步骤', 'publish-npm 缺少 npm publish 步骤');
  expect(report, npmPublishStep?.env?.NODE_AUTH_TOKEN === '${{ secrets.NPM_TOKEN }}', 'npm publish 用 NODE_AUTH_TOKEN 注入 token', 'npm publish 步骤必须用 env.NODE_AUTH_TOKEN: ${{ secrets.NPM_TOKEN }}');
  expect(report, asString(npmPublishStep?.run).includes('--provenance'), 'npm publish 生成 provenance', 'npm publish 应带 --provenance（id-token 已开）');

  // publish-pypi：非 dry-run + 目标匹配；trusted publisher（OIDC）而不是 token。
  const publishPypi = jobs['publish-pypi'] ?? {};
  const pypiCondition = asString(publishPypi.if);
  expect(
    report,
    pypiCondition.includes("needs.preflight.outputs.publish == 'true'"),
    'publish-pypi 需要 preflight publish=true（非 dry-run）',
    'publish-pypi.if 必须要求 needs.preflight.outputs.publish == true',
  );
  expect(
    report,
    pypiCondition.includes("needs.preflight.outputs.python == 'true'"),
    'publish-pypi 只发布 Python 目标',
    'publish-pypi.if 必须要求 needs.preflight.outputs.python == true',
  );
  expect(report, asArray(publishPypi.needs).includes('preflight') && asArray(publishPypi.needs).includes('python-sdk'), 'publish-pypi 依赖 preflight + python-sdk', 'publish-pypi.needs 必须包含 preflight 和 python-sdk');
  expect(report, publishPypi.environment === 'pypi', 'publish-pypi 使用 pypi environment（可加审批）', 'publish-pypi 应设置 environment: pypi');
  expect(report, publishPypi.permissions?.['id-token'] === 'write', 'publish-pypi 有 id-token: write（trusted publisher）', 'publish-pypi 需要 id-token: write（OIDC trusted publisher）');
  const pypiPublishStep = findStep(publishPypi, (step) => asString(step?.uses).startsWith('pypa/gh-action-pypi-publish@'));
  expect(report, Boolean(pypiPublishStep), '用 pypa/gh-action-pypi-publish 上传', 'publish-pypi 缺少 pypa/gh-action-pypi-publish 步骤');
  expect(report, !pypiPublishStep?.with?.password, 'PyPI 上传不使用 password secret', 'PyPI 上传不应传 password（用 trusted publisher，不硬编码/token secret）');
  expect(report, asString(pypiPublishStep?.with?.['packages-dir']).includes('dist/sdk-python'), 'PyPI 上传 dist/sdk-python', 'PyPI 上传的 packages-dir 应指向构建产物目录');
  expect(report, !jobUsesAction(typescript, 'pypa/gh-action-pypi-publish') && !jobUsesAction(python, 'pypa/gh-action-pypi-publish'), 'publish 动作只出现在 publish job', 'build job 里出现了发布动作');

  // ---------- secret 位置与硬编码 ----------
  // 允许出现 ${{ secrets.* }} 的只有两处：preflight 的发布计划步骤（判断 NPM_TOKEN
  // 是否存在）和 publish-npm 的发布步骤（把 token 注入 NODE_AUTH_TOKEN）。其余位置
  // 出现 secret 都视为流程被改坏的关键信号。
  const allowedSecretPaths = new Set();
  const planIndex = (preflight.steps ?? []).indexOf(planStep);
  if (planIndex >= 0) allowedSecretPaths.add(`jobs.preflight.steps[${planIndex}].env.NPM_TOKEN`);
  const npmPublishIndex = (publishNpm.steps ?? []).indexOf(npmPublishStep);
  if (npmPublishIndex >= 0) allowedSecretPaths.add(`jobs.publish-npm.steps[${npmPublishIndex}].env.NODE_AUTH_TOKEN`);

  const strings = collectStrings(doc, '', []);
  for (const { path, value } of strings) {
    if (value.includes('${{ secrets.')) {
      expect(report, allowedSecretPaths.has(path), `secret 只出现在允许位置：${path}`, `secret 出现在非预期位置：${path}`);
    }
    for (const [pattern, label] of SECRET_PATTERNS) {
      if (pattern.test(value)) {
        report.errors.push(`疑似硬编码凭据（${label}）出现在 ${path}`);
      }
    }
  }
  const nonPublishRuns = stepRuns(preflight).concat(stepRuns(typescript), stepRuns(python));
  expect(
    report,
    !nonPublishRuns.some((run) => run.includes('npm publish')),
    'preflight/build 步骤没有 npm publish',
    'preflight/build 步骤里出现了 npm publish',
  );

  return report;
}

export function verifyReleaseWorkflowFile(file = resolve(REPO_ROOT, WORKFLOW_PATH)) {
  const text = readFileSync(file, 'utf8');
  return verifyReleaseWorkflow(text);
}

function main() {
  const argv = process.argv.slice(2);
  let file = resolve(REPO_ROOT, WORKFLOW_PATH);
  for (let index = 0; index < argv.length; index += 1) {
    if (argv[index] === '--file') file = resolve(argv[++index] ?? '');
    else if (argv[index] === '-h' || argv[index] === '--help') {
      console.log('用法：node scripts/sdk/verify-release-workflow.mjs [--file <path>]');
      return;
    } else {
      console.error(`未知参数：${argv[index]}`);
      process.exitCode = 2;
      return;
    }
  }
  let report;
  try {
    report = verifyReleaseWorkflowFile(file);
  } catch (error) {
    console.error(`verify-release-workflow: ${error.message}`);
    process.exitCode = 2;
    return;
  }
  console.log(`verify-release-workflow: ${file}`);
  for (const line of report.checks) console.log(`  ✓ ${line}`);
  for (const line of report.warnings) console.log(`  ! ${line}`);
  for (const line of report.errors) console.error(`  ✗ ${line}`);
  if (report.errors.length > 0) {
    console.error(`verify-release-workflow: FAILED（${report.errors.length} 个错误）`);
    process.exitCode = 1;
    return;
  }
  console.log(`verify-release-workflow: OK（${report.checks.length} 项检查通过）`);
}

const invokedPath = process.argv[1] ? pathToFileURL(resolve(process.argv[1])).href : '';
if (import.meta.url === invokedPath) {
  main();
}
