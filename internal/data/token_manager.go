package data

import (
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sync"
	"time"

	"go.uber.org/zap"
)

// TokenManager 全局 Token 管理器
type TokenManager struct {
	qgssid     string
	lastUpdate time.Time
	mu         sync.RWMutex
	client     *http.Client
	log        *zap.Logger
}

// NewTokenManager 创建一个新的 Token 管理器
func NewTokenManager(log *zap.Logger) *TokenManager {
	return &TokenManager{
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
		log: log,
	}
}

// GetToken 获取当前有效的 Token，如果超过 1 小时则自动更新
func (tm *TokenManager) GetToken() (string, error) {
	tm.mu.RLock()
	if tm.qgssid != "" && time.Since(tm.lastUpdate) < time.Hour {
		token := tm.qgssid
		tm.mu.RUnlock()
		return token, nil
	}
	tm.mu.RUnlock()

	// 触发更新
	if err := tm.UpdateToken(); err != nil {
		return "", err
	}

	tm.mu.RLock()
	defer tm.mu.RUnlock()
	return tm.qgssid, nil
}

// UpdateToken 重新获取 Token
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

// fetchNewToken 从 HTML 中提取 Token
func (tm *TokenManager) fetchNewToken() (string, error) {
	url := "http://quote.eastmoney.com/center/gridlist.html"
	req, _ := http.NewRequest("GET", url, nil)
	// 基础伪装
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

	// 使用正则提取 qgssid=([a-zA-Z0-9]+)
	re := regexp.MustCompile(`qgssid=([a-zA-Z0-9]+)`)
	matches := re.FindSubmatch(body)
	if len(matches) < 2 {
		return "", fmt.Errorf("qgssid not found in HTML response")
	}

	return string(matches[1]), nil
}
