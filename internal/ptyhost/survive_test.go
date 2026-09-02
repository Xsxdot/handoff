//go:build unix

// survive_test.go —— A 的验收判据：agentd 侧客户端整个消失再回来，会话与滚屏都还在。
//
// 职责：把客户端 Host 丢掉、重新从 sessdir 扫描认领，再验证 backlog 与继续写入。
// 边界：不测试 agentd HTTP/WS，也不测试如何 fork handoff 二进制；hostproc 进程是真实
// goroutine 对端，验收集中在跨客户端生命周期的协议与持久进程边界。
package ptyhost_test

import (
	"bytes"
	"os"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/ptyhost"
	"github.com/Xsxdot/handoff/internal/ptyhost/sessdir"
)

func TestSurviveAgentdClientRestart(t *testing.T) {
	root, h1, id, _, late := startClientHostWithExitMarker(t)
	att1, err := h1.Attach(id, 0)
	if err != nil {
		t.Fatalf("h1 Attach: %v", err)
	}
	if err := h1.Write(id, []byte("echo BEFORE\n")); err != nil {
		t.Fatalf("h1 Write: %v", err)
	}
	if !attachmentContains(att1, "BEFORE") {
		t.Fatal("h1 没收到 BEFORE")
	}
	pidBefore, ok := h1.Get(id)
	if !ok {
		t.Fatal("h1 应能查询会话")
	}

	// 模拟 agentd 整体消失：只断订阅，不发 kill，也不再使用 h1。
	att1.Detach()

	entries, err := sessdir.Scan(root)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(entries) != 1 || entries[0].State != sessdir.StateLive {
		t.Fatalf("重启扫描 = %+v，期望一条 live 会话", entries)
	}

	h2 := ptyhost.New(root, "", testLog())
	h2.Adopt(entries)
	list := h2.List()
	if len(list) != 1 || list[0].ID != id || list[0].PID != pidBefore.PID {
		t.Fatalf("h2 List = %+v，期望认领原 PID=%d", list, pidBefore.PID)
	}

	att2, err := h2.Attach(id, 0)
	if err != nil {
		t.Fatalf("h2 Attach: %v", err)
	}
	defer att2.Detach()
	if !bytes.Contains(att2.Backlog, []byte("BEFORE")) {
		t.Fatalf("h2 backlog = %q，必须含客户端消失前的 BEFORE", att2.Backlog)
	}
	if err := h2.Write(id, []byte("echo AFTER\n")); err != nil {
		t.Fatalf("h2 Write: %v", err)
	}
	if !attachmentContains(att2, "AFTER") {
		t.Fatal("h2 没收到 AFTER，会话不能继续使用")
	}

	if err := h2.Close(id); err != nil {
		t.Fatalf("h2 Close: %v", err)
	}
	if _, err := os.Stat(late); err != nil {
		t.Fatalf("h2.Close 返回后 late marker 不存在: %v", err)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(sessdir.Dir(root, id)); os.IsNotExist(err) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := os.Stat(sessdir.Dir(root, id)); !os.IsNotExist(err) {
		t.Fatalf("Close 后会话目录应清掉: %v", err)
	}
	if entries, err := sessdir.Scan(root); err != nil || len(entries) != 0 {
		t.Fatalf("Close 后 Scan = entries=%+v err=%v，期望空", entries, err)
	}
}
