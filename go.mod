// 声明该Go模块的名称为HubP
module HubP

// 设置Go语言的版本为1.21.11
go 1.21.11

// 引入外部依赖：github.com/sirupsen/logrus v1.9.3
// logrus 是一个结构化的日志库，用于记录程序的日志，方便调试和生产环境的日志管理。
require github.com/sirupsen/logrus v1.9.3

// 引入外部依赖：golang.org/x/sys v0.0.0-20220715151400-c0bba94af5f8（间接依赖）
// golang.org/x/sys 是Go语言的系统级包，提供了访问底层操作系统功能的接口。
// 该依赖是间接依赖（即在直接依赖的库中被间接引用）。
require golang.org/x/sys v0.0.0-20220715151400-c0bba94af5f8 // indirect
