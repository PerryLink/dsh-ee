package main

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/hikariming/dsh-ee/server/internal/extension"
	"github.com/hikariming/dsh-ee/server/internal/extension/exttest"
)

// 写扩展时用的其余命令：新建、跑起来测、模拟环境、调一次、打包、交付说明（docs/plugins.md §10、docs/agent-dev.md §4～§6）。

var extCommands = map[string]func(args []string, stdout, stderr io.Writer) int{
	"init":   cliExtInit,
	"test":   cliExtTest,
	"dev":    cliExtDev,
	"call":   cliExtCall,
	"pack":   cliExtPack,
	"report": cliExtReport,
	"keygen": cliExtKeygen,
	"sign":   cliExtSign,
	"verify": cliExtVerify,
}

// options 把参数分成普通参数、带值的选项（--key 值 或 --key=值）和开关。
func options(args []string, withValue, switches []string) (rest []string, vals map[string]string, on map[string]bool, err error) {
	vals, on = map[string]string{}, map[string]bool{}
	has := func(list []string, x string) bool {
		for _, v := range list {
			if v == x {
				return true
			}
		}
		return false
	}
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !strings.HasPrefix(a, "-") || a == "-" {
			rest = append(rest, a)
			continue
		}
		name, value, inline := strings.Cut(a, "=")
		switch {
		case has(switches, name) && !inline:
			on[name] = true
		case has(withValue, name):
			if !inline {
				if i+1 >= len(args) {
					return nil, nil, nil, fmt.Errorf("%s 后面要跟一个值", name)
				}
				i++
				value = args[i]
			}
			vals[name] = value
		default:
			return nil, nil, nil, fmt.Errorf("不认识的参数 %s", name)
		}
	}
	return rest, vals, on, nil
}

func isURL(s string) bool { return strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://") }

func parseJSONObject(raw, what string) (map[string]any, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, fmt.Errorf("%s 要是一个 JSON 对象：%w", what, err)
	}
	return out, nil
}

func printJSON(w io.Writer, v any) {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

// ---- test ----

// syncBuffer 收扩展进程的输出，测试结束后查凭据有没有被打进日志。
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.b.Len() < 4<<20 {
		s.b.Write(p)
	}
	return len(p), nil
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func freeAddr() (string, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	defer ln.Close()
	return ln.Addr().String(), nil
}

// startExtension 用 run 命令把扩展跑起来，等它能取到清单。返回它的地址、日志和停掉它的函数。
func startExtension(dir, run, eeURL string, stderr io.Writer) (string, *syncBuffer, func(), error) {
	addr, err := freeAddr()
	if err != nil {
		return "", nil, nil, err
	}
	url := "http://" + addr
	cmd := exec.Command("sh", "-c", run)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "DSH_EE_URL="+eeURL, "EXTENSION_ADDR="+addr, "EXTENSION_URL="+url, "DSH_EE_INSECURE=")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	logs := &syncBuffer{}
	cmd.Stdout, cmd.Stderr = logs, logs
	if err := cmd.Start(); err != nil {
		return "", nil, nil, fmt.Errorf("启动命令跑不起来：%w", err)
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	stop := func() {
		// 连同它起的子进程一起停（go run 会再起一个进程）
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
		select {
		case <-exited:
		case <-time.After(3 * time.Second):
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
	}
	deadline := time.Now().Add(120 * time.Second)
	hc := &http.Client{Timeout: 2 * time.Second}
	for time.Now().Before(deadline) {
		select {
		case err := <-exited:
			return "", nil, nil, fmt.Errorf("扩展启动后就退出了（%v）。它的输出：\n%s", err, tail(logs.String(), 2000))
		default:
		}
		if resp, err := hc.Get(url + "/.well-known/dsh-ee-extension.json"); err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return url, logs, stop, nil
			}
		}
		time.Sleep(300 * time.Millisecond)
	}
	stop()
	return "", nil, nil, fmt.Errorf("等了 120 秒，扩展还没在 %s 上提供清单。启动命令要让扩展监听环境变量 EXTENSION_ADDR 给的地址（SDK 的 ListenAndServe(\"\") 会这样做）。它的输出：\n%s", addr, tail(logs.String(), 2000))
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "…" + s[len(s)-n:]
}

