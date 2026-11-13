// dsh-ee 服务端入口。
package main

import (
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"github.com/hikariming/dsh-ee/server/internal/datahub"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/hikariming/dsh-ee/server/internal/api"
	"github.com/hikariming/dsh-ee/server/internal/config"
	"github.com/hikariming/dsh-ee/server/internal/extension"
	"github.com/hikariming/dsh-ee/server/internal/secret"
	"github.com/hikariming/dsh-ee/server/internal/store"
	"github.com/hikariming/dsh-ee/server/internal/webui"
)

// version 由构建时 -ldflags "-X main.version=…" 写入。
var version = "dev"

func main() {
	if len(os.Args) > 1 && (os.Args[1] == "version" || os.Args[1] == "--version") {
		fmt.Println(version)
		return
	}
	if handled, code := runCLI(os.Args[1:], os.Stdout, os.Stderr); handled {
		os.Exit(code)
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if err := run(log); err != nil {
		log.Error("启动失败", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	st, err := openStore(ctx, cfg, log)
	if err != nil {
		return err
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		return err
	}

	created, err := api.Bootstrap(ctx, st, cfg.BootstrapAdmin, cfg.BootstrapAdminPassword)
	if err != nil {
		return err
	}
	if created {
		log.Info("已创建初始管理员", "username", cfg.BootstrapAdmin)
	} else if n, _ := st.CountUsers(ctx); n == 0 {
		log.Warn("还没有任何用户：设置 DSH_EE_BOOTSTRAP_ADMIN_PASSWORD 后重启以创建管理员")
	}
	// 库里的设置（例如手工改库写进的非法嵌入来源）在启动时就校验；不合法直接拒绝启动。
	if err := api.ValidateStoredSettings(ctx, st); err != nil {
		return err
	}

	box, err := secret.NewBox(cfg.SecretKey)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(cfg.DataDir, 0o750); err != nil {
		return fmt.Errorf("创建数据目录 %s: %w", cfg.DataDir, err)
	}
	if err := st.FailRunningJobs(ctx); err != nil {
		return err
	}
	// 数据中台：开不起来时只关掉这一块，其余照常
	var dataHub *datahub.Hub
	dataErr := ""
	if cfg.DataDSN != "" {
		if dataHub, err = api.OpenDataHub(ctx, st, box, cfg.DataDSN, cfg.DataReadDSN); err != nil {
			log.Warn("数据中台没有开起来", "err", err)
			dataErr = "数据中台没有开起来：" + err.Error()
		} else if dataHub.ReadErr != "" {
			log.Warn("数据中台的只读账号不可用，数据集暂时不能查", "reason", dataHub.ReadErr)
		}
	}
	// 扩展签名：环境变量里的公钥只在启动时读（库里的信任根改完立即生效）。
	// 配了强制签名却一把受信任公钥都没有时只警告、不拒绝启动：管理员可以先起服务，再用
	// /api/admin/signing-keys 加公钥。
	extKeys, err := extensionTrustedKeys(cfg)
	if err != nil {
		return err
	}
	dbKeys, err := st.ListTrustedSigningKeys(ctx)
	if err != nil {
		return fmt.Errorf("读扩展签名信任根：%w", err)
	}
	// 只数现在能用的：撤销的、valid_from 还没到的不算
	usable := 0
	now := time.Now().UTC()
	for _, k := range dbKeys {
		if k.RevokedAt == nil && !now.Before(k.ValidFrom) {
			usable++
		}
	}
	if cfg.ExtensionRequireSignature || len(extKeys) > 0 || len(dbKeys) > 0 {
		log.Info("扩展签名策略", "require_signature", cfg.ExtensionRequireSignature,
			"env_keys", len(extKeys), "trusted_keys", usable)
	}
	if msg := extensionSigningWarning(cfg.ExtensionRequireSignature, len(extKeys), usable); msg != "" {
		log.Warn(msg)
	}
	srv := api.New(st, box, api.Options{
		CookieSecure:   cfg.CookieSecure,
		SessionTTL:     cfg.SessionTTL,
		Version:        version,
		Web:            webui.Handler(),
		DataDir:        cfg.DataDir,
		PublicURL:      cfg.PublicURL,
		TrustedProxies: cfg.TrustedProxies,
		CORSOrigins:    cfg.CORSOrigins,
		Addr:           cfg.Addr,
		AdminModeUnit:  cfg.AdminModeUnit,
		// 旧令牌 scope 收敛（DSH_EE_RESTRICT_LEGACY_TOKEN_SCOPES）；默认关闭，不破坏现有客户端
		RestrictLegacyTokenScopes: cfg.RestrictLegacyTokenScopes,
		DataHub:                   dataHub,
		DataHubErr:                dataErr,
		DemoSite:                  demoSite(cfg),
		ExtensionTrustedKeys:      extKeys,
		ExtensionRequireSignature: cfg.ExtensionRequireSignature,
		EmbedOrigins:              cfg.EmbedOrigins,
		EmbedCrossSite:            cfg.EmbedCrossSite,
		EmbedSessionTTL:           cfg.EmbedSessionTTL,
		// 每令牌的 /v1 与 /mcp 请求数；默认 60，可用 DSH_EE_RATE_LIMIT 调整或关闭
		RateLimitPerMinute:     cfg.RateLimitPerMinute,
		LoginIPLimit:           cfg.LoginIPLimit,
		LoginIPWindow:          cfg.LoginIPWindow,
		Metrics:                cfg.Metrics,
		EmbedMintPerMinute:     cfg.EmbedMintPerMinute,
		EmbedExchangePerMinute: cfg.EmbedExchangePerMinute,
	}, log)
	if cfg.RateLimitPerMinute > 0 {
		log.Info("已启用公开协议面限流", "per_minute_per_token", cfg.RateLimitPerMinute)
	} else {
		log.Warn("公开协议面限流已关闭", "reason", "DSH_EE_RATE_LIMIT=0/off")
	}

	go sweepSessions(ctx, st, log)
	go sweepEmbed(ctx, st, log)
	go srv.RunBackground(ctx)

	httpSrv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
		// 不设 WriteTimeout：流式回答可能持续数分钟
	}
	httpSrv.RegisterOnShutdown(srv.CloseStreams)
	errCh := make(chan error, 1)
	go func() {
		log.Info("dsh-ee 已启动", "addr", cfg.Addr, "version", version)
		errCh <- httpSrv.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case <-ctx.Done():
		log.Info("正在关闭")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		return httpSrv.Shutdown(shutdownCtx)
	}
	return nil
}

// openStore 连接数据库；连不上时在 DBWait 内每 3 秒重试一次。
func openStore(ctx context.Context, cfg *config.Config, log *slog.Logger) (*store.Store, error) {
	deadline := time.Now().Add(cfg.DBWait)
	for {
		st, err := store.Open(ctx, cfg.DSN)
		if err == nil {
			return st, nil
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			return nil, fmt.Errorf("连接数据库: %w", err)
		}
		log.Warn("数据库还没就绪，3 秒后重试", "err", err)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(3 * time.Second):
		}
	}
}

// sweepSessions 每小时清理过期会话与设备码。
func sweepSessions(ctx context.Context, st *store.Store, log *slog.Logger) {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if n, err := st.DeleteExpiredSessions(ctx); err != nil {
				log.Warn("清理过期会话失败", "err", err)
			} else if n > 0 {
				log.Info("已清理过期会话", "count", n)
			}
			if n, err := st.DeleteExpiredEmbedSessions(ctx); err != nil {
				log.Warn("清理过期嵌入会话失败", "err", err)
			} else if n > 0 {
				log.Info("已清理过期嵌入会话", "count", n)
			}
			if _, err := st.DeleteExpiredAgentConfirms(ctx); err != nil {
				log.Warn("清理过期确认码失败", "err", err)
			}
			if n, err := st.DeleteExpiredDeviceCodes(ctx); err != nil {
				log.Warn("清理过期设备码失败", "err", err)
			} else if n > 0 {
				log.Info("已清理过期设备码", "count", n)
			}
		}
	}
}

