package agentd

import (
	"reflect"
	"testing"
)

// TestB434NeedsHumanSystemActorTable 锁契约条 180/181：needs_human 系统组件 actor
// 表与前缀的字面量（P5=B 排除规则）。改表 = 契约变更。
func TestB434NeedsHumanSystemActorTable(t *testing.T) {
	want := []string{"agentd", "keystone", "coordinator"}
	if !reflect.DeepEqual(desktopNeedsHumanSystemActors, want) {
		t.Fatalf("desktopNeedsHumanSystemActors = %v，契约冻结值 %v", desktopNeedsHumanSystemActors, want)
	}
	if desktopNeedsHumanSystemActorPrefix != "node:" {
		t.Fatalf("desktopNeedsHumanSystemActorPrefix = %q，契约冻结值 %q", desktopNeedsHumanSystemActorPrefix, "node:")
	}
}
