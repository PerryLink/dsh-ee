package runtime

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
)

const (
	openSandboxKey           = "OPEN-SANDBOX-API-KEY"
	computerCreationMetadata = "dsh-ee-computer-creation"
	maxMetadata              = 1 << 20
	maxOutput                = 1 << 20
)

type OpenSandbox struct {
	base, key string
	client    http.Client
}

func NewOpenSandbox(base, key string) (*OpenSandbox, error) {
	u, err := url.Parse(strings.TrimRight(base, "/"))
	if err != nil || !validEndpoint(u) || u.RawQuery != "" {
		return nil, errors.New("invalid OpenSandbox lifecycle URL")
	}
	if !strings.HasSuffix(u.Path, "/v1") {
		u.Path = strings.TrimRight(u.Path, "/") + "/v1"
	}
	return &OpenSandbox{base: u.String(), key: key, client: http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}, nil
}

func validEndpoint(u *url.URL) bool {
	return u != nil && (u.Scheme == "https" || u.Scheme == "http") &&
		u.Hostname() != "" && u.User == nil && u.Fragment == ""
}

func parseGatewayURL(base string) (string, error) {
	u, err := url.Parse(base)
	if err != nil || !validEndpoint(u) || u.RawQuery != "" || (u.Path != "" && u.Path != "/") {
		return "", errors.New("invalid runtime gateway origin")
	}
	return strings.TrimRight(u.String(), "/"), nil
}

type sandboxStatus struct {
	ID       string            `json:"id"`
	Metadata map[string]string `json:"metadata"`
	Status   struct {
		State string `json:"state"`
	} `json:"status"`
}

type sandboxEndpoint struct {
	Endpoint string            `json:"endpoint"`
	Headers  map[string]string `json:"headers"`
}

type openSandboxInstance struct {
	provider       *OpenSandbox
	id             string
	path, execBase string
	headers        http.Header
}

func (s *openSandboxInstance) ID() string { return s.id }

func (p *OpenSandbox) Create(ctx context.Context, spec SandboxSpec) (Sandbox, error) {
	if spec.Timeout <= 0 {
		return nil, errors.New("sandbox image and timeout are required")
	}
	return p.create(ctx, spec, false)
}

// CreatePersistent 新建一台常驻沙箱：不带 timeout，OpenSandbox 不会按 TTL 删除它；
// 空闲休眠和最终回收由 dsh-ee 自己负责（见 api 包的 runCloudComputerReaper）。
func (p *OpenSandbox) CreatePersistent(ctx context.Context, spec SandboxSpec) (PersistentSandbox, error) {
	return p.create(ctx, spec, true)
}

func (p *OpenSandbox) create(ctx context.Context, spec SandboxSpec, persistent bool) (*openSandboxInstance, error) {
	if spec.Image == "" {
		return nil, errors.New("sandbox image and timeout are required")
	}
	var status sandboxStatus
	limits := map[string]string{"cpu": "1", "memory": "1Gi"}
	if spec.DiskQuotaBytes > 0 {
		limits["disk"] = strconv.FormatInt(spec.DiskQuotaBytes, 10)
	}
	input := map[string]any{
		"image":          map[string]string{"uri": spec.Image},
		"entrypoint":     []string{"tail", "-f", "/dev/null"},
		"resourceLimits": limits,
	}
	if !persistent {
		input["timeout"] = max(60, int64((spec.Timeout-1)/time.Second)+1)
	} else if spec.CreationKey != "" {
		input["metadata"] = map[string]string{computerCreationMetadata: spec.CreationKey}
	}
	if len(spec.Env) > 0 {
		input["env"] = spec.Env
	}
	if spec.AllowedHosts != nil {
		input["networkPolicy"] = openSandboxNetworkPolicy(spec.AllowedHosts)
	}
	err := p.lifecycle(ctx, http.MethodPost, "/sandboxes", input, &status)
	if err != nil {
		return nil, err
	}
	s, err := p.instance(status.ID)
	if err != nil {
		return nil, err
	}
	ready := false
	defer func() {
		if !ready {
			cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()
			_ = s.Destroy(cleanup)
		}
	}()
	if err := s.connect(ctx, status.Status.State); err != nil {
		return nil, err
	}
	ready = true
	return s, nil
}

func openSandboxNetworkPolicy(hosts []string) map[string]any {
	rules := make([]map[string]string, 0, len(hosts))
	for _, host := range hosts {
		rules = append(rules, map[string]string{"action": "allow", "target": host})
	}
	return map[string]any{"defaultAction": "deny", "egress": rules}
}

