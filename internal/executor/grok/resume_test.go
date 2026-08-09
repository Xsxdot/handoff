package grok_test

import (
	"testing"

	"github.com/xushixin/handoff/internal/executor/grok"
)

func TestResumeWithoutSessionIDIsNotAlive(t *testing.T) {
	a := grok.New(nil)
	alive, err := a.Resume("t1", t.TempDir(), t.TempDir(), "")
	if alive {
		t.Error("无 sessionID 无法 session/load，必须判不可恢复")
	}
	if err != nil {
		t.Errorf("不可恢复不是错误，应静默返回 false，得到 %v", err)
	}
}

func TestResumeWithoutServeInfoIsNotAlive(t *testing.T) {
	a := grok.New(nil)
	alive, _ := a.Resume("t1", t.TempDir(), t.TempDir(), "sess-1")
	if alive {
		t.Error("serve.json 缺失时必须判不可恢复")
	}
}
