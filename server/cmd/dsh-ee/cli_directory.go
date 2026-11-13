package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/hikariming/dsh-ee/server/internal/directory"
	"github.com/hikariming/dsh-ee/server/internal/extension"
)

// 企业目录的签名（docs/plugin-signing.md §8）：企业运维/平台组本地用 Ed25519 私钥给
// dsh-ee-directory.json 整体签名，管理员把签好的 JSON 贴进管理接口。私钥只在企业自己手里，
// dsh-ee 不生成、不托管、不入库；服务端只保存受信任的公钥（和扩展签名共用 trust root）。

// directoryHostsEnv 和 config 里同一个名字：tarball 来源的主机白名单。
const directoryHostsEnv = "DSH_EE_DIRECTORY_TARBALL_HOSTS"

// cliDirectory 处理 `dsh-ee directory sign|verify`。
func cliDirectory(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, directoryUsage)
		return 2
	}
	switch args[0] {
	case "-h", "--help", "help":
		fmt.Fprint(stdout, directoryUsage)
		return 0
	case "sign":
		return cliDirectorySign(args[1:], stdout, stderr)
	case "verify":
		return cliDirectoryVerify(args[1:], stdout, stderr)
	}
	fmt.Fprintf(stderr, "不认识的命令 dsh-ee directory %s\n\n%s", args[0], directoryUsage)
	return 2
}

const directoryUsage = `用法：
  dsh-ee directory sign <文件> --key <私钥文件> [-o 文件] [--tarball-hosts 主机,...] [--json]
                         给企业目录（dsh-ee-directory.json）整体签名；默认原地写回
  dsh-ee directory verify <文件> [--key 公钥文件] [--tarball-hosts 主机,...] [--json]
                         核对目录签名与条目；没有签名或验不过时退出码为 1

目录格式见 docs/plugin-signing.md §8（format 2）。目录里包含插件（npm 包）和扩展条目，
tarball 来源必须是 https 地址；内网对象存储要把主机写进 DSH_EE_DIRECTORY_TARBALL_HOSTS
（或 --tarball-hosts）白名单。受信任的签名公钥用 --key 给文件，或设
DSH_EE_EXTENSION_TRUSTED_KEYS（和服务端同名；目录签名复用同一套信任根）。
`

// directoryHosts 取 tarball 主机白名单：命令行参数优先，其次环境变量。
func directoryHosts(flagValue string) ([]string, error) {
	raw := flagValue
	if raw == "" {
		raw = os.Getenv(directoryHostsEnv)
	}
	if raw == "" {
		return nil, nil
	}
	hosts, err := directory.ParseTarballHosts(raw)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", directoryHostsEnv, err)
	}
	return hosts, nil
}

// printDirectoryIssues 打印逐条校验结果。
func printDirectoryIssues(w io.Writer, r *directory.Result) {
	if len(r.Issues) == 0 {
		return
	}
	sev := map[string]string{"error": "错误", "warning": "提醒"}
	for _, is := range r.Issues {
		where := is.Field
		if is.Index >= 0 {
			where = fmt.Sprintf("第 %d 条 %s", is.Index+1, is.Field)
		}
		if where != "" {
			where = "（" + where + "）"
		}
		fmt.Fprintf(w, "  [%s] %s %s%s\n", sev[is.Severity], is.Code, is.Message, where)
	}
}

