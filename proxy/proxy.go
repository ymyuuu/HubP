// proxy/proxy.go
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
	registryHost   = "registry-1.docker.io" // Docker Registry 的主机地址
	authHost       = "auth.docker.io"       // Docker Auth 的主机地址
	cloudflareHost = "production.cloudflare.docker.com" // Docker Cloudflare 的主机地址
)

// 自定义 HTTP 客户端，用于所有请求
var client = &http.Client{
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		// 不自动跟随重定向，让我们自己处理
		return http.ErrUseLastResponse
	},
}

// HandleRegistryRequest 处理 Docker Registry 请求
func HandleRegistryRequest(w http.ResponseWriter, r *http.Request) {
	// 获取当前域名，用于设置认证头
	currentDomain := r.Host

	// 记录请求信息
	logrus.Debugf("[Registry] 收到请求: %s %s", r.Method, r.URL.Path)

	// 提取路径
	path := strings.TrimPrefix(r.URL.Path, "/v2/")

	// 修改 URL，将请求转发到 Docker Registry
	targetURL := &url.URL{
		Scheme:   "https",
		Host:     registryHost,
		Path:     fmt.Sprintf("/v2/%s", path),
		RawQuery: r.URL.RawQuery,
	}

	// 复制请求头，并设置 Host
	headers := CopyHttpHeaders(r.Header)
	headers.Set("Host", registryHost)
	headers.Del("Accept-Encoding") // 防止压缩响应

	// 创建新的请求
	newReq, err := http.NewRequest(r.Method, targetURL.String(), r.Body)
	if err != nil {
		logrus.Errorf("[Registry] 创建请求失败: %v", err)
		http.Error(w, "服务器错误", http.StatusInternalServerError)
		return
	}
	newReq.Header = headers

	// 发送请求并获取响应
	resp, err := client.Do(newReq)
	if err != nil {
		logrus.Errorf("[Registry] 请求失败: %v", err)
		http.Error(w, "服务器错误", http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()

	// 处理 401 认证错误
	if resp.StatusCode == http.StatusUnauthorized {
		logrus.Debug("[Registry] 需要认证，尝试获取 token")
		// 处理认证
		handleAuthChallenge(w, r, resp, targetURL.String(), currentDomain)
		return
	}

	// 创建新的响应头
	respHeaders := CopyHttpHeaders(resp.Header)

	// 修改认证头，指向我们自己的代理服务
	if authHeader := respHeaders.Get("WWW-Authenticate"); authHeader != "" {
		respHeaders.Set("WWW-Authenticate", 
			fmt.Sprintf(`Bearer realm="https://%s/auth/token", service="registry.docker.io"`, currentDomain))
	}

	// 返回修改后的响应
	w.WriteHeader(resp.StatusCode)
	for key, values := range respHeaders {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}

	// 传输响应体
	written, err := io.Copy(w, resp.Body)
	if err != nil {
		logrus.Errorf("[Registry] 传输响应失败: %v", err)
		return
	}

	logrus.Debugf("[Registry] 响应完成 [状态码: %d] [大小: %.2f KB]", 
		resp.StatusCode, float64(written)/1024)
}

// HandleAuthRequest 处理 Docker Auth 请求
func HandleAuthRequest(w http.ResponseWriter, r *http.Request) {
	// 记录请求信息
	logrus.Debugf("[Auth] 收到请求: %s %s", r.Method, r.URL.Path)

	// 提取路径
	path := strings.TrimPrefix(r.URL.Path, "/auth/")

	// 修改 URL，将请求转发到 Docker Auth
	targetURL := &url.URL{
		Scheme:   "https",
		Host:     authHost,
		Path:     fmt.Sprintf("/%s", path),
		RawQuery: r.URL.RawQuery,
	}

	// 复制请求头，并设置 Host
	headers := CopyHttpHeaders(r.Header)
	headers.Set("Host", authHost)
	headers.Del("Accept-Encoding") // 防止压缩响应

	// 创建新的请求
	newReq, err := http.NewRequest(r.Method, targetURL.String(), r.Body)
	if err != nil {
		logrus.Errorf("[Auth] 创建请求失败: %v", err)
		http.Error(w, "服务器错误", http.StatusInternalServerError)
		return
	}
	newReq.Header = headers

	// 发送请求并获取响应
	resp, err := client.Do(newReq)
	if err != nil {
		logrus.Errorf("[Auth] 请求失败: %v", err)
		http.Error(w, "服务器错误", http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()

	// 返回响应
	for key, values := range resp.Header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	w.WriteHeader(resp.StatusCode)

	// 传输响应体
	written, err := io.Copy(w, resp.Body)
	if err != nil {
		logrus.Errorf("[Auth] 传输响应失败: %v", err)
		return
	}

	logrus.Debugf("[Auth] 响应完成 [状态码: %d] [大小: %.2f KB]", 
		resp.StatusCode, float64(written)/1024)
}

// HandleCloudflareRequest 处理 Docker Cloudflare 请求
func HandleCloudflareRequest(w http.ResponseWriter, r *http.Request) {
	// 记录请求信息
	logrus.Debugf("[Cloudflare] 收到请求: %s %s", r.Method, r.URL.Path)

	// 提取路径
	path := strings.TrimPrefix(r.URL.Path, "/production-cloudflare/")

	// 修改 URL，将请求转发到 Docker Cloudflare
	targetURL := &url.URL{
		Scheme:   "https",
		Host:     cloudflareHost,
		Path:     fmt.Sprintf("/%s", path),
		RawQuery: r.URL.RawQuery,
	}

	// 复制请求头，并设置 Host
	headers := CopyHttpHeaders(r.Header)
	headers.Set("Host", cloudflareHost)
	headers.Del("Accept-Encoding") // 防止压缩响应

	// 创建新的请求
	newReq, err := http.NewRequest(r.Method, targetURL.String(), r.Body)
	if err != nil {
		logrus.Errorf("[Cloudflare] 创建请求失败: %v", err)
		http.Error(w, "服务器错误", http.StatusInternalServerError)
		return
	}
	newReq.Header = headers

	// 发送请求并获取响应
	resp, err := client.Do(newReq)
	if err != nil {
		logrus.Errorf("[Cloudflare] 请求失败: %v", err)
		http.Error(w, "服务器错误", http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()

	// 返回响应
	for key, values := range resp.Header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	w.WriteHeader(resp.StatusCode)

	// 传输响应体
	written, err := io.Copy(w, resp.Body)
	if err != nil {
		logrus.Errorf("[Cloudflare] 传输响应失败: %v", err)
		return
	}

	logrus.Debugf("[Cloudflare] 响应完成 [状态码: %d] [大小: %.2f KB]", 
		resp.StatusCode, float64(written)/1024)
}

// CopyHeaders 复制 HTTP 头
func CopyHeaders(dst, src http.Header) {
	for key, values := range src {
		for _, value := range values {
			dst.Add(key, value)
		}
	}
}

// CopyHttpHeaders 复制 HTTP 头并返回新的头集合
func CopyHttpHeaders(src http.Header) http.Header {
	dst := make(http.Header)
	CopyHeaders(dst, src)
	return dst
}

// handleAuthChallenge 处理认证挑战
func handleAuthChallenge(w http.ResponseWriter, r *http.Request, resp *http.Response, targetURL, currentDomain string) {
	// 获取认证参数
	authHeader := resp.Header.Get("WWW-Authenticate")
	authParams := parseAuth(authHeader)

	// 修改认证头，指向我们自己的代理服务
	modifiedAuthHeader := fmt.Sprintf(`Bearer realm="https://%s/auth/token", service="registry.docker.io"`, currentDomain)
	if scope := authParams["scope"]; scope != "" {
		modifiedAuthHeader += fmt.Sprintf(`, scope="%s"`, scope)
	}

	// 复制响应头
	for key, values := range resp.Header {
		for _, value := range values {
			if key == "WWW-Authenticate" {
				w.Header().Add(key, modifiedAuthHeader)
			} else {
				w.Header().Add(key, value)
			}
		}
	}

	// 设置状态码
	w.WriteHeader(resp.StatusCode)

	// 传输响应体
	written, err := io.Copy(w, resp.Body)
	if err != nil {
		logrus.Errorf("[Registry] 传输认证响应失败: %v", err)
		return
	}

	logrus.Debugf("[Registry] 认证响应完成 [状态码: %d] [大小: %.2f KB]", 
		resp.StatusCode, float64(written)/1024)
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

// fetchToken 从认证服务器获取 token (保留以备需要)
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

	logrus.Debugf("[Registry] 请求 token: %s", reqURL.String())

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
