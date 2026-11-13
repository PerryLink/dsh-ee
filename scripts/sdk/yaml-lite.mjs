// yaml-lite：给发布流程自检用的极简 YAML 子集解析器（零依赖）。
//
// 仓库没有引入 YAML 解析依赖，而发布工作流需要在没有网络/没有 npm install 的
// preflight 里也能被解析和校验（`scripts/sdk/verify-release-workflow.mjs`）。
// 这个解析器只覆盖 `.github/workflows/*.yml` 常用的子集：
//
//   - 缩进映射与序列（只用空格缩进，2 空格一层；制表符缩进报错）
//   - 标量：单/双引号字符串、true/false/null、数字、简单 flow 序列 `[a, b]`
//   - 块标量：`|`（保留换行）与 `>`（折叠为空格）
//   - 行注释与整行注释；`---` / `...` 文档标记
//
// 明确不支持：锚点/别名（`&`/`*`）、复杂 flow map、多文档、sexagesimal 等 YAML 1.1
// 特性。解析失败会抛出带行号的 YamlParseError，避免静默放过语法错误。
// YAML 1.2 语义：`on`/`yes`/`no` 一律当字符串（键名 `on:` 必须保持字符串）。

export class YamlParseError extends Error {
  constructor(message, line) {
    super(`${message}（第 ${line} 行）`);
    this.name = 'YamlParseError';
    this.line = line;
  }
}

const BOOLEANS = new Map([
  ['true', true],
  ['false', false],
]);

function isBlank(raw) {
  return raw.trim() === '';
}

function isComment(raw) {
  return raw.trimStart().startsWith('#');
}

function isDocumentMarker(raw) {
  const trimmed = raw.trim();
  return trimmed === '---' || trimmed === '...';
}

function indentOf(raw, lineNo) {
  let index = 0;
  while (index < raw.length && raw[index] === ' ') index += 1;
  if (raw[index] === '\t') {
    throw new YamlParseError('缩进里不允许使用制表符', lineNo);
  }
  return index;
}

// 去掉 plain/scalar 行尾注释：只有引号外的 ` #` 才作为注释起点。
function stripTrailingComment(text) {
  let quoted = null;
  for (let i = 0; i < text.length; i += 1) {
    const ch = text[i];
    if (quoted) {
      if (ch === '\\' && quoted === '"') {
        i += 1;
      } else if (ch === quoted) {
        quoted = null;
      }
    } else if (ch === '"' || ch === "'") {
      quoted = ch;
    } else if (ch === '#' && (i === 0 || text[i - 1] === ' ' || text[i - 1] === '\t')) {
      return text.slice(0, i).trimEnd();
    }
  }
  return text;
}

function splitFlowSequence(text) {
  const items = [];
  let current = '';
  let quoted = null;
  let depth = 0;
  for (let i = 0; i < text.length; i += 1) {
    const ch = text[i];
    if (quoted) {
      current += ch;
      if (ch === '\\' && quoted === '"') {
        current += text[i + 1] ?? '';
        i += 1;
      } else if (ch === quoted) {
        quoted = null;
      }
    } else if (ch === '"' || ch === "'") {
      quoted = ch;
      current += ch;
    } else if (ch === '[') {
      depth += 1;
      current += ch;
    } else if (ch === ']') {
      depth -= 1;
      current += ch;
    } else if (ch === ',' && depth === 0) {
      items.push(current.trim());
      current = '';
    } else {
      current += ch;
    }
  }
  if (current.trim() !== '') items.push(current.trim());
  return items;
}

// 找到映射键值分隔用的 `:`（引号外、且后面是空格或行尾）。
function findKeySeparator(text) {
  let quoted = null;
  for (let i = 0; i < text.length; i += 1) {
    const ch = text[i];
    if (quoted) {
      if (ch === '\\' && quoted === '"') i += 1;
      else if (ch === quoted) quoted = null;
      continue;
    }
    if (ch === '"' || ch === "'") {
      quoted = ch;
      continue;
    }
    if (ch === ':' && (i + 1 === text.length || text[i + 1] === ' ' || text[i + 1] === '\t')) {
      return i;
    }
  }
  return -1;
}

function unquoteKey(key, lineNo) {
  const text = key.trim();
  if (text.length >= 2 && text.startsWith("'") && text.endsWith("'")) {
    return text.slice(1, -1).replaceAll("''", "'");
  }
  if (text.length >= 2 && text.startsWith('"') && text.endsWith('"')) {
    try {
      return JSON.parse(text);
    } catch {
      throw new YamlParseError(`无法解析双引号键 ${text}`, lineNo);
    }
  }
  return text;
}

function parseScalar(text, lineNo) {
  const raw = text.trim();
  if (raw.startsWith("'") && raw.endsWith("'") && raw.length >= 2) {
    return raw.slice(1, -1).replaceAll("''", "'");
  }
  if (raw.startsWith('"') && raw.endsWith('"') && raw.length >= 2) {
    try {
      return JSON.parse(raw);
    } catch {
      throw new YamlParseError(`无法解析双引号字符串 ${raw}`, lineNo);
    }
  }
  if (raw.startsWith('[')) {
    if (!raw.endsWith(']')) {
      throw new YamlParseError('不支持的跨行 flow 序列', lineNo);
    }
    const inner = raw.slice(1, -1).trim();
    if (inner === '') return [];
    return splitFlowSequence(inner).map((item) => parseScalar(item, lineNo));
  }
  if (raw.startsWith('{')) {
    throw new YamlParseError('不支持 flow map 写法，请用缩进映射', lineNo);
  }
  if (raw.startsWith('&') || raw.startsWith('*')) {
    throw new YamlParseError('不支持 YAML 锚点/别名', lineNo);
  }
  if (BOOLEANS.has(raw)) return BOOLEANS.get(raw);
  if (raw === 'null' || raw === '~') return null;
  if (/^-?(?:0|[1-9]\d*)(?:\.\d+)?$/.test(raw)) return Number(raw);
  return raw;
}

