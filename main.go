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
  // 允许重定向，而不是返回错误
  CheckRedirect: func(req *http.Request, via []*http.Request) error {
    // 复制原始请求的头部到重定向请求
    for key, val := range via[0].Header {
      if _, ok := req.Header[key]; !ok {
        req.Header[key] = val
      }
    }
    return nil
  },
  Timeout: 30 * time.Second,
  Transport: &http.Transport{
    DisableKeepAlives: false,              // 启用长连接
    MaxIdleConns:      100,                // 最大空闲连接数
    IdleConnTimeout:   90 * time.Second,   // 空闲连接超时
    TLSHandshakeTimeout: 10 * time.Second, // TLS握手超时
    ExpectContinueTimeout: 1 * time.Second,// 处理100 Continue的超时时间
  },
}

// 自定义日志格式器
type CustomFormatter struct {
  logrus.TextFormatter
}

// Format 自定义日志格式输出方法
func (f *CustomFormatter) Format(entry *logrus.Entry) ([]byte, error) {
  // 获取时间戳格式
  timestamp := entry.Time.Format("2006-01-02 15:04:05.000")
  
  // 获取日志级别并进行格式化
  var levelColor string
  
  switch entry.Level {
  case logrus.DebugLevel:
    levelColor = "\033[36m" // 青色
  case logrus.InfoLevel:
    levelColor = "\033[32m" // 绿色
  case logrus.WarnLevel:
    levelColor = "\033[33m" // 黄色
  case logrus.ErrorLevel:
    levelColor = "\033[31m" // 红色
  case logrus.FatalLevel, logrus.PanicLevel:
    levelColor = "\033[35m" // 紫色
  }
  
  // 重置颜色的ANSI转义序列
  resetColor := "\033[0m"
  
  // 组装日志信息
  logMessage := fmt.Sprintf("%s %s[%s]%s %s\n",
    timestamp,
    levelColor,
    strings.ToUpper(entry.Level.String()),
    resetColor,
    entry.Message)
  
  return []byte(logMessage), nil
}

