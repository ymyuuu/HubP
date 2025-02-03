# HubP

**HubP** 是一款基于 Go 开发的超轻量级 Docker 镜像加速工具。旨在提升拉取效率，规避网络限制，伪装非 Docker 请求，有效拉低风控

- **群聊**：[HeroCore](https://t.me/HeroCore)
- **频道**：[HeroMsg](https://t.me/HeroMsg)

## 安装与使用

### 1. 安装

#### 使用 Git 克隆源码并编译

1. 克隆项目代码：
   
    ```bash
    git clone https://github.com/ymyuuu/HubP.git
    ```
    
3. 进入项目目录：
   
    ```bash
    cd HubP
    ```
    
5. 编译生成可执行文件：
   
    ```bash
    go build -o HubP main.go
    ```
    
   编译过程中会自动下载依赖，生成的可执行文件在当前目录下（Windows 平台下为 `HubP.exe`）。

### 2. 配置

HubP 支持通过命令行参数进行配置，支持长短参数别名。具体参数说明如下：

| 参数                   | 别名                     | 说明                                               | 默认值                  |
| ---------------------- | ------------------------ | -------------------------------------------------- | ----------------------- |
| `-l` / `--listen`      | 监听地址                 | 设置服务器监听地址，如 `0.0.0.0`                     | `0.0.0.0`               |
| `-p` / `--port`        | 监听端口                 | 设置服务器监听端口，如 `18826`                       | `18826`                 |
| `-ll` / `--log-level`  | 日志级别                 | 设置日志输出级别，可选值：`debug`、`info`、`warn`、`error` | `info`                  |
| `-w` / `--disguise`    | 伪装网站 URL             | 设置伪装代理目标网站，用于转发非 Docker 请求，如 `onlinealarmkur.com` | `onlinealarmkur.com`    |

例如，你可以使用如下命令行参数启动 HubP：

```bash
./HubP -l 0.0.0.0 -p 18826 -ll debug -w onlinealarmkur.com
```

*注意*：当不传入某个参数时，程序会自动使用默认值运行

### 3. 运行

编译完成后，直接运行生成的可执行文件即可启动服务：

```bash
./HubP
```

## 预构建二进制文件

你可以直接从 [GitHub Releases](https://github.com/ymyuuu/HubP/releases) 页面下载适用于不同操作系统和架构的预构建二进制文件，无需自行编译

### 下载与运行

- **Linux/macOS**  
  下载后赋予执行权限，并启动：
  
  ```bash
  chmod +x HubP
  ./HubP
  ```

- **Windows**
  
  下载后直接双击运行 `HubP.exe` 或在命令行中执行：
  ```cmd
  HubP.exe
  ```

## 构建说明

若你希望自行构建 HubP，可按照以下步骤操作：

1. **整理依赖**  
   在项目根目录下运行：
   
   ```bash
   go mod tidy
   go mod download
   ```

3. **编译生成二进制文件**  
   使用以下命令编译并注入版本号（例如版本为 `v1.0.0`）：
   
   ```bash
   go build -ldflags="-s -w -X main.Version=v1.0.0" -o HubP main.go
   ```

编译成功后，将生成对应平台的可执行文件，供部署使用

## 使用 Docker 镜像

你可以使用 Docker 镜像来运行 HubP，无需自行编译和配置环境。以下是使用 Docker 镜像的步骤：

### 拉取 Docker 镜像

从 Docker Hub 拉取最新的 HubP 镜像：

```bash
docker pull ymyuuu/hubp:latest
```

### 运行 Docker 容器

使用拉取的 Docker 镜像运行 HubP 容器：

```bash
docker run -d --name hubp -p 18826:18826 ymyuuu/hubp:latest
```

### 配置环境变量

你可以通过设置环境变量来配置 HubP 容器。例如，设置监听地址和端口：

```bash
docker run -d --name hubp -p 18826:18826 \
  -e HUBP_LISTEN=0.0.0.0 \
  -e HUBP_PORT=18826 \
  -e HUBP_LOG_LEVEL=info \
  -e HUBP_DISGUISE=onlinealarmkur.com \
  ymyuuu/hubp:latest
```

## 许可证

本项目采用 Apache 许可证，详细内容请参见 [LICENSE](LICENSE) 文件
