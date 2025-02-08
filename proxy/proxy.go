// proxy.go
package proxy

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/sirupsen/logrus"
)

// 常量定义
const (
	targetHost = "registry-1.docker.io" // Docker Hub 的目标主机地址
)

// 自定义 HTTP 客户端
var client = &http.Client{
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

// HandleProxy 处理 Docker 相关代理请求
func HandleProxy(w http.ResponseWriter, r *http.Request) {
	// 构造目标 URL
	targetURL := &url.URL{
		Scheme:   "https",
		Host:     targetHost,
		Path:     r.URL.Path,
		RawQuery: r.URL.RawQuery,
	}

	logrus.Debugf("[Docker] 收到请求: %s %s", r.Method, r.URL.Path)

	// 发起代理请求
	if err := proxyRequest(w, r, targetURL.String()); err != nil {
		logrus.Errorf("[Docker] 代理请求失败: %v", err)
		if !isResponseWritten(w) {
			http.Error(w, "服务器错误", http.StatusInternalServerError)
		}
	}
}

// proxyRequest 发起代理请求并处理响应
func proxyRequest(w http.ResponseWriter, r *http.Request, targetURL string) error {
	// 创建新的代理请求
	newReq, err := http.NewRequest(r.Method, targetURL, r.Body)
	if err != nil {
		return fmt.Errorf("创建请求失败: %v", err)
	}

	// 复制请求头
	copyHeaders(newReq.Header, r.Header)
	newReq.Header.Set("Host", targetHost)
	newReq.Header.Del("Accept-Encoding")

	logrus.Debugf("[Docker] 转发请求至: %s", targetURL)

	// 发送请求
	resp, err := client.Do(newReq)
	if err != nil {
		return fmt.Errorf("发送请求失败: %v", err)
	}
	defer resp.Body.Close()

	// 处理 401 认证错误
	if resp.StatusCode == http.StatusUnauthorized {
		logrus.Debug("[Docker] 需要认证，尝试获取 token")
		return handleAuthAndRetry(w, r, targetURL, resp)
	}

	// 处理重定向响应
	if isRedirect(resp.StatusCode) {
		logrus.Debug("[Docker] 处理重定向响应")
		return handleRedirect(w, r, resp, targetURL)
	}

	// 写入常规响应
	return writeResponse(w, resp)
}

// handleAuthAndRetry 处理认证并重试请求
func handleAuthAndRetry(w http.ResponseWriter, r *http.Request, targetURL string, resp *http.Response) error {
	// 获取认证参数
	authHeader := resp.Header.Get("WWW-Authenticate")
	authParams := parseAuth(authHeader)

	if authParams["realm"] == "" || authParams["service"] == "" {
		logrus.Debug("[Docker] 认证参数不完整，返回原始响应")
		return writeResponse(w, resp)
	}

	// 获取 token
	token, err := fetchToken(authParams)
	if err != nil {
		logrus.Errorf("[Docker] 获取 token 失败: %v", err)
		return writeResponse(w, resp)
	}

	logrus.Debug("[Docker] 获取 token 成功，重试请求")
	return retryWithToken(w, r, targetURL, token)
}

// retryWithToken 使用 token 重试请求
func retryWithToken(w http.ResponseWriter, r *http.Request, targetURL, token string) error {
	// 创建新的认证请求
	newReq, err := http.NewRequest(r.Method, targetURL, r.Body)
	if err != nil {
		return fmt.Errorf("创建认证请求失败: %v", err)
	}

	// 设置请求头
	copyHeaders(newReq.Header, r.Header)
	newReq.Header.Set("Host", targetHost)
	newReq.Header.Set("Authorization", "Bearer "+token)
	newReq.Header.Del("Accept-Encoding")

	// 发送请求
	resp, err := client.Do(newReq)
	if err != nil {
		return fmt.Errorf("发送认证请求失败: %v", err)
	}
	defer resp.Body.Close()

	// 处理重定向
	if isRedirect(resp.StatusCode) {
		return handleRedirect(w, r, resp, targetURL)
	}

	return writeResponse(w, resp)
}

// handleRedirect 处理重定向请求
func handleRedirect(w http.ResponseWriter, r *http.Request, resp *http.Response, targetURL string) error {
	location := resp.Header.Get("Location")
	if location == "" {
		logrus.Debug("[Docker] 重定向响应缺少 Location 头")
		return writeResponse(w, resp)
	}

	// 解析重定向 URL
	redirectURL, err := url.Parse(location)
	if err != nil {
		return fmt.Errorf("解析重定向 URL 失败: %v", err)
	}

	baseURL, err := url.Parse(targetURL)
	if err != nil {
		return fmt.Errorf("解析基础 URL 失败: %v", err)
	}

	// 获取新的完整 URL
	newURL := baseURL.ResolveReference(redirectURL)
	logrus.Debugf("[Docker] 重定向至: %s", newURL.String())
	
	return proxyRequest(w, r, newURL.String())
}

// writeResponse 将响应写回客户端
func writeResponse(w http.ResponseWriter, resp *http.Response) error {
	// 复制响应头
	copyHeaders(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)

	// 流式传输响应体
	written, err := io.Copy(w, resp.Body)
	if err != nil {
		return fmt.Errorf("写入响应体失败: %v", err)
	}

	if logrus.IsLevelEnabled(logrus.DebugLevel) {
		logrus.Debugf("[Docker] 响应完成 [状态码: %d] [大小: %.2f KB]", 
			resp.StatusCode, float64(written)/1024)
	}

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
func fetchToken(authParams map[string]string) (string, error) {
	// 构造认证 URL
	reqURL, err := url.Parse(authParams["realm"])
	if err != nil {
		return "", fmt.Errorf("解析认证 URL 失败: %v", err)
	}

	// 设置查询参数
	query := reqURL.Query()
	query.Set("service", authParams["service"])
	if scope := authParams["scope"]; scope != "" {
		query.Set("scope", scope)
	}
	reqURL.RawQuery = query.Encode()

	logrus.Debugf("[Docker] 请求 token: %s", reqURL.String())

	// 发送请求获取 token
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

	return "", fmt.Errorf("响应中未找到有效的 token")
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
