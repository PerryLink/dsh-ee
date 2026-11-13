#!/usr/bin/env node
/**
 * dsh-ee-local 命令入口。
 *
 * npm 安装后 bin 是指向本文件的符号链接；Node 会把 import.meta.url 解析成真实路径，
 * 所以入口判断放在这个独立文件里直接执行，cli.ts 只导出 main（被 import 时不会跑）。
 */
import { main } from "./cli.js";

main(process.argv.slice(2)).then(
  (code) => process.exit(code),
  (error) => {
    process.stderr.write(`${error instanceof Error ? error.message : String(error)}\n`);
    process.exit(1);
  },
);