func cliExtTest(args []string, stdout, stderr io.Writer) int {
	rest, vals, on, err := options(args, []string{"--run", "--listen", "--credential", "--config"}, []string{"--json", "--write"})
	if err != nil || len(rest) != 1 {
		if err != nil {
			fmt.Fprintln(stderr, err)
		}
		fmt.Fprintln(stderr, `用法：dsh-ee extension test <目录|地址> [--run "启动命令"] [--listen 地址] [--credential 值] [--config JSON] [--write] [--json]
  给目录：先做 check；是服务扩展的话用 --run 给的命令把它跑起来再测（启动命令会拿到 DSH_EE_URL、EXTENSION_ADDR 两个环境变量），目录下 tests/*.yaml 的场景测试一起跑
  给地址：测一个已经在跑的服务扩展；它启动时的 DSH_EE_URL 要指向这里的模拟环境（默认 http://`+exttest.DefaultListen+`）
  --write 连写工具也用合法的参数真的调一次（会改数据）`)
		return 2
	}
	cfg, err := parseJSONObject(vals["--config"], "--config")
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	target := rest[0]
	listen := vals["--listen"]
	var scenarios fs.FS
	run := vals["--run"]
	if !isURL(target) {
		st, err := os.Stat(target)
		if err != nil || !st.IsDir() {
			fmt.Fprintf(stderr, "%s 不是一个目录，也不是 http(s) 地址\n", target)
			return 2
		}
		scenarios = os.DirFS(target)
		// 目录里有清单就先做静态检查；声明式扩展到这里就测完了
		if _, err := os.Stat(filepath.Join(target, extension.ManifestFile)); err == nil {
			res := extension.Check(scenarios)
			if !res.OK || res.Kind == extension.KindDeclarative {
				if on["--json"] {
					printJSON(stdout, map[string]any{"ok": res.OK, "check": res, "note": "声明式扩展没有代码，check 通过就是合格；装进 dsh-ee 后可以用「试连」看接口通不通"})
				} else {
					printCheck(stdout, res)
					if res.OK {
						fmt.Fprintln(stdout, "声明式扩展没有代码，check 通过就是合格。")
					}
				}
				if !res.OK {
					return 1
				}
				return 0
			}
		}
		if run == "" {
			fmt.Fprintln(stderr, `这是一个服务扩展的目录：加 --run "启动命令" 让我把它跑起来，例如 --run "go run ."`)
			return 2
		}
		if listen == "" {
			// 自己起扩展时不用固定端口，CI 里几个测试能同时跑
			listen = "127.0.0.1:0"
		}
	}
	if listen == "" {
		listen = exttest.DefaultListen
	}
	env, err := exttest.Start(listen)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	defer env.Close()
	opt := exttest.Options{URL: target, Credential: vals["--credential"], Config: cfg, Write: on["--write"], Scenarios: scenarios}
	if !isURL(target) {
		url, logs, stop, err := startExtension(target, run, env.URL, stderr)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		defer stop()
		opt.URL, opt.Logs = url, logs.String
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	rep := exttest.Run(ctx, env, opt)
	if on["--json"] {
		printJSON(stdout, rep)
	} else {
		printTest(stdout, rep)
	}
	if !rep.OK {
		return 1
	}
	return 0
}

func printTest(w io.Writer, rep *exttest.Report) {
	if rep.Identity != "" {
		fmt.Fprintf(w, "%s  %s\n", rep.Identity, rep.URL)
	}
	group := ""
	mark := map[string]string{"pass": "通过", "fail": "没过", "skip": "跳过"}
	for _, c := range rep.Cases {
		if c.Group != group {
			group = c.Group
			fmt.Fprintf(w, "\n%s\n", group)
		}
		fmt.Fprintf(w, "  %s  %s\n", mark[c.Status], c.Name)
		if c.Status == "fail" {
			fmt.Fprintf(w, "        %s  %s\n", c.Code, c.Message)
			if c.Fix != "" {
				fmt.Fprintf(w, "        改法：%s\n", c.Fix)
			}
		} else if c.Status == "skip" && c.Message != "" {
			fmt.Fprintf(w, "        %s\n", c.Message)
		}
	}
	fmt.Fprintln(w)
	if rep.OK {
		fmt.Fprintf(w, "通过：%d 项通过，%d 项跳过\n", rep.Passed, rep.Skipped)
	} else {
		fmt.Fprintf(w, "没通过：%d 项没过，%d 项通过，%d 项跳过\n", rep.Failed, rep.Passed, rep.Skipped)
	}
}

// ---- dev / call ----

func cliExtDev(args []string, stdout, stderr io.Writer) int {
	rest, vals, _, err := options(args, []string{"--listen"}, nil)
	if err != nil || len(rest) != 0 {
		if err != nil {
			fmt.Fprintln(stderr, err)
		}
		fmt.Fprintln(stderr, "用法：dsh-ee extension dev [--listen 地址]")
		return 2
	}
	listen := vals["--listen"]
	if listen == "" {
		listen = exttest.DefaultListen
	}
	env, err := exttest.Start(listen)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	defer env.Close()
	fmt.Fprintf(stdout, "模拟的 dsh-ee 在 %s（只有 JWKS 和几个预置的员工，不是真的 dsh-ee）\n\n", env.URL)
	fmt.Fprintf(stdout, "另开一个终端启动扩展：\n  DSH_EE_URL=%s EXTENSION_ADDR=127.0.0.1:9000 go run .\n\n", env.URL)
	fmt.Fprintln(stdout, "然后以某位员工的身份调它：")
	fmt.Fprintf(stdout, "  dsh-ee extension call http://127.0.0.1:9000 <工具> --as zhangsan --args '{}'\n")
	fmt.Fprintf(stdout, "  dsh-ee extension test http://127.0.0.1:9000\n\n预置的员工：\n")
	for _, u := range exttest.Users {
		fmt.Fprintf(stdout, "  %-10s %s，%s，角色 %s\n", u.Username, u.Name, u.Dept, strings.Join(u.Roles, " / "))
	}
	fmt.Fprintln(stdout, "\n按 Ctrl+C 结束。")
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	<-sig
	return 0
}

func cliExtCall(args []string, stdout, stderr io.Writer) int {
	rest, vals, _, err := options(args, []string{"--as", "--args", "--credential", "--config", "--listen"}, nil)
	if err != nil || len(rest) != 2 || !isURL(rest[0]) {
		if err != nil {
			fmt.Fprintln(stderr, err)
		}
		fmt.Fprintln(stderr, "用法：dsh-ee extension call <地址> <工具> [--as 员工] [--args JSON] [--credential 值] [--config JSON] [--listen 地址]\n  扩展启动时的 DSH_EE_URL 要指向模拟环境（`dsh-ee extension dev` 打印的地址）")
		return 2
	}
	toolArgs, err := parseJSONObject(vals["--args"], "--args")
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	cfg, err := parseJSONObject(vals["--config"], "--config")
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	who := vals["--as"]
	if who == "" {
		who = exttest.Users[0].Username
	}
	user, ok := exttest.FindUser(who)
	if !ok {
		fmt.Fprintf(stderr, "模拟环境里没有员工 %s；有 zhangsan、lisi、wangwu、admin\n", who)
		return 2
	}
	listen := vals["--listen"]
	if listen == "" {
		listen = exttest.DefaultListen
	}
	// `extension dev` 开着的话端口已经被它占了：签名密钥是同一把，直接签就行
	env, err := exttest.Start(listen)
	if err != nil {
		if env, err = exttest.Start("127.0.0.1:0"); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
	}
	defer env.Close()
	base := strings.TrimRight(rest[0], "/")
	hc := &http.Client{Timeout: 30 * time.Second}
	resp, err := hc.Get(base + "/.well-known/dsh-ee-extension.json")
	if err != nil {
		fmt.Fprintln(stderr, "连不上扩展：", err)
		return 1
	}
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	resp.Body.Close()
	m, err := extension.ParseManifest(raw)
	if err != nil || m.ID == "" {
		fmt.Fprintln(stderr, "取不到扩展的清单")
		return 1
	}
	if toolArgs == nil {
		toolArgs = map[string]any{}
	}
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": rest[1], "arguments": toolArgs}})
	req, _ := http.NewRequest(http.MethodPost, base+"/mcp", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+env.Token(m.ID, user))
	if c := vals["--credential"]; c != "" {
		req.Header.Set("X-DSH-EE-Credential", c)
	}
	if cfg != nil {
		b, _ := json.Marshal(cfg)
		req.Header.Set("X-DSH-EE-Config", base64.StdEncoding.EncodeToString(b))
	}
	resp, err = hc.Do(req)
	if err != nil {
		fmt.Fprintln(stderr, "调用失败：", err)
		return 1
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	var pretty any
	if json.Unmarshal(out, &pretty) == nil {
		printJSON(stdout, pretty)
	} else {
		fmt.Fprintf(stdout, "HTTP %d\n%s\n", resp.StatusCode, out)
	}
	if resp.StatusCode != http.StatusOK {
		return 1
	}
	return 0
}

// ---- pack ----

func cliExtPack(args []string, stdout, stderr io.Writer) int {
	rest, vals, on, err := options(args, []string{"-o", "--out"}, []string{"--json"})
	if err != nil || len(rest) != 1 {
		if err != nil {
			fmt.Fprintln(stderr, err)
		}
		fmt.Fprintln(stderr, "用法：dsh-ee extension pack <目录> [-o 文件] [--json]")
		return 2
	}
	dir := rest[0]
	res := extension.Check(os.DirFS(dir))
	if !res.OK {
		printCheck(stderr, res)
		fmt.Fprintln(stderr, "检查没通过，不打包。")
		return 1
	}
	if res.Kind != extension.KindDeclarative {
		fmt.Fprintln(stderr, "只有声明式扩展要打包；服务扩展交付的是代码仓库或镜像，管理员装的时候填服务地址。")
		return 2
	}
	m := res.Manifest
	files := []string{extension.ManifestFile, m.OpenAPI}
	// 签过名的扩展：把签名一起打进去，管理员上传后安装计划里能看到签名状态
	if _, err := os.Stat(filepath.Join(dir, extension.SignatureFile)); err == nil {
		files = append(files, extension.SignatureFile)
	}
	if m.Icon != "" {
		files = append(files, m.Icon)
	}
	files = append(files, m.Apps...)
	for _, extra := range []string{"README.md", "AGENTS.md"} {
		if _, err := os.Stat(filepath.Join(dir, extra)); err == nil {
			files = append(files, extra)
		}
	}
	sort.Strings(files)
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	seen := map[string]bool{}
	for _, name := range files {
		name = strings.TrimPrefix(name, "./")
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(name)))
		if err != nil {
			fmt.Fprintf(stderr, "读 %s：%v\n", name, err)
			return 1
		}
		// 固定时间：同样的内容打出来的包一模一样，校验和才对得上
		w, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate, Modified: time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)})
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		_, _ = w.Write(data)
	}
	if err := zw.Close(); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	out := vals["-o"]
	if out == "" {
		out = vals["--out"]
	}
	if out == "" {
		_, name, _ := strings.Cut(m.ID, "/")
		out = name + "-" + m.Version + ".zip"
	}
	if err := os.WriteFile(out, buf.Bytes(), 0o644); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	sum := sha256.Sum256(buf.Bytes())
	info := map[string]any{"file": out, "identity": res.Identity, "digest": res.Digest, "sha256": hex.EncodeToString(sum[:]), "size": buf.Len()}
	if on["--json"] {
		printJSON(stdout, info)
	} else {
		fmt.Fprintf(stdout, "%s（%s）\n包的 sha256：%s\n清单和接口文档的摘要：%s\n管理员在「企业连接 → 安装扩展」上传这个文件。\n", out, res.Identity, info["sha256"], res.Digest)
	}
	return 0
}

