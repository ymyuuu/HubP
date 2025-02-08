// 文件: proxy.go
// 包名: proxy
// 功能: 处理 /v2/ 路径下的 Docker Registry 相关请求，实现 Bearer Token 自动获取
// 作者: ChatGPT 优化示例
// 日期: 2025-02-08

package proxy

import (
    "encoding/json"
    "errors"
    "fmt"
    "io"
    "net/http"
    "net/url"
    "strings"

    "github.com/sirupsen/logrus"
)

// targetHost 定义目标 Docker 主机地址
// 这里以官方 Docker Hub registry-1.docker.io 为例
const targetHost = "registry-1.docker.io"

// 自定义 HTTP 客户端，禁用自动重定向
var client = &http.Client{
    CheckRedirect: func(req *http.Request, via []*http.Request) error {
        // 返回此错误以阻止自动重定向
        return http.ErrUseLastResponse
    },
}

// HandleProxy 处理 /v2/ 路径下的 Docker Registry 相关请求
// 核心逻辑：
//   1. 将请求转发到 Docker Registry
//   2. 如果遇到 401，需要获取 Bearer Token 后重试
//   3. 处理 3xx 重定向
func HandleProxy(w http.ResponseWriter, r *http.Request) {
    // 构造目标 URL
    targetURL := &url.URL{
        Scheme:   "https",
        Host:     targetHost,
        Path:     r.URL.Path,
        RawQuery: r.URL.RawQuery,
    }
    logrus.Debugf("Docker 代理请求: %s %s", r.Method, targetURL.String())

    // 发起代理请求并处理响应
    if err := proxyRequest(w, r, targetURL.String()); err != nil {
        http.Error(w, "代理请求失败", http.StatusInternalServerError)
        logrus.Errorf("代理请求失败: %v", err)
    }
}

// proxyRequest 发起一次代理请求，并处理各种情况：401、重定向等
func proxyRequest(w http.ResponseWriter, r *http.Request, target string) error {
    // 创建新的请求对象(流式拷贝 body)
    newReq, err := http.NewRequest(r.Method, target, r.Body)
    if err != nil {
        return err
    }
    // 复制原请求头
    copyHeaders(newReq.Header, r.Header)
    // 设置 Host 头为 Docker Registry
    newReq.Host = targetHost
    // (可选) 去除 Accept-Encoding
    // newReq.Header.Del("Accept-Encoding")

    resp, err := client.Do(newReq)
    if err != nil {
        return err
    }
    defer resp.Body.Close()

    // 若返回 401，则尝试获取 token 并重试
    if resp.StatusCode == http.StatusUnauthorized {
        logrus.Debug("收到 401，需要获取 token")
        authHeader := resp.Header.Get("WWW-Authenticate")
        authParams := parseAuth(authHeader)
        if authParams["realm"] == "" || authParams["service"] == "" {
            // 若关键参数缺失，直接返回原响应
            return writeResponse(w, resp)
        }
        // 获取 token
        token, err := fetchToken(authParams)
        if err != nil || token == "" {
            logrus.Errorf("获取 token 失败: %v", err)
            return writeResponse(w, resp)
        }
        // 成功获取 token 后，带上 token 重新请求
        logrus.Debug("成功获取 token，重新发起请求")
        return fetchWithToken(w, r, target, token)
    }

    // 处理 3xx 重定向
    if isRedirect(resp.StatusCode) {
        location := resp.Header.Get("Location")
        if location != "" {
            // 计算新的 URL
            redirectURL, err := url.Parse(location)
            if err != nil {
                return err
            }
            baseURL, err := url.Parse(target)
            if err != nil {
                return err
            }
            newURL := baseURL.ResolveReference(redirectURL)
            logrus.Debugf("处理重定向，新的 URL: %s", newURL.String())
            return proxyRequest(w, r, newURL.String())
        }
    }

    // 普通情况，直接将响应返回给客户端
    return writeResponse(w, resp)
}

