// seat_desktop_test.go —— B434.1 账本层行为锁：桌面端席位记录（seat_desktops）
// 与项目拉起器登记（desktop_launchers）。
//
// 职责：锁契约 docs/superpowers/specs/b434-contract.md §3.1 ValidateDesktopSessionID、
// §3.3、§3.4，与 §4.4 条 49–59/62–63/65、§4.9 条 139/140/142/143 的 Store 一侧。
// 反面断言一律数行数与事件数（count 查询），不只断言 err（拆解稿 §5.1 假绿族）；
// 生命周期测试走真实 RebindSeat / CloseCard / ClearSeat 入口，不手写 SQL 模拟联动。
//
// 边界：CLI 一侧（条 42–48、60–61、141）归 B434.2；PG 方言往返在
// seat_desktop_pg_test.go（LEDGER_TEST_PG_DSN 门控，未设即 skip）。
package ledger

import (
	"errors"
	"testing"

	"github.com/Xsxdot/handoff/internal/proto"
)

// b434DesktopSession 是契约条 55 的合法样本 id。
const b434DesktopSession = "local_31eda11a-a6fe-4a5c-a229-fc12d682d1b6"

// TestB434ValidateDesktopSessionID 锁契约条 55–59：前缀 local_ + 余部非空且只含 [A-Za-z0-9-]。
// 拒绝用例同时断言错误不是 ErrDesktopNotImplemented——否则空壳的恒错会让反例假绿。
func TestB434ValidateDesktopSessionID(t *testing.T) {
	cases := []struct {
		name string
		id   string
		ok   bool
	}{
		{"条55 合法 local_uuid", b434DesktopSession, true},
		{"条55 合法 只含字母数字", "local_ABCdef0123", true},
		{"条56 空串", "", false},
		{"条57 只有前缀", "local_", false},
		{"条58 无前缀", "31eda11a-a6fe-4a5c-a229-fc12d682d1b6", false},
		{"条58 前缀大小写不符", "LOCAL_31eda11a", false},
		{"条59 含空格", "local_31eda11a a6fe", false},
		{"条59 含制表符", "local_31eda11a\ta6fe", false},
		{"条59 尾随换行", "local_31eda11a\n", false},
		{"条59 前导空白", " local_31eda11a", false},
		{"条59 含#", "local_31eda11a#a6fe", false},
		{"条59 含/", "local_31eda11a/a6fe", false},
		{"余部含下划线", "local_ab_cd", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := proto.ValidateDesktopSessionID(tc.id)
			if tc.ok && err != nil {
				t.Fatalf("ValidateDesktopSessionID(%q) 应通过，得 %v", tc.id, err)
			}
			if !tc.ok {
				if err == nil {
					t.Fatalf("ValidateDesktopSessionID(%q) 应报错", tc.id)
				}
				if errors.Is(err, proto.ErrDesktopNotImplemented) {
					t.Fatalf("ValidateDesktopSessionID(%q) 返回空壳错误，不是形状判定: %v", tc.id, err)
				}
			}
		})
	}
}