func init() {
  // 配置日志格式
  logrus.SetFormatter(&CustomFormatter{
    TextFormatter: logrus.TextFormatter{
      DisableColors:    false,
      FullTimestamp:   true,
      TimestampFormat: "2006-01-02 15:04:05.000",
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
  
  // 安全检查：确保不会修改空的命令行参数
  if len(newArgs) > 0 {
    os.Args = newArgs
  } else {
    logrus.Warn("命令行参数为空，使用原始参数")
  }
}

// usage 自定义帮助信息
func usage() {
  const helpText = `HubP - Docker Hub 代理服务器

参数说明:
    -l, --listen       监听地址 (默认: 0.0.0.0)
    -p, --port         监听端口 (默认: 18184)
    -ll, --log-level   日志级别: debug/info/warn/error (默认: info)
    -w, --disguise     伪装网站 URL (默认: onlinealarmkur.com)

示例:
    ./HubP -l 0.0.0.0 -p 18184 -ll debug -w www.bing.com
    ./HubP --listen=0.0.0.0 --port=18184 --log-level=debug --disguise=www.bing.com`

  fmt.Fprintf(os.Stderr, "%s\n", helpText)
}



func main() {
  // 预处理命令行参数
  preprocessArgs()
  flag.Usage = usage

  // 设置默认值
  defaultListenAddress := getEnv("HUBP_LISTEN", "0.0.0.0")
  defaultPort := getEnvAsInt("HUBP_PORT", 18184) // 修改默认端口为18184
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
  
  logrus.Info("服务启动成功")
  if err := http.ListenAndServe(addr, nil); err != nil {
    logrus.Fatal("服务启动失败: ", err)
  }
}

// printStartupInfo 打印启动信息
func printStartupInfo() {
  // 更加美观且具有品牌特色的启动信息显示
  const blue = "\033[34m"
  const green = "\033[32m"
  const reset = "\033[0m"
  
  // 使用颜色和Unicode字符创建更美观的边框
  fmt.Println(blue + "\n╔════════════════════════════════════════════════════════════╗" + reset)
  fmt.Println(blue + "║" + green + "               HubP Docker Hub 代理服务器               " + blue + "║" + reset)
  fmt.Printf(blue+"║"+green+"               版本: %-33s"+blue+"║\n"+reset, Version)
  fmt.Println(blue + "╠════════════════════════════════════════════════════════════╣" + reset)
  fmt.Printf(blue+"║"+reset+" 监听地址: %-43s"+blue+"║\n"+reset, config.ListenAddress)
  fmt.Printf(blue+"║"+reset+" 监听端口: %-43d"+blue+"║\n"+reset, config.Port)
  fmt.Printf(blue+"║"+reset+" 日志级别: %-43s"+blue+"║\n"+reset, config.LogLevel)
  fmt.Printf(blue+"║"+reset+" 伪装网站: %-43s"+blue+"║\n"+reset, config.DisguiseURL)
  fmt.Println(blue + "╚════════════════════════════════════════════════════════════╝" + reset)
  
  // 在启动信息之后空一行，提高可读性
  fmt.Println()
}

// handleRequest 处理所有 HTTP 请求
func handleRequest(w http.ResponseWriter, r *http.Request) {
  path := r.URL.Path
  
  // DEBUG 级别打印详细请求信息
  if logrus.IsLevelEnabled(logrus.DebugLevel) {
    // 根据请求路径选择不同的标签，使日志更加清晰
    var routeTag string
    if strings.HasPrefix(path, "/v2/") {
      routeTag = "[Docker]"
    } else if strings.HasPrefix(path, "/auth/") {
      routeTag = "[认证]"
    } else if strings.HasPrefix(path, "/production-cloudflare/") {
      routeTag = "[CF]"
    } else {
      routeTag = "[伪装]"
    }
    
    logrus.Debugf("%s 请求: [%s %s] 来自 %s",
      routeTag, r.Method, r.URL.String(), r.RemoteAddr)
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
  
  logrus.Debugf("Docker镜像: 转发请求至 %s", url.String())
  
  // 发送请求
  resp, err := sendRequest(r.Method, url.String(), headers, r.Body)
  if err != nil {
    logrus.Errorf("Docker镜像: 请求失败 - %v", err)
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
    logrus.Errorf("Docker镜像: 传输响应失败 - %v", err)
    return
  }
  
  if logrus.IsLevelEnabled(logrus.DebugLevel) {
    logrus.Debugf("Docker镜像: 响应完成 [状态: %d] [大小: %.2f KB]",
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
  
  logrus.Debugf("认证服务: 转发请求至 %s", url.String())
  
  // 发送请求
  resp, err := sendRequest(r.Method, url.String(), headers, r.Body)
  if err != nil {
    logrus.Errorf("认证服务: 请求失败 - %v", err)
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
    logrus.Errorf("认证服务: 传输响应失败 - %v", err)
    return
  }
  
  if logrus.IsLevelEnabled(logrus.DebugLevel) {
    logrus.Debugf("认证服务: 响应完成 [状态: %d] [大小: %.2f KB]",
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
  
  logrus.Debugf("Cloudflare: 转发请求至 %s", url.String())
  
  // 发送请求
  resp, err := sendRequest(r.Method, url.String(), headers, r.Body)
  if err != nil {
    logrus.Errorf("Cloudflare: 请求失败 - %v", err)
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
    logrus.Errorf("Cloudflare: 传输响应失败 - %v", err)
    return
  }
  
  if logrus.IsLevelEnabled(logrus.DebugLevel) {
    logrus.Debugf("Cloudflare: 响应完成 [状态: %d] [大小: %.2f KB]",
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
  _, err := io.Copy(w, resp.Body)
  if err != nil {
    logrus.Errorf("认证响应传输失败: %v", err)
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
    logrus.Debugf("伪装页面: 转发请求至 %s", targetURL.String())
  }

  // 复制请求头
  headers := copyHeaders(r.Header)
  headers.Del("Accept-Encoding") // 防止压缩响应

  // 发送请求
  resp, err := sendRequest(r.Method, targetURL.String(), headers, r.Body)
  if err != nil {
    logrus.Errorf("伪装页面: 请求失败 - %v", err)
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
    logrus.Errorf("伪装页面: 传输响应失败 - %v", err)
    return
  }

  if logrus.IsLevelEnabled(logrus.DebugLevel) {
    logrus.Debugf("伪装页面: 响应完成 [状态: %d] [大小: %.2f KB]",
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
  
  // 记录开始时间，用于计算请求耗时
  startTime := time.Now()
  
  // 发送请求
  resp, err := client.Do(req)
  
  // 如果启用了DEBUG日志，记录请求耗时
  if err == nil && logrus.IsLevelEnabled(logrus.DebugLevel) {
    duration := time.Since(startTime)
    logrus.Debugf("请求耗时: %.2f 秒 (%s)", duration.Seconds(), url)
  }
  
  return resp, err
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
