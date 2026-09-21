// 端戳隔离的源码级机器守卫（B358.9 契约 §5 D 组条 25 / §10）。
//
// 冻结语义（spec 决定 1）：RoomMessage.Device 只做落款，不参与任何「人是谁」
// 的判定——寻址（ResolveDelivery）、写者执法（VerifyWriter/sessionWriters）、
// 已读游标（cursor.Store）都不得引用它。这条不能只靠「实现轮记着别用」：端戳
// 一旦流入判定面，多端同人会被拆成两个主体（决定 2 的拉权对称被破坏），且从
// 任何声明缝都构造不出「没引用」这条断言——只有读源码的机器守卫有牙齿。
//
// 判据：internal/collab 与 internal/ledger 的**生产** .go 文件（不含 _test.go）
// 零引用 .Device / Device: 字段。agentd 设置端戳、proto 定义端戳，都在本扫描
// 面之外（合法载体），不进本判据。
//
// 为什么是读源码而不是 graph check：新增一条真实引用而不写进视图 diff 时，
// 闸门眼里它不存在（先例 internal/collab/room/delivery_gate_test.go 头注）。
package collab

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDeviceStampStaysOutOfJudgementPaths 锁 D-25：端戳不参与寻址/写者/游标。
func TestDeviceStampStaysOutOfJudgementPaths(t *testing.T) {
	banned := []string{".Device", "Device:", "Device =", "Device=="}
	roots := []string{".", "../ledger"}
	for _, root := range roots {
		if err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			raw, readErr := os.ReadFile(path)
			if readErr != nil {
				return readErr
			}
			src := string(raw)
			for _, bad := range banned {
				if strings.Contains(src, bad) {
					t.Errorf("%s 出现端戳引用 %q——Device 只做落款，不得进入寻址/写者/游标判定面（契约 D-25）", path, bad)
				}
			}
			return nil
		}); err != nil {
			t.Fatalf("扫描 %s: %v", root, err)
		}
	}
}
