package main

// version 由 CI 注入：-ldflags "-X main.version=v1.2.3"；本地构建为 "dev"。
// 必须被真正引用（--version 与启动日志），否则链接器会优化掉符号，-X 静默失效。
var version = "dev"
