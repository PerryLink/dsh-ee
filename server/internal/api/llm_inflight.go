package api

// LLM 网关的每用户在途请求闸与预留 token 记账（进程内）。
//
// 月度额度原来是「先 SUM(usage_logs) 与 limit 比较 → 放行 → 响应写完后才插入
// usage_logs」的 check-then-use：并发请求会读到同一个旧用量、全部通过检查，随后
// 各自记账，实际用量可以越过月度额度一个数量级（例如额度 10 万时并发 N 个
// max_tokens=32768 的请求能吃掉几十倍额度）。
//
// 这里在放行前做两件事，让「检查 → 使用」之间的窗口在进程内闭合：
//  1. 每用户在途并发闸：同一账号同时最多 llmInflightPerUser 个 LLM 请求，超限 429；
//  2. 预留 token 记账：把每次在途请求声明的 max_tokens 计入额度判定，
//     used + reserved + maxTokens(requested) <= limit 才放行。
//
// 预留只在进程内，不写 usage_logs：月度余额展示（admin / me / MonthTokensByUser /
// TokenMonthUsage）仍然只统计真实用量，不会因为一次大 max_tokens 请求被永久占住。
// 请求结束（成功、上游失败、流式中断、客户端断开）统一由 handler 的 defer 释放预留，
// 并在此之前按实际 usage 记账，所以「记账提交 → 释放预留」的顺序不会让并发请求
// 同时漏掉两者。
//
// 多实例部署的残余：预留与并发计数都是每实例一份（不落库），同一账号的并发请求落在
// 不同实例上时，每个实例按自己看到的用量与预留放行，整体超额被收敛到
// 「实例数 × 单请求 max_tokens」的量级，而不是完全消除。要做到跨实例严格，需要把
// 预留行落库（usage_logs 加 status='reserved' 并在事务里校验），改动面更大。

import (
	"fmt"
	"net/http"
	"sync"

	"github.com/hikariming/dsh-ee/server/internal/store"
)

// llmInflightPerUser 是单个账号同时允许的在途 LLM 请求数。
//
// 它不是限流（每令牌每分钟调用次数），而是额度准入的放大器：预留 token 只在请求
// 在途期间占住额度，必须有并发上限，否则「并发请求 × 预留」仍可能远超额度。
// 4 对正常对话 / 工具调用足够（DSH 客户端的并行子任务也远小于这个量级）。
const llmInflightPerUser = 4

// llmInflight 是进程内的在途计数与预留 token。零值可直接使用（字段在首次使用时创建）。
type llmInflight struct {
	mu     sync.Mutex
	users  map[int64]*llmUserInflight
	tokens map[int64]int64 // 令牌 id → 在途请求预留的 token 数
}

// llmUserInflight 是一个账号的在途请求数与预留 token 数。
type llmUserInflight struct {
	count    int
	reserved int64
}

// llmQuotaLease 是一次被放行请求占用的在途名额与预留 token。
// 调用方必须在请求收尾时 release；所有返回路径（成功 / 失败 / 流式中断 / 客户端断开）
// 都要覆盖，否则名额与预留会泄漏，后续请求会被误拒。
type llmQuotaLease struct {
	inflight      *llmInflight
	userID        int64
	tokenID       int64
	userReserved  int64
	tokenReserved int64
	released      bool
}

// release 归还在途名额与预留 token。重复调用安全；nil lease 安全。
func (l *llmQuotaLease) release() {
	if l == nil {
		return
	}
	l.inflight.mu.Lock()
	defer l.inflight.mu.Unlock()
	l.inflight.releaseLocked(l)
}

// releaseLocked 是 release 的持锁版本；调用方必须持有 f.mu。
func (f *llmInflight) releaseLocked(l *llmQuotaLease) {
	if l == nil || l.released {
		return
	}
	l.released = true
	if u := f.users[l.userID]; u != nil {
		u.count--
		u.reserved -= l.userReserved
		// 清理：请求结束后不留空条目，map 的大小只与当前在途账号数相关。
		if u.count <= 0 && u.reserved <= 0 {
			delete(f.users, l.userID)
		}
	}
	if l.tokenReserved > 0 && l.tokenID > 0 {
		if left := f.tokens[l.tokenID] - l.tokenReserved; left > 0 {
			f.tokens[l.tokenID] = left
		} else {
			delete(f.tokens, l.tokenID)
		}
	}
}