export function parseYaml(source) {
  const rawLines = String(source).replace(/\r\n?/g, '\n').split('\n');
  if (rawLines.length > 0 && rawLines[0].startsWith('\uFEFF')) {
    rawLines[0] = rawLines[0].slice(1);
  }
  let cursor = 0;

  function peek() {
    while (cursor < rawLines.length) {
      const raw = rawLines[cursor];
      if (isBlank(raw) || isComment(raw) || isDocumentMarker(raw)) {
        cursor += 1;
        continue;
      }
      const indent = indentOf(raw, cursor + 1);
      return { raw, indent, lineNo: cursor + 1 };
    }
    return null;
  }

  function parseNode(minIndent) {
    const line = peek();
    if (!line || line.indent < minIndent) return null;
    const content = line.raw.slice(line.indent);
    if (content === '-' || content.startsWith('- ')) return parseSequence(line.indent);
    return parseMapping(line.indent);
  }

  function parseChild(parentIndent) {
    const line = peek();
    if (!line || line.indent <= parentIndent) return null;
    return parseNode(line.indent);
  }

  function parseMapping(indent) {
    const result = {};
    while (true) {
      const line = peek();
      if (!line) break;
      if (line.indent < indent) break;
      if (line.indent > indent) {
        throw new YamlParseError('映射条目缩进不一致', line.lineNo);
      }
      const content = line.raw.slice(indent);
      if (content === '-' || content.startsWith('- ')) {
        throw new YamlParseError('映射位置出现了序列条目', line.lineNo);
      }
      const separator = findKeySeparator(content);
      if (separator < 0) {
        throw new YamlParseError(`不是合法的 key: value 行：${content}`, line.lineNo);
      }
      const key = unquoteKey(content.slice(0, separator), line.lineNo);
      const rest = stripTrailingComment(content.slice(separator + 1)).trim();
      cursor += 1;
      if (key in result) {
        throw new YamlParseError(`重复的键 ${key}`, line.lineNo);
      }
      if (rest === '') {
        result[key] = parseChild(indent);
        continue;
      }
      if (/^[|>][+-]?$/.test(rest)) {
        result[key] = parseBlockScalar(indent, rest, line.lineNo);
        continue;
      }
      result[key] = parseScalar(rest, line.lineNo);
    }
    return result;
  }

  function parseSequence(indent) {
    const result = [];
    while (true) {
      const line = peek();
      if (!line) break;
      if (line.indent < indent) break;
      if (line.indent > indent) {
        throw new YamlParseError('序列条目缩进不一致', line.lineNo);
      }
      const content = line.raw.slice(indent);
      if (content !== '-' && !content.startsWith('- ')) break;
      const rest = content === '-' ? '' : content.slice(2);
      if (rest === '') {
        cursor += 1;
        result.push(parseChild(indent));
        continue;
      }
      // `- key: value`：把首行补成缩进映射，复用 parseMapping 解析该项和后续同层行。
      const itemIndent = indent + 2;
      const separator = findKeySeparator(rest);
      if (separator >= 0 && !rest.startsWith('[') && !rest.startsWith('{')) {
        rawLines[cursor] = ' '.repeat(itemIndent) + rest;
        result.push(parseMapping(itemIndent));
      } else {
        cursor += 1;
        result.push(parseScalar(rest, line.lineNo));
      }
    }
    return result;
  }

  function parseBlockScalar(parentIndent, indicator, lineNo) {
    const style = indicator[0];
    const chomping = indicator[1] ?? '';
    const lines = [];
    let blockIndent = null;
    while (cursor < rawLines.length) {
      const raw = rawLines[cursor];
      if (isBlank(raw)) {
        lines.push('');
        cursor += 1;
        continue;
      }
      const indent = indentOf(raw, cursor + 1);
      if (blockIndent === null) {
        if (indent <= parentIndent) break;
        blockIndent = indent;
      }
      if (indent < blockIndent) break;
      lines.push(raw.slice(blockIndent));
      cursor += 1;
    }
    if (blockIndent === null) {
      throw new YamlParseError('块标量缺少内容', lineNo);
    }
    // 尾部空行按 chomping 处理：默认 clip 保留一个换行，`-` 去掉，`+` 全部保留。
    let trailing = 0;
    while (trailing < lines.length && lines[lines.length - 1 - trailing] === '') trailing += 1;
    const body = lines.slice(0, lines.length - trailing);
    let text;
    if (style === '|') {
      text = body.join('\n');
    } else {
      const folded = [];
      for (const line of body) {
        if (line === '') {
          folded.push('\n');
        } else if (folded.length > 0 && folded[folded.length - 1] !== '\n') {
          folded[folded.length - 1] += ` ${line}`;
        } else {
          folded.push(line);
        }
      }
      text = folded.join('');
    }
    if (chomping === '-') return text;
    if (chomping === '+') return `${text}\n${'\n'.repeat(trailing)}`;
    return text === '' ? '' : `${text}\n`;
  }

  const first = peek();
  if (!first) return null;
  if (first.indent !== 0) {
    throw new YamlParseError('顶层内容必须从第 0 列开始', first.lineNo);
  }
  const value = parseNode(0);
  const leftover = peek();
  if (leftover) {
    throw new YamlParseError(`顶层内容后还有无法解析的行：${leftover.raw.trim()}`, leftover.lineNo);
  }
  return value;
}
