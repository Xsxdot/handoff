package grok_test

import (
	"testing"

	"github.com/Xsxdot/handoff/internal/executor"
	"github.com/Xsxdot/handoff/internal/executor/grok"
)

func TestResumeWithoutSessionIDIsNotAlive(t *testing.T) {
	a := grok.New(nil)
	out, err := a.Resume(executor.ResumeReq{TaskID: "t1", TaskDir: t.TempDir(), RepoPath: t.TempDir()})
	if out.Alive {
		t.Error("无 sessionID 无法 session/load，必须判不可恢复")
	}
	if err != nil {
		t.Errorf("不可恢复不是错误，应静默返回不存活，得到 %v", err)
	}
}

func TestResumeWithoutServeInfoIsNotAlive(t *testing.T) {
	a := grok.New(nil)
	out, _ := a.Resume(executor.ResumeReq{TaskID: "t1", TaskDir: t.TempDir(), RepoPath: t.TempDir(), SessionID: "sess-1"})
	if out.Alive {
		t.Error("serve.json 缺失时必须判不可恢复")
	}
}
