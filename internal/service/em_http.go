package service

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-resty/resty/v2"
	"go.uber.org/zap"
)

// ═══════════════════════════════════════════════════════════════
// em_http.go — 东方财富 HTTP 请求统一封装（基于 go-resty）
//
// 设计目标：
//   1. 全局共享 resty.Client，复用底层连接池
//   2. 统一的请求头设置，完整模拟 Chrome
//   3. 统一的 Cookie 注入
//   4. 指数退避重试（resty 原生支持）
//   5. 可配置的超时和重试参数
//
// 使用方式：
//   client := GetEMHTTPClient()
//   body, err := client.FetchBody(ctx, url, nil)
// ═══════════════════════════════════════════════════════════════

// ─────────────────────────────────────────────────────────────────
// 配置常量
// ─────────────────────────────────────────────────────────────────

const (
	// 连接池
	// ★ IdleConnTimeout 设为 15s：
	//   东财 push2 服务器 Keep-Alive 约 20s，设 15s 确保客户端主动淘汰
	//   避免拿到已被服务器关闭的 stale 连接导致 EOF
	emMaxIdleConns        = 100
	emMaxIdleConnsPerHost = 10 // 降低单 host 空闲连接数，减少 stale 连接积压
	emMaxConnsPerHost     = 30
	emIdleConnTimeout     = 15 * time.Second // ★ 从 30s 降到 15s，核心修复

	// 超时
	emDialTimeout           = 5 * time.Second
	emTLSHandshakeTimeout   = 5 * time.Second
	emResponseHeaderTimeout = 15 * time.Second
	emDefaultRequestTimeout = 20 * time.Second
	emTCPKeepAlive          = 15 * time.Second

	// 重试
	// ★ 从 3 次提高到 5 次：EOF 重试本身很快（<1s），多给几次机会
	emDefaultMaxRetries = 5
	emBaseRetryDelay    = 100 * time.Millisecond
	emMaxRetryDelay     = 3 * time.Second
)

// ─────────────────────────────────────────────────────────────────
// EMHTTPClient — 东财专用 HTTP 客户端（resty 版）
// ─────────────────────────────────────────────────────────────────

type EMHTTPClient struct {
	r          *resty.Client
	log        *zap.Logger
	maxRetries int
}

var (
	globalEMClient     *EMHTTPClient
	globalEMClientOnce sync.Once
)

