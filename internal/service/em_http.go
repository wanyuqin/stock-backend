package service

import (
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
)

// ═══════════════════════════════════════════════════════════════
// em_http.go — 东方财富 HTTP 请求统一封装
//
// 设计目标：
//   1. 全局共享连接池，启用 Keep-Alive，大幅提升性能
//   2. 统一的请求头设置，完整模拟 Chrome
//   3. 统一的 Cookie 注入
//   4. 指数退避重试策略
//   5. 可配置的超时和重试参数
//
// 使用方式：
//   client := GetEMHTTPClient()
//   resp, err := client.DoWithRetry(ctx, url, nil)
// ═══════════════════════════════════════════════════════════════

// ─────────────────────────────────────────────────────────────────
// 配置常量
// ─────────────────────────────────────────────────────────────────

const (
	// 连接池配置
	emMaxIdleConns        = 100
	emMaxIdleConnsPerHost = 20
	emMaxConnsPerHost     = 30
	emIdleConnTimeout     = 90 * time.Second

	// 超时配置
	emDialTimeout           = 5 * time.Second
	emTLSHandshakeTimeout   = 5 * time.Second
	emResponseHeaderTimeout = 10 * time.Second
	emDefaultRequestTimeout = 15 * time.Second
	emTCPKeepAlive          = 30 * time.Second

	// 重试配置
	emDefaultMaxRetries = 3
	emBaseRetryDelay    = 200 * time.Millisecond
	emMaxRetryDelay     = 2 * time.Second
)

// ─────────────────────────────────────────────────────────────────
// EMHTTPClient — 东财专用 HTTP 客户端
// ─────────────────────────────────────────────────────────────────

// EMHTTPClient 封装了东方财富 API 请求的所有逻辑
type EMHTTPClient struct {
	client     *http.Client
	log        *zap.Logger
	maxRetries int
}

var (
	globalEMClient     *EMHTTPClient
	globalEMClientOnce sync.Once
)

// GetEMHTTPClient 返回全局单例的东财 HTTP 客户端
func GetEMHTTPClient() *EMHTTPClient {
	globalEMClientOnce.Do(func() {
		transport := &http.Transport{
			// 连接池配置
			MaxIdleConns:        emMaxIdleConns,
			MaxIdleConnsPerHost: emMaxIdleConnsPerHost,
			MaxConnsPerHost:     emMaxConnsPerHost,
			IdleConnTimeout:     emIdleConnTimeout,

			// 超时配置
			DialContext: (&net.Dialer{
				Timeout:   emDialTimeout,
				KeepAlive: emTCPKeepAlive,
			}).DialContext,
			TLSHandshakeTimeout:   emTLSHandshakeTimeout,
			ResponseHeaderTimeout: emResponseHeaderTimeout,

			// 启用 Keep-Alive（关键！复用连接大幅提速）
			DisableKeepAlives: false,

			// HTTP/2 对东财 CDN 有时不稳定，禁用
			ForceAttemptHTTP2: false,
		}

		globalEMClient = &EMHTTPClient{
			client: &http.Client{
				Timeout:   emDefaultRequestTimeout,
				Transport: transport,
			},
			log:        globalTMLog, // 复用 em_client.go 中的全局 logger
			maxRetries: emDefaultMaxRetries,
		}
	})
	return globalEMClient
}

// EMRequestOption 请求配置选项
type EMRequestOption struct {
	Timeout    time.Duration     // 请求超时（覆盖默认值）
	MaxRetries int               // 最大重试次数（覆盖默认值）
	Headers    map[string]string // 额外的请求头
	SkipCookie bool              // 跳过 Cookie 注入（用于不需要 Cookie 的接口）
}

// ─────────────────────────────────────────────────────────────────
// 核心方法
// ─────────────────────────────────────────────────────────────────

// Do 执行单次 HTTP GET 请求（不带重试）
func (c *EMHTTPClient) Do(ctx context.Context, url string, opt *EMRequestOption) (*http.Response, error) {
	timeout := emDefaultRequestTimeout
	if opt != nil && opt.Timeout > 0 {
		timeout = opt.Timeout
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}

	// 设置标准请求头
	setEMStandardHeaders(req)

	// 设置额外请求头
	if opt != nil && opt.Headers != nil {
		for k, v := range opt.Headers {
			req.Header.Set(k, v)
		}
	}

	// 注入 Cookie
	if opt == nil || !opt.SkipCookie {
		if !injectCookie(req) && c.log != nil {
			c.log.Warn("em_http: proceeding without cookie — may get EOF",
				zap.String("url", url))
		}
	}

	return c.client.Do(req)
}

