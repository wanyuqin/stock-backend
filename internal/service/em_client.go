package service

import (
	"net/http"
	"sync"

	"go.uber.org/zap"

	"stock-backend/internal/data"
)

// ═══════════════════════════════════════════════════════════════
// em_client.go — 东财 Cookie 管理
//
// 核心：injectCookie(req) 函数
//   东财 CDN（push2 / push2his）在 TLS 握手后检查 Cookie，
//   无 Cookie 的请求直接 RST → Go 读响应得到 EOF。
//   所有访问 *.eastmoney.com 的请求都必须注入有效 Cookie。
//
// 注意：HTTP Client 已迁移至 em_http.go
// ═══════════════════════════════════════════════════════════════

var (
	globalTM    *data.TokenManager
	globalTMMu  sync.Once
	globalTMLog *zap.Logger
)

// InitGlobalTokenManager 初始化全局 TokenManager，在 router.New 时调用一次。
func InitGlobalTokenManager(log *zap.Logger) {
	globalTMMu.Do(func() {
		globalTMLog = log
		globalTM = data.NewTokenManager(log)
		// 异步预热
		go func() {
			cookie, err := globalTM.GetStockCookie()
			if err != nil {
				log.Error("em_client: cookie pre-warm FAILED — 东财请求将 EOF 直到 cookie 获取成功",
					zap.Error(err))
			} else {
				log.Info("em_client: cookie pre-warm succeeded",
					zap.Int("cookie_len", len(cookie)))
			}
		}()
	})
}

// injectCookie 将东财 Cookie 注入请求头，并记录诊断日志。
// 返回 cookie 是否成功注入（供调用方决策）。
func injectCookie(req *http.Request) bool {
	if globalTM == nil {
		if globalTMLog != nil {
			globalTMLog.Error("em_client: injectCookie called but globalTM is nil — InitGlobalTokenManager 未调用")
		}
		return false
	}

	cookie, err := globalTM.GetStockCookie()
	if err != nil {
		if globalTMLog != nil {
			globalTMLog.Error("em_client: GetStockCookie failed — 请求将无 Cookie 发送，预期 EOF",
				zap.String("url", req.URL.String()),
				zap.Error(err))
		}
		return false
	}
	if cookie == "" {
		if globalTMLog != nil {
			globalTMLog.Error("em_client: cookie is EMPTY — 请求将无 Cookie 发送，预期 EOF",
				zap.String("url", req.URL.String()))
		}
		return false
	}

	req.Header.Set("Cookie", cookie)
	if globalTMLog != nil {
		globalTMLog.Debug("em_client: cookie injected",
			zap.String("host", req.URL.Host),
			zap.Int("cookie_len", len(cookie)))
	}
	return true
}

// ForceRefreshCookie 强制刷新全局 Cookie（在 EOF 重试时调用）。
func ForceRefreshCookie() error {
	if globalTM == nil {
		return nil
	}
	return globalTM.UpdateStockCookie()
}

// GetCookieStatus 返回当前 Cookie 状态（供调试接口使用）。
func GetCookieStatus() (cookieLen int, initialized bool) {
	if globalTM == nil {
		return 0, false
	}
	cookie, err := globalTM.GetStockCookie()
	if err != nil {
		return 0, true
	}
	return len(cookie), true
}

// GetGlobalTokenManager 返回全局 TokenManager（供需要直接访问的服务使用）
func GetGlobalTokenManager() *data.TokenManager {
	return globalTM
}
