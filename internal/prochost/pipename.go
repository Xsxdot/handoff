// pipename.go —— Windows 命名管道名的确定性推导。
//
// 职责：把输入通道路径映射成一个稳定、等长、不含路径分隔符的管道全名。
//
// 边界：
//   - 纯函数，不碰系统调用、不判平台。**故意不加 build tag**：它是 Windows
//     输入通道里唯一能在任何平台上被真正执行到的逻辑，加 tag 等于放弃这块覆盖
//   - 不负责安全：名字可推导不等于可随意连接，抢占防护由
//     FILE_FLAG_FIRST_PIPE_INSTANCE 与安全描述符承担（见 inputch_windows.go）
package prochost

import (
	"crypto/sha256"
	"encoding/hex"
	pathpkg "path"
	"strings"
)

// pipeNamePrefix 是所有 handoff 管道的公共前缀，便于运维一眼认出归属。
const pipeNamePrefix = `\\.\pipe\handoff-`

// pipeNameFor 由输入通道路径推导 Windows 命名管道全名。
//
// 参数：path 为通道路径（Spec.InputCh 的值）
//
// 返回：形如 `\\.\pipe\handoff-a1b2c3d4e5f60718` 的全名，恒为 32 个字符。
//
// 注意：
//   - **确定性是硬要求**：agentd 与 shim 是两个进程、不共享额外状态，
//     只能各自从同一个 InputCh 值算出同一个名字。这也是 proc.json 与三个
//     adapter 能零改动的原因
//   - 取哈希而不是直接编码路径：路径含 `\` 与 `:`，而管道名里 `\` 是命名空间
//     分隔符；且路径长度无上限，管道名上限 256
//   - 只取前 8 字节（16 个十六进制字符）：碰撞面是同一台机器上并存的任务数
//     （量级几十），2^64 远够用，换来一个短到便于日志阅读的名字
func pipeNameFor(path string) string {
	// 使用 slash 语义显式归一化，而不是 filepath.Clean：这段纯函数在 macOS
	// 上也要处理 Windows 风格的 InputCh，agentd 与 shim 才能在两种环境下得到
	// 相同结果。
	normalized := pathpkg.Clean(strings.ReplaceAll(path, `\`, `/`))
	sum := sha256.Sum256([]byte(normalized))
	return pipeNamePrefix + hex.EncodeToString(sum[:8])
}
