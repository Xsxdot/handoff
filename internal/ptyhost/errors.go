package ptyhost

import "errors"

// ErrNotSupported 表示当前平台没有 PTY 实现（Windows：ConPTY 是另一套 API，
// 本轮如实降级而不假装支持，见 spec §10）。
//
// 这个变量刻意放在**无构建标签**的文件里：两套 platform_*.go 都要引用它，
// 放进任一带标签的文件都会让另一套编译不过。
var ErrNotSupported = errors.New("当前平台不支持 PTY 终端")

// ErrNoSession 表示会话 id 不存在（或已被显式关闭）。
var ErrNoSession = errors.New("终端会话不存在")

// ErrTooManySubscribers 表示该会话的订阅者已达上限。
var ErrTooManySubscribers = errors.New("终端会话的连接数已达上限")

// ErrSessionExited 表示 shell 已经退出，只能读历史不能再写。
var ErrSessionExited = errors.New("终端会话已退出")
