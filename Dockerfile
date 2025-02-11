# 使用官方的 Go 语言镜像作为基础镜像
# 这个镜像包含了Go语言的运行环境和工具链，适合用来构建Go应用。
FROM golang:1.21.11 AS builder

# 设置工作目录
# 这里我们把工作目录设置为/app，后续的操作（例如编译）都会在这个目录下进行。
WORKDIR /app

# 复制 go.mod 和 go.sum 文件并下载依赖
# 复制这两个文件后，使用`go mod download`命令来下载项目所依赖的包，确保依赖是完整的。
COPY go.mod go.sum ./
RUN go mod download

# 复制项目的所有源代码
# 复制当前目录下的所有源代码到镜像中的工作目录（/app）。
COPY . .

# 编译项目
# 通过 `go build` 命令编译Go应用，生成一个名为HubP的可执行文件。
# 设置 `CGO_ENABLED=0` 可以禁用Cgo，确保二进制文件在无C语言运行时环境的Linux容器中运行。
# `GOOS=linux` 让Go程序为Linux操作系统编译。
RUN CGO_ENABLED=0 GOOS=linux go build -o HubP main.go

# 使用一个更小的基础镜像来运行应用程序
# 为了减小镜像大小，使用 Alpine Linux 镜像作为基础镜像。Alpine 是一个小巧且安全的Linux发行版，常用于容器镜像中。
FROM alpine:latest

# 设置工作目录
# 在运行阶段，我们将工作目录设置为/root，容器内的可执行文件将会在这个目录下运行。
WORKDIR /root/

# 从构建阶段复制编译好的二进制文件
# 使用 `--from=builder` 将之前在 `builder` 阶段生成的 HubP 可执行文件从构建阶段复制到当前镜像的工作目录中。
COPY --from=builder /app/HubP .

# 确保二进制文件具有可执行权限
# 通过 `chmod +x` 命令确保复制的二进制文件具有可执行权限，以便在容器中运行。
RUN chmod +x HubP

# 设置环境变量
# 设置环境变量，供应用在运行时读取。环境变量通常用于配置应用的行为。
ENV HUBP_LISTEN=0.0.0.0
ENV HUBP_PORT=18826
ENV HUBP_LOG_LEVEL=info
ENV HUBP_DISGUISE=onlinealarmkur.com

# 运行应用程序
# 使用 `CMD` 命令指定容器启动时运行的命令，这里是启动编译好的二进制文件 `HubP`。
CMD ["./HubP"]
