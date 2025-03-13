// main.go
package main

import (
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
)

// Version 用于嵌入构建版本号
var Version = "dev"

// Config 定义配置结构体
type Config struct {
	ListenAddress string // 监听地址
	Port          int    // 监听端口
	LogLevel      string // 日志级别
	DisguiseURL   string // 伪装网站 URL
}

// 全局配置变量
var config Config

// 自定义 HTTP 客户端
var client = &http.Client{
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	},
	Timeout: 30 * time.Second,
}

func init() {
	// 配置日志格式
	logrus.SetFormatter(&logrus.TextFormatter{
		FullTimestamp:   true,                       // 启用完整时间戳
		TimestampFormat: "2006-01-02 15:04:05.000", // 自定义时间格式
		PadLevelText:    true,                       // 日志级别文本对齐
		FieldMap: logrus.FieldMap{
			logrus.FieldKeyTime:  "时间",
			logrus.FieldKeyLevel: "级别",
			logrus.FieldKeyMsg:   "信息",
		},
	})
}

// preprocessArgs 预处理命令行参数
func preprocessArgs() {
	// 定义参数映射
	alias := map[string]string{
		"--listen":    "-l",
		"--port":      "-p",
		"--log-level": "-ll",
		"--disguise":  "-w",
	}

	// 构造新参数列表
	newArgs := make([]string, 0, len(os.Args))
	newArgs = append(newArgs, os.Args[0])

	// 处理每个参数
	for _, arg := range os.Args[1:] {
		if strings.HasPrefix(arg, "--") && strings.Contains(arg, "=") {
			parts := strings.SplitN(arg, "=", 2)
			if short, ok := alias[parts[0]]; ok {
				arg = short + "=" + parts[1]
			}
		} else if short, ok := alias[arg]; ok {
			arg = short
		}
		newArgs = append(newArgs, arg)
	}
	os.Args = newArgs
}

// usage 自定义帮助信息
func usage() {
	const helpText = `HubP - Docker Hub 代理服务器

参数说明:
    -l, --listen       监听地址 (默认: 0.0.0.0)
    -p, --port         监听端口 (默认: 18826)
    -ll, --log-level   日志级别: debug/info/warn/error (默认: info)
    -w, --disguise     伪装网站 URL (默认: onlinealarmkur.com)

示例:
    ./HubP -l 0.0.0.0 -p 18826 -ll debug -w www.bing.com
    ./HubP --listen=0.0.0.0 --port=18826 --log-level=debug --disguise=www.bing.com`

	fmt.Fprintf(os.Stderr, "%s\n", helpText)
}

func main() {
	// 预处理命令行参数
	preprocessArgs()
	flag.Usage = usage

	// 设置默认值
	defaultListenAddress := getEnv("HUBP_LISTEN", "0.0.0.0")
	defaultPort := getEnvAsInt("HUBP_PORT", 18826)
	defaultLogLevel := getEnv("HUBP_LOG_LEVEL", "debug")
	defaultDisguiseURL := getEnv("HUBP_DISGUISE", "onlinealarmkur.com")

	// 定义命令行参数
	flag.StringVar(&config.ListenAddress, "l", defaultListenAddress, "监听地址")
	flag.IntVar(&config.Port, "p", defaultPort, "监听端口")
	flag.StringVar(&config.LogLevel, "ll", defaultLogLevel, "日志级别")
	flag.StringVar(&config.DisguiseURL, "w", defaultDisguiseURL, "伪装网站 URL")

	// 解析命令行参数
	if err := flag.CommandLine.Parse(os.Args[1:]); err != nil {
		logrus.Fatal("解析命令行参数失败：", err)
	}

	// 设置日志级别
	level, err := logrus.ParseLevel(config.LogLevel)
	if err != nil {
		logrus.Warnf("无效的日志级别 '%s'，使用默认级别 'info'", config.LogLevel)
		level = logrus.InfoLevel
	}
	logrus.SetLevel(level)

	// 输出启动信息
	printStartupInfo()

	// 启动服务器
	addr := fmt.Sprintf("%s:%d", config.ListenAddress, config.Port)
	http.HandleFunc("/", handleRequest)
	
	logrus.Info("服务器启动中...")
	if err := http.ListenAndServe(addr, nil); err != nil {
		logrus.Fatal("服务器启动失败: ", err)
	}
}

// printStartupInfo 打印启动信息
func printStartupInfo() {
	const line = "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
	logrus.Info(line)
	logrus.Info("  HubP Docker Hub 代理服务器")
	logrus.Infof(" Version: %s", Version)
	logrus.Info(line)
	logrus.Infof(" 监听地址: %s", config.ListenAddress)
	logrus.Infof(" 监听端口: %d", config.Port)
	logrus.Infof(" 日志级别: %s", config.LogLevel)
	logrus.Infof(" 伪装网站: %s", config.DisguiseURL)
	logrus.Info(line)
}

