// recipients_b435_test.go —— B435 匹配函数接缝的消费方契约断言（契约 b435-contract.md ②）。
//
// 锁两件事：导出签名（四个消费方据此独立编译）、默认收件人「≤1」由返回类型保证。
// Ticket 0 的「空壳不得成功」一支已随 K0 落码删除（契约 §12 欠账 4）；判定表行为见
// recipients_decision_b435_test.go。
package collab

import (
	"reflect"
	"testing"

	"github.com/Xsxdot/handoff/internal/proto"
)

// 条 C-211/C-212：消费方按此签名编译；改签名必红。
var (
	_ func(*Service, proto.RoomMessage, string) (proto.Delivery, error)                    = (*Service).Recipients
	_ func(*Service, proto.RoomMessage, string, proto.DeliveryAddress) (bool, error)       = (*Service).Matches
	_ func(*Service, string) (proto.DeliveryAddress, proto.NoRecipientReason, bool, error) = (*Service).defaultRecipient
)

// 条 C-213：默认收件人分支的产出是单个地址，不是切片/映射——「解析结果 ≤ 1 人」写不出反例。
func TestB435DefaultRecipientReturnsSingleAddress(t *testing.T) {
	ft := reflect.TypeOf((*Service).defaultRecipient)
	if ft.NumOut() != 4 {
		t.Fatalf("defaultRecipient 返回值个数 = %d，want 4", ft.NumOut())
	}
	if got := ft.Out(0); got != reflect.TypeOf(proto.DeliveryAddress{}) {
		t.Fatalf("defaultRecipient 首个返回值必须是单个 proto.DeliveryAddress，得到 %v", got)
	}
	for i := 0; i < ft.NumOut(); i++ {
		switch ft.Out(i).Kind() {
		case reflect.Slice, reflect.Map, reflect.Array, reflect.Chan:
			t.Fatalf("defaultRecipient 第 %d 个返回值是集合类型 %v——默认收件人 ≤1 的形状被破坏", i, ft.Out(i))
		}
	}
}
