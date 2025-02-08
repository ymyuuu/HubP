// proxy.go
package proxy

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

// 常量定义
const (
	targetHost = "registry-1.docker.io"  // Docker Hub 的目标主机地址
	tokenTTL   = 5 * time.Minute        // token 缓存时间
)

// TokenInfo 保存 token 信息的结构体
type TokenInfo struct {
	Token      string    // token 值
	Expiry     time.Time // 过期时间
	CreateTime time.Time // 创建时间
}

// tokenCache token 缓存管理器
type tokenCacheManager struct {
	sync.RWMutex
	cache map[string]TokenInfo
}

// 全局变量
var (
	// 创建 token 缓存实例
	tokenCache = &tokenCacheManager{
		cache: make(map[string]TokenInfo),
	}

	// 自定义 HTTP 客户端，禁用自动重定向
	client = &http.Client{
		Timeout: 30 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
)

// HandleProxy 处理 Docker 相关代理请求的入口函数
func HandleProxy(w http.ResponseWriter, r *http.Request) {
	startTime := time.Now()
	requestID := fmt.Sprintf("%d", startTime.UnixNano())

	// 记录请求开始
	logrus.Debugf("[Docker][%s] 收到请求: %s %s", requestID, r.Method, r.URL.Path)

	// 构造目标 URL
	targetURL := &url.URL{
		Scheme:   "https",
		Host:     targetHost,
		Path:     r.URL.Path,
		RawQuery: r.URL.RawQuery,
	}

	// 发起代理请求
	if err := proxyRequest(w, r, targetURL.String(), requestID); err != nil {
		logrus.Errorf("[Docker][%s] 代理请求失败: %v", requestID, err)
		if !isResponseWritten(w) {
			http.Error(w, "内部服务器错误", http.StatusInternalServerError)
		}
		return
	}

	// 记录请求完成
	duration := time.Since(startTime)
	logrus.Debugf("[Docker][%s] 请求完成，耗时: %v", requestID, duration)
}

// proxyRequest 发起代理请求并处理响应
func proxyRequest(w http.ResponseWriter, r *http.Request, targetURL, requestID string) error {
	logrus.Debugf("[Docker][%s] 转发请求至: %s", requestID, targetURL)

	// 创建新的请求
	newReq, err := http.NewRequest(r.Method, targetURL, r.Body)
	if err != nil {
		return fmt.Errorf("创建请求失败: %v", err)
	}

	// 复制请求头
	copyHeaders(newReq.Header, r.Header)
	newReq.Header.Set("Host", targetHost)
	newReq.Header.Del("Accept-Encoding")

	// 发起请求
	resp, err := client.Do(newReq)
	if err != nil {
		return fmt.Errorf("发送请求失败: %v", err)
	}
	defer resp.Body.Close()

	// 处理认证错误
	if resp.StatusCode == http.StatusUnauthorized {
		logrus.Debugf("[Docker][%s] 需要认证，正在获取 token", requestID)
		return handleAuthAndRetry(w, r, targetURL, resp, requestID)
	}

	// 处理重定向
	if isRedirect(resp.StatusCode) {
		logrus.Debugf("[Docker][%s] 处理重定向响应", requestID)
		return handleRedirect(w, r, resp, targetURL, requestID)
	}

	// 写入响应
	return writeResponse(w, resp, requestID)
}

// handleAuthAndRetry 处理认证并重试请求
func handleAuthAndRetry(w http.ResponseWriter, r *http.Request, targetURL string, resp *http.Response, requestID string) error {
	authHeader := resp.Header.Get("WWW-Authenticate")
	token, err := handleAuth(authHeader, requestID)
	if err != nil {
		logrus.Debugf("[Docker][%s] 获取 token 失败: %v", requestID, err)
		return writeResponse(w, resp, requestID)
	}

	logrus.Debugf("[Docker][%s] 使用新 token 重试请求", requestID)
	return retryWithToken(w, r, targetURL, token, requestID)
}

// handleAuth 处理认证，获取 token
func handleAuth(authHeader, requestID string) (string, error) {
	authParams := parseAuth(authHeader)
	
	// 验证必要参数
	realm := authParams["realm"]
	service := authParams["service"]
	scope := authParams["scope"]
	
	if realm == "" || service == "" {
		return "", errors.New("认证参数不完整")
	}

	// 构造缓存键
	cacheKey := fmt.Sprintf("%s:%s:%s", realm, service, scope)

	// 检查缓存
	tokenCache.RLock()
	if info, exists := tokenCache.cache[cacheKey]; exists && time.Now().Before(info.Expiry) {
		tokenCache.RUnlock()
		logrus.Debugf("[Docker][%s] 使用缓存的 token (剩余有效期: %v)", 
			requestID, info.Expiry.Sub(time.Now()))
		return info.Token, nil
	}
	tokenCache.RUnlock()

	// 获取新 token
	logrus.Debugf("[Docker][%s] 正在获取新的 token", requestID)
	token, err := fetchToken(realm, service, scope, requestID)
	if err != nil {
		return "", err
	}

	// 更新缓存
	tokenCache.Lock()
	tokenCache.cache[cacheKey] = TokenInfo{
		Token:      token,
		CreateTime: time.Now(),
		Expiry:     time.Now().Add(tokenTTL),
	}
	tokenCache.Unlock()

	return token, nil
}

// retryWithToken 使用 token 重试请求
func retryWithToken(w http.ResponseWriter, r *http.Request, targetURL, token, requestID string) error {
	newReq, err := http.NewRequest(r.Method, targetURL, r.Body)
	if err != nil {
		return err
	}

	copyHeaders(newReq.Header, r.Header)
	newReq.Header.Set("Host", targetHost)
	newReq.Header.Set("Authorization", "Bearer "+token)
	newReq.Header.Del("Accept-Encoding")

	resp, err := client.Do(newReq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if isRedirect(resp.StatusCode) {
		return handleRedirect(w, r, resp, targetURL, requestID)
	}

	return writeResponse(w, resp, requestID)
}

// handleRedirect 处理重定向请求
func handleRedirect(w http.ResponseWriter, r *http.Request, resp *http.Response, targetURL, requestID string) error {
	location := resp.Header.Get("Location")
	if location == "" {
		logrus.Debugf("[Docker][%s] 重定向响应中缺少 Location 头", requestID)
		return writeResponse(w, resp, requestID)
	}

	redirectURL, err := url.Parse(location)
	if err != nil {
		return fmt.Errorf("解析重定向 URL 失败: %v", err)
	}

	baseURL, err := url.Parse(targetURL)
	if err != nil {
		return fmt.Errorf("解析基础 URL 失败: %v", err)
	}

	newURL := baseURL.ResolveReference(redirectURL)
	logrus.Debugf("[Docker][%s] 重定向至: %s", requestID, newURL.String())
	
	return proxyRequest(w, r, newURL.String(), requestID)
}

// writeResponse 将响应写回客户端
func writeResponse(w http.ResponseWriter, resp *http.Response, requestID string) error {
	copyHeaders(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)

	written, err := io.Copy(w, resp.Body)
	if err != nil {
		return fmt.Errorf("写入响应体失败: %v", err)
	}

	logrus.Debugf("[Docker][%s] 已写入响应 (状态码: %d, 大小: %d 字节)", 
		requestID, resp.StatusCode, written)
	return nil
}

// copyHeaders 复制 HTTP 头
func copyHeaders(dst, src http.Header) {
	for key, values := range src {
		dst[key] = append([]string(nil), values...)
	}
}

// parseAuth 解析认证头
func parseAuth(header string) map[string]string {
	result := make(map[string]string)
	header = strings.TrimPrefix(header, "Bearer ")
	
	for _, part := range strings.Split(header, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		
		if kv := strings.SplitN(part, "=", 2); len(kv) == 2 {
			key := strings.TrimSpace(kv[0])
			value := strings.Trim(strings.TrimSpace(kv[1]), `"`)
			result[key] = value
		}
	}
	
	return result
}

// fetchToken 从认证服务器获取 token
func fetchToken(realm, service, scope, requestID string) (string, error) {
	reqURL, err := url.Parse(realm)
	if err != nil {
		return "", fmt.Errorf("解析认证 URL 失败: %v", err)
	}

	query := reqURL.Query()
	query.Set("service", service)
	if scope != "" {
		query.Set("scope", scope)
	}
	reqURL.RawQuery = query.Encode()

	logrus.Debugf("[Docker][%s] 请求 token URL: %s", requestID, reqURL.String())

	// 发送请求
	tokenResp, err := client.Get(reqURL.String())
	if err != nil {
		return "", fmt.Errorf("请求 token 失败: %v", err)
	}
	defer tokenResp.Body.Close()

	if tokenResp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("获取 token 失败，状态码: %d", tokenResp.StatusCode)
	}

	// 解析响应
	var response struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in,omitempty"`
	}

	if err := json.NewDecoder(tokenResp.Body).Decode(&response); err != nil {
		return "", fmt.Errorf("解析 token 响应失败: %v", err)
	}

	// 返回 token
	if response.Token != "" {
		return response.Token, nil
	}
	if response.AccessToken != "" {
		return response.AccessToken, nil
	}

	return "", errors.New("响应中未找到有效的 token")
}

// isRedirect 判断状态码是否为重定向
func isRedirect(statusCode int) bool {
	switch statusCode {
	case http.StatusMovedPermanently,
		http.StatusFound,
		http.StatusSeeOther,
		http.StatusTemporaryRedirect,
		http.StatusPermanentRedirect:
		return true
	default:
		return false
	}
}

// isResponseWritten 检查响应是否已经被写入
func isResponseWritten(w http.ResponseWriter) bool {
	if rw, ok := w.(interface{ Status() int }); ok {
		return rw.Status() != 0
	}
	return false
}