// handleRequest 处理所有 HTTP 请求
func handleRequest(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	
	// DEBUG 级别打印详细请求信息
	if logrus.IsLevelEnabled(logrus.DebugLevel) {
		logrus.Debugf("[%s] 收到请求 [%s %s] 来自 %s", 
			path, r.Method, r.URL.String(), r.RemoteAddr)
	}

	// 根据路径选择处理方式
	if strings.HasPrefix(path, "/v2/") {
		handleRegistryRequest(w, r)
	} else if strings.HasPrefix(path, "/auth/") {
		handleAuthRequest(w, r)
	} else if strings.HasPrefix(path, "/production-cloudflare/") {
		handleCloudflareRequest(w, r)
	} else {
		handleDisguise(w, r)
	}
}

// handleRegistryRequest 处理 Docker Registry 的请求
func handleRegistryRequest(w http.ResponseWriter, r *http.Request) {
	const targetHost = "registry-1.docker.io"
	
	// 提取路径部分
	pathParts := strings.Split(r.URL.Path, "/")
	v2PathParts := pathParts[2:]
	pathString := strings.Join(v2PathParts, "/")
	
	// 构造目标 URL
	url := &url.URL{
		Scheme:   "https",
		Host:     targetHost,
		Path:     "/v2/" + pathString,
		RawQuery: r.URL.RawQuery,
	}
	
	// 复制原始请求头
	headers := copyHeaders(r.Header)
	headers.Set("Host", targetHost)
	
	logrus.Debugf("[Docker] 转发请求至: %s", url.String())
	
	// 发送请求
	resp, err := sendRequest(r.Method, url.String(), headers, r.Body)
	if err != nil {
		logrus.Errorf("[Docker] 请求失败: %v", err)
		http.Error(w, "服务器错误", http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()
	
	// 处理认证
	if resp.StatusCode == http.StatusUnauthorized {
		handleAuthChallenge(w, r, resp)
		return
	}
	
	// 处理响应头
	respHeaders := copyHeaders(resp.Header)
	
	// 修改认证头
	if respHeaders.Get("WWW-Authenticate") != "" {
		currentDomain := r.Host
		respHeaders.Set("WWW-Authenticate", 
			fmt.Sprintf(`Bearer realm="https://%s/auth/token", service="registry.docker.io"`, currentDomain))
	}
	
	// 写入响应头和状态码
	for k, v := range respHeaders {
		for _, val := range v {
			w.Header().Add(k, val)
		}
	}
	w.WriteHeader(resp.StatusCode)
	
	// 写入响应体
	written, err := io.Copy(w, resp.Body)
	if err != nil {
		logrus.Errorf("[Docker] 传输响应失败: %v", err)
		return
	}
	
	if logrus.IsLevelEnabled(logrus.DebugLevel) {
		logrus.Debugf("[Docker] 响应完成 [状态码: %d] [大小: %.2f KB]", 
			resp.StatusCode, float64(written)/1024)
	}
}

// handleAuthRequest 处理 Docker 认证服务的请求
func handleAuthRequest(w http.ResponseWriter, r *http.Request) {
	const targetHost = "auth.docker.io"
	
	// 提取路径部分
	pathParts := strings.Split(r.URL.Path, "/")
	authPathParts := pathParts[2:]
	pathString := strings.Join(authPathParts, "/")
	
	// 构造目标 URL
	url := &url.URL{
		Scheme:   "https",
		Host:     targetHost,
		Path:     "/" + pathString,
		RawQuery: r.URL.RawQuery,
	}
	
	// 复制原始请求头
	headers := copyHeaders(r.Header)
	headers.Set("Host", targetHost)
	
	logrus.Debugf("[Auth] 转发请求至: %s", url.String())
	
	// 发送请求
	resp, err := sendRequest(r.Method, url.String(), headers, r.Body)
	if err != nil {
		logrus.Errorf("[Auth] 请求失败: %v", err)
		http.Error(w, "服务器错误", http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()
	
	// 写入响应头和状态码
	for k, v := range resp.Header {
		for _, val := range v {
			w.Header().Add(k, val)
		}
	}
	w.WriteHeader(resp.StatusCode)
	
	// 写入响应体
	written, err := io.Copy(w, resp.Body)
	if err != nil {
		logrus.Errorf("[Auth] 传输响应失败: %v", err)
		return
	}
	
	if logrus.IsLevelEnabled(logrus.DebugLevel) {
		logrus.Debugf("[Auth] 响应完成 [状态码: %d] [大小: %.2f KB]", 
			resp.StatusCode, float64(written)/1024)
	}
}

// handleCloudflareRequest 处理 Cloudflare 相关的请求
func handleCloudflareRequest(w http.ResponseWriter, r *http.Request) {
	const targetHost = "production.cloudflare.docker.com"
	
	// 提取路径部分
	pathParts := strings.Split(r.URL.Path, "/")
	cfPathParts := pathParts[2:]
	pathString := strings.Join(cfPathParts, "/")
	
	// 构造目标 URL
	url := &url.URL{
		Scheme:   "https",
		Host:     targetHost,
		Path:     "/" + pathString,
		RawQuery: r.URL.RawQuery,
	}
	
	// 复制原始请求头
	headers := copyHeaders(r.Header)
	headers.Set("Host", targetHost)
	
	logrus.Debugf("[Cloudflare] 转发请求至: %s", url.String())
	
	// 发送请求
	resp, err := sendRequest(r.Method, url.String(), headers, r.Body)
	if err != nil {
		logrus.Errorf("[Cloudflare] 请求失败: %v", err)
		http.Error(w, "服务器错误", http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()
	
	// 写入响应头和状态码
	for k, v := range resp.Header {
		for _, val := range v {
			w.Header().Add(k, val)
		}
	}
	w.WriteHeader(resp.StatusCode)
	
	// 写入响应体
	written, err := io.Copy(w, resp.Body)
	if err != nil {
		logrus.Errorf("[Cloudflare] 传输响应失败: %v", err)
		return
	}
	
	if logrus.IsLevelEnabled(logrus.DebugLevel) {
		logrus.Debugf("[Cloudflare] 响应完成 [状态码: %d] [大小: %.2f KB]", 
			resp.StatusCode, float64(written)/1024)
	}
}

// handleAuthChallenge 处理认证挑战
func handleAuthChallenge(w http.ResponseWriter, r *http.Request, resp *http.Response) {
	// 处理响应头
	for k, v := range resp.Header {
		for _, val := range v {
			w.Header().Add(k, val)
		}
	}
	
	// 修改认证头
	if authHeader := w.Header().Get("WWW-Authenticate"); authHeader != "" {
		currentDomain := r.Host
		w.Header().Set("WWW-Authenticate", 
			fmt.Sprintf(`Bearer realm="https://%s/auth/token", service="registry.docker.io"`, currentDomain))
	}
	
	// 写入状态码
	w.WriteHeader(resp.StatusCode)
	
	// 写入响应体
	io.Copy(w, resp.Body)
}

// handleDisguise 处理伪装页面请求
func handleDisguise(w http.ResponseWriter, r *http.Request) {
	// 构造目标 URL
	targetURL := &url.URL{
		Scheme:   "https",
		Host:     config.DisguiseURL,
		Path:     r.URL.Path,
		RawQuery: r.URL.RawQuery,
	}

	if logrus.IsLevelEnabled(logrus.DebugLevel) {
		logrus.Debugf("[伪装] 转发请求: %s", targetURL.String())
	}

	// 复制请求头
	headers := copyHeaders(r.Header)
	headers.Del("Accept-Encoding") // 防止压缩响应

	// 发送请求
	resp, err := sendRequest(r.Method, targetURL.String(), headers, r.Body)
	if err != nil {
		logrus.Errorf("[伪装] 请求失败: %v", err)
		http.Error(w, "服务器错误", http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()

	// 复制响应头
	for k, v := range resp.Header {
		for _, val := range v {
			w.Header().Add(k, val)
		}
	}
	w.WriteHeader(resp.StatusCode)

	// 流式传输响应体
	written, err := io.Copy(w, resp.Body)
	if err != nil {
		logrus.Errorf("[伪装] 传输响应失败: %v", err)
		return
	}

	if logrus.IsLevelEnabled(logrus.DebugLevel) {
		logrus.Debugf("[伪装] 响应完成 [状态码: %d] [大小: %.2f KB]", 
			resp.StatusCode, float64(written)/1024)
	}
}

// sendRequest 发送 HTTP 请求
func sendRequest(method, url string, headers http.Header, body io.ReadCloser) (*http.Response, error) {
	// 创建新请求
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		return nil, fmt.Errorf("创建请求失败: %v", err)
	}
	
	// 设置请求头
	req.Header = headers
	
	// 发送请求
	return client.Do(req)
}

// copyHeaders 复制 HTTP 头
func copyHeaders(src http.Header) http.Header {
	dst := make(http.Header)
	for key, values := range src {
		dst[key] = append([]string(nil), values...)
	}
	return dst
}

// getEnv 获取环境变量
func getEnv(key, defaultValue string) string {
	if value, exists := os.LookupEnv(key); exists {
		return value
	}
	return defaultValue
}

// getEnvAsInt 获取整数类型环境变量
func getEnvAsInt(key string, defaultValue int) int {
	if valueStr, exists := os.LookupEnv(key); exists {
		if value, err := strconv.Atoi(valueStr); err == nil {
			return value
		}
	}
	return defaultValue
}
