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

	"HubP/proxy" // 引用我们在 proxy/ 下的代理逻辑
	"github.com/sirupsen/logrus"
)

// Version 用于嵌入构建版本号，由 ldflags 设置；默认值为 "dev"
var Version = "dev"

// Config 定义配置结构体，用于存储命令行参数配置
// ListenAddress：监听地址；Port：端口；LogLevel：日志级别；DisguiseURL：伪装网站 URL
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

	// 构造新的参数列表
	newArgs := []string{os.Args[0]}
	for _, arg := range os.Args[1:] {
		// 如果参数以 "--" 开头并且包含 "="，例如 "--listen=0.0.0.0"
		if strings.HasPrefix(arg, "--") && strings.Contains(arg, "=") {
			parts := strings.SplitN(arg, "=", 2)
			if short, ok := alias[parts[0]]; ok {
				arg = short + "=" + parts[1]
			}
		} else if short, ok := alias[arg]; ok {
			// 如果参数与映射关系匹配，则转换为短参数
			arg = short
		}
		newArgs = append(newArgs, arg)
	}
	os.Args = newArgs
}

// usage 自定义 flag.Usage 函数，显示详细的帮助信息
func usage() {
	helpText := `
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

	// 预处理命令行参数，将长参数转换为短参数
	preprocessArgs()

	// 设置自定义的 flag.Usage 函数
	flag.Usage = usage

	// 从环境变量中获取默认值，如果没有则使用写死的默认值
	defaultListenAddress := getEnv("HUBP_LISTEN", "0.0.0.0")
	defaultPort := getEnvAsInt("HUBP_PORT", 18826)
	defaultLogLevel := getEnv("HUBP_LOG_LEVEL", "info")
	defaultDisguiseURL := getEnv("HUBP_DISGUISE", "onlinealarmkur.com")

	// 定义命令行参数变量
	var flagListen string
	var flagPort int
	var flagLogLevel string
	var flagDisguise string

	// 使用短参数名称进行定义
	flag.StringVar(&flagListen, "l", "", "监听地址，例如 0.0.0.0（命令行参数优先）")
	flag.IntVar(&flagPort, "p", 0, "监听端口，例如 18826（命令行参数优先）")
	flag.StringVar(&flagLogLevel, "ll", "", "日志级别，例如 debug, info, warn, error（命令行参数优先）")
	flag.StringVar(&flagDisguise, "w", "", "伪装网站 URL，例如 www.bing.com（命令行参数优先）")

	// 解析命令行参数
	err := flag.CommandLine.Parse(os.Args[1:])
	if err != nil {
		usage()
		os.Exit(1)
	}

	// 根据默认值和命令行参数设置最终配置
	finalListenAddress := defaultListenAddress
	finalPort := defaultPort
	finalLogLevel := defaultLogLevel
	finalDisguiseURL := defaultDisguiseURL

	if flagListen != "" {
		finalListenAddress = flagListen
	}
	if flagPort != 0 {
		finalPort = flagPort
	}
	if flagLogLevel != "" {
		finalLogLevel = flagLogLevel
	}
	if flagDisguise != "" {
		finalDisguiseURL = flagDisguise
	}

	// 将最终配置写入全局 config 变量
	config = Config{
		ListenAddress: finalListenAddress,
		Port:          finalPort,
		LogLevel:      finalLogLevel,
		DisguiseURL:   finalDisguiseURL,
	}

	// 设置日志级别
	level, err := logrus.ParseLevel(config.LogLevel)
	if err != nil {
		logrus.Warnf("无法解析日志级别 %s，使用默认级别 info", config.LogLevel)
		level = logrus.InfoLevel
	}
	logrus.SetLevel(level)

	// 输出最终配置，便于调试
	logrus.Infof("最终配置: %+v", config)
	logrus.Infof("当前版本: %s", Version)

	addr := fmt.Sprintf("%s:%d", config.ListenAddress, config.Port)

	// 注册 HTTP 路由
	// 如果请求路径以 /v2/ 开头，则调用 Docker 相关的代理操作 (proxy.HandleProxy)；
	// 否则使用伪装代理 (handleDisguise)，将请求转发到配置中指定的伪装网站。
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/v2/") {
			// 处理 Docker Registry 相关请求
			proxy.HandleProxy(w, r)
		} else {
			// 处理伪装网站请求
			handleDisguise(w, r)
		}
	})

	// 启动 HTTP 服务器，并监听指定地址
	logrus.Infof("服务器启动，监听 %s", addr)
	if err := http.ListenAndServe(addr, nil); err != nil {
		logrus.Fatalf("服务器启动失败: %v", err)
	}
}

// handleDisguise 处理伪装页面的反向代理
// 将请求转发到配置中指定的伪装网站 (例如 www.bing.com)，并使用流式拷贝响应
func handleDisguise(w http.ResponseWriter, r *http.Request) {
	// 根据配置中的伪装网站构造目标 URL
	targetURL := &url.URL{
		Scheme:   "https",
		Host:     config.DisguiseURL,
		Path:     r.URL.Path,
		RawQuery: r.URL.RawQuery,
	}
	logrus.Debugf("伪装代理请求: %s %s", r.Method, targetURL.String())

	// 将原请求体落盘缓存，防止过大数据占用内存
	bodyReader, contentLength, err := spoolRequestBody(r)
	if err != nil {
		http.Error(w, "读取请求体失败", http.StatusInternalServerError)
		logrus.Errorf("读取请求体失败: %v", err)
		return
	}
	// 在函数结束时删除临时文件
	defer cleanupSpool(bodyReader)

	// 创建新的请求
	newReq, err := http.NewRequest(r.Method, targetURL.String(), bodyReader)
	if err != nil {
		http.Error(w, "创建代理请求失败", http.StatusInternalServerError)
		logrus.Errorf("创建代理请求失败: %v", err)
		return
	}

	// 复制原请求头
	copyHeaders(newReq.Header, r.Header)
	// 不要压缩
	newReq.Header.Del("Accept-Encoding")
	// 修正 Content-Length
	if contentLength >= 0 {
		newReq.ContentLength = contentLength
	}

	// 发送请求
	resp, err := http.DefaultClient.Do(newReq)
	if err != nil {
		http.Error(w, "发起代理请求失败", http.StatusInternalServerError)
		logrus.Errorf("发起代理请求失败: %v", err)
		return
	}
	defer resp.Body.Close()

	// 将目标网站返回的响应头复制到客户端响应
	copyHeaders(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)

	// 流式复制响应体，避免一次性读入内存
	if _, err := io.Copy(w, resp.Body); err != nil {
		logrus.Errorf("转发响应体失败: %v", err)
		return
	}
}

// copyHeaders 用于将 src 中的所有头复制到 dst 中
func copyHeaders(dst, src http.Header) {
	for key, values := range src {
		// 因为是 Add，所以可以保留同名多值
		for _, value := range values {
			dst.Add(key, value)
		}
	}
}

// spoolRequestBody 将请求体落盘缓存，并返回一个可重复读取 (io.ReadSeeker) 的 reader
// 同时返回缓存大小 (contentLength)；如果无法确定长度或为 chunked 则返回 -1
// 对于 GET/HEAD 请求，通常没有请求体，也就无需落盘
func spoolRequestBody(r *http.Request) (io.ReadSeeker, int64, error) {
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		// 直接返回一个空的 reader
		return nil, 0, nil
	}

	// 创建临时文件用于存储请求体
	tmpFile, err := os.CreateTemp("", "hubp_req_*")
	if err != nil {
		return nil, -1, err
	}

	// 将请求体复制到临时文件
	written, err := io.Copy(tmpFile, r.Body)
	if err != nil {
		tmpFile.Close()
		return nil, -1, err
	}

	// 将文件指针移动回开头，方便后续再次读取
	if _, err := tmpFile.Seek(0, 0); err != nil {
		tmpFile.Close()
		return nil, -1, err
	}

	// 如果原请求头里带了 Content-Length，则用它；否则只能以写入字节数为准
	contentLength := r.ContentLength
	if contentLength < 0 {
		// 如果原本是 chunked 等情况，这里只能把实际写入字节数作为 length
		contentLength = written
	}

	return tmpFile, contentLength, nil
}

// cleanupSpool 在使用完临时文件后进行清理
// 如果 spoolRequestBody 返回的 reader 不是临时文件，则无需清理
func cleanupSpool(reader io.ReadSeeker) {
	if reader == nil {
		return
	}
	if f, ok := reader.(*os.File); ok {
		f.Close()
		os.Remove(f.Name())
	}
}

// getEnv 获取环境变量的值，如果不存在则返回默认值
func getEnv(key, defaultValue string) string {
	if value, exists := os.LookupEnv(key); exists {
		return value
	}
	return defaultValue
}

// getEnvAsInt 获取环境变量的值并转换为整数，如果不存在则返回默认值
func getEnvAsInt(key string, defaultValue int) int {
	valueStr := getEnv(key, "")
	if value, err := strconv.Atoi(valueStr); err == nil {
		return value
	}
	return defaultValue
}