// DoWithRetry 执行 HTTP GET 请求，带指数退避重试
func (c *EMHTTPClient) DoWithRetry(ctx context.Context, url string, opt *EMRequestOption) (*http.Response, error) {
	maxRetries := c.maxRetries
	if opt != nil && opt.MaxRetries > 0 {
		maxRetries = opt.MaxRetries
	}

	var lastErr error
	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			delay := calculateRetryDelay(attempt)
			if c.log != nil {
				c.log.Warn("em_http: retrying request",
					zap.String("url", truncateURL(url, 100)),
					zap.Int("attempt", attempt+1),
					zap.Duration("delay", delay),
					zap.Error(lastErr),
				)
			}

			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(delay):
			}

			// 重试前刷新 Cookie
			if refreshErr := ForceRefreshCookie(); refreshErr != nil && c.log != nil {
				c.log.Warn("em_http: cookie refresh failed", zap.Error(refreshErr))
			}
		}

		resp, err := c.Do(ctx, url, opt)
		if err == nil {
			return resp, nil
		}

		lastErr = err

		// 非可重试错误，直接返回
		if !isRetryableError(err.Error()) {
			return nil, fmt.Errorf("em_http: non-retryable error: %w", err)
		}
	}

	return nil, fmt.Errorf("em_http: failed after %d attempts: %w", maxRetries, lastErr)
}

// FetchBody 执行请求并读取响应体（自动处理 gzip 解压）
func (c *EMHTTPClient) FetchBody(ctx context.Context, url string, opt *EMRequestOption) ([]byte, error) {
	resp, err := c.DoWithRetry(ctx, url, opt)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 500))
		return nil, fmt.Errorf("em_http: status %d: %s", resp.StatusCode, truncateBytes(body, 200))
	}

	// 根据 Content-Encoding 处理解压
	var reader io.Reader = resp.Body
	if resp.Header.Get("Content-Encoding") == "gzip" {
		gzReader, err := gzip.NewReader(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("em_http: gzip reader: %w", err)
		}
		defer gzReader.Close()
		reader = gzReader
	}

	body, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("em_http: read body: %w", err)
	}

	if len(body) == 0 {
		return nil, fmt.Errorf("em_http: empty response body")
	}

	return body, nil
}

// ─────────────────────────────────────────────────────────────────
// 辅助函数
// ─────────────────────────────────────────────────────────────────

// setEMStandardHeaders 设置东财 API 所需的完整请求头
// 注意：不设置 Accept-Encoding，让 Go 的 http.Transport 自动处理
// 如果手动设置了 Accept-Encoding，Go 不会自动解压，需要手动处理
func setEMStandardHeaders(req *http.Request) {
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8")
	// 显式声明支持 gzip，服务器会返回压缩数据，我们在 FetchBody 中处理解压
	req.Header.Set("Accept-Encoding", "gzip")
	req.Header.Set("Referer", "https://quote.eastmoney.com/")
	req.Header.Set("Sec-Fetch-Dest", "empty")
	req.Header.Set("Sec-Fetch-Mode", "cors")
	req.Header.Set("Sec-Fetch-Site", "same-site")
	req.Header.Set("Connection", "keep-alive")
}

// calculateRetryDelay 计算指数退避延迟
func calculateRetryDelay(attempt int) time.Duration {
	delay := emBaseRetryDelay * time.Duration(1<<uint(attempt))
	if delay > emMaxRetryDelay {
		delay = emMaxRetryDelay
	}
	return delay
}

// isRetryableError 判断是否值得重试的网络层错误
func isRetryableError(errMsg string) bool {
	keywords := []string{
		"EOF", "connection reset", "connection refused",
		"broken pipe", "i/o timeout", "TLS handshake timeout",
		"no such host", "transport connection broken",
		"use of closed network connection",
		"context deadline exceeded",
	}
	lower := strings.ToLower(errMsg)
	for _, kw := range keywords {
		if strings.Contains(lower, strings.ToLower(kw)) {
			return true
		}
	}
	return false
}

// truncateURL 截断 URL 用于日志（隐藏敏感参数）
func truncateURL(url string, maxLen int) string {
	if len(url) <= maxLen {
		return url
	}
	return url[:maxLen] + "..."
}

// truncateBytes 截断字节切片用于日志
func truncateBytes(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "..."
}

// ─────────────────────────────────────────────────────────────────
// 兼容性适配器（供旧代码过渡使用）
// ─────────────────────────────────────────────────────────────────

// newEMClient 创建独立的 HTTP Client（旧接口，建议使用 GetEMHTTPClient）
// Deprecated: 建议使用 GetEMHTTPClient() 获取共享客户端
func newEMClient(timeout time.Duration) *http.Client {
	if timeout <= 0 {
		timeout = emDefaultRequestTimeout
	}
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			MaxIdleConns:          emMaxIdleConns,
			MaxIdleConnsPerHost:   emMaxIdleConnsPerHost,
			IdleConnTimeout:       emIdleConnTimeout,
			DisableKeepAlives:     false, // 启用连接复用
			TLSHandshakeTimeout:   emTLSHandshakeTimeout,
			ResponseHeaderTimeout: emResponseHeaderTimeout,
			DialContext: (&net.Dialer{
				Timeout:   emDialTimeout,
				KeepAlive: emTCPKeepAlive,
			}).DialContext,
			ForceAttemptHTTP2: false,
		},
	}
}
