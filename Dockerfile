# 使用官方的 Go 语言镜像作为基础镜像
FROM golang:1.21.11 AS builder

# 设置工作目录
WORKDIR /app

# 复制 go.mod 和 go.sum 文件并下载依赖
COPY go.mod go.sum ./
RUN go mod download

# 复制项目的所有源代码
COPY . .

# 编译项目
RUN go build -o HubP main.go

# 使用一个更小的基础镜像来运行应用程序
FROM alpine:latest

# 设置工作目录
WORKDIR /root/

# 从构建阶段复制编译好的二进制文件
COPY --from=builder /app/HubP .

# 暴露应用程序的端口
EXPOSE 18826

# 运行应用程序
CMD ["./HubP"]