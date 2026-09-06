// fingerprint.go —— 权限复用指纹的唯一编码（B233.1）。
//
// 职责：把一次 AdapterEvent 编成稳定 sha256 十六进制串，供同任务同快照复用查询。
//
// 边界：纯函数，无 I/O。写入（建单）与查询必须走同一函数，规则分叉会让复用静默失效。
package executor

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"
)

// PermFingerprint 计算一次权限请求的裁决指纹。
//
// 域规则（B91，本卡升为执行契约编码）：
//   - Perm.Command 非空 → 命令域：sha256("cmd\x00" + command)
//   - Command 为空但 Paths 非空 → 路径域：sha256("paths\x00" + Tool + "\x00" + 排序后 Paths)
//   - 其余（Perm 为 nil，或提取不出结构）→ 全文域：sha256(Text)
func PermFingerprint(ev AdapterEvent) string {
	if ev.Perm != nil {
		if ev.Perm.Command != "" {
			return permFingerprintHash("cmd\x00" + ev.Perm.Command)
		}
		if len(ev.Perm.Paths) > 0 {
			paths := append([]string(nil), ev.Perm.Paths...)
			sort.Strings(paths)
			return permFingerprintHash("paths\x00" + ev.Perm.Tool + "\x00" + strings.Join(paths, "\x00"))
		}
	}
	return permFingerprintHash(ev.Text)
}

func permFingerprintHash(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

// ReuseFingerprint 把政策版本盐进权限指纹。P2(a)：不改 Ticket 字段。
func ReuseFingerprint(version, permFP string) string {
	return permFingerprintHash(version + "\x00" + permFP)
}


