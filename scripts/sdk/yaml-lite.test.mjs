// yaml-lite 的单元测试：覆盖发布工作流用到的 YAML 子集与常见错误。
import { test } from 'node:test';
import assert from 'node:assert/strict';

import { parseYaml, YamlParseError } from './yaml-lite.mjs';

test('解析嵌套映射 / 序列 / 引号 / 数字 / 布尔', () => {
  const doc = parseYaml(`name: release-sdk
on:
  workflow_dispatch:
    inputs:
      dry_run:
        type: boolean
        default: true
      package:
        options:
          - both
          - typescript
  push:
    tags:
      - 'sdk-ts-v*'
      - "sdk-py-v*"
jobs:
  preflight:
    runs-on: ubuntu-latest
    timeout: 30
`);
  assert.equal(doc.name, 'release-sdk');
  // YAML 1.2 语义：键 on 保持字符串，而不是 YAML 1.1 的 true
  assert.deepEqual(Object.keys(doc.on), ['workflow_dispatch', 'push']);
  assert.equal(doc.on.workflow_dispatch.inputs.dry_run.default, true);
  assert.deepEqual(doc.on.workflow_dispatch.inputs.package.options, ['both', 'typescript']);
  assert.deepEqual(doc.on.push.tags, ['sdk-ts-v*', 'sdk-py-v*']);
  assert.equal(doc.jobs.preflight['runs-on'], 'ubuntu-latest');
  assert.equal(doc.jobs.preflight.timeout, 30);
});

test('解析块标量 | 与 fold >（保留 shell 里的 # 和空行）', () => {
  const doc = parseYaml(`run: |
    set -euo pipefail
    # 这是 shell 注释，不是 YAML 注释
    node scripts/sdk/release-plan.mjs

    echo done
folded: >
  a
  b

  c
`);
  assert.equal(doc.run, 'set -euo pipefail\n# 这是 shell 注释，不是 YAML 注释\nnode scripts/sdk/release-plan.mjs\n\necho done\n');
  assert.equal(doc.folded, 'a b\nc\n');
});

test('解析 flow 序列与行尾注释', () => {
  const doc = parseYaml(`options: [both, typescript, python] # 注释
empty: []
value: 'a # not comment'
`);
  assert.deepEqual(doc.options, ['both', 'typescript', 'python']);
  assert.deepEqual(doc.empty, []);
  assert.equal(doc.value, 'a # not comment');
});

test('忽略整行注释、空行与文档标记', () => {
  const doc = parseYaml(`# 头部注释
---
a: 1

b: two
...
`);
  assert.deepEqual(doc, { a: 1, b: 'two' });
});

test('拒绝制表符缩进', () => {
  assert.throws(() => parseYaml('a:\n\tb: 1\n'), (error) => error instanceof YamlParseError && /制表符/.test(error.message));
});

test('拒绝缩进不一致与非法 key: value 行', () => {
  assert.throws(() => parseYaml('a:\n    b: 1\n  c: 2\n'), (error) => error instanceof YamlParseError);
  assert.throws(() => parseYaml('a:\n  just-a-scalar\n'), (error) => error instanceof YamlParseError && /key: value/.test(error.message));
});

test('拒绝不支持的 flow map 与锚点', () => {
  assert.throws(() => parseYaml('a: {b: 1}\n'), (error) => error instanceof YamlParseError && /flow map/.test(error.message));
  assert.throws(() => parseYaml('a: &anchor 1\n'), (error) => error instanceof YamlParseError && /锚点/.test(error.message));
});

test('顶层必须从第 0 列开始', () => {
  assert.throws(() => parseYaml('  a: 1\n'), (error) => error instanceof YamlParseError && /第 0 列/.test(error.message));
});
