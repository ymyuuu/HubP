package proxy

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"

	"encoding/json"
	"errors"

	"github.com/sirupsen/logrus"
)

// targetHost 定义目标 Docker Registry 的地址
// 所有 /v2/ 请求将被转发至该地址
const targetHost = "registry-1.docker.io"

// 自定义 HTTP 客户端，用于控制重定向行为。因为对 Docker Registry 的访问常常需要
// 处理 401、重定向等情况，所以我们需要自定义 CheckRedirect。
var client = &http.Client{
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		// 返回此错误时，http.Client 不会自动重定向
		return http.ErrUseLastResponse
	},
}

// HandleProxy 处理 Docker 相关代理请求的入口函数
// 当 main.go 中发现请求路径以 /v2/ 开头时，就会调用本函数
func HandleProxy(w http.ResponseWriter, r *http.Request) {
	// 构造目标 URL，保持原请求的 Path 和 Query
	targetURL := &url.URL{
		Scheme:   "https",
		Host:     targetHost,
		Path:     r.URL.Path,
		RawQuery: r.URL.RawQuery,
	}
	logrus.Debugf("Docker 代理请求: %s %s", r.Method, targetURL.String())

	// 首次发起真正的代理请求
	if err := proxyRequest(w, r, targetURL.String()); err != nil {
		http.Error(w, "代理请求失败", http.StatusInternalServerError)
		logrus.Errorf("代理请求失败: %v", err)
	}
}

// proxyRequest 发起代理请求并处理响应
func proxyRequest(w http.ResponseWriter, originalReq *http.Request, targetURL string) error {
	// 将请求体落盘，以便在 401 需要重试时可以再次读取
	bodyReader, contentLength, err := spoolRequestBody(originalReq)
	if err != nil {
		return err
	}
	defer cleanupSpool(bodyReader)

	// 构造新的请求
	newReq, err := http.NewRequest(originalReq.Method, targetURL, bodyReader)
	if err != nil {
		return err
	}

	// 复制原请求头
	copyHeaders(newReq.Header, originalReq.Header)
	// 设置 Host 头为目标主机
	newReq.Host = targetHost
	newReq.Header.Set("Host", targetHost)
	// 删除 Accept-Encoding，避免压缩
	newReq.Header.Del("Accept-Encoding")
	// 修正 Content-Length
	if contentLength >= 0 {
		newReq.ContentLength = contentLength
	}

	// 发送请求
	resp, err := client.Do(newReq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// 如果返回 401，则表明需要 Token 认证
	if resp.StatusCode == http.StatusUnauthorized {
		logrus.Debug("收到 401 响应，尝试获取 token")
		authHeader := resp.Header.Get("WWW-Authenticate")
		authParams := parseAuth(authHeader)
		if authParams["realm"] == "" || authParams["service"] == "" {
			// 如果认证参数不全，则直接返回原响应
			return writeResponse(w, resp)
		}

		// 获取 token
		token, err := fetchToken(authParams)
		if err != nil || token == "" {
			logrus.Errorf("获取 token 失败: %v", err)
			return writeResponse(w, resp)
		}

		logrus.Debug("成功获取 token，重新发起请求")
		// 带上 token 再次请求
		return fetchWithToken(w, originalReq, targetURL, token)
	}

	// 如果响应为 3xx 重定向，则处理重定向
	if isRedirect(resp.StatusCode) {
		location := resp.Header.Get("Location")
		if location != "" {
			redirectURL, err := url.Parse(location)
			if err != nil {
				return err
			}
			baseURL, err := url.Parse(targetURL)
			if err != nil {
				return err
			}
			// 拼装新的重定向 URL
			newURL := baseURL.ResolveReference(redirectURL).String()
			logrus.Debugf("处理重定向，新的 URL: %s", newURL)
			return proxyRequest(w, originalReq, newURL)
		}
	}

	// 其他情况，直接将响应原样返回
	return writeResponse(w, resp)
}

// fetchWithToken 使用 Bearer token 重新发起请求
// 注意：为了避免再次读取请求体，这里会再次落盘原请求体（或复用之前已经落盘的文件）
// 实际上，之前 spoolRequestBody() 已经存在的临时文件会被关闭，所以需要重新做一遍
func fetchWithToken(w http.ResponseWriter, originalReq *http.Request, targetURL string, token string) error {
	bodyReader, contentLength, err := spoolRequestBody(originalReq)
	if err != nil {
		return err
	}
	defer cleanupSpool(bodyReader)

	newReq, err := http.NewRequest(originalReq.Method, targetURL, bodyReader)
	if err != nil {
		return err
	}

	copyHeaders(newReq.Header, originalReq.Header)
	newReq.Host = targetHost
	newReq.Header.Set("Host", targetHost)
	newReq.Header.Del("Accept-Encoding")
	newReq.Header.Set("Authorization", "Bearer "+token)
	if contentLength >= 0 {
		newReq.ContentLength = contentLength
	}

	resp, err := client.Do(newReq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// 再次处理重定向
	if isRedirect(resp.StatusCode) {
		location := resp.Header.Get("Location")
		if location != "" {
			redirectURL, err := url.Parse(location)
			if err != nil {
				return err
			}
			baseURL, err := url.Parse(targetURL)
			if err != nil {
				return err
			}
			newURL := baseURL.ResolveReference(redirectURL).String()
			logrus.Debugf("使用 token 处理重定向，新的 URL: %s", newURL)
			return fetchWithToken(w, originalReq, newURL, token)
		}
	}

	// 如果仍返回 401，则回到原逻辑
	if resp.StatusCode == http.StatusUnauthorized {
		logrus.Debug("使用 token 请求仍返回 401，回到主逻辑重新代理")
		return proxyRequest(w, originalReq, targetURL)
	}

	return writeResponse(w, resp)
}

// writeResponse 将 resp 的状态码、头信息、Body 流式写回客户端
func writeResponse(w http.ResponseWriter, resp *http.Response) error {
	copyHeaders(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)
	_, err := io.Copy(w, resp.Body)
	return err
}

// isRedirect 判断是否为 3xx 重定向状态码
func isRedirect(statusCode int) bool {
	return statusCode == http.StatusMovedPermanently ||
		statusCode == http.StatusFound ||
		statusCode == http.StatusSeeOther ||
		statusCode == http.StatusTemporaryRedirect ||
		statusCode == http.StatusPermanentRedirect
}

// parseAuth 解析 WWW-Authenticate 中的 Bearer 参数，返回一个键值对 map
func parseAuth(header string) map[string]string {
	result := make(map[string]string)
	// 去掉 "Bearer "
	header = strings.TrimPrefix(header, "Bearer ")
	// 以逗号分割
	parts := strings.Split(header, ",")
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		kv := strings.SplitN(part, "=", 2)
		if len(kv) != 2 {
			continue
		}
		key := strings.TrimSpace(kv[0])
		// 去掉两边引号
		value := strings.Trim(strings.TrimSpace(kv[1]), `"`)
		result[key] = value
	}
	return result
}

// fetchToken 向认证服务器获取 Bearer Token
// authParams 通常包含 realm, service, scope 等字段
func fetchToken(authParams map[string]string) (string, error) {
	realm, ok := authParams["realm"]
	if !ok || realm == "" {
		return "", errors.New("缺少 realm 参数")
	}
	service, ok := authParams["service"]
	if !ok || service == "" {
		return "", errors.New("缺少 service 参数")
	}

	reqURL, err := url.Parse(realm)
	if err != nil {
		return "", err
	}

	// 设置 query 参数
	query := reqURL.Query()
	query.Set("service", service)
	if scope, ok := authParams["scope"]; ok && scope != "" {
		query.Set("scope", scope)
	}
	reqURL.RawQuery = query.Encode()

	logrus.Debugf("获取 token 的请求 URL: %s", reqURL.String())

	resp, err := client.Get(reqURL.String())
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("获取 token 请求返回状态码: %d", resp.StatusCode)
	}

	var data map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return "", err
	}

	// 优先检查 token 字段，其次检查 access_token 字段
	if token, ok := data["token"].(string); ok && token != "" {
		return token, nil
	}
	if accessToken, ok := data["access_token"].(string); ok && accessToken != "" {
		return accessToken, nil
	}
	return "", errors.New("未找到 token")
}

