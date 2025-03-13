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

	"HubP/proxy"
	"github.com/sirupsen/logrus"
)

// Version 用于嵌入构建版本号
var Version = "dev"

// Config 定义配置结构体
type Config struct {
	ListenAddress  string // 监听地址
	Port           int    // 监听端口
	LogLevel       string // 日志级别
	DisguiseURL    string // 伪装网站 URL
	CustomDomain   string // 自定义域名，用于设置认证头
	UseHttps       bool   // 是否使用 HTTPS
	DisableDisguise bool  // 是否禁用伪装
}

// 全局配置变量
var config Config

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
		"--listen":           "-l",
		"--port":             "-p",
		"--log-level":        "-ll",
		"--disguise":         "-w",
		"--domain":           "-d",
		"--https":            "-s",
		"--disable-disguise": "-nd",
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
    -l, --listen           监听地址 (默认: 0.0.0.0)
    -p, --port             监听端口 (默认: 18826)
    -ll, --log-level       日志级别: debug/info/warn/error (默认: info)
    -w, --disguise         伪装网站 URL (默认: onlinealarmkur.com)
    -d, --domain           自定义域名，用于认证头 (默认: 使用请求中的 Host 头)
    -s, --https            使用 HTTPS 协议 (默认: false)
    -nd, --disable-disguise 禁用伪装功能，仅作为 Docker 代理 (默认: false)

示例:
    ./HubP -l 0.0.0.0 -p 18826 -ll debug -w www.bing.com
    ./HubP --listen=0.0.0.0 --port=18826 --log-level=debug --domain=mydocker.example.com`

	fmt.Fprintf(os.Stderr, "%s\n", helpText)
}

func main() {
	// 预处理命令行参数
	preprocessArgs()
	flag.Usage = usage

	// 设置默认值
	defaultListenAddress := getEnv("HUBP_LISTEN", "0.0.0.0")
	defaultPort := getEnvAsInt("HUBP_PORT", 18826)
	defaultLogLevel := getEnv("HUBP_LOG_LEVEL", "info")
	defaultDisguiseURL := getEnv("HUBP_DISGUISE", "onlinealarmkur.com")
	defaultCustomDomain := getEnv("HUBP_DOMAIN", "")
	defaultUseHttps := getEnvAsBool("HUBP_HTTPS", false)
	defaultDisableDisguise := getEnvAsBool("HUBP_DISABLE_DISGUISE", false)

	// 定义命令行参数
	flag.StringVar(&config.ListenAddress, "l", defaultListenAddress, "监听地址")
	flag.IntVar(&config.Port, "p", defaultPort, "监听端口")
	flag.StringVar(&config.LogLevel, "ll", defaultLogLevel, "日志级别")
	flag.StringVar(&config.DisguiseURL, "w", defaultDisguiseURL, "伪装网站 URL")
	flag.StringVar(&config.CustomDomain, "d", defaultCustomDomain, "自定义域名")
	flag.BoolVar(&config.UseHttps, "s", defaultUseHttps, "使用 HTTPS")
	flag.BoolVar(&config.DisableDisguise, "nd", defaultDisableDisguise, "禁用伪装")

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

	// 设置代理选项
	proxy.SetOptions(proxy.Options{
		CustomDomain: config.CustomDomain,
		UseHttps:     config.UseHttps,
	})

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
	
	if config.DisableDisguise {
		logrus.Info(" 伪装功能: 已禁用")
	} else {
		logrus.Infof(" 伪装网站: %s", config.DisguiseURL)
	}
	
	if config.CustomDomain != "" {
		logrus.Infof(" 自定义域名: %s", config.CustomDomain)
	}
	
	protocol := "HTTP"
	if config.UseHttps {
		protocol = "HTTPS"
	}
	logrus.Infof(" 协议: %s", protocol)
	
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
		// Docker Registry 请求
		proxy.HandleRegistryRequest(w, r)
	} else if strings.HasPrefix(path, "/auth/") {
		// Docker 认证请求
		proxy.HandleAuthRequest(w, r)
	} else if strings.HasPrefix(path, "/production-cloudflare/") {
		// Cloudflare 请求
		proxy.HandleCloudflareRequest(w, r)
	} else if config.DisableDisguise {
		// 如果禁用了伪装，返回 404
		http.NotFound(w, r)
	} else {
		// 伪装页面
		handleDisguise(w, r)
	}
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

	// 创建新请求
	newReq, err := http.NewRequest(r.Method, targetURL.String(), r.Body)
	if err != nil {
		logrus.Errorf("[伪装] 创建请求失败: %v", err)
		http.Error(w, "服务器错误", http.StatusInternalServerError)
		return
	}

	// 复制请求头
	copyHeaders(newReq.Header, r.Header)
	newReq.Header.Set("Host", config.DisguiseURL)
	newReq.Header.Del("Accept-Encoding") // 防止压缩响应

	// 发送请求
	resp, err := http.DefaultClient.Do(newReq)
	if err != nil {
		logrus.Errorf("[伪装] 请求失败: %v", err)
		http.Error(w, "服务器错误", http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()

	// 复制响应头
	copyHeaders(w.Header(), resp.Header)
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

// copyHeaders 复制 HTTP 头
func copyHeaders(dst, src http.Header) {
	for key, values := range src {
		dst[key] = append([]string(nil), values...)
	}
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

// getEnvAsBool 获取布尔类型环境变量
func getEnvAsBool(key string, defaultValue bool) bool {
	if valueStr, exists := os.LookupEnv(key); exists {
		if value, err := strconv.ParseBool(valueStr); err == nil {
			return value
		}
	}
	return defaultValue
}