func (p *OpenSandbox) ReplaceAllowedHosts(ctx context.Context, id string, hosts []string) error {
	if len(hosts) == 0 {
		return errors.New("OpenSandbox network policy requires allowed hosts")
	}
	s, err := p.instance(id)
	if err != nil {
		return err
	}
	return p.lifecycle(ctx, http.MethodPut, s.path+"/networkpolicy", openSandboxNetworkPolicy(hosts), nil)
}

func (p *OpenSandbox) FindPersistent(ctx context.Context, creationKey string) (string, error) {
	if creationKey == "" {
		return "", ErrSandboxGone
	}
	filter := url.Values{computerCreationMetadata: {creationKey}}
	query := url.Values{"metadata": {filter.Encode()}, "pageSize": {"20"}}
	var found string
	for page := 1; ; page++ {
		query.Set("page", strconv.Itoa(page))
		var response struct {
			Items      []sandboxStatus `json:"items"`
			Pagination struct {
				HasNextPage bool `json:"hasNextPage"`
			} `json:"pagination"`
		}
		if err := p.lifecycle(ctx, http.MethodGet, "/sandboxes?"+query.Encode(), nil, &response); err != nil {
			return "", err
		}
		for _, item := range response.Items {
			if item.Metadata[computerCreationMetadata] != creationKey || goneState(item.Status.State) {
				continue
			}
			if found != "" && found != item.ID {
				return "", errors.New("OpenSandbox returned multiple computers for one creation key")
			}
			found = item.ID
		}
		if !response.Pagination.HasNextPage {
			break
		}
	}
	if found == "" {
		return "", ErrSandboxGone
	}
	return found, nil
}

func (p *OpenSandbox) instance(id string) (*openSandboxInstance, error) {
	if id == "" || id == "." || id == ".." {
		return nil, errors.New("OpenSandbox returned an invalid ID")
	}
	return &openSandboxInstance{provider: p, id: id, path: "/sandboxes/" + url.PathEscape(id)}, nil
}

func (p *OpenSandbox) CheckPersistent(ctx context.Context, id string) error {
	s, err := p.instance(id)
	if err != nil {
		return err
	}
	_, err = s.state(ctx)
	return err
}

// Wake 按沙箱当前状态把它带回运行：Paused 先 resume；Pausing 等到 Paused 再 resume；
// Pending / Resuming 等它跑起来；已经删除或进入终态返回 ErrSandboxGone。
func (p *OpenSandbox) Wake(ctx context.Context, id string) (PersistentSandbox, error) {
	s, err := p.instance(id)
	if err != nil {
		return nil, err
	}
	state, err := s.state(ctx)
	if err != nil {
		return nil, err
	}
	if state == "Pausing" {
		if state, err = s.waitWhile(ctx, "Pausing"); err != nil {
			return nil, err
		}
	}
	if state == "Paused" {
		if err := s.post(ctx, "/resume"); err != nil {
			return nil, err
		}
		state = "Resuming"
	}
	if err := s.connect(ctx, state); err != nil {
		return nil, err
	}
	return s, nil
}

// Hibernate 暂停沙箱（OpenSandbox pause），文件系统和内存都保留，等到状态确实是 Paused 才返回。
func (p *OpenSandbox) Hibernate(ctx context.Context, id string) error {
	s, err := p.instance(id)
	if err != nil {
		return err
	}
	state, err := s.state(ctx)
	if err != nil {
		return err
	}
	if state == "Resuming" || state == "Pending" {
		if state, err = s.waitWhile(ctx, state); err != nil {
			return err
		}
	}
	if state == "Running" {
		if err := s.post(ctx, "/pause"); err != nil {
			return err
		}
		state = "Pausing"
	}
	if state == "Pausing" {
		if state, err = s.waitWhile(ctx, "Pausing", "Running"); err != nil {
			return err
		}
	}
	if state != "Paused" {
		return fmt.Errorf("OpenSandbox sandbox is %s, not Paused", state)
	}
	return nil
}

func (p *OpenSandbox) Remove(ctx context.Context, id string) error {
	s, err := p.instance(id)
	if err != nil {
		return err
	}
	return s.Destroy(ctx)
}

func goneState(state string) bool {
	return state == "Stopping" || state == "Terminated" || state == "Failed"
}

// state 读沙箱当前状态；404 和终态都当作 ErrSandboxGone。
func (s *openSandboxInstance) state(ctx context.Context) (string, error) {
	var status sandboxStatus
	err := s.provider.lifecycle(ctx, http.MethodGet, s.path, nil, &status)
	var httpErr *httpStatusError
	if errors.As(err, &httpErr) && httpErr.status == http.StatusNotFound {
		return "", ErrSandboxGone
	}
	if err != nil {
		return "", err
	}
	if goneState(status.Status.State) {
		return status.Status.State, ErrSandboxGone
	}
	return status.Status.State, nil
}