// fetchWithToken 带 Bearer token 再次请求
func fetchWithToken(w http.ResponseWriter, r *http.Request, target string, token string) error {
    newReq, err := http.NewRequest(r.Method, target, r.Body)
    if err != nil {
        return err
    }
    copyHeaders(newReq.Header, r.Header)
    newReq.Host = targetHost
    // 重点：设置 Authorization
    newReq.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))

    resp, err := client.Do(newReq)
    if err != nil {
        return err
    }
    defer resp.Body.Close()

    // 若仍然返回 401，说明 token 失效或其他问题，回到主逻辑
    if resp.StatusCode == http.StatusUnauthorized {
        logrus.Debug("使用 token 请求仍返回 401，回到主逻辑")
        return proxyRequest(w, r, target)
    }

    // 处理可能的重定向
    if isRedirect(resp.StatusCode) {
        location := resp.Header.Get("Location")
        if location != "" {
            redirectURL, err := url.Parse(location)
            if err != nil {
                return err
            }
            baseURL, err := url.Parse(target)
            if err != nil {
                return err
            }
            newURL := baseURL.ResolveReference(redirectURL)
            logrus.Debugf("使用 token 处理重定向，新的 URL: %s", newURL.String())
            return fetchWithToken(w, r, newURL.String(), token)
        }
    }

    // 正常情况
    return writeResponse(w, resp)
}

// writeResponse 将目标服务器返回的响应写回给客户端(流式拷贝)
func writeResponse(w http.ResponseWriter, resp *http.Response) error {
    // 复制响应头
    copyHeaders(w.Header(), resp.Header)
    // 写入响应码
    w.WriteHeader(resp.StatusCode)
    // 将目标响应体流式拷贝给客户端
    if _, err := io.Copy(w, resp.Body); err != nil {
        return err
    }
    return nil
}

// isRedirect 判断是否为 3xx 重定向
func isRedirect(statusCode int) bool {
    return statusCode == http.StatusMovedPermanently ||
        statusCode == http.StatusFound ||
        statusCode == http.StatusSeeOther ||
        statusCode == http.StatusTemporaryRedirect ||
        statusCode == http.StatusPermanentRedirect
}

// copyHeaders 复制 header
func copyHeaders(dst, src http.Header) {
    for k, vv := range src {
        for _, v := range vv {
            dst.Add(k, v)
        }
    }
}

// parseAuth 解析 WWW-Authenticate 头，提取 realm, service, scope 等
func parseAuth(header string) map[string]string {
    result := make(map[string]string)
    // 去掉 Bearer 前缀
    header = strings.TrimPrefix(header, "Bearer ")
    // 以逗号分隔
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
        value := strings.Trim(strings.TrimSpace(kv[1]), `"`)
        result[key] = value
    }
    return result
}

// fetchToken 根据返回的 realm, service, scope 等信息，向认证服务器获取 Bearer Token
func fetchToken(authParams map[string]string) (string, error) {
    realm := authParams["realm"]
    if realm == "" {
        return "", errors.New("缺少 realm 参数")
    }
    service := authParams["service"]
    if service == "" {
        return "", errors.New("缺少 service 参数")
    }
    // 构造请求 URL
    reqURL, err := url.Parse(realm)
    if err != nil {
        return "", err
    }
    query := reqURL.Query()
    query.Set("service", service)
    if scope := authParams["scope"]; scope != "" {
        query.Set("scope", scope)
    }
    reqURL.RawQuery = query.Encode()

    logrus.Debugf("获取 token 的请求 URL: %s", reqURL.String())
    // 发起 GET
    resp, err := client.Get(reqURL.String())
    if err != nil {
        return "", err
    }
    defer resp.Body.Close()

    if resp.StatusCode != http.StatusOK {
        return "", fmt.Errorf("获取 token 请求返回状态码: %d", resp.StatusCode)
    }
    // 解析响应 JSON
    var data map[string]interface{}
    if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
        return "", err
    }
    // 优先 token，其次 access_token
    if token, ok := data["token"].(string); ok && token != "" {
        return token, nil
    }
    if accessToken, ok := data["access_token"].(string); ok && accessToken != "" {
        return accessToken, nil
    }
    return "", errors.New("返回数据中未找到 token")
}
