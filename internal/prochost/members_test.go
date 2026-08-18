// members.json 的读写测试：序列化往返、缺失与损坏的容错。
package prochost

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMembersRoundTrip(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, MembersFileName)
	want := memberSnapshot{PIDs: []int{100, 200, 300}, SampledAt: 1755500000000000000}
	if err := writeMembers(p, want); err != nil {
		t.Fatalf("writeMembers: %v", err)
	}
	got, err := readMembers(p)
	if err != nil {
		t.Fatalf("readMembers: %v", err)
	}
	if got.SampledAt != want.SampledAt || len(got.PIDs) != len(want.PIDs) {
		t.Fatalf("往返不一致：got=%+v want=%+v", got, want)
	}
	for i := range want.PIDs {
		if got.PIDs[i] != want.PIDs[i] {
			t.Fatalf("第 %d 个 pid 不一致：got=%d want=%d", i, got.PIDs[i], want.PIDs[i])
		}
	}
}

// 文件不存在是正常形态（任务刚起、还没采过），必须报错而不是返回空快照。
func TestReadMembersMissingFileErrors(t *testing.T) {
	if _, err := readMembers(filepath.Join(t.TempDir(), "nope.json")); err == nil {
		t.Fatal("文件不存在时应报错，返回空快照会被误读成「没有成员」")
	}
}

// 损坏的文件同样必须报错，不能静默当空。
func TestReadMembersCorruptErrors(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, MembersFileName)
	if err := os.WriteFile(p, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readMembers(p); err == nil {
		t.Fatal("文件损坏时应报错")
	}
}

// 路径推导要与 roster 同款：与 proc.json 同目录。
func TestMembersPathBesideInfoPath(t *testing.T) {
	got := membersPath("/data/tasks/abc/proc.json")
	want := filepath.Join("/data/tasks/abc", MembersFileName)
	if got != want {
		t.Fatalf("got=%q want=%q", got, want)
	}
	if membersPath("") != "" {
		t.Fatal("infoPath 为空时应返回空串（与 rosterPath 同款降级）")
	}
}