func cliDirectorySign(args []string, stdout, stderr io.Writer) int {
	rest, vals, on, err := options(args, []string{"--key", "-o", "--out", "--tarball-hosts"}, []string{"--json"})
	if err != nil || len(rest) != 1 || vals["--key"] == "" {
		if err != nil {
			fmt.Fprintln(stderr, err)
		}
		fmt.Fprintln(stderr, "用法：dsh-ee directory sign <文件> --key <私钥文件> [-o 文件] [--tarball-hosts 主机,...] [--json]")
		return 2
	}
	path := rest[0]
	raw, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintln(stderr, "读目录：", err)
		return 1
	}
	doc, err := directory.Parse(raw)
	if err != nil {
		fmt.Fprintln(stderr, "目录不对：", err)
		return 1
	}
	hosts, err := directoryHosts(vals["--tarball-hosts"])
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	res := directory.Validate(doc, directory.Options{TarballHosts: hosts})
	if !res.OK {
		fmt.Fprintf(stderr, "目录有 %d 个错误，不签名：\n", res.Errors)
		printDirectoryIssues(stderr, res)
		return 1
	}
	if res.Warnings > 0 {
		fmt.Fprintf(stderr, "目录有 %d 个提醒（不影响签名）：\n", res.Warnings)
		printDirectoryIssues(stderr, res)
	}
	priv, err := extension.LoadPrivateKey(vals["--key"])
	if err != nil {
		fmt.Fprintln(stderr, "读私钥：", err)
		return 1
	}
	if err := directory.SignDocument(doc, priv); err != nil {
		fmt.Fprintln(stderr, "签名失败：", err)
		return 1
	}
	out := vals["-o"]
	if out == "" {
		out = vals["--out"]
	}
	if out == "" {
		out = path
	}
	body, err := marshalDirectory(doc)
	if err != nil {
		fmt.Fprintln(stderr, "写目录：", err)
		return 1
	}
	if err := os.WriteFile(out, body, 0o644); err != nil {
		fmt.Fprintln(stderr, "写目录：", err)
		return 1
	}
	if on["--json"] {
		printJSON(stdout, map[string]any{
			"file": out, "directory_id": doc.DirectoryID, "entries": len(doc.Entries),
			"key_id": doc.Signature.KeyID, "digest": doc.Signature.Digest,
			"warnings": res.Warnings,
		})
		return 0
	}
	fmt.Fprintf(stdout, "已签名：%s\n", out)
	fmt.Fprintf(stdout, "目录：%s（%d 条）\n签名者 key id：%s\n内容摘要：%s\n", doc.DirectoryID, len(doc.Entries), doc.Signature.KeyID, doc.Signature.Digest)
	fmt.Fprintln(stdout, "\n把公钥交给管理员加进受信任公钥（purpose=directory 或 extension），再把这份 JSON 贴进 /api/admin/directory/check 校验、apply 登记。")
	return 0
}

func cliDirectoryVerify(args []string, stdout, stderr io.Writer) int {
	rest, vals, on, err := options(args, []string{"--key", "--tarball-hosts"}, []string{"--json"})
	if err != nil || len(rest) != 1 {
		if err != nil {
			fmt.Fprintln(stderr, err)
		}
		fmt.Fprintln(stderr, "用法：dsh-ee directory verify <文件> [--key 公钥文件] [--tarball-hosts 主机,...] [--json]")
		return 2
	}
	path := rest[0]
	raw, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintln(stderr, "读目录：", err)
		return 1
	}
	doc, err := directory.Parse(raw)
	if err != nil {
		fmt.Fprintln(stderr, "目录不对：", err)
		return 1
	}
	hosts, err := directoryHosts(vals["--tarball-hosts"])
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	res := directory.Validate(doc, directory.Options{TarballHosts: hosts})
	var trusted []extension.TrustedKey
	keyFile := vals["--key"]
	if keyFile == "" {
		keyFile = os.Getenv(trustedKeysEnv)
	}
	if keyFile != "" {
		pubs, err := extension.LoadPublicKeys(keyFile)
		if err != nil {
			fmt.Fprintln(stderr, "读受信任公钥：", err)
			return 1
		}
		trusted = extension.ActiveKeys(pubs)
	}
	sig := directory.VerifyDocument(doc, trusted)
	ok := res.OK && sig.Status == directory.SignatureValid
	if on["--json"] {
		printJSON(stdout, map[string]any{
			"file": path, "directory_id": doc.DirectoryID, "entries": len(doc.Entries),
			"ok": ok, "signature": sig, "errors": res.Errors, "warnings": res.Warnings, "issues": res.Issues,
		})
	} else {
		printDirectoryIssues(stdout, res)
		if sig.KeyID != "" {
			fmt.Fprintf(stdout, "签名：%s（key id %s，公钥 %s）\n", sig.Status, sig.KeyID, sig.KeyState)
		} else {
			fmt.Fprintf(stdout, "签名：%s\n", sig.Status)
		}
		if sig.Message != "" {
			fmt.Fprintln(stdout, sig.Message)
		}
		fmt.Fprintf(stdout, "条目：%d 条，%d 个错误，%d 个提醒\n", len(doc.Entries), res.Errors, res.Warnings)
		if ok {
			fmt.Fprintf(stdout, "通过：%s\n", doc.DirectoryID)
		}
	}
	if !ok {
		return 1
	}
	return 0
}

// marshalDirectory 把目录写成带缩进的 JSON（签名对 canonical payload，缩进不影响验签）。
func marshalDirectory(doc *directory.Document) ([]byte, error) {
	raw, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(raw, '\n'), nil
}
