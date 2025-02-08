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

// preprocessArgs 预处理命令行参数，将长参数转换为短参数，便于统一处理
func preprocessArgs() {
	// 定义长参数到短参数的映射关系
	alias := map[string]string{
		"--listen":    "-l",
		"--port":      "-p",
		"--log-level": "-ll",
		"--disguise":  "-w",
	}

	// 构造新的参数列表，预分配容量以减少内存分配
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

// usage 自定义 flag.Usage 函数，显示详细的帮助信息
func usage() {
	const helpText = `
Help:
    程序支持长参数和短参数形式：
        -l, --listen       监听地址，例如 0.0.0.0 (默认值: 0.0.0.0)
        -p, --port         监听端口，例如 18826 (默认值: 18826)
        -ll, --log-level   日志级别，例如 debug, info, warn, error (默认值: info)
        -w, --disguise     伪装网站 URL，例如 onlinealarmkur.com (默认值: onlinealarmkur.com)

Demo:
    ./HubP -l 0.0.0.0 -p 18826 --log-level=debug --disguise=onlinealarmkur.com
`
	fmt.Fprintln(os.Stderr, helpText)
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

	// 定义命令行参数变量
	flag.StringVar(&config.ListenAddress, "l", defaultListenAddress, "监听地址")
	flag.IntVar(&config.Port, "p", defaultPort, "监听端口")
	flag.StringVar(&config.LogLevel, "ll", defaultLogLevel, "日志级别")
	flag.StringVar(&config.DisguiseURL, "w", defaultDisguiseURL, "伪装网站 URL")

	// 解析命令行参数
	if err := flag.CommandLine.Parse(os.Args[1:]); err != nil {
		usage()
		os.Exit(1)
	}

	// 设置日志级别
	if level, err := logrus.ParseLevel(config.LogLevel); err != nil {
		logrus.Warnf("无法解析日志级别 %s，使用默认级别 info", config.LogLevel)
		logrus.SetLevel(logrus.InfoLevel)
	} else {
		logrus.SetLevel(level)
	}

	// 输出配置信息
	logrus.Infof("最终配置: %+v", config)
	logrus.Infof("当前版本: %s", Version)

	// 启动 HTTP 服务器
	addr := fmt.Sprintf("%s:%d", config.ListenAddress, config.Port)
	http.HandleFunc("/", handleRequest)
	
	logrus.Infof("服务器启动，监听 %s", addr)
	if err := http.ListenAndServe(addr, nil); err != nil {
		logrus.Fatalf("服务器启动失败: %v", err)
	}
}

// handleRequest 处理所有 HTTP 请求的入口函数
func handleRequest(w http.ResponseWriter, r *http.Request) {
	// 判断请求路径是否以 "/v2/" 开头
	if strings.HasPrefix(r.URL.Path, "/v2/") {
		proxy.HandleProxy(w, r)
		return
	}
	handleDisguise(w, r)
}

// handleDisguise 处理伪装页面的反向代理，使用流式传输
func handleDisguise(w http.ResponseWriter, r *http.Request) {
	// 构造目标 URL
	targetURL := &url.URL{
		Scheme:   "https",
		Host:     config.DisguiseURL,
		Path:     r.URL.Path,
		RawQuery: r.URL.RawQuery,
	}
	logrus.Debugf("伪装代理请求: %s %s", r.Method, targetURL.String())

	// 创建新的 HTTP 请求
	newReq, err := http.NewRequest(r.Method, targetURL.String(), r.Body)
	if err != nil {
		http.Error(w, "创建代理请求失败", http.StatusInternalServerError)
		logrus.Errorf("创建代理请求失败: %v", err)
		return
	}

	// 复制请求头
	copyHeaders(newReq.Header, r.Header)
	newReq.Header.Del("Accept-Encoding") // 防止压缩

	// 发起请求
	resp, err := http.DefaultClient.Do(newReq)
	if err != nil {
		http.Error(w, "发起代理请求失败", http.StatusInternalServerError)
		logrus.Errorf("发起代理请求失败: %v", err)
		return
	}
	defer resp.Body.Close()

	// 复制响应头
	copyHeaders(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)

	// 使用 io.Copy 进行流式传输响应体
	if _, err := io.Copy(w, resp.Body); err != nil {
		logrus.Errorf("传输响应体失败: %v", err)
	}
}

// copyHeaders 复制 HTTP 头，使用更高效的方式
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