// waitWhile 轮询直到状态不再是 states 之一，返回新状态。
func (s *openSandboxInstance) waitWhile(ctx context.Context, states ...string) (string, error) {
	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-tick.C:
		}
		state, err := s.state(ctx)
		if err != nil {
			return "", err
		}
		if !slices.Contains(states, state) {
			return state, nil
		}
	}
}

func (s *openSandboxInstance) post(ctx context.Context, action string) error {
	err := s.provider.lifecycle(ctx, http.MethodPost, s.path+action, nil, nil)
	var httpErr *httpStatusError
	if errors.As(err, &httpErr) && httpErr.status == http.StatusNotFound {
		return ErrSandboxGone
	}
	return err
}

// connect 等沙箱进入 Running，取 execd 端点并等它响应 ping。
func (s *openSandboxInstance) connect(ctx context.Context, state string) error {
	p := s.provider
	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()
	for state != "Running" {
		switch state {
		case "Pending", "Starting", "Resuming", "":
		default:
			if goneState(state) {
				return ErrSandboxGone
			}
			return errors.New("OpenSandbox did not enter the running state")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
		}
		var err error
		if state, err = s.state(ctx); err != nil {
			return err
		}
	}
	var endpoint sandboxEndpoint
	if err := p.lifecycle(ctx, http.MethodGet, s.path+"/endpoints/44772", nil, &endpoint); err != nil {
		return err
	}
	e := endpoint.Endpoint
	if !strings.Contains(e, "://") {
		scheme, _, _ := strings.Cut(p.base, "://")
		e = scheme + "://" + e
	}
	u, err := url.Parse(e)
	if err != nil || !validEndpoint(u) || u.RawQuery != "" ||
		(strings.HasPrefix(p.base, "https://") && u.Scheme != "https") {
		return errors.New("OpenSandbox returned an invalid execd endpoint")
	}
	s.execBase = strings.TrimRight(u.String(), "/")
	s.headers = make(http.Header, len(endpoint.Headers))
	for name, value := range endpoint.Headers {
		s.headers.Set(name, value)
	}
	for {
		response, err := p.request(ctx, http.MethodGet, s.execBase+"/ping", "", s.headers, nil)
		if err == nil {
			response.Close()
			return nil
		}
		var status *httpStatusError
		if errors.As(err, &status) && status.status >= 400 && status.status < 500 && status.status != http.StatusNotFound {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
		}
	}
}

type httpStatusError struct {
	method string
	status int
}

func (e *httpStatusError) Error() string {
	return fmt.Sprintf("OpenSandbox %s returned HTTP %d", e.method, e.status)
}

func (p *OpenSandbox) request(ctx context.Context, method, target, contentType string, headers http.Header, body io.Reader) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, method, target, body)
	if err != nil {
		return nil, errors.New("invalid OpenSandbox request")
	}
	req.Header = headers.Clone()
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := p.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("OpenSandbox %s request failed", method)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		resp.Body.Close()
		return nil, &httpStatusError{method, resp.StatusCode}
	}
	return resp.Body, nil
}

func (p *OpenSandbox) lifecycle(ctx context.Context, method, route string, input any, output any) error {
	var body io.Reader
	var contentType string
	if input != nil {
		data, err := json.Marshal(input)
		if err != nil {
			return err
		}
		body, contentType = bytes.NewReader(data), "application/json"
	}
	response, err := p.request(ctx, method, p.base+route, contentType, http.Header{openSandboxKey: {p.key}}, body)
	if err != nil {
		return err
	}
	defer response.Close()
	if output != nil {
		return json.NewDecoder(io.LimitReader(response, maxMetadata)).Decode(output)
	}
	return nil
}

func (s *openSandboxInstance) Destroy(ctx context.Context) error {
	err := s.provider.lifecycle(ctx, http.MethodDelete, s.path, nil, nil)
	var status *httpStatusError
	if errors.As(err, &status) && status.status == http.StatusNotFound {
		return nil
	}
	return err
}