// GetEMHTTPClient 返回全局单例
func GetEMHTTPClient() *EMHTTPClient {
	globalEMClientOnce.Do(func() {
		transport := &http.Transport{
			MaxIdleConns:        emMaxIdleConns,
			MaxIdleConnsPerHost: emMaxIdleConnsPerHost, // ★ 已降至 10
			MaxConnsPerHost:     emMaxConnsPerHost,
			IdleConnTimeout:     emIdleConnTimeout, // ★ 已降至 15s
			DialContext: (&net.Dialer{
				Timeout:   emDialTimeout,
				KeepAlive: emTCPKeepAlive,
			}).DialContext,
			TLSHandshakeTimeout:   emTLSHandshakeTimeout,
			ResponseHeaderTimeout: emResponseHeaderTimeout,
			// ★ 关键：允许 Go 在写请求后探测连接是否仍然有效
			// 若服务器已关闭连接，Go 会立即重拨而非返回 EOF 给上层
			ExpectContinueTimeout: 1 * time.Second,
			DisableKeepAlives:     false,
			ForceAttemptHTTP2:     false,
		}

		r := resty.NewWithClient(&http.Client{Transport: transport})

		// 全局默认超时
		r.SetTimeout(emDefaultRequestTimeout)

		// 全局默认请求头（模拟 Chrome）
		r.SetHeaders(map[string]string{
			"User-Agent":      "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36",
			"Accept":          "*/*",
			"Accept-Language": "zh-CN,zh;q=0.9,en;q=0.8",
			"Accept-Encoding": "gzip",
			"Referer":         "https://quote.eastmoney.com/",
			"Sec-Fetch-Dest":  "empty",
			"Sec-Fetch-Mode":  "cors",
			"Sec-Fetch-Site":  "same-site",
			"Connection":      "keep-alive",
		})

		// 重试次数和等待时间（resty 内置指数退避）
		r.SetRetryCount(emDefaultMaxRetries).
			SetRetryWaitTime(emBaseRetryDelay).
			SetRetryMaxWaitTime(emMaxRetryDelay)

		// 重试条件：网络错误（含 EOF/stale 连接）或 5xx
		// ★ 同时检查 resp 为 nil 的情况（连接层错误时 resp 可能为 nil）
		r.AddRetryCondition(func(resp *resty.Response, err error) bool {
			if err != nil {
				return isRetryableError(err.Error())
			}
			if resp == nil {
				return true
			}
			return resp.StatusCode() >= 500
		})

		// hook：每次请求前注入最新 Cookie（含重试）
		r.OnBeforeRequest(func(c *resty.Client, req *resty.Request) error {
			if globalTM != nil {
				cookie, err := globalTM.GetStockCookie()
				if err == nil && cookie != "" {
					req.SetHeader("Cookie", cookie)
				}
			}
			return nil
		})

		// hook：重试时打印日志，方便观察 EOF 重试频率
		r.OnAfterResponse(func(c *resty.Client, resp *resty.Response) error {
			if globalTMLog != nil && resp != nil && resp.Request != nil {
				attempt := resp.Request.Attempt
				if attempt > 1 {
					globalTMLog.Warn("em_http: retry succeeded",
						zap.Int("attempt", attempt),
						zap.String("url", truncateURL(resp.Request.URL, 80)),
						zap.Int("status", resp.StatusCode()),
					)
				}
			}
			return nil
		})

		globalEMClient = &EMHTTPClient{
			r:          r,
			log:        globalTMLog,
			maxRetries: emDefaultMaxRetries,
		}
	})
	return globalEMClient
}

// EMRequestOption 请求配置选项
type EMRequestOption struct {
	Timeout    time.Duration     // 覆盖默认超时
	MaxRetries int               // 覆盖默认重试次数（暂不支持 per-request 覆盖，需 fork client）
	Headers    map[string]string // 额外请求头
	SkipCookie bool              // 跳过 Cookie 注入
}

// ─────────────────────────────────────────────────────────────────
// 核心方法
// ─────────────────────────────────────────────────────────────────

// FetchBody 执行 GET 请求并返回响应 body 字节。
// resty 在函数返回前已完成全部 IO（包括 gzip 解压），
// 不存在原生 net/http defer cancel 导致 context canceled 的问题。
func (c *EMHTTPClient) FetchBody(ctx context.Context, url string, opt *EMRequestOption) ([]byte, error) {
	resp, err := c.fetchBodyOnce(ctx, url, opt)
	if err != nil && isEOFLikeError(err) {
		if globalTM != nil {
			_ = ForceRefreshCookie()
		}
		retryOpt := cloneEMRequestOption(opt)
		if retryOpt.Headers == nil {
			retryOpt.Headers = make(map[string]string, 1)
		}
		// EOF 常见于复用到已失效的 keep-alive 连接；兜底改为新连接再试一次。
		retryOpt.Headers["Connection"] = "close"
		resp, err = c.fetchBodyOnce(ctx, url, retryOpt)
	}
	if err != nil {
		if c.log != nil {
			c.log.Error("em_http: request failed",
				zap.String("url", truncateURL(url, 120)),
				zap.Error(err),
			)
		}
		return nil, fmt.Errorf("em_http: %w", err)
	}

	if resp.StatusCode() != http.StatusOK {
		return nil, fmt.Errorf("em_http: status %d: %s",
			resp.StatusCode(), truncateBytes(resp.Body(), 200))
	}

	body := resp.Body()
	if len(body) == 0 {
		return nil, fmt.Errorf("em_http: empty response body")
	}

	if c.log != nil {
		c.log.Debug("em_http: ok",
			zap.String("url", truncateURL(url, 80)),
			zap.Int("status", resp.StatusCode()),
			zap.Int("bytes", len(body)),
			zap.Duration("elapsed", resp.Time()),
		)
	}

	return body, nil
}

