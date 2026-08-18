package codex_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/executor"
	"github.com/Xsxdot/handoff/internal/executor/codex"
	"github.com/Xsxdot/handoff/internal/proto"
)

func TestStartInjectsDisciplineIntoPrompt(t *testing.T) {
	got := codex.RenderStartPromptForTest(executor.StartReq{
		Task:        proto.Task{ID: "T1"},
		PlanContent: "计划正文",
		Discipline:  "# 执行纪律\n单上下文版内容",
	})
	if !strings.Contains(got, "单上下文版内容") {
		t.Fatalf("纪律块没进 prompt，实得：%.120s", got)
	}
	if !strings.Contains(got, "计划正文") {
		t.Error("计划正文丢了")
	}
}

func TestSandboxPolicyGrantsTaskTmp(t *testing.T) {
	p := codex.SandboxPolicyForTest("/data/tasks/T1/tmp")
	roots, _ := p["writableRoots"].([]any)
	if len(roots) != 1 || roots[0] != "/data/tasks/T1/tmp" {
		t.Fatalf("writableRoots = %v，任务专属 tmp 没进可写域", p["writableRoots"])
	}
	if p["excludeSlashTmp"] != true || p["excludeTmpdirEnvVar"] != true {
		t.Error("两个 exclude 被放开了，任务隔离被破坏")
	}
	if p["networkAccess"] != true {
		t.Error("networkAccess 被改动")
	}
}

func TestTmpEnvPointsGoToolchainAtTaskTmp(t *testing.T) {
	kvs := codex.TmpEnvKVsForTest("/data/tasks/T1/tmp")
	want := map[string]string{
		"TMPDIR":   "/data/tasks/T1/tmp",
		"GOTMPDIR": "/data/tasks/T1/tmp",
		"GOCACHE":  "/data/tasks/T1/tmp/gocache",
	}
	got := map[string]string{}
	for _, kv := range kvs {
		k, v, _ := strings.Cut(kv, "=")
		got[k] = v
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
}

func TestTaskTmpDirLivesUnderTaskDir(t *testing.T) {
	got := codex.TaskTmpDirForTest("/data/tasks/T1")
	if got != filepath.Join("/data/tasks/T1", "tmp") {
		t.Fatalf("taskTmpDir = %q", got)
	}
}
