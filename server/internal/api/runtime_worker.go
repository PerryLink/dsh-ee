package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/hikariming/dsh-ee/server/internal/feishu"
	"github.com/hikariming/dsh-ee/server/internal/rbac"
	"github.com/hikariming/dsh-ee/server/internal/runtime"
	"github.com/hikariming/dsh-ee/server/internal/secret"
	"github.com/hikariming/dsh-ee/server/internal/store"
)

const (
	runtimeOutputDir = "/tmp/dsh-ee-output"
	runtimePatchPath = "/tmp/dsh-ee-runtime.patch.yml"
	runtimeMaxFile   = 8 << 20
	runtimeMaxFiles  = 8
	runtimeAttempts  = 2
)

// runtimeFileRule 是对话和任务提示词里共用的交付文件说明。DSH 以 runtimeOutputDir 为工作目录启动，
// 这个目录因此就是它沙箱策略里的工作区根。DSH 的 shell 工具在 bwrap 下按 workspace-write 运行时，
// 会先把 /tmp 换成这条命令独占的 tmpfs，再只把工作区根 bind 回真实目录：以前工作目录是 /tmp 本身，
// 机器人用 shell 写进 /tmp/dsh-ee-output 的文件落在一次性的 tmpfs 里，命令一结束就没了，
// 沙箱里列目录看不到，文件库也就是空的。
const runtimeFileRule = "Your current working directory is " + runtimeOutputDir + ". Save every requested deliverable file directly in it " +
	"(a relative file name such as notes.md is enough, no subdirectories); files saved there are published to the employee's file library, " +
	"and files saved anywhere else are discarded."