func (c *EMHTTPClient) fetchBodyOnce(ctx context.Context, url string, opt *EMRequestOption) (*resty.Response, error) {
	req, cancel := c.buildRequest(ctx, opt)
	defer cancel()
	return req.Get(url)
}

// buildRequest 构造 resty.Request，注入 per-request 级别的配置。
// resty.Request 没有 SetTimeout，per-request 超时通过给 ctx 加 deadline 实现。
func (c *EMHTTPClient) buildRequest(ctx context.Context, opt *EMRequestOption) (*resty.Request, context.CancelFunc) {
	// 默认不需要额外 cancel
	cancel := context.CancelFunc(func() {})

	// per-request 超时：用带 deadline 的子 ctx 传给 resty
	if opt != nil && opt.Timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, opt.Timeout)
	}

	req := c.r.R().SetContext(ctx)

	if opt == nil {
		return req, cancel
	}

	// 额外请求头
	if len(opt.Headers) > 0 {
		req.SetHeaders(opt.Headers)
	}

	// SkipCookie：覆盖掉 OnBeforeRequest 注入的 Cookie
	if opt.SkipCookie {
		req.SetHeader("Cookie", "")
	}

	return req, cancel
}

// ─────────────────────────────────────────────────────────────────
// 兼容旧接口（Do / DoWithRetry）
// ─────────────────────────────────────────────────────────────────

// Do 兼容旧调用方，返回原始 *http.Response。
// 注意：Body 由 resty 已读完并缓存，RawResponse.Body 实际已关闭，
// 请直接使用 resp.Body 字段（[]byte），或改用 FetchBody。
func (c *EMHTTPClient) Do(ctx context.Context, url string, opt *EMRequestOption) (*http.Response, error) {
	req, cancel := c.buildRequest(ctx, opt)
	defer cancel()
	resp, err := req.Get(url)
	if err != nil {
		return nil, fmt.Errorf("em_http: %w", err)
	}
	return resp.RawResponse, nil
}

// DoWithRetry 等同于 Do（resty 已内置重试，无需区分）。
func (c *EMHTTPClient) DoWithRetry(ctx context.Context, url string, opt *EMRequestOption) (*http.Response, error) {
	return c.Do(ctx, url, opt)
}

// ─────────────────────────────────────────────────────────────────
// 辅助函数
// ─────────────────────────────────────────────────────────────────

// isRetryableError 判断是否值得重试的网络层错误
// EOF 是最常见的 stale Keep-Alive 连接错误，必须重试
func isRetryableError(errMsg string) bool {
	keywords := []string{
		// stale 连接 / Keep-Alive 过期
		"EOF",
		"unexpected EOF",
		"use of closed network connection",
		"transport connection broken",
		// 网络层瞬断
		"connection reset",
		"connection refused",
		"broken pipe",
		// 超时类
		"i/o timeout",
		"TLS handshake timeout",
		"context deadline exceeded",
		// DNS
		"no such host",
	}
	lower := strings.ToLower(errMsg)
	for _, kw := range keywords {
		if strings.Contains(lower, strings.ToLower(kw)) {
			return true
		}
	}
	return false
}

func isEOFLikeError(err error) bool {
	if err == nil {
		return false
	}
	lower := strings.ToLower(err.Error())
	return strings.Contains(lower, "eof") ||
		strings.Contains(lower, "closed network connection") ||
		strings.Contains(lower, "transport connection broken")
}

func cloneEMRequestOption(opt *EMRequestOption) *EMRequestOption {
	if opt == nil {
		return &EMRequestOption{}
	}
	cloned := *opt
	if opt.Headers != nil {
		cloned.Headers = make(map[string]string, len(opt.Headers))
		for k, v := range opt.Headers {
			cloned.Headers[k] = v
		}
	}
	return &cloned
}

// truncateURL 截断 URL 用于日志
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

// newEMClient 旧接口兼容，建议迁移到 GetEMHTTPClient()
// Deprecated: 使用 GetEMHTTPClient() 代替
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
			DisableKeepAlives:     false,
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
