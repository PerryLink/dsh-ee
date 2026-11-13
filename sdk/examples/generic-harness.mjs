// 通用 Harness 示例（可运行）：用一个统一令牌走完
// 发现 → 能力摘要 → 模型网关（非流式 + 流式）→ MCP 工具。
//
// 需要先在仓库内构建 SDK：
//   cd sdk/typescript && npm ci --no-audit --no-fund && npm run build
//
// 运行（DSH_EE_TOKEN 至少需要 models + connections；要 ee_* 工具再加 agent）：
//   DSH_EE_BASE='https://dsh-ee.example.com' DSH_EE_TOKEN='ee_at_…' \
//     node sdk/examples/generic-harness.mjs
//
// 未发布 npm；直接从本仓库构建产物引入。
import {
  DshApiError,
  DshHarnessClient,
  DshJsonRpcError,
  extractConfirmation,
} from '../typescript/dist/index.js';

const baseUrl = process.env.DSH_EE_BASE;
const token = process.env.DSH_EE_TOKEN;
if (!baseUrl || !token) {
  console.error('需要 DSH_EE_BASE 与 DSH_EE_TOKEN');
  process.exit(2);
}

const client = new DshHarnessClient({
  baseUrl,
  token,
  onResponse: (info) => {
    if (info.requestId) console.error(`[${info.requestId}] ${info.method} ${info.path} → ${info.status}`);
  },
});

try {
  // 1) 发现与能力（#123 合并后才存在；旧部署会抛 404）
  const discovery = await client.discovery().catch((error) => {
    console.error('发现端点不可用（服务端未合并 #123？）：', error.message);
    return null;
  });
  if (discovery) {
    console.error(`服务端 ${discovery.name} ${discovery.version}，MCP ${discovery.mcp.protocol_versions.join('/')}`);
  }

  const capabilities = await client.capabilities().catch(() => null);
  if (capabilities?.authenticated) {
    console.error(`当前用户 ${capabilities.user?.username}，可用模型 ${capabilities.models.length} 个`);
  }

  // 2) 模型网关（OpenAI chat completions 子集）
  const models = await client.listModels();
  if (!models.length) throw new Error('当前令牌没有可用模型');
  const model = models[0].id;
  console.error(`使用模型 ${model}`);

  const completion = await client.createChatCompletion({
    model,
    messages: [{ role: 'user', content: '用一句话介绍你自己' }],
  });
  console.log('非流式回答：', completion.choices?.[0]?.message?.content);

  process.stdout.write('流式回答：');
  for await (const chunk of client.streamChatCompletion({
    model,
    messages: [{ role: 'user', content: '数到三' }],
  })) {
    const text = chunk.choices?.[0]?.delta?.content;
    if (text) process.stdout.write(text);
  }
  process.stdout.write('\n');

  // 3) MCP 工具
  const session = await client.connectMcp();
  console.error(`MCP ${session.serverInfo?.name}，会话 ${session.sessionId}`);
  const tools = await client.listAllTools();
  console.error(`工具 ${tools.length} 个：${tools.slice(0, 5).map((tool) => tool.name).join(', ')}…`);

  const me = tools.find((tool) => tool.name === 'ee_me');
  if (me) {
    const result = await client.callTool('ee_me');
    console.log('ee_me：', result.content.map((item) => item.text ?? '').join('\n'));
    const confirmation = extractConfirmation(result);
    if (confirmation) {
      console.log(`该调用需要人工确认：${confirmation.url}（${confirmation.expires_at} 前有效）`);
    }
  }

  await client.close();
} catch (error) {
  if (error instanceof DshApiError || error instanceof DshJsonRpcError) {
    const code = error instanceof DshJsonRpcError ? error.rpcCode : error.apiCode;
    console.error(`请求失败 status=${error.status} request-id=${error.requestId ?? '-'} code=${code ?? '-'}`);
    if (error.isRateLimited) {
      console.error(`被限流，${error.retryAfter ?? 60}s 后重试（限流余量 ${error.rateLimit?.remaining}）`);
    }
  } else {
    console.error(error);
  }
  process.exitCode = 1;
}
