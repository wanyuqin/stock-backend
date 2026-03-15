package data

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
	"github.com/go-resty/resty/v2"
	"go.uber.org/zap"
)

// TokenManager 全局 Token 管理器
type TokenManager struct {
	// qgssid token
	qgssid     string
	lastUpdate time.Time

	// stock cookie（chromedp 浏览器抓取）
	stockCookie           string
	stockCookieLastUpdate time.Time
	stockCookieTTL        time.Duration // 默认 30 分钟，可覆盖（测试用）

	mu     sync.RWMutex
	client *http.Client
	rc     *resty.Client

	log *zap.Logger
}

// NewTokenManager 创建一个新的 Token 管理器
func NewTokenManager(log *zap.Logger) *TokenManager {
	return &TokenManager{
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
		rc:             resty.New().SetRedirectPolicy(resty.FlexibleRedirectPolicy(5)),
		stockCookieTTL: 30 * time.Minute,
		log:            log,
	}
}

// ─────────────────────────────────────────────────────────────────
// qgssid Token 管理
// ─────────────────────────────────────────────────────────────────

// GetToken 获取当前有效的 Token，如果超过 1 小时则自动更新
func (tm *TokenManager) GetToken() (string, error) {
	tm.mu.RLock()
	if tm.qgssid != "" && time.Since(tm.lastUpdate) < time.Hour {
		token := tm.qgssid
		tm.mu.RUnlock()
		return token, nil
	}
	tm.mu.RUnlock()

	if err := tm.UpdateToken(); err != nil {
		return "", err
	}

	tm.mu.RLock()
	defer tm.mu.RUnlock()
	return tm.qgssid, nil
}

// UpdateToken 重新获取 qgssid Token
func (tm *TokenManager) UpdateToken() error {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	tm.log.Info("TokenManager: updating qgssid token...")
	token, err := tm.fetchNewToken()
	if err != nil {
		return fmt.Errorf("fetchNewToken failed: %w", err)
	}

	tm.qgssid = token
	tm.lastUpdate = time.Now()
	tm.log.Info("TokenManager: qgssid token updated successfully", zap.String("token", token))
	return nil
}

// fetchNewToken 从 HTML 中提取 qgssid Token
func (tm *TokenManager) fetchNewToken() (string, error) {
	url := "http://quote.eastmoney.com/center/gridlist.html"
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36")

	resp, err := tm.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("unexpected status: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	re := regexp.MustCompile(`qgssid=([a-zA-Z0-9]+)`)
	matches := re.FindSubmatch(body)
	if len(matches) < 2 {
		return "", fmt.Errorf("qgssid not found in HTML response")
	}

	return string(matches[1]), nil
}

// ─────────────────────────────────────────────────────────────────
// Stock Cookie 管理（chromedp 浏览器抓取）
// ─────────────────────────────────────────────────────────────────

// GetStockCookie 获取当前有效的东方财富 Cookie。
// 与 GetToken 逻辑对齐：
//   - 缓存有效期内（默认 30 分钟）直接返回缓存值，避免重复启动无头浏览器
//   - 缓存过期或首次调用时触发 UpdateStockCookie 重新抓取
//   - 更新失败时若仍有旧缓存则降级返回旧值，不影响业务运行
func (tm *TokenManager) GetStockCookie() (string, error) {
	// 快路径：读锁检查缓存
	tm.mu.RLock()
	if tm.stockCookie != "" && time.Since(tm.stockCookieLastUpdate) < tm.stockCookieTTL {
		cookie := tm.stockCookie
		tm.mu.RUnlock()
		return cookie, nil
	}
	tm.mu.RUnlock()

	// 缓存失效，触发更新
	if err := tm.UpdateStockCookie(); err != nil {
		// 更新失败时，若仍有旧缓存则降级返回（stale fallback）
		tm.mu.RLock()
		old := tm.stockCookie
		tm.mu.RUnlock()
		if old != "" {
			tm.log.Warn("TokenManager: stock cookie refresh failed, using stale cache",
				zap.Error(err),
				zap.Duration("stale_age", time.Since(tm.stockCookieLastUpdate)),
			)
			return old, nil
		}
		return "", err
	}

	tm.mu.RLock()
	defer tm.mu.RUnlock()
	return tm.stockCookie, nil
}

// UpdateStockCookie 强制重新通过无头浏览器抓取 Cookie，并写入缓存。
func (tm *TokenManager) UpdateStockCookie() error {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	tm.log.Info("TokenManager: updating stock cookie via headless browser...")

	cookie, err := tm.fetchStockCookie()
	if err != nil {
		return fmt.Errorf("fetchStockCookie failed: %w", err)
	}

	tm.stockCookie = cookie
	tm.stockCookieLastUpdate = time.Now()
	tm.log.Info("TokenManager: stock cookie updated successfully",
		zap.Int("cookie_len", len(cookie)),
		zap.Time("expires_at", tm.stockCookieLastUpdate.Add(tm.stockCookieTTL)),
	)
	return nil
}

// fetchStockCookie 模拟真实浏览器行为抓取 Cookie（内部实现，调用方持有写锁）
func (tm *TokenManager) fetchStockCookie() (string, error) {
	targetURL := "https://quote.eastmoney.com/center/gridlist.html"

	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.DisableGPU,
		chromedp.NoSandbox,
		chromedp.Flag("headless", true),
	)

	allocCtx, allocCancel := chromedp.NewExecAllocator(context.Background(), opts...)
	defer allocCancel()

	ctx, ctxCancel := chromedp.NewContext(allocCtx)
	defer ctxCancel()

	ctx, timeoutCancel := context.WithTimeout(ctx, 30*time.Second)
	defer timeoutCancel()

	var cookiesStr string

	err := chromedp.Run(ctx,
		network.Enable(),
		chromedp.Navigate(targetURL),
		chromedp.Sleep(5*time.Second),
		chromedp.ActionFunc(func(ctx context.Context) error {
			cookies, err := network.GetCookies().Do(ctx)
			if err != nil {
				return err
			}
			var parts []string
			for _, c := range cookies {
				parts = append(parts, fmt.Sprintf("%s=%s", c.Name, c.Value))
			}
			cookiesStr = strings.Join(parts, "; ")
			return nil
		}),
	)

	if err != nil {
		return "", fmt.Errorf("headless browser failed: %w", err)
	}
	if cookiesStr == "" {
		return "", fmt.Errorf("no cookies extracted from page")
	}

	return cookiesStr, nil
}
