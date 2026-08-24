// disciplines_test.go —— 缝 2 的不可变版本化与写入校验（与 templates 同构判据）。
package ledger

import (
	"errors"
	"strings"
	"testing"
)

func TestDisciplineStoreRoundTrip(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.PutDiscipline("implement", "落台账。"); err != nil {
		t.Fatalf("put v1: %v", err)
	}
	if _, err := s.PutDiscipline("implement", "落台账。（补台账纪律后的 v2）"); err != nil {
		t.Fatalf("put v2: %v", err)
	}
	newest, err := s.GetDiscipline("implement", 0)
	if err != nil || newest.Version != 2 {
		t.Fatalf("get latest: %+v err=%v", newest, err)
	}
	old, err := s.GetDiscipline("implement", 1)
	if err != nil || old.Body != "落台账。" {
		t.Fatalf("旧版本必须原样可读: %+v err=%v", old, err)
	}
	if _, err := s.GetDiscipline("nope", 0); !errors.Is(err, ErrNotFound) {
		t.Fatalf("不存在的名字要 ErrNotFound，得到: %v", err)
	}
	for _, name := range []string{"", "   ", "../evil", "a/b", ".", ".."} {
		if _, err := s.PutDiscipline(name, "x"); err == nil {
			t.Fatalf("非法名 %q 要被拒绝", name)
		}
	}
	if _, err := s.PutDiscipline("blank", "   "); err == nil {
		t.Fatal("空正文要被拒绝")
	}
	if _, err := s.PutDiscipline("huge", strings.Repeat("x", 65<<10)); err == nil {
		t.Fatal("超 64KiB 正文要被拒绝")
	}
	names, err := s.ListDisciplineNames()
	if err != nil || len(names) != 1 || names[0] != "implement" {
		t.Fatalf("list: %v %v", names, err)
	}
}

func TestOpenCreatesSchemaHasDisciplines(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.db.Exec("SELECT * FROM disciplines LIMIT 0"); err != nil {
		t.Fatalf("表 disciplines 不存在: %v", err)
	}
}
