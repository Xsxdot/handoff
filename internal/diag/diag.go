// Package diag 提供 B409 S4 读取诊断的最小进程内关联件：服务端为每次同步读
// 生成 operation_id、为每轮后台远端刷新生成 refresh_id，经 context 传播，使
// agentd/collab/ledger 各层日志可按同一 id 拼回一次读取的完整阶段。
//
// 边界：它是日志关联 helper，不是通用 tracing/metrics 子系统——id 只出现在
// 日志里，不进任何 API/CLI wire 字段；不采样、不上报、不引入第三方库。
// 独立成包的唯一理由是 context 键需要被 collab（禁 import ledger）与 ledger
// 同时读取，两包共同的既有依赖都不适合承载日志关联件；本包是零依赖叶子。
package diag

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"
)

// ctxKey 是本包私有的 context 键类型；导出面只有 Info 与 With/From，
// 防止其他包绕过类型伪造任意载荷。
type ctxKey struct{}

// Info 是随 context 传播的读取诊断关联标识。零值表示无关联（如内部调用、
// 未接线的入口），日志侧跳过空字段，不把空 id 写进日志冒充有关联。
type Info struct {
	// OperationID 是一次同步读取（HTTP 请求或 CLI 命令）的服务端关联 id。
	OperationID string
	// RefreshID 是一轮后台远端刷新（unlinked 摘要 / room attach）的关联 id。
	// 后台刷新生命周期独立于触发它的请求：请求可安排刷新，两者耗时不相干，
	// 禁止共用同一 id 造成「请求等了刷新」的误读。
	RefreshID string
}

// With 返回携带 info 的 context 派生。
func With(ctx context.Context, info Info) context.Context {
	return context.WithValue(ctx, ctxKey{}, info)
}

// From 取出关联标识；无关联时返回零值 Info。
func From(ctx context.Context) Info {
	info, _ := ctx.Value(ctxKey{}).(Info)
	return info
}

// Attrs 把非空关联标识展开为 slog 键值对，调用方直接拼接自己的字段：
//
//	log().Info("读取完成", append(diag.Attrs(ctx), "rows", n)...)
//
// 无关联时返回 nil——slog 接受 nil 变参，等价于不追加任何字段。
func Attrs(ctx context.Context) []any {
	info := From(ctx)
	var out []any
	if info.OperationID != "" {
		out = append(out, "operation_id", info.OperationID)
	}
	if info.RefreshID != "" {
		out = append(out, "refresh_id", info.RefreshID)
	}
	return out
}

// NewOperationID 生成一次同步读取的关联 id（op- 前缀 + 8 位随机十六进制）。
// id 由服务端生成：客户端不可见不可信，只用于本进程与下游各层日志的关联。
func NewOperationID() string { return "op-" + randomID() }

// NewRefreshID 生成一轮后台远端刷新的关联 id（rf- 前缀），前缀区分让日志
// 消费者不会把后台耗时误读进同步请求。
func NewRefreshID() string { return "rf-" + randomID() }

// randomID 返回 4 字节随机数的十六进制；进程内生成频率为每读一次一个，
// 32 bit 空间的生日碰撞对日志关联足够，且碰撞只影响可读性不影响正确性。
// crypto/rand 不可用时退化为时间戳：关联性退化不是读路径的阻断理由。
func randomID() string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%016x", time.Now().UnixNano())[:8]
	}
	return hex.EncodeToString(b[:])
}
