package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/sirupsen/logrus"
	"HubP/proxy" // 导入自定义代理模块，模块名称与 go.mod 保持一致
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
	// 遍历所有命令行参数（从 os.Args[1:] 开始）
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
	// 替换 os.Args 为新的参数列表
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

	// 定义默认值
	defaultListenAddress := "0.0.0.0"
	defaultPort := 18826
	defaultLogLevel := "info"
	defaultDisguiseURL := "onlinealarmkur.com"

	// 定义命令行参数变量
	var flagListen string
	var flagPort int
	var flagLogLevel string
	var flagDisguise string

	// 使用短参数名称进行定义：
	// -l 表示监听地址，-p 表示端口，-ll 表示日志级别，-w 表示伪装网站 URL
	flag.StringVar(&flagListen, "l", "", "监听地址，例如 0.0.0.0（命令行参数优先）")
	flag.IntVar(&flagPort, "p", 0, "监听端口，例如 18826（命令行参数优先）")
	flag.StringVar(&flagLogLevel, "ll", "", "日志级别，例如 debug, info, warn, error（命令行参数优先）")
	flag.StringVar(&flagDisguise, "w", "", "伪装网站 URL，例如 www.bing.com（命令行参数优先）")

	// 解析命令行参数，参数错误时会显示帮助信息并退出
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
	level, err := logrus.ParseLevel(finalLogLevel)
	if err != nil {
		logrus.Warnf("无法解析日志级别 %s，使用默认级别 info", finalLogLevel)
		level = logrus.InfoLevel
	}
	logrus.SetLevel(level)

	// 输出最终配置，便于调试
	logrus.Infof("最终配置: %+v", config)
	logrus.Infof("当前版本: %s", Version)

	// 构造监听地址，例如 "0.0.0.0:18826"
	addr := fmt.Sprintf("%s:%d", config.ListenAddress, config.Port)

	// 设置 HTTP 路由：
	// 如果请求路径以 /v2/ 开头，则调用 Docker 相关的代理操作；
	// 否则使用伪装代理，将请求转发到配置中指定的伪装网站（默认 www.bing.com）
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// 对于非 GET/HEAD 请求，读取请求体数据
		var bodyBytes []byte
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			var err error
			bodyBytes, err = ioutil.ReadAll(r.Body)
			if err != nil {
				http.Error(w, "读取请求体失败", http.StatusInternalServerError)
				logrus.Errorf("读取请求体失败: %v", err)
				return
			}
		}

		// 判断请求路径是否以 "/v2/" 开头
		if strings.HasPrefix(r.URL.Path, "/v2/") {
			// 调用代理模块处理 Docker 相关请求
			proxy.HandleProxy(w, r, bodyBytes)
		} else {
			// 其他路径，调用伪装代理处理，转发到伪装网站
			handleDisguise(w, r, bodyBytes)
		}
	})

	// 启动 HTTP 服务器，并监听指定地址
	logrus.Infof("服务器启动，监听 %s", addr)
	if err := http.ListenAndServe(addr, nil); err != nil {
		logrus.Fatalf("服务器启动失败: %v", err)
	}
}

// handleDisguise 处理伪装页面的反向代理
// 将请求转发到配置中指定的伪装网站（默认 www.bing.com）
// 参数说明：
//   w         : HTTP 响应写入器
//   r         : 原始 HTTP 请求
//   bodyBytes : 请求体数据
func handleDisguise(w http.ResponseWriter, r *http.Request, bodyBytes []byte) {
	// 构造目标 URL，使用配置中的伪装网站地址
	targetURL := &url.URL{
		Scheme:   "https",
		Host:     config.DisguiseURL,
		Path:     r.URL.Path,
		RawQuery: r.URL.RawQuery,
	}
	logrus.Debugf("伪装代理请求: %s %s", r.Method, targetURL.String())

	// 根据请求方法判断是否需要传递请求体
	var bodyReader io.Reader
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		bodyReader = bytes.NewReader(bodyBytes)
	}

	// 创建新的 HTTP 请求对象
	newReq, err := http.NewRequest(r.Method, targetURL.String(), bodyReader)
	if err != nil {
		http.Error(w, "创建代理请求失败", http.StatusInternalServerError)
		logrus.Errorf("创建代理请求失败: %v", err)
		return
	}

	// 复制原请求中的所有请求头到新请求中
	copyHeaders(newReq.Header, r.Header)
	// 删除 Accept-Encoding 头，防止返回压缩数据
	newReq.Header.Del("Accept-Encoding")

	// 使用默认 HTTP 客户端发起请求
	resp, err := http.DefaultClient.Do(newReq)
	if err != nil {
		http.Error(w, "发起代理请求失败", http.StatusInternalServerError)
		logrus.Errorf("发起代理请求失败: %v", err)
		return
	}
	defer resp.Body.Close()

	// 将目标网站返回的响应头复制到客户端响应中
	for key, values := range resp.Header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}

	// 写入响应状态码
	w.WriteHeader(resp.StatusCode)

	// 读取目标网站响应体，并写入到客户端响应中
	respBody, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		logrus.Errorf("读取响应体失败: %v", err)
		return
	}
	_, err = w.Write(respBody)
	if err != nil {
		logrus.Errorf("写入响应体失败: %v", err)
		return
	}
}

// copyHeaders 复制请求头，将 src 中的所有 header 复制到 dst 中
func copyHeaders(dst, src http.Header) {
	for key, values := range src {
		for _, value := range values {
			dst.Add(key, value)
		}
	}
}
