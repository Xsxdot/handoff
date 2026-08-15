// 镜像存储测试：幂等追加、水位、区间读、快照 upsert 与路由索引。
package store

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/proto"
)

// openTestStore 打开一个临时库并注册清理（package store 白盒测试的本地辅助）。
func openTestStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "handoff.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func TestAppendMirrorEventIdempotent(t *testing.T) {
	st := openTestStore(t)
	ev := proto.Event{Seq: 7, TaskID: "T1", Type: proto.EventTypeQuestion,
		Payload: json.RawMessage(`{"text":"继续吗"}`), CreatedAt: time.Now().UTC()}

	first, err := st.AppendMirrorEvent("T1", ev)
	if err != nil || !first {
		t.Fatalf("首次追加应插入：inserted=%v err=%v", first, err)
	}
	// 重连补拉会把同一条再送一遍：必须幂等，且如实报告「没插入」
	again, err := st.AppendMirrorEvent("T1", ev)
	if err != nil {
		t.Fatalf("重复追加不该报错：%v", err)
	}
	if again {
		t.Error("重复的 (task_id, seq) 不该被计为插入")
	}

	wm, err := st.MirrorWatermark("T1")
	if err != nil || wm != 7 {
		t.Fatalf("水位 = %d（err=%v），期望 7", wm, err)
	}
	// 没有任何镜像事件的任务，水位是 0——首次订阅从头拉
	if wm2, err := st.MirrorWatermark("T-none"); err != nil || wm2 != 0 {
		t.Fatalf("空任务水位 = %d（err=%v），期望 0", wm2, err)
	}

	evs, err := st.MirrorEventsFrom("T1", 0, 100)
	if err != nil || len(evs) != 1 || evs[0].Seq != 7 {
		t.Fatalf("区间读结果不对：%+v err=%v", evs, err)
	}
	// 远端 seq 原值保留：本机不重编号，重连凭它续拉
	if evs[0].Type != proto.EventTypeQuestion {
		t.Errorf("事件类型丢了：%+v", evs[0])
	}
	if none, _ := st.MirrorEventsFrom("T1", 7, 100); len(none) != 0 {
		t.Errorf("from_seq 是开区间，seq=7 不该再出现：%+v", none)
	}
}

func TestMirrorTaskSnapshotAndRouting(t *testing.T) {
	st := openTestStore(t)
	now := time.Now().UTC()
	task := proto.Task{ID: "T1", Name: "远端任务", State: proto.TaskStateRunning,
		RepoPath: "/remote/handoff", CreatedAt: now, UpdatedAt: now}
	if err := st.UpsertMirrorTask("devbox", task, now); err != nil {
		t.Fatalf("UpsertMirrorTask: %v", err)
	}
	task.State = proto.TaskStateWaitingReview
	if err := st.UpsertMirrorTask("devbox", task, now.Add(time.Minute)); err != nil {
		t.Fatalf("二次 upsert: %v", err)
	}

	list, err := st.ListMirrorTasks()
	if err != nil || len(list) != 1 {
		t.Fatalf("镜像任务数 = %d（err=%v），期望 1", len(list), err)
	}
	if list[0].Task.State != proto.TaskStateWaitingReview {
		t.Errorf("快照应被覆盖为最新状态：%+v", list[0].Task)
	}
	if list[0].Target != "devbox" {
		t.Errorf("target 丢了：%+v", list[0])
	}

	target, ok, err := st.MirrorTaskTarget("T1")
	if err != nil || !ok || target != "devbox" {
		t.Fatalf("路由索引查不到：target=%q ok=%v err=%v", target, ok, err)
	}
	if _, ok, _ := st.MirrorTaskTarget("T-none"); ok {
		t.Error("不存在的任务不该报告命中")
	}
}