func (s *Server) runCloudWorker(ctx context.Context) {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		task, err := s.store.ClaimRuntimeTask(ctx, runtimeAttempts, 45*time.Second)
		if err == nil {
			s.runCloudClaim(ctx, task)
		} else if !notFound(err) && ctx.Err() == nil {
			s.log.Warn("领取云任务失败", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

func (s *Server) runCloudClaim(ctx context.Context, task *store.RuntimeTask) {
	run, cancel := context.WithTimeout(ctx, time.Duration(task.TimeoutSeconds)*time.Second)
	defer cancel()
	watchDone := make(chan struct{})
	go func() {
		defer close(watchDone)
		tick := time.NewTicker(time.Second)
		defer tick.Stop()
		for {
			select {
			case <-run.Done():
				return
			case <-tick.C:
				// 按所有者读：管理员通配（owner=0）看不到网页对话的轮次。
				t, err := s.store.RuntimeTask(run, task.ID, task.OwnerID)
				if err != nil || t.Attempts != task.Attempts || t.Status != store.RuntimeRunning {
					cancel()
					return
				}
			}
		}
	}()
	summary, artifacts, err := s.executeCloudTask(run, task)
	runErr := run.Err()
	cancel()
	<-watchDone
	finish, stop := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer stop()
	status, message := store.RuntimeSucceeded, ""
	if err != nil {
		status, message = store.RuntimeFailed, err.Error()
		switch {
		case errors.Is(runErr, context.DeadlineExceeded):
			message = "cloud task timed out"
		case task.Attempts < runtimeAttempts && !errors.Is(err, errCloudEmployee) && !errors.Is(err, errCloudQuota):
			status = store.RuntimeQueued
		}
	}
	if err := s.store.FinishRuntimeTask(finish, task, status, summary, message, artifacts); err != nil {
		s.removeCloudArtifacts(artifacts)
		if !errors.Is(err, store.ErrConflict) {
			s.log.Warn("保存云任务结果失败", "task_id", task.ID, "err", err)
		}
		return
	}
	if status == store.RuntimeQueued {
		return
	}
	s.auditSystem(finish, "runtime.task."+status, strconv.FormatInt(task.ID, 10), map[string]any{"run_as": task.RunAsID, "attempt": task.Attempts})
	// 网页对话轮次的 BotID 为空（机器人记在对话上），结果只在网页里看，不投递飞书。
	if task.BotID != nil && task.ChatConversationID == nil {
		s.deliverCloudTask(finish, task, status, summary)
	}
}

var (
	errCloudEmployee = errors.New("run identity is not an eligible employee")
	// errCloudQuota 是终态失败：额度未分配或已用完，重试不会改变结果。
	errCloudQuota = errors.New("cloud task run-as employee has no model quota")
)

// cloudQuotaPreflight 在分配沙箱和短期凭证之前检查运行身份的月度额度，规则与模型网关
// checkLLMQuota 一致（nil 不限；0 未分配；已用 >= 正数上限即用完）。它只是尽早给出
// 可读的失败原因，不预留额度，也不替代网关的逐请求校验。读库出错是可重试的普通错误。
func (s *Server) cloudQuotaPreflight(ctx context.Context, id int64) error {
	u, err := s.runtimeEmployee(ctx, id)
	if err != nil {
		if notFound(err) {
			return errCloudEmployee
		}
		return errors.New("checking cloud task identity failed")
	}
	if u.MonthlyTokenLimit == nil {
		return nil
	}
	if *u.MonthlyTokenLimit == 0 {
		return fmt.Errorf("%w: monthly token quota is unallocated (0); ask an administrator to allocate a nonzero or unlimited quota", errCloudQuota)
	}
	used, err := s.store.MonthTokensByUser(ctx, id)
	if err != nil {
		return errors.New("checking cloud task quota failed")
	}
	if used >= *u.MonthlyTokenLimit {
		return fmt.Errorf("%w: monthly token quota is exhausted (%d of %d used); ask an administrator to raise it", errCloudQuota, used, *u.MonthlyTokenLimit)
	}
	return nil
}

func (s *Server) cloudCredential(ctx context.Context, task *store.RuntimeTask) (string, func(), error) {
	if _, err := s.runtimeEmployee(ctx, task.RunAsID); err != nil {
		if notFound(err) {
			return "", nil, errCloudEmployee
		}
		return "", nil, errors.New("checking cloud task identity failed")
	}
	raw := runtimeTokenPrefix + secret.Token()
	expires := time.Now().UTC().Add(time.Duration(task.TimeoutSeconds) * time.Second)
	t := &store.AccessToken{
		UserID: task.RunAsID, Name: "cloud runtime", Prefix: raw[:12], Source: "runtime", ClientKind: store.ClientKindThirdParty,
		Scopes: []string{rbac.ScopeModels, rbac.ScopeConnections}, AllowedModels: []string{s.opts.Runtime.Model}, ExpiresAt: &expires,
		CreatedBy: task.RunAsID,
	}
	if err := s.store.CreateAccessToken(ctx, t, secret.Hash(raw)); err != nil {
		return "", nil, errors.New("creating cloud task credential failed")
	}
	revoke := func() {
		c, stop := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer stop()
		if err := s.store.RevokeToken(c, t.ID); err != nil {
			s.log.Warn("吊销云任务短期令牌失败", "task_id", task.ID, "err", err)
		}
	}
	return raw, revoke, nil
}

func runtimeDSHPatch(base, model string) []byte {
	return []byte(fmt.Sprintf(`- id: llm-pi-ai
  config:
    providers:
      dsh-ee:
        apiKeyEnv: DSH_EE_RUNTIME_TOKEN
        api: openai-completions
        baseURL: %s
        models:
          - id: %s
            input: [text]
            maxTokens: %d
- id: agent-default-model
  config:
    provider: dsh-ee
    model: %s
- insert:
    - id: dsh-ee-runtime-mcp
      name: '@deepseek-ai/dsh-mcp-client'
      config:
        serverName: ee
        transport: streamable-http
        url: %s
        failOnStartupError: true
        headers:
          Authorization: !!js "'Bearer ' + process.env.DSH_EE_RUNTIME_TOKEN"
`, yamlQuote(base+"/v1"), yamlQuote(model), maxTokensPerRequest, yamlQuote(model), yamlQuote(base+"/mcp")))
}

func (s *Server) executeCloudTask(ctx context.Context, task *store.RuntimeTask) (string, []store.RuntimeArtifact, error) {
	opts := s.opts.Runtime
	gateway, err := url.Parse(opts.BaseURL)
	if err != nil || gateway.Hostname() == "" {
		return "", nil, errors.New("invalid cloud task gateway")
	}
	if err := s.cloudQuotaPreflight(ctx, task.RunAsID); err != nil {
		return "", nil, err
	}
	timeout := time.Duration(task.TimeoutSeconds) * time.Second
	sandbox, err := opts.Provider.Create(ctx, runtime.SandboxSpec{Image: opts.Image, Timeout: timeout + 30*time.Second, AllowedHosts: []string{gateway.Hostname()}})
	if err != nil {
		return "", nil, errors.New("creating cloud sandbox failed")
	}
	defer func() {
		c, stop := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer stop()
		if err := sandbox.Destroy(c); err != nil {
			s.log.Warn("销毁云任务沙箱失败", "task_id", task.ID, "err", err)
		}
	}()
	setup, err := sandbox.Exec(ctx, runtime.Command{Args: []string{"mkdir", "-p", runtimeOutputDir}, Timeout: 10 * time.Second})
	if err != nil || setup.ExitCode != 0 {
		return "", nil, errors.New("preparing cloud sandbox failed")
	}
	if err := sandbox.WriteFile(ctx, runtimePatchPath, runtimeDSHPatch(opts.BaseURL, opts.Model)); err != nil {
		return "", nil, errors.New("writing DSH configuration failed")
	}
	token, revoke, err := s.cloudCredential(ctx, task)
	if err != nil {
		return "", nil, err
	}
	defer revoke()
	prompt := "Enterprise task:\n" + task.Prompt + "\n\nThe enterprise task is read-only. " + runtimeFileRule + " Return a short final summary of the result without a step-by-step account."
	if task.ChatConversationID != nil {
		// 网页对话的提示词在入队时已拼好（runtimeChatPlan）：要的是直接回答员工，而不是任务摘要；
		// 当前消息必须留在最后，这里不再追加任何内容。
		prompt = task.Prompt
	}
	args := make([]string, len(opts.Command))
	for i, arg := range opts.Command {
		switch arg {
		case "{patch}":
			arg = runtimePatchPath
		case "{prompt}":
			arg = prompt
		case "{output}":
			arg = runtimeOutputDir
		}
		args[i] = arg
	}
	deadline, _ := ctx.Deadline()
	var final runtime.DSHFinalOutput
	// 工作目录必须是产物目录，见 runtimeFileRule。
	cmd := runtime.Command{Args: args, Dir: runtimeOutputDir, Timeout: time.Until(deadline), Env: map[string]string{
		"DSH_EE_RUNTIME_TOKEN": token, "DSH_HOME": "/tmp/dsh-ee-home", "DSH_EE_OUTPUT_DIR": runtimeOutputDir,
	}}
	if slices.Contains(opts.Command, "--json") {
		cmd.OnStdout = final.Add
	}
	result, err := sandbox.Exec(ctx, cmd)
	if err != nil || result.ExitCode != 0 {
		return "", nil, runtimeExecError(result, err, final.Failure(), token)
	}
	artifacts, err := s.collectCloudFiles(ctx, task, sandbox)
	if err != nil {
		return "", nil, err
	}
	reply := result.Stdout
	if cmd.OnStdout != nil {
		reply = final.Reply()
	}
	if task.ChatConversationID != nil {
		return runtimeChatReply(reply, token), artifacts, nil
	}
	return runtimeSummary(reply, token), artifacts, nil
}

func runtimeChatReply(reply, token string) string {
	return cleanRuntimeOutput(reply, token, runtimeChatReplyRunes-1)
}

func runtimeSummary(reply, token string) string {
	return cleanRuntimeOutput(reply, token, 2400)
}

func runtimeExecError(result runtime.Result, err error, dshError, token string) error {
	prefix := "headless DSH execution failed"
	if err == nil {
		prefix = fmt.Sprintf("headless DSH exited with status %d", result.ExitCode)
	}
	detail := cleanRuntimeOutput(result.Stderr, token, 512)
	if detail == "" {
		detail = cleanRuntimeOutput(dshError, token, 512)
	}
	if detail == "" && err != nil {
		detail = cleanRuntimeOutput(err.Error(), token, 512)
	}
	if detail == "" {
		return errors.New(prefix)
	}
	return fmt.Errorf("%s: %s", prefix, detail)
}

func cleanRuntimeOutput(output, token string, limit int) string {
	text := strings.TrimSpace(strings.ToValidUTF8(strings.ReplaceAll(output, token, "[redacted]"), "�"))
	return feishu.Clip(text, limit)
}

// collectCloudFiles 把产物目录（含 runtime.ListDepth 层以内的子目录）里的普通文件存成任务产物。
// 产物名是相对产物目录的路径（子目录里的文件带目录前缀，例如 reports/周报.md），按路径排序，最多 runtimeMaxFiles 个。
// 被跳过的文件都记一条日志并写明原因：以前这里静默丢弃，机器人说写了文件、文件库却是空的时无从排查。
func (s *Server) collectCloudFiles(ctx context.Context, task *store.RuntimeTask, sandbox runtime.Sandbox) ([]store.RuntimeArtifact, error) {
	files, err := sandbox.ListFiles(ctx, runtimeOutputDir)
	if err != nil {
		return nil, errors.New("listing cloud task files failed")
	}
	type candidate struct{ remote, name string }
	var picked []candidate
	skip := func(f runtime.File, reason string) {
		s.log.Warn("跳过云任务产物", "task_id", task.ID, "path", f.Path, "type", f.Type, "size", f.Size, "reason", reason)
	}
	for _, f := range files {
		name, ok := runtimeArtifactName(f.Path)
		switch {
		case f.Type == "directory":
			continue
		case !ok:
			skip(f, "path outside the output directory or not a plain name")
		case f.Type != "file":
			skip(f, "not a regular file")
		case f.Size > runtimeMaxFile:
			skip(f, "larger than the artifact size limit")
		case strings.HasPrefix(name, ".") || strings.Contains(name, "/."):
			// 隐藏文件和隐藏目录是工具自己的状态（工作目录就是产物目录），不当成交付物，也不记日志。
		default:
			picked = append(picked, candidate{f.Path, name})
		}
	}
	slices.SortFunc(picked, func(a, b candidate) int { return strings.Compare(a.name, b.name) })
	if len(picked) > runtimeMaxFiles {
		for _, c := range picked[runtimeMaxFiles:] {
			skip(runtime.File{Path: c.remote, Type: "file"}, "more than the per-task artifact limit")
		}
		picked = picked[:runtimeMaxFiles]
	}
	artifacts := []store.RuntimeArtifact{}
	for _, c := range picked {
		a, err := s.saveCloudFile(ctx, sandbox, c.remote, c.name)
		if err != nil {
			s.removeCloudArtifacts(artifacts)
			return nil, errors.New("saving cloud task file failed")
		}
		artifacts = append(artifacts, a)
	}
	return artifacts, nil
}

// runtimeArtifactName 返回产物在产物目录里的相对路径；不在目录里、层级超过 runtime.ListDepth、
// 不是规范路径或带控制字符 / 反斜杠的一律拒绝。
func runtimeArtifactName(remote string) (string, bool) {
	rel, ok := strings.CutPrefix(remote, runtimeOutputDir+"/")
	if !ok || rel == "" || path.Clean(remote) != remote || len(rel) > 255 || strings.ContainsAny(rel, "\x00\r\n\\") ||
		strings.Count(rel, "/") >= runtime.ListDepth || !utf8.ValidString(rel) {
		return "", false
	}
	return rel, true
}

func (s *Server) saveCloudFile(ctx context.Context, sandbox runtime.Sandbox, remote, name string) (store.RuntimeArtifact, error) {
	dir := filepath.Join(s.opts.DataDir, "runtime-artifacts")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return store.RuntimeArtifact{}, err
	}
	input, err := sandbox.ReadFile(ctx, remote)
	if err != nil {
		return store.RuntimeArtifact{}, err
	}
	defer input.Close()
	output, err := os.CreateTemp(dir, "artifact-*")
	if err != nil {
		return store.RuntimeArtifact{}, err
	}
	size, err := io.Copy(output, io.LimitReader(input, runtimeMaxFile+1))
	if closeErr := output.Close(); err == nil {
		err = closeErr
	}
	if size > runtimeMaxFile {
		err = errors.New("task artifact is too large")
	}
	if err != nil {
		_ = os.Remove(output.Name())
		return store.RuntimeArtifact{}, err
	}
	return store.RuntimeArtifact{Name: name, StorageKey: filepath.Base(output.Name()), Size: size}, nil
}

func (s *Server) removeCloudArtifacts(artifacts []store.RuntimeArtifact) {
	for _, a := range artifacts {
		_ = os.Remove(filepath.Join(s.opts.DataDir, "runtime-artifacts", a.StorageKey))
	}
}

func (s *Server) deliverCloudTask(ctx context.Context, task *store.RuntimeTask, status, summary string) {
	u, err := s.runtimeEmployee(ctx, task.OwnerID)
	if err != nil {
		return
	}
	client, _, err := s.feishuNotificationClient(ctx)
	if err != nil || client == nil {
		return
	}
	openID, err := s.chatSender(ctx, u, "feishu", "feishu:")
	if err != nil || openID == "" {
		return
	}
	base, _ := s.connectBase(ctx, nil)
	base = strings.TrimRight(base, "/")
	text := fmt.Sprintf("云任务 #%d：%s", task.ID, status)
	if summary != "" {
		text += "\n\n" + summary
	}
	if base != "" {
		if files, err := s.store.RuntimeArtifacts(ctx, task.ID, task.OwnerID); err == nil {
			for _, a := range files {
				text += fmt.Sprintf("\n%s/api/runtime/tasks/%d/artifacts/%d", base, task.ID, a.ID)
			}
		}
	}
	if _, err := client.SendText(ctx, openID, text); err != nil {
		s.log.Warn("发送云任务飞书结果失败", "task_id", task.ID, "err", err)
	}
}