// reserveLLMQuota 是 LLM 额度准入的唯一入口：持锁完成「在途名额 + 读月度用量 +
// 预留」，让同一进程内的并发请求串行判定，后到者一定能看见先到者的预留。
// 返回的 lease 由调用方负责 release；返回 nil 时状态码 / 类型 / 错误码 / 文案已经就绪。
//
// 持有 f.mu 期间读库是有意为之：如果先读用量再抢锁，正好有一条已完成请求处在
// 「usage 已提交、预留还没释放」之间时，后到者可能既看不到它的记账（读到旧 SUM）
// 又看不到它的预留（刚被释放），从而多放行一次。持锁读库保证「预留还在」或
// 「记账已提交」至少有一个可见，最坏也只是保守地少放行。
func (s *Server) reserveLLMQuota(r *http.Request, k *Principal, quota *store.UserQuota, maxTokens int64) (*llmQuotaLease, int, string, string, string) {
	f := &s.llmInflight
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.users == nil {
		f.users = map[int64]*llmUserInflight{}
	}
	if f.tokens == nil {
		f.tokens = map[int64]int64{}
	}
	u := f.users[k.User.ID]
	if u == nil {
		u = &llmUserInflight{}
		f.users[k.User.ID] = u
	}
	// 并发闸放在额度判定之前：超过上限的请求不再查库。
	if u.count >= llmInflightPerUser {
		return nil, http.StatusTooManyRequests, "rate_limit_error", "rate_limit_exceeded",
			"当前并发请求过多，请稍后重试"
	}
	u.count++
	lease := &llmQuotaLease{inflight: f, userID: k.User.ID, tokenID: k.TokenID}

	if quota.MonthlyTokenLimit != nil {
		limit := *quota.MonthlyTokenLimit
		used, err := s.store.MonthTokensByUser(r.Context(), k.User.ID)
		if err != nil {
			f.releaseLocked(lease)
			s.log.Error("month tokens", "user", k.User.ID, "err", err)
			return nil, http.StatusInternalServerError, "server_error", "internal_error", "服务器内部错误"
		}
		if msg := llmQuotaRejectMessage(used, u.reserved, maxTokens, limit, false); msg != "" {
			f.releaseLocked(lease)
			return nil, http.StatusTooManyRequests, "insufficient_quota", "monthly_quota_exceeded", msg
		}
		u.reserved += maxTokens
		lease.userReserved = maxTokens
	}

	if k.Limit != nil {
		limit := *k.Limit
		used, err := s.store.TokenMonthUsage(r.Context(), k.TokenID)
		if err != nil {
			f.releaseLocked(lease)
			s.log.Error("token month tokens", "token", k.TokenID, "err", err)
			return nil, http.StatusInternalServerError, "server_error", "internal_error", "服务器内部错误"
		}
		tokenReserved := f.tokens[k.TokenID]
		if msg := llmQuotaRejectMessage(used, tokenReserved, maxTokens, limit, true); msg != "" {
			f.releaseLocked(lease)
			return nil, http.StatusTooManyRequests, "insufficient_quota", "monthly_quota_exceeded", msg
		}
		f.tokens[k.TokenID] = tokenReserved + maxTokens
		lease.tokenReserved = maxTokens
	}
	return lease, 0, "", "", ""
}

// llmQuotaRejectMessage 返回额度拒绝文案；空字符串表示放行。
// 判定口径（账号与令牌一致）：used + reserved + maxTokens > limit 拒绝。
// 这里保留原来「已用完 / 没分配额度」的文案，只是把在途预留算进 used 一侧；
// 新增「剩余额度装不下本次 max_tokens」的文案，避免客户端以为额度已耗尽。
func llmQuotaRejectMessage(used, reserved, maxTokens, limit int64, perToken bool) string {
	tokenSuffix := ""
	if perToken {
		tokenSuffix = "（此令牌）"
	}
	switch {
	case limit == 0 && !perToken:
		return "管理员还没有给你分配额度，请联系管理员"
	case used+reserved >= limit:
		return "本月 token 额度已用完" + tokenSuffix + "，请联系管理员"
	case used+reserved+maxTokens > limit:
		return fmt.Sprintf("本次请求的 max_tokens（%d）超出本月剩余额度%s，请调小 max_tokens 或联系管理员",
			maxTokens, tokenSuffix)
	}
	return ""
}
