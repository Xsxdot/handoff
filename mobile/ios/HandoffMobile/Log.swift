import os

// 统一结构化日志出口。禁 print/NSLog。
// 凭据卫生（contract §4.3 条 18）：只记机器名、长度、错误类别；绝不记 token/cookie/bundle 值。
enum Log {
    static let shell = Logger(subsystem: "dev.handoff.HandoffMobile", category: "shell")
}