// ========== 以下是与“落盘缓存请求体”相关的辅助函数，和 main.go 类似，为了在本包内也能使用 ==========

// spoolRequestBody 将请求体落盘，返回可读可seek的 io.ReadSeeker 和总长度
func spoolRequestBody(r *http.Request) (io.ReadSeeker, int64, error) {
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		// 对于 GET/HEAD 请求体通常可以忽略
		return nil, 0, nil
	}

	tmpFile, err := os.CreateTemp("", "hubp_req_*")
	if err != nil {
		return nil, -1, err
	}

	written, err := io.Copy(tmpFile, r.Body)
	if err != nil {
		tmpFile.Close()
		return nil, -1, err
	}
	if _, err := tmpFile.Seek(0, 0); err != nil {
		tmpFile.Close()
		return nil, -1, err
	}

	contentLength := r.ContentLength
	if contentLength < 0 {
		contentLength = written
	}

	return tmpFile, contentLength, nil
}

// cleanupSpool 清理临时文件
func cleanupSpool(reader io.ReadSeeker) {
	if reader == nil {
		return
	}
	if f, ok := reader.(*os.File); ok {
		f.Close()
		os.Remove(f.Name())
	}
}

// copyHeaders 复制请求头到新请求或响应 (与 main.go 中相同, 这里再实现一遍是为了包隔离)
func copyHeaders(dst, src http.Header) {
	for key, values := range src {
		for _, value := range values {
			dst.Add(key, value)
		}
	}
}