// ---- report ----

func cliExtReport(args []string, stdout, stderr io.Writer) int {
	rest, _, _, err := options(args, nil, nil)
	if err != nil || len(rest) != 1 {
		if err != nil {
			fmt.Fprintln(stderr, err)
		}
		fmt.Fprintln(stderr, "用法：dsh-ee extension report <目录|地址>")
		return 2
	}
	var res *extension.Result
	if isURL(rest[0]) {
		resp, err := (&http.Client{Timeout: 15 * time.Second}).Get(strings.TrimRight(rest[0], "/") + "/.well-known/dsh-ee-extension.json")
		if err != nil {
			fmt.Fprintln(stderr, "连不上扩展：", err)
			return 1
		}
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		resp.Body.Close()
		res = extension.CheckFiles(map[string][]byte{extension.ManifestFile: raw})
	} else {
		res = extension.Check(os.DirFS(rest[0]))
	}
	if res.Manifest == nil {
		printCheck(stderr, res)
		return 1
	}
	fmt.Fprint(stdout, extension.Report(res))
	if !res.OK {
		return 1
	}
	return 0
}

// ---- init ----

func cliExtInit(args []string, stdout, stderr io.Writer) int {
	rest, vals, _, err := options(args, []string{"--id", "--kind"}, nil)
	if err != nil || len(rest) != 1 || vals["--id"] == "" {
		if err != nil {
			fmt.Fprintln(stderr, err)
		}
		fmt.Fprintln(stderr, "用法：dsh-ee extension init <目录> --id 作者/名字 [--kind declarative|service]\n  有接口文档、不写代码的用 declarative（默认）；要写逻辑的用 service")
		return 2
	}
	kind := vals["--kind"]
	if kind == "" {
		kind = extension.KindDeclarative
	}
	files, err := extension.Template(kind, vals["--id"])
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	dir := rest[0]
	if entries, err := os.ReadDir(dir); err == nil && len(entries) > 0 {
		fmt.Fprintf(stderr, "%s 不是空目录，不往里写\n", dir)
		return 1
	}
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		if err := os.WriteFile(path, files[name], 0o644); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		fmt.Fprintf(stdout, "  %s\n", filepath.Join(dir, name))
	}
	fmt.Fprintf(stdout, "\n建好了。下一步见 %s。\n", filepath.Join(dir, "AGENTS.md"))
	return 0
}