// sweepEmbed 启动时清理一次、之后每分钟清理过期的一次性 embed code 与限流窗口。
// code TTL 只有 60 秒，靠每小时的总清理太慢；多实例各自跑一遍是幂等的（DELETE 条件相同）。
func sweepEmbed(ctx context.Context, st *store.Store, log *slog.Logger) {
	clean := func() {
		if n, err := st.DeleteExpiredEmbedCodes(ctx); err != nil {
			log.Warn("清理过期嵌入 code 失败", "err", err)
		} else if n > 0 {
			log.Info("已清理过期嵌入 code", "count", n)
		}
		// 只保留当前及以后的窗口；上一分钟的限流行已经没用了。
		if n, err := st.DeleteOldEmbedRates(ctx, time.Now().UTC().Truncate(time.Minute)); err != nil {
			log.Warn("清理嵌入限流窗口失败", "err", err)
		} else if n > 0 {
			log.Info("已清理嵌入限流窗口", "count", n)
		}
	}
	clean()
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			clean()
		}
	}
}

// demoSite 把配置里的演示站开关转成服务端选项；没开时返回 nil。
func demoSite(cfg *config.Config) *api.DemoSite {
	if !cfg.DemoMode {
		return nil
	}
	return &api.DemoSite{Username: cfg.BootstrapAdmin, Password: cfg.BootstrapAdminPassword, Reset: cfg.DemoReset, ResetEvery: cfg.DemoResetEvery, VideoDir: cfg.DemoVideoDir, StatsFile: cfg.DemoStatsFile}
}

// extensionTrustedKeys 读环境变量里受信任的签名公钥（只读兼容来源）。
// 主要来源是库里的信任根（/api/admin/signing-keys）；这里只负责启动时读一份 env 的。
func extensionTrustedKeys(cfg *config.Config) ([]ed25519.PublicKey, error) {
	if cfg.ExtensionTrustedKeys == "" {
		return nil, nil
	}
	keys, err := extension.LoadPublicKeys(cfg.ExtensionTrustedKeys)
	if err != nil {
		return nil, fmt.Errorf("DSH_EE_EXTENSION_TRUSTED_KEYS: %w", err)
	}
	return keys, nil
}

// extensionSigningWarning 返回启动时要提醒的签名配置问题：配了强制验证签名，但库和
// 环境变量里都还没有受信任公钥——这时任何扩展都装不了。不拒绝启动，先用管理 API 加公钥即可。
func extensionSigningWarning(require bool, envKeys, dbKeys int) string {
	if require && envKeys+dbKeys == 0 {
		return "配了 DSH_EE_EXTENSION_REQUIRE_SIGNATURE=1，但还没有受信任的签名公钥：现在所有扩展都会被拒绝安装；先用 /api/admin/signing-keys 加公钥，或设 DSH_EE_EXTENSION_TRUSTED_KEYS"
	}
	return ""
}
