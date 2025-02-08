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

	"github.com/sirupsen/logrus"
)

// targetHost 定义目标 Docker 主机地址
const targetHost = "registry-1.docker.io"

// tokenCache 用于缓存认证 token
var tokenCache = struct {
	sync.RWMutex
	cache map[string]string
}{
	cache: make(map[string]string),
}

// 创建自定义的 HTTP 客户端，禁用自动重定向
var client = &http.Client{
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

// HandleProxy 处理 Docker 相关代理请求的入口函数
func HandleProxy(w http.ResponseWriter, r *http.Request) {
	// 构造目标 URL
	targetURL := &url.URL{
		Scheme:   "https",
		Host:     targetHost,
		Path:     r.URL.Path,
		RawQuery: r.URL.RawQuery,
	}
	logrus.Debugf("Docker 代理请求: %s %s", r.Method, targetURL.String())

	// 发起代理请求
	if err := proxyRequest(w, r, targetURL.String()); err != nil {
		http.Error(w, "代理请求失败", http.StatusInternalServerError)
		logrus.Errorf("代理请求失败: %v", err)
	}
}

// proxyRequest 发起代理请求并处理响应，使用流式处理
func proxyRequest(w http.ResponseWriter, r *http.Request, targetURL string) error {
	// 创建新的 HTTP 请求，保持原始请求体
	newReq, err := http.NewRequest(r.Method, targetURL, r.Body)
	if err != nil {
		return fmt.Errorf("创建请求失败: %v", err)
	}

	// 复制请求头
	copyHeaders(newReq.Header, r.Header)
	newReq.Header.Set("Host", targetHost)
	newReq.Header.Del("Accept-Encoding") // 防止压缩响应

	// 发起请求
	resp, err := client.Do(newReq)
	if err != nil {
		return fmt.Errorf("发送请求失败: %v", err)
	}
	defer resp.Body.Close()

	// 处理 401 认证错误
	if resp.StatusCode == http.StatusUnauthorized {
		token, err := handleAuth(resp.Header.Get("WWW-Authenticate"))
		if err != nil {
			return writeResponse(w, resp)
		}
		return retryWithToken(w, r, targetURL, token)
	}

	// 处理重定向
	if isRedirect(resp.StatusCode) {
		return handleRedirect(w, r, resp, targetURL)
	}

	// 写入响应
	return writeResponse(w, resp)
}

// handleAuth 处理认证，获取 token
func handleAuth(authHeader string) (string, error) {
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
	if token, exists := tokenCache.cache[cacheKey]; exists {
		tokenCache.RUnlock()
		return token, nil
	}
	tokenCache.RUnlock()

	// 获取新 token
	token, err := fetchToken(realm, service, scope)
	if err != nil {
		return "", err
	}

	// 更新缓存
	tokenCache.Lock()
	tokenCache.cache[cacheKey] = token
	tokenCache.Unlock()

	return token, nil
}

// retryWithToken 使用 token 重试请求
func retryWithToken(w http.ResponseWriter, r *http.Request, targetURL, token string) error {
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
		return handleRedirect(w, r, resp, targetURL)
	}

	return writeResponse(w, resp)
}

// handleRedirect 处理重定向请求
func handleRedirect(w http.ResponseWriter, r *http.Request, resp *http.Response, targetURL string) error {
	location := resp.Header.Get("Location")
	if location == "" {
		return writeResponse(w, resp)
	}

	redirectURL, err := url.Parse(location)
	if err != nil {
		return err
	}

	baseURL, err := url.Parse(targetURL)
	if err != nil {
		return err
	}

	newURL := baseURL.ResolveReference(redirectURL)
	logrus.Debugf("处理重定向，新的 URL: %s", newURL.String())
	
	return proxyRequest(w, r, newURL.String())
}

// writeResponse 将响应写回客户端，使用流式处理
func writeResponse(w http.ResponseWriter, resp *http.Response) error {
	// 复制响应头
	copyHeaders(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)

	// 流式复制响应体
	_, err := io.Copy(w, resp.Body)
	return err
}

// copyHeaders 高效复制 HTTP 头
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
func fetchToken(realm, service, scope string) (string, error) {
	// 构造认证 URL
	reqURL, err := url.Parse(realm)
	if err != nil {
		return "", err
	}

	query := reqURL.Query()
	query.Set("service", service)
	if scope != "" {
		query.Set("scope", scope)
	}
	reqURL.RawQuery = query.Encode()

	// 发送请求
	tokenResp, err := client.Get(reqURL.String())
	if err != nil {
		return "", err
	}
	defer tokenResp.Body.Close()

	if tokenResp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("获取 token 失败，状态码: %d", tokenResp.StatusCode)
	}

	// 解析响应
	var response struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
	}

	if err := json.NewDecoder(tokenResp.Body).Decode(&response); err != nil {
		return "", err
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
