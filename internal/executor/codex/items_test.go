package codex_test

import (
	"testing"

	"github.com/xushixin/handoff/internal/executor/codex"
)

func TestParseItemNotificationExtractsFileChangePaths(t *testing.T) {
	raw := []byte(`{"threadId":"t1","item":{
		"type":"fileChange","id":"patch-1",
		"changes":[{"path":"/w/a.go","kind":{"type":"update"}},
		           {"path":"/w/b.go","kind":{"type":"add"}}]}}`)
	it, ok := codex.ParseItemNotificationForTest(raw)
	if !ok {
		t.Fatal("应解析成功")
	}
	if it.Type() != "fileChange" || it.ID() != "patch-1" || len(it.Changes()) != 2 {
		t.Fatalf("解析结果不对: %+v", it)
	}
	if it.Changes()[0].Path != "/w/a.go" || it.Changes()[0].Kind.Type != "update" {
		t.Fatalf("changes[0] = %+v", it.Changes()[0])
	}
	if it.Changes()[1].Kind.Type != "add" {
		t.Fatalf("changes[1].kind = %+v", it.Changes()[1].Kind)
	}
}

func TestParseItemNotificationRejectsGarbage(t *testing.T) {
	for _, raw := range []string{``, `{}`, `{"item":{}}`, `not json`, `{"item":{"id":"x"}}`} {
		if it, ok := codex.ParseItemNotificationForTest([]byte(raw)); ok {
			t.Fatalf("垃圾输入 %q 不应解析成功，实得 %+v", raw, it)
		}
	}
}

func TestItemIndexEvictsOldestBeyondCap(t *testing.T) {
	idx := codex.NewItemIndexForTest(2)
	idx.PutForTest("a", "fileChange")
	idx.PutForTest("b", "fileChange")
	idx.PutForTest("c", "fileChange")
	if _, ok := idx.GetForTest("a"); ok {
		t.Fatal("超出上限后最旧条目应被淘汰")
	}
	if _, ok := idx.GetForTest("b"); !ok {
		t.Fatal("b 应还在")
	}
	if _, ok := idx.GetForTest("c"); !ok {
		t.Fatal("c 应还在")
	}
}

func TestItemIndexPutSameIDUpdatesInPlace(t *testing.T) {
	idx := codex.NewItemIndexForTest(2)
	idx.PutForTest("a", "commandExecution")
	idx.PutForTest("a", "commandExecution") // item/started → item/completed 会重复投递同一 id
	idx.PutForTest("b", "fileChange")
	if _, ok := idx.GetForTest("a"); !ok {
		t.Fatal("同 id 重复 put 不得占两个槽位把自己挤掉")
	}
}
