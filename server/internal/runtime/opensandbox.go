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
	"strings"
	"time"
)

const (
	openSandboxKey = "OPEN-SANDBOX-API-KEY"
	maxMetadata    = 1 << 20
	maxOutput      = 1 << 20
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
	ID     string `json:"id"`
	Status struct {
		State string `json:"state"`
	} `json:"status"`
}

type sandboxEndpoint struct {
	Endpoint string            `json:"endpoint"`
	Headers  map[string]string `json:"headers"`
}

type openSandboxInstance struct {
	provider       *OpenSandbox
	path, execBase string
	headers        http.Header
}

func (p *OpenSandbox) Create(ctx context.Context, spec SandboxSpec) (Sandbox, error) {
	if spec.Image == "" || spec.Timeout <= 0 {
		return nil, errors.New("sandbox image and timeout are required")
	}
	var status sandboxStatus
	input := map[string]any{
		"image":          map[string]string{"uri": spec.Image},
		"entrypoint":     []string{"tail", "-f", "/dev/null"},
		"timeout":        max(60, int64((spec.Timeout-1)/time.Second)+1),
		"resourceLimits": map[string]string{"cpu": "1", "memory": "1Gi"},
	}
	if len(spec.Env) > 0 {
		input["env"] = spec.Env
	}
	if spec.AllowedHosts != nil {
		rules := make([]map[string]string, 0, len(spec.AllowedHosts))
		for _, host := range spec.AllowedHosts {
			rules = append(rules, map[string]string{"action": "allow", "target": host})
		}
		input["networkPolicy"] = map[string]any{"defaultAction": "deny", "egress": rules}
	}
	err := p.lifecycle(ctx, http.MethodPost, "/sandboxes", input, &status)
	if err != nil {
		return nil, err
	}
	if status.ID == "" || status.ID == "." || status.ID == ".." {
		return nil, errors.New("OpenSandbox returned an invalid ID")
	}
	s := &openSandboxInstance{provider: p, path: "/sandboxes/" + url.PathEscape(status.ID)}
	ready := false
	defer func() {
		if !ready {
			cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()
			_ = s.Destroy(cleanup)
		}
	}()
	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()
	for status.Status.State != "Running" {
		switch status.Status.State {
		case "Pending", "Starting", "":
		default:
			return nil, errors.New("OpenSandbox did not enter the running state")
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-tick.C:
		}
		status = sandboxStatus{}
		if err := p.lifecycle(ctx, http.MethodGet, s.path, nil, &status); err != nil {
			return nil, err
		}
	}
	var endpoint sandboxEndpoint
	if err := p.lifecycle(ctx, http.MethodGet, s.path+"/endpoints/44772", nil, &endpoint); err != nil {
		return nil, err
	}
	e := endpoint.Endpoint
	if !strings.Contains(e, "://") {
		scheme, _, _ := strings.Cut(p.base, "://")
		e = scheme + "://" + e
	}
	u, err := url.Parse(e)
	if err != nil || !validEndpoint(u) || u.RawQuery != "" ||
		(strings.HasPrefix(p.base, "https://") && u.Scheme != "https") {
		return nil, errors.New("OpenSandbox returned an invalid execd endpoint")
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
			break
		}
		var status *httpStatusError
		if errors.As(err, &status) && status.status >= 400 && status.status < 500 && status.status != http.StatusNotFound {
			return nil, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-tick.C:
		}
	}
	ready = true
	return s, nil
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
				ExitCode *int `json:"exit_code"`
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
			return Result{}, errors.New("OpenSandbox command failed")
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
	query := url.Values{"path": {dir}, "depth": {"1"}}
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
