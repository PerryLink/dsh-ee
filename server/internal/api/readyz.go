package api

import (
	"context"
	"net/http"
	"os"
	"time"
)

// readyzTimeout 是一次就绪检查的总预算。探针会被负载均衡 / 编排系统高频调用，
// 依赖卡住时必须几秒内给出失败，不能跟着依赖一起挂住。
const readyzTimeout = 2 * time.Second

// readyCheck 是一项依赖检查的结果。message 是固定文案，不带 DSN、地址、密钥或原始错误；
// 原始错误只写服务端日志，避免探针响应把内部信息暴露给能访问该端口的人。
type readyCheck struct {
	Status  string `json:"status"`            // ok / fail
	Message string `json:"message,omitempty"` // 失败原因分类
}

// readyResponse 是 GET /readyz 的响应。checks 按依赖分项，任何一项 fail 时 status=unready。
type readyResponse struct {
	Status  string                `json:"status"` // ok / unready
	Version string                `json:"version,omitempty"`
	Checks  map[string]readyCheck `json:"checks"`
}

// handleReadyz 报告本实例现在能不能接流量：数据库可连、迁移已到最新、数据目录可写、
// 主密钥已加载。它不检查进程本身（那是 /healthz），也不参与重启判定。
func (s *Server) handleReadyz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), readyzTimeout)
	defer cancel()

	checks := map[string]readyCheck{
		"database":   s.readyDatabase(ctx),
		"migrations": s.readyMigrations(ctx),
		"data_dir":   s.readyDataDir(),
		"secret_key": s.readySecretKey(),
	}
	code, status := http.StatusOK, "ok"
	for _, c := range checks {
		if c.Status != "ok" {
			code, status = http.StatusServiceUnavailable, "unready"
			break
		}
	}
	// 就绪状态变化快，不能让中间缓存把 unready 缓存住。
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, code, readyResponse{Status: status, Version: s.opts.Version, Checks: checks})
}

// readyDatabase 检查数据库还能不能执行语句；只看连接，不读业务数据。
func (s *Server) readyDatabase(ctx context.Context) readyCheck {
	if s.store == nil {
		return readyCheck{Status: "fail", Message: "数据库没有配置"}
	}
	if err := s.store.DB().PingContext(ctx); err != nil {
		s.log.Warn("就绪检查：数据库不可用", "err", err)
		return readyCheck{Status: "fail", Message: "数据库不可用"}
	}
	return readyCheck{Status: "ok"}
}

// readyMigrations 检查内置迁移是否都已执行。滚动升级时旧实例会在这里显示 unready，
// 编排系统据此把流量切到新实例；多实例同时首次启动的迁移竞争不在本检查范围内。
func (s *Server) readyMigrations(ctx context.Context) readyCheck {
	if s.store == nil {
		return readyCheck{Status: "fail", Message: "数据库没有配置"}
	}
	state, err := s.store.MigrationStatus(ctx)
	if err != nil {
		s.log.Warn("就绪检查：读取迁移状态失败", "err", err)
		return readyCheck{Status: "fail", Message: "无法确认迁移状态"}
	}
	if len(state.Pending) > 0 {
		s.log.Warn("就绪检查：还有未执行的迁移", "pending", len(state.Pending), "latest", state.Latest)
		return readyCheck{Status: "fail", Message: "数据库迁移未完成"}
	}
	return readyCheck{Status: "ok"}
}

// readyDataDir 在数据目录里实际建一个临时文件再删掉，验证磁盘可写：只读挂载、磁盘满、
// 目录权限被改都会在这里失败。npm tarball 与上游缓存都写在这个目录里。
func (s *Server) readyDataDir() readyCheck {
	if s.opts.DataDir == "" {
		return readyCheck{Status: "fail", Message: "数据目录没有配置"}
	}
	if err := probeDataDir(s.opts.DataDir); err != nil {
		s.log.Warn("就绪检查：数据目录不可写", "dir", s.opts.DataDir, "err", err)
		return readyCheck{Status: "fail", Message: "数据目录不可写"}
	}
	return readyCheck{Status: "ok"}
}

// probeDataDir 写一个临时文件并删除；目录不存在、只读或不可写时返回错误。
func probeDataDir(dir string) error {
	f, err := os.CreateTemp(dir, ".readyz-*")
	if err != nil {
		return err
	}
	name := f.Name()
	_, writeErr := f.Write([]byte("readyz"))
	closeErr := f.Close()
	removeErr := os.Remove(name)
	switch {
	case writeErr != nil:
		return writeErr
	case closeErr != nil:
		return closeErr
	default:
		return removeErr
	}
}

// readySecretKey 确认主密钥已加载：没有它，上游 key、连接凭据和签名密钥都解不开。
func (s *Server) readySecretKey() readyCheck {
	if s.box == nil {
		return readyCheck{Status: "fail", Message: "主密钥没有加载"}
	}
	return readyCheck{Status: "ok"}
}
