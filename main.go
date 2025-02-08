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

	"HubP/proxy"
	"github.com/sirupsen/logrus"
)

// Version 用于嵌入构建版本号，由 ldflags 设置；默认值为 "dev"
var Version = "dev"

// Config 定义配置结构体，用于存储命令行参数配置
type Config struct {
	ListenAddress string // 监听地址，例如 "0.0.0.0"
	Port          int    // 监听端口，例如 18826
	LogLevel      string // 日志级别，例如 "info"
	DisguiseURL   string // 伪装网站 URL，例如 "www.bing.com"
}

// 全局配置变量
var config Config

func init() {
	// 配置日志格式
	logrus.SetFormatter(&logrus.TextFormatter{
		FullTimestamp:   true,                      // 显示完整时间戳
		TimestampFormat: "2006-01-02 15:04:05.000", // 自定义时间格式
		DisableColors:   false,                     // 启用颜色输出
		PadLevelText:    true,                      // 对齐日志级别文本
	})
}

// preprocessArgs 预处理命令行参数，将长参数转换为短参数
func preprocessArgs() {
	// 定义长参数到短参数的映射关系
	alias := map[string]string{
		"--listen":    "-l",
		"--port":      "-p",
		"--log-level": "-ll",
		"--disguise":  "-w",
	}

	// 构造新的参数列表，预分配容量
	newArgs := make([]string, 0, len(os.Args))
	newArgs = append(newArgs, os.Args[0])

	// 遍历所有命令行参数
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
    -l, --listen       监听地址，例如 0.0.0.0 (默认值: 0.0.0.0)
    -p, --port         监听端口，例如 18826 (默认值: 18826)
    -ll, --log-level   日志级别，可选值: debug, info, warn, error (默认值: info)
    -w, --disguise     伪装网站 URL，例如 www.bing.com (默认值: www.bing.com)

使用示例:
    ./HubP -l 0.0.0.0 -p 18826 -ll debug -w www.bing.com
    ./HubP --listen=0.0.0.0 --port=18826 --log-level=debug --disguise=www.bing.com

环境变量:
    HUBP_LISTEN    - 监听地址
    HUBP_PORT      - 监听端口
    HUBP_LOG_LEVEL - 日志级别
    HUBP_DISGUISE  - 伪装网站 URL`

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
	defaultDisguiseURL := getEnv("HUBP_DISGUISE", "www.bing.com")

	// 定义命令行参数变量
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
	logrus.Info("============================================")
	logrus.Infof("HubP Docker Hub 代理服务器 (版本: %s)", Version)
	logrus.Info("============================================")
	logrus.Infof("监听地址: %s", config.ListenAddress)
	logrus.Infof("监听端口: %d", config.Port)
	logrus.Infof("日志级别: %s", config.LogLevel)
	logrus.Infof("伪装网站: %s", config.DisguiseURL)
	logrus.Info("============================================")

	// 创建自定义的 HTTP 服务器
	server := &http.Server{
		Addr:         fmt.Sprintf("%s:%d", config.ListenAddress, config.Port),
		Handler:      http.HandlerFunc(handleRequest),
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	// 启动服务器
	logrus.Info("服务器启动中...")
	if err := server.ListenAndServe(); err != nil {
		logrus.Fatal("服务器启动失败: ", err)
	}
}

// handleRequest 处理所有 HTTP 请求的入口函数
func handleRequest(w http.ResponseWriter, r *http.Request) {
	startTime := time.Now()
	
	// 添加请求日志
	if logrus.IsLevelEnabled(logrus.DebugLevel) {
		logrus.Debugf("[请求] %s %s %s", r.Method, r.URL.Path, r.RemoteAddr)
	}

	// 根据路径选择处理方式
	if strings.HasPrefix(r.URL.Path, "/v2/") {
		proxy.HandleProxy(w, r)
	} else {
		handleDisguise(w, r)
	}

	// 添加完成日志
	if logrus.IsLevelEnabled(logrus.DebugLevel) {
		duration := time.Since(startTime)
		logrus.Debugf("[完成] %s %s (耗时: %v)", r.Method, r.URL.Path, duration)
	}
}

// handleDisguise 处理伪装页面的反向代理
func handleDisguise(w http.ResponseWriter, r *http.Request) {
	targetURL := &url.URL{
		Scheme:   "https",
		Host:     config.DisguiseURL,
		Path:     r.URL.Path,
		RawQuery: r.URL.RawQuery,
	}

	if logrus.IsLevelEnabled(logrus.DebugLevel) {
		logrus.Debugf("[伪装] 转发请求至: %s", targetURL.String())
	}

	// 创建新的 HTTP 请求
	newReq, err := http.NewRequest(r.Method, targetURL.String(), r.Body)
	if err != nil {
		logrus.Errorf("[伪装] 创建请求失败: %v", err)
		http.Error(w, "内部服务器错误", http.StatusInternalServerError)
		return
	}

	// 复制请求头
	copyHeaders(newReq.Header, r.Header)
	newReq.Header.Del("Accept-Encoding") // 防止压缩响应

	// 发起请求
	resp, err := http.DefaultClient.Do(newReq)
	if err != nil {
		logrus.Errorf("[伪装] 请求失败: %v", err)
		http.Error(w, "内部服务器错误", http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()

	// 复制响应头
	copyHeaders(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)

	// 流式传输响应体
	if _, err := io.Copy(w, resp.Body); err != nil {
		logrus.Errorf("[伪装] 传输响应失败: %v", err)
	}
}

// copyHeaders 复制 HTTP 头
func copyHeaders(dst, src http.Header) {
	for key, values := range src {
		dst[key] = append([]string(nil), values...)
	}
}

// getEnv 获取环境变量值
func getEnv(key, defaultValue string) string {
	if value, exists := os.LookupEnv(key); exists {
		return value
	}
	return defaultValue
}

// getEnvAsInt 获取环境变量的整数值
func getEnvAsInt(key string, defaultValue int) int {
	if valueStr, exists := os.LookupEnv(key); exists {
		if value, err := strconv.Atoi(valueStr); err == nil {
			return value
		}
	}
	return defaultValue
}
