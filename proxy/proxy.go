package proxy

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/url"
	"strings"

	"github.com/sirupsen/logrus"
)

// targetHost 定义目标 Docker 主机地址，所有 /v2/ 请求将转发至此主机
const targetHost = "registry-1.docker.io"

// 定义 HTTP 客户端，并禁用自动重定向处理，由代码手动处理重定向逻辑
var client = &http.Client{
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		// 返回此错误时，http.Client 不会自动重定向
		return http.ErrUseLastResponse
	},
}

// HandleProxy 处理 Docker 相关代理请求的入口函数
// 参数说明：
//   w         : HTTP 响应写入器
//   r         : 原始 HTTP 请求
//   bodyBytes : 请求体数据（适用于非 GET/HEAD 请求）
func HandleProxy(w http.ResponseWriter, r *http.Request, bodyBytes []byte) {
	// 构造目标 URL，保持原请求的路径和查询参数不变
	targetURL := &url.URL{
		Scheme:   "https",
		Host:     targetHost,
		Path:     r.URL.Path,
		RawQuery: r.URL.RawQuery,
	}
	logrus.Debugf("Docker 代理请求: %s %s", r.Method, targetURL.String())

	// 发起代理请求，并处理响应
	if err := proxyRequest(w, r, targetURL.String(), bodyBytes); err != nil {
		http.Error(w, "代理请求失败", http.StatusInternalServerError)
		logrus.Errorf("代理请求失败: %v", err)
	}
}

// proxyRequest 发起代理请求并处理响应
func proxyRequest(w http.ResponseWriter, r *http.Request, targetURL string, bodyBytes []byte) error {
	var bodyReader io.Reader
	// 如果请求方法不是 GET 或 HEAD，则传递请求体
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		bodyReader = bytes.NewReader(bodyBytes)
	}
	// 创建新的 HTTP 请求对象
	newReq, err := http.NewRequest(r.Method, targetURL, bodyReader)
	if err != nil {
		return err
	}

	// 复制原请求的所有请求头到新请求中
	copyHeaders(newReq.Header, r.Header)
	// 设置 Host 头为目标主机
	newReq.Header.Set("Host", targetHost)
	// 删除 Accept-Encoding 头，防止服务端返回压缩数据
	newReq.Header.Del("Accept-Encoding")

	// 使用自定义 HTTP 客户端发起请求
	resp, err := client.Do(newReq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// 如果返回 401 状态码，尝试获取 token 后重试请求
	if resp.StatusCode == http.StatusUnauthorized {
		logrus.Debug("收到 401 响应，尝试获取 token")
		authHeader := resp.Header.Get("WWW-Authenticate")
		authParams := parseAuth(authHeader)
		if authParams["realm"] == "" || authParams["service"] == "" {
			// 如果认证参数不全，则直接返回原响应
			return writeResponse(w, resp)
		}
		// 尝试获取 token
		token, err := fetchToken(authParams)
		if err != nil || token == "" {
			logrus.Errorf("获取 token 失败: %v", err)
			return writeResponse(w, resp)
		}
		logrus.Debug("成功获取 token，重新发起请求")
		return fetchWithToken(w, r, targetURL, bodyBytes, token)
	}

	// 如果响应为重定向状态码，则处理重定向
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
			// 解析新的重定向 URL
			newURL := baseURL.ResolveReference(redirectURL)
			logrus.Debugf("处理重定向，新的 URL: %s", newURL.String())
			return proxyRequest(w, r, newURL.String(), bodyBytes)
		}
	}

	// 将目标服务器的响应写回给客户端
	return writeResponse(w, resp)
}

