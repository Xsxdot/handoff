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
	kvs := codex.TmpEnvKVsForTest("/root/.handoff/tmp/137a7dc9")
	want := map[string]string{
		"TMPDIR":   "/root/.handoff/tmp/137a7dc9",
		"GOTMPDIR": "/root/.handoff/tmp/137a7dc9",
		"GOCACHE":  "/root/.handoff/tmp/137a7dc9/gocache",
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

// TestTaskTmpDirFitsUnixSocketBudget 守的不是字面量，而是一条预算不等式：
// 任务专属 tmp 必须短到能容下 claudecode 测试的真实子路径。unix socket 的
// AF_UNIX sun_path 上限是 108 字节（含结尾 NUL），internal/executor/claudecode/perm.go
// 的 sunPathMax = 107。
//
// 预算 51 来自本次实测的两个数字：
//   - 41 = <用例名><随机数>/001：Go t.TempDir() 在 TMPDIR 下再套的这一层；
//   - 10 = /perm.sock：claudecode 测试在临时目录里建的 unix socket。
//
// 用真实形状的 taskDir（默认 DataDir /root/.handoff（14 字节）+ /tasks/ +
// 36 字符 UUID）算出 taskTmpDir，断言 len + 51 < 107。DataDir 必须取默认位长度
// （≥9 字节）这条判据才真的会红：把 DataDir 换成短的（如 /t），被改回旧形状
// <DataDir>/tasks/<36 位 UUID>/tmp（61 字节）的 taskTmpDir 也塞得下预算，
// 变异就不红了——默认位长度正是这条防线本身。
//
// 另断言 taskTmpDir 仍在 DataDir 之内（相对 DataDir 恰为 tmp/<id8>）且不在仓库
// 工作区内：把 TMPDIR 指进仓库会让「非 git 目录应报错」用例的临时目录落入仓库，
// git 命令正常成功而假红。
func TestTaskTmpDirFitsUnixSocketBudget(t *testing.T) {
	const (
		dataDir  = "/root/.handoff"                       // 默认 DataDir，14 字节
		taskID   = "137a7dc9-df89-4c1c-891e-ebe106c68b37" // 36 字符 UUID
		testPath = 41                                     // t.TempDir() 的 <用例名><随机数>/001
		socket   = 10                                     // claudecode 测试的 /perm.sock
	)
	taskDir := filepath.Join(dataDir, "tasks", taskID)
	got := codex.TaskTmpDirForTest(taskDir)

	if len(got)+testPath+socket >= 107 {
		t.Fatalf("taskTmpDir = %q（len=%d），预算后 %d >= 107，unix socket 放不下",
			got, len(got), len(got)+testPath+socket)
	}

	if rel, err := filepath.Rel(dataDir, got); err != nil || rel != filepath.Join("tmp", "137a7dc9") {
		t.Fatalf("taskTmpDir = %q，相对 DataDir 应为 tmp/137a7dc9，实得 %q（err=%v）", got, rel, err)
	}
}
