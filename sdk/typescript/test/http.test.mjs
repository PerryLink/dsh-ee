// 限流/重试头解析的单元测试（导出给调用方直接使用的纯函数）。

import { test } from 'node:test';
import assert from 'node:assert/strict';

import { parseRateLimitHeaders, parseRetryAfter } from '../dist/index.js';

test('parseRateLimitHeaders：三个头齐全时解析并换算 resetAt', () => {
  const info = parseRateLimitHeaders(
    new Headers({
      'X-RateLimit-Limit': '60',
      'X-RateLimit-Remaining': '42',
      'X-RateLimit-Reset': '1700000000',
    }),
  );
  assert.deepEqual(info, {
    limit: 60,
    remaining: 42,
    reset: 1700000000,
    resetAt: new Date(1700000000 * 1000),
  });
});

test('parseRateLimitHeaders：缺头或非法值返回 undefined', () => {
  assert.equal(parseRateLimitHeaders(new Headers()), undefined);
  assert.equal(
    parseRateLimitHeaders(
      new Headers({ 'X-RateLimit-Limit': '60', 'X-RateLimit-Remaining': '42' }),
    ),
    undefined,
  );
  assert.equal(
    parseRateLimitHeaders(
      new Headers({
        'X-RateLimit-Limit': 'many',
        'X-RateLimit-Remaining': '42',
        'X-RateLimit-Reset': '1700000000',
      }),
    ),
    undefined,
  );
});

test('parseRetryAfter：秒数、HTTP-date 与非法值', () => {
  assert.equal(parseRetryAfter(new Headers({ 'Retry-After': '30' })), 30);
  assert.equal(parseRetryAfter(new Headers({ 'Retry-After': '0' })), 0);
  assert.equal(parseRetryAfter(new Headers({ 'Retry-After': '1.2' })), 2);
  assert.equal(parseRetryAfter(new Headers()), undefined);
  assert.equal(parseRetryAfter(new Headers({ 'Retry-After': 'not-a-value' })), undefined);

  const future = new Date(Date.now() + 60_000).toUTCString();
  const fromDate = parseRetryAfter(new Headers({ 'Retry-After': future }));
  assert.ok(fromDate >= 55 && fromDate <= 61, `HTTP-date 应换算成约 60 秒，实际 ${fromDate}`);
});