// fetchWithToken 使用 Bearer token 重新发起请求
func fetchWithToken(w http.ResponseWriter, r *http.Request, targetURL string, bodyBytes []byte, token string) error {
	var bodyReader io.Reader
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		bodyReader = bytes.NewReader(bodyBytes)
	}
	newReq, err := http.NewRequest(r.Method, targetURL, bodyReader)
	if err != nil {
		return err
	}

	// 复制请求头，并添加 Authorization 头
	copyHeaders(newReq.Header, r.Header)
	newReq.Header.Set("Host", targetHost)
	newReq.Header.Del("Accept-Encoding")
	newReq.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))

	resp, err := client.Do(newReq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// 处理重定向情况
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
			newURL := baseURL.ResolveReference(redirectURL)
			logrus.Debugf("使用 token 处理重定向，新的 URL: %s", newURL.String())
			return fetchWithToken(w, r, newURL.String(), bodyBytes, token)
		}
	}

	// 如果仍返回 401，则放弃 token 处理，回到主逻辑重新代理请求
	if resp.StatusCode == http.StatusUnauthorized {
		logrus.Debug("使用 token 请求仍返回 401，回到主逻辑")
		return proxyRequest(w, r, targetURL, bodyBytes)
	}

	// 将响应写回客户端
	return writeResponse(w, resp)
}

// copyHeaders 复制请求头，将 src 中所有 header 复制到 dst 中
func copyHeaders(dst, src http.Header) {
	for key, values := range src {
		for _, value := range values {
			dst.Add(key, value)
		}
	}
}

// writeResponse 将目标服务器的响应写回给客户端
func writeResponse(w http.ResponseWriter, resp *http.Response) error {
	// 复制响应头
	for key, values := range resp.Header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	// 写入响应状态码
	w.WriteHeader(resp.StatusCode)
	// 读取响应体
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	// 写入响应体数据
	_, err = w.Write(body)
	return err
}

// isRedirect 判断 HTTP 状态码是否为重定向状态码
func isRedirect(statusCode int) bool {
	return statusCode == http.StatusMovedPermanently ||
		statusCode == http.StatusFound ||
		statusCode == http.StatusSeeOther ||
		statusCode == http.StatusTemporaryRedirect ||
		statusCode == http.StatusPermanentRedirect
}

// parseAuth 解析 WWW-Authenticate 头信息，提取认证参数
func parseAuth(header string) map[string]string {
	result := make(map[string]string)
	// 去除 "Bearer " 前缀
	header = strings.TrimPrefix(header, "Bearer ")
	// 以逗号分隔参数
	parts := strings.Split(header, ",")
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		// 以等号分隔键值对
		kv := strings.SplitN(part, "=", 2)
		if len(kv) != 2 {
			continue
		}
		key := strings.TrimSpace(kv[0])
		// 去除两边的引号
		value := strings.Trim(strings.TrimSpace(kv[1]), `"`)
		result[key] = value
	}
	return result
}

// fetchToken 根据认证参数从认证服务器获取 token
func fetchToken(authParams map[string]string) (string, error) {
	realm, ok := authParams["realm"]
	if !ok || realm == "" {
		return "", errors.New("缺少 realm 参数")
	}
	service, ok := authParams["service"]
	if !ok || service == "" {
		return "", errors.New("缺少 service 参数")
	}
	// 解析 realm URL
	reqURL, err := url.Parse(realm)
	if err != nil {
		return "", err
	}
	// 设置 query 参数：service 和可选的 scope
	query := reqURL.Query()
	query.Set("service", service)
	if scope, ok := authParams["scope"]; ok && scope != "" {
		query.Set("scope", scope)
	}
	reqURL.RawQuery = query.Encode()

	logrus.Debugf("获取 token 的请求 URL: %s", reqURL.String())
	// 发起 GET 请求获取 token
	tokenResp, err := client.Get(reqURL.String())
	if err != nil {
		return "", err
	}
	defer tokenResp.Body.Close()

	// 如果返回状态码不为 200，则视为失败
	if tokenResp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("获取 token 请求返回状态码: %d", tokenResp.StatusCode)
	}

	var data map[string]interface{}
	// 读取响应体
	body, err := ioutil.ReadAll(tokenResp.Body)
	if err != nil {
		return "", err
	}
	// 解析 JSON 数据
	err = json.Unmarshal(body, &data)
	if err != nil {
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