func (s *openSandboxInstance) Exec(ctx context.Context, cmd Command) (Result, error) {
	if len(cmd.Args) == 0 || cmd.Timeout <= 0 {
		return Result{}, errors.New("command argv and timeout are required")
	}
	argv := make([]string, len(cmd.Args))
	for i, arg := range cmd.Args {
		if strings.ContainsRune(arg, 0) {
			return Result{}, errors.New("command argv contains a NUL byte")
		}
		argv[i] = "'" + strings.ReplaceAll(arg, "'", "'\"'\"'") + "'"
	}
	input := map[string]any{
		"command": "exec " + strings.Join(argv, " "),
		"timeout": max(1, cmd.Timeout.Milliseconds()), "background": false,
	}
	if cmd.Dir != "" {
		input["cwd"] = cmd.Dir
	}
	if len(cmd.Env) > 0 {
		input["envs"] = cmd.Env
	}
	body, err := json.Marshal(input)
	if err != nil {
		return Result{}, err
	}
	response, err := s.provider.request(ctx, http.MethodPost, s.execBase+"/command", "application/json", s.headers, bytes.NewReader(body))
	if err != nil {
		return Result{}, err
	}
	defer response.Close()
	return parseCommandStream(response, cmd.OnStdout)
}

func parseCommandStream(r io.Reader, onStdout func(string)) (Result, error) {
	var stdout, stderr strings.Builder
	out := func(exitCode int) Result { return Result{stdout.String(), stderr.String(), exitCode} }
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 4096), 32<<20)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "data:") {
			line = strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		} else if !strings.HasPrefix(line, "{") {
			continue
		}
		if line == "" {
			continue
		}
		var event struct {
			Type     string `json:"type"`
			Text     string `json:"text"`
			ExitCode *int   `json:"exit_code"`
			Error    struct {
				ExitCode *int   `json:"exit_code"`
				Name     string `json:"ename"`
				Value    string `json:"evalue"`
			} `json:"error"`
		}
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			return Result{}, errors.New("invalid OpenSandbox execd event")
		}
		switch event.Type {
		case "stdout":
			if onStdout != nil {
				onStdout(event.Text)
			}
			stdout.WriteString(event.Text[:min(len(event.Text), maxOutput-stdout.Len())])
		case "stderr":
			stderr.WriteString(event.Text[:min(len(event.Text), maxOutput-stderr.Len())])
		case "execution_complete":
			if event.ExitCode == nil {
				return out(0), nil
			}
			return out(*event.ExitCode), nil
		case "error":
			if event.Error.ExitCode != nil && *event.Error.ExitCode != 0 {
				return out(*event.Error.ExitCode), nil
			}
			if event.Error.Name == "CommandExecError" {
				if code, err := strconv.Atoi(event.Error.Value); err == nil && code != 0 {
					return out(code), nil
				}
			}
			return out(0), errors.New("OpenSandbox command failed")
		}
	}
	if err := scanner.Err(); err != nil {
		return Result{}, err
	}
	return Result{}, errors.New("OpenSandbox command stream ended without a terminal event")
}

func (s *openSandboxInstance) WriteFile(ctx context.Context, path string, data []byte) error {
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	h := textproto.MIMEHeader{
		"Content-Disposition": {`form-data; name="metadata"; filename="metadata.json"`},
		"Content-Type":        {"application/json"},
	}
	meta, err := form.CreatePart(h)
	if err != nil {
		return err
	}
	if err := json.NewEncoder(meta).Encode(map[string]any{"path": path, "mode": 600}); err != nil {
		return err
	}
	file, err := form.CreateFormFile("file", "upload")
	if err != nil {
		return err
	}
	if _, err := file.Write(data); err != nil {
		return err
	}
	if err := form.Close(); err != nil {
		return err
	}
	response, err := s.provider.request(ctx, http.MethodPost, s.execBase+"/files/upload", form.FormDataContentType(), s.headers, &body)
	if err != nil {
		return err
	}
	return response.Close()
}

func (s *openSandboxInstance) ListFiles(ctx context.Context, dir string) ([]File, error) {
	query := url.Values{"path": {dir}, "depth": {strconv.Itoa(ListDepth)}}
	body, err := s.provider.request(ctx, http.MethodGet, s.execBase+"/directories/list?"+query.Encode(), "", s.headers, nil)
	if err != nil {
		return nil, err
	}
	defer body.Close()
	var files []File
	err = json.NewDecoder(io.LimitReader(body, maxMetadata)).Decode(&files)
	return files, err
}

func (s *openSandboxInstance) ReadFile(ctx context.Context, path string) (io.ReadCloser, error) {
	query := url.Values{"path": {path}}
	return s.provider.request(ctx, http.MethodGet, s.execBase+"/files/download?"+query.Encode(), "", s.headers, nil)
}

var _ PersistentProvider = (*OpenSandbox)(nil)
