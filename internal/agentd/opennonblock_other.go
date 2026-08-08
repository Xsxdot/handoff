//go:build !unix

// opennonblock_other.go —— 打开审阅文件时的 O_NONBLOCK 标志（非 unix 退化实现）。
//
// 职责：在没有 O_NONBLOCK 语义的平台上让 ReadFile 照常编译。
//
// 边界：只声明常量，不含任何逻辑。
package agentd

// openNonBlock 在非 unix 平台退化为 0：这些平台没有 FIFO 的阻塞打开语义，
// ReadFile 的 IsRegular 判定本就可达（见 ReadFile 的 why 注释）。
const openNonBlock = 0
