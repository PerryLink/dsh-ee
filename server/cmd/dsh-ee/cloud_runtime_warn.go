package main

import (
	"context"
	"log/slog"
)

// cloudRuntimeFootprintCounter 让启动告警不用起数据库/服务就能测。
type cloudRuntimeFootprintCounter interface {
	CountCloudRuntimeFootprint(context.Context) (int, int, error)
}

// warnCloudRuntimeWithoutProvider 在云 runtime 未配置时提示库里遗留的云电脑与待回收沙箱。
// 没有 provider 时回收器（休眠、按保留期回收、迁移遗留沙箱清理）不会启动，这些 OpenSandbox
// 沙箱会一直计费；只打一条 Warn、不阻断启动：重新配置 DSH_EE_RUNTIME_*，或到 OpenSandbox
// 侧手工清理即可收敛。
func warnCloudRuntimeWithoutProvider(ctx context.Context, st cloudRuntimeFootprintCounter, log *slog.Logger, providerConfigured bool) {
	if providerConfigured {
		return
	}
	computers, orphans, err := st.CountCloudRuntimeFootprint(ctx)
	if err != nil {
		log.Warn("统计遗留云 runtime 资源失败", "err", err)
		return
	}
	if computers == 0 && orphans == 0 {
		return
	}
	log.Warn("云 runtime 未配置，但库里还有云电脑/待回收沙箱：没有 provider 时它们不会被自动休眠或回收，会持续产生云成本；请重新配置 DSH_EE_RUNTIME_*，或到 OpenSandbox 侧手工清理",
		"computers", computers, "orphans", orphans)
}
