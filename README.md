# HubP

HubP 是一款基于 Go 开发的超轻量级 Docker 镜像加速工具。它能有效提升镜像拉取效率，绕过网络限制，并通过请求伪装降低风控风险

- **群聊**：[HeroCore](https://t.me/HeroCore)
- **频道**：[HeroMsg](https://t.me/HeroMsg)

## 快速开始

### 下载安装

提供两种安装方式:

1. **直接下载二进制文件**

从 [GitHub Releases](https://github.com/ymyuuu/HubP/releases) 下载对应系统的预编译文件:

```bash
# Linux/macOS
chmod +x HubP
./HubP

# Windows
HubP.exe
```

2. **源码编译**

```bash
# 克隆代码
git clone https://github.com/ymyuuu/HubP.git
cd HubP

# 编译
go build -o HubP main.go
```

### Docker 部署

```bash
# 拉取镜像
docker pull ymyuuu/hubp:latest

# 运行容器
docker run -d --name hubp -p 18826:18826 ymyuuu/hubp:latest
```

## 配置说明

HubP 支持命令行参数和环境变量两种配置方式:

### 命令行参数

| 参数 | 说明 | 默认值 |
|------|------|--------|
| `-l, --listen` | 监听地址 | `0.0.0.0` |
| `-p, --port` | 监听端口 | `18826` |
| `-ll, --log-level` | 日志级别 (debug/info/warn/error) | `info` |
| `-w, --disguise` | 伪装网站 URL | `onlinealarmkur.com` |

示例:

```bash
./HubP -l 0.0.0.0 -p 18826 -ll debug -w example.com
```

### 环境变量 (Docker)

```bash
sudo docker run -d --name hubp \
  -p 18826:18826 \
  -e HUBP_LOG_LEVEL=debug \
  -e HUBP_DISGUISE=onlinealarmkur.com \
  ymyuuu/hubp:latest
```

## 开发指南

如需自行构建,请按以下步骤操作:

```bash
# 安装依赖
go mod tidy
go mod download

# 编译(注入版本号)
go build -ldflags="-s -w -X main.Version=v1.0.0" -o HubP main.go
```

## 许可证

本项目采用 Apache 许可证，详细内容请参见 [LICENSE](LICENSE) 文件
