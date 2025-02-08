// 文件: main.go
// 包名: main
// 功能: 提供服务主入口、解析命令行、设置路由以及伪装代理
// 作者: ChatGPT 优化示例
// 日期: 2025-02-08

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

    // 这里引入 proxy 子包
    "HubP/proxy"

    "github.com/sirupsen/logrus"
)

// Version 用于嵌入构建版本号（由 ldflags 设置），默认 "dev"
var Version = "dev"

// Config 定义配置结构体，用于存储命令行参数配置
//   - ListenAddress: 监听地址
//   - Port: 端口
//   - LogLevel: 日志级别
//   - DisguiseURL: 伪装网站 URL
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
    // 替换 os.Args 为新的参数列表
    os.Args = newArgs
}

// usage 自定义 flag.Usage 函数，显示帮助信息
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
    // 预处理命令行参数
    preprocessArgs()
    // 设置自定义的 flag.Usage 函数
    flag.Usage = usage

    // 定义默认值（可从环境变量获取）
    defaultListenAddress := getEnv("HUBP_LISTEN", "0.0.0.0")
    defaultPort := getEnvAsInt("HUBP_PORT", 18826)
    defaultLogLevel := getEnv("HUBP_LOG_LEVEL", "info")
    defaultDisguiseURL := getEnv("HUBP_DISGUISE", "onlinealarmkur.com")

    // 命令行参数变量
    var flagListen string
    var flagPort int
    var flagLogLevel string
    var flagDisguise string

    // 定义短参数
    flag.StringVar(&flagListen, "l", "", "监听地址，例如 0.0.0.0（命令行参数优先）")
    flag.IntVar(&flagPort, "p", 0, "监听端口，例如 18826（命令行参数优先）")
    flag.StringVar(&flagLogLevel, "ll", "", "日志级别，例如 debug, info, warn, error（命令行参数优先）")
    flag.StringVar(&flagDisguise, "w", "", "伪装网站 URL，例如 www.bing.com（命令行参数优先）")

    // 解析命令行
    err := flag.CommandLine.Parse(os.Args[1:])
    if err != nil {
        usage()
        os.Exit(1)
    }

    // 最终参数与默认值合并
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

    // 写入全局 config
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

    // 设置 HTTP 路由
    // 路由逻辑: 若路径以 /v2/ 开头，则走 Docker 代理，否则走伪装网站代理
    http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
        // 注意：只在必要时读取 Body，而不要盲目 ReadAll
        // 对 GET/HEAD 请求，通常不需要 Body
        // 这里我们只在发起新请求时用到 Body

        // 判断是否是 Docker Registry 相关请求
        if strings.HasPrefix(r.URL.Path, "/v2/") {
            // 交给 Docker 代理模块
            proxy.HandleProxy(w, r)
        } else {
            // 其他路径，交给伪装代理
            handleDisguise(w, r)
        }
    })

    // 启动 HTTP 服务器
    logrus.Infof("服务器启动，监听 %s", addr)
    if err := http.ListenAndServe(addr, nil); err != nil {
        logrus.Fatalf("服务器启动失败: %v", err)
    }
}

// handleDisguise 处理伪装页面的反向代理
// 将请求转发到 config.DisguiseURL
func handleDisguise(w http.ResponseWriter, r *http.Request) {
    // 构造目标 URL
    targetURL := &url.URL{
        Scheme: "https", // 这里假设https，视伪装网站实际情况而定
        Host:   config.DisguiseURL,
        Path:   r.URL.Path,
        RawQuery: r.URL.RawQuery,
    }
    logrus.Debugf("伪装代理请求: %s %s", r.Method, targetURL.String())

    // 创建新的请求对象
    // 注意使用流式拷贝: r.Body -> newReq.Body（通过 io.ReadCloser）
    newReq, err := http.NewRequest(r.Method, targetURL.String(), r.Body)
    if err != nil {
        http.Error(w, "创建代理请求失败", http.StatusInternalServerError)
        logrus.Errorf("创建代理请求失败: %v", err)
        return
    }
    // 复制 Header
    copyHeaders(newReq.Header, r.Header)

    // (可选) 如果你确实不希望返回压缩内容，可以删除 Accept-Encoding
    // newReq.Header.Del("Accept-Encoding")

    // 使用默认的客户端发送
    resp, err := http.DefaultClient.Do(newReq)
    if err != nil {
        http.Error(w, "发起代理请求失败", http.StatusInternalServerError)
        logrus.Errorf("发起代理请求失败: %v", err)
        return
    }
    defer resp.Body.Close()

    // 将响应头复制到 w
    copyHeaders(w.Header(), resp.Header)
    // 写入响应状态
    w.WriteHeader(resp.StatusCode)

    // 将响应体直接拷贝回客户端(流式)
    if _, err := io.Copy(w, resp.Body); err != nil {
        logrus.Errorf("拷贝响应体失败: %v", err)
        return
    }
}

// copyHeaders 复制 src 的所有 header 到 dst
func copyHeaders(dst, src http.Header) {
    for k, vv := range src {
        for _, v := range vv {
            dst.Add(k, v)
        }
    }
}

// getEnv 获取环境变量值，不存在则返回默认值
func getEnv(key, defaultValue string) string {
    if value, exists := os.LookupEnv(key); exists {
        return value
    }
    return defaultValue
}

// getEnvAsInt 获取环境变量并转换为 int，不存在或转换失败则返回默认值
func getEnvAsInt(key string, defaultValue int) int {
    strVal := getEnv(key, "")
    if val, err := strconv.Atoi(strVal); err == nil {
        return val
    }
    return defaultValue
}
