// release workflow 的约定测试。
//
// 为什么值得单测一个 CI 配置：资产命名与注入路径是「一处约定、多处消费」——
// workflow 产出、install.sh 消费、B54.3 的自更新是第三处。改错任何一边都不会
// 在编译期暴露，只会在真机上表现为「404 找不到资产」或「装上的二进制自称 unknown」。
// 这个测试让漂移在 go test 阶段就翻红。
package main

import (
	"os"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// readWorkflow 读 workflow 原文，并顺带验证它是合法 YAML。
func readWorkflow(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(".github/workflows/release.yml")
	if err != nil {
		t.Fatalf("读 workflow 失败: %v", err)
	}
	var doc any
	if err := yaml.Unmarshal(b, &doc); err != nil {
		t.Fatalf("workflow 不是合法 YAML: %v", err)
	}
	return string(b)
}

// ldflags 的 -X 路径必须是 module path，写成 GitHub owner 会静默失效
// （构建成功、二进制自称 unknown、自动更新永远认为自己已是最新）。
func TestWorkflowInjectsVersionAtModulePath(t *testing.T) {
	const want = "-X github.com/xushixin/handoff/internal/buildinfo.releaseVersion="
	if !strings.Contains(readWorkflow(t), want) {
		t.Fatalf("workflow 缺少注入路径 %q", want)
	}
}

// 资产命名是与 install.sh 的契约，模式变了两边必须一起变。
func TestWorkflowUsesAgreedAssetNaming(t *testing.T) {
	wf := readWorkflow(t)
	for _, want := range []string{
		`handoff_${TAG}_${GOOS}_${GOARCH}.tar.gz`,
		"checksums.txt",
	} {
		if !strings.Contains(wf, want) {
			t.Fatalf("workflow 缺少约定 %q", want)
		}
	}
}

// 平台清单必须正好是这六项。
//
// 这条断言在 B86 之前是「正好四项，且不得含 windows」——理由是 agentd 在
// Windows 上跑不起来（B37），发一个装了也用不了的二进制是负价值。B84 让
// 纯协调者机不再需要 agentd 之后，Windows 二进制第一次有了真实用途（只当
// 协调者），于是断言反转。**反转不等于取消**：少一项等于某个平台装不上，
// 多一项等于发一个没人验证过的资产。
//
// 清单从两处收集并取并集：交叉编译 job 的 matrix.include，以及签名 job 的
// DARWIN_ARCHES（darwin 两项要在同一个 job 里合并成一个 zip 一次提交公证，
// 所以它不是矩阵）。取并集而不是写死取哪个 job，这条断言才能跨越
// 「darwin 还在矩阵里」与「darwin 已拆走」两种布局都成立。
func TestWorkflowCoversExactlySixPlatforms(t *testing.T) {
	jobs := releaseJobs(t)
	got := map[string]bool{}
	for _, j := range jobs {
		for _, e := range j.Strategy.Matrix.Include {
			got[e.Goos+"/"+e.Goarch] = true
		}
		for _, a := range strings.Fields(j.Env["DARWIN_ARCHES"]) {
			got["darwin/"+a] = true
		}
	}
	want := map[string]bool{
		"darwin/arm64": true, "darwin/amd64": true,
		"linux/amd64": true, "linux/arm64": true,
		"windows/amd64": true, "windows/arm64": true,
	}
	for p := range want {
		if !got[p] {
			t.Errorf("平台清单缺 %s", p)
		}
	}
	for p := range got {
		if !want[p] {
			t.Errorf("平台清单多出 %s —— 多一项等于发一个没人验证过的资产", p)
		}
	}
}

// 归档格式按平台分，这是与 internal/release.archiveExt 及两个 install 脚本
// 四处共同的契约：Windows 出 zip（资源管理器可双击、Expand-Archive 人人有），
// 其余出 tar.gz。
func TestWorkflowUsesZipForWindowsOnly(t *testing.T) {
	wf := readWorkflow(t)
	for _, want := range []string{
		`handoff_${TAG}_${GOOS}_${GOARCH}.zip`,
		`handoff_${TAG}_${GOOS}_${GOARCH}.tar.gz`,
		"handoff.exe",
	} {
		if !strings.Contains(wf, want) {
			t.Fatalf("workflow 缺约定 %q", want)
		}
	}
}

// wfJob 是 workflow 里一个 job 的关键字段。
//
// Needs 声明成 any：GitHub Actions 允许它是单个字符串，也允许是字符串数组，
// 两种写法都合法且都会在真实 workflow 里出现。
//
// Strategy 与 Env 是给平台矩阵断言用的：平台清单分散在两处（交叉编译 job 的
// matrix，与签名 job 的 DARWIN_ARCHES），解析结构比 grep 字符串稳。
type wfJob struct {
	Uses     string            `yaml:"uses"`
	RunsOn   string            `yaml:"runs-on"`
	Needs    any               `yaml:"needs"`
	Env      map[string]string `yaml:"env"`
	Strategy struct {
		Matrix struct {
			Include []struct {
				Goos   string `yaml:"goos"`
				Goarch string `yaml:"goarch"`
			} `yaml:"include"`
		} `yaml:"matrix"`
	} `yaml:"strategy"`
}

// releaseJobs 解析 release.yml 的 jobs 段。
func releaseJobs(t *testing.T) map[string]wfJob {
	t.Helper()
	b, err := os.ReadFile(".github/workflows/release.yml")
	if err != nil {
		t.Fatalf("读 release.yml 失败: %v", err)
	}
	var doc struct {
		Jobs map[string]wfJob `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(b, &doc); err != nil {
		t.Fatalf("release.yml 不是合法 YAML: %v", err)
	}
	return doc.Jobs
}

// needsSet 把 needs 字段归一成集合。
func needsSet(v any) map[string]bool {
	out := map[string]bool{}
	switch n := v.(type) {
	case string:
		out[n] = true
	case []any:
		for _, e := range n {
			if s, ok := e.(string); ok {
				out[s] = true
			}
		}
	}
	return out
}

// dependsOnVerify 判断某 job 的 needs 闭包里是否含 verify。
//
// 判闭包而不是判直接依赖：release job 依赖 build、build 依赖 verify，
// 这已经被挡住了，不该强迫它再直接写一遍 verify。
func dependsOnVerify(jobs map[string]wfJob, name string, seen map[string]bool) bool {
	if seen[name] {
		return false
	}
	seen[name] = true
	for dep := range needsSet(jobs[name].Needs) {
		if dep == "verify" || dependsOnVerify(jobs, dep, seen) {
			return true
		}
	}
	return false
}

// readCI 读 ci.yml 原文，并顺带验证它是合法 YAML。
func readCI(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(".github/workflows/ci.yml")
	if err != nil {
		t.Fatalf("读 ci.yml 失败: %v", err)
	}
	var doc any
	if err := yaml.Unmarshal(b, &doc); err != nil {
		t.Fatalf("ci.yml 不是合法 YAML: %v", err)
	}
	return string(b)
}

// 每个 job 都必须被验证门挡着。
//
// 这条守的是「删掉之后一切照常绿、只有用户遭殃」的东西：把 verify 摘掉，
// release 会跑得更快、照样出资产，只是从此没有任何测试挡在推 tag 之前——
// 而 release_workflow_test.go 与 install_test.sh 恰恰是专为发布路径写的。
func TestEveryReleaseJobIsGatedByVerify(t *testing.T) {
	jobs := releaseJobs(t)
	v, ok := jobs["verify"]
	if !ok {
		t.Fatal("release.yml 缺 verify job")
	}
	if v.Uses != "./.github/workflows/ci.yml" {
		t.Fatalf("verify 必须复用 ci.yml（写两份定义必然漂移），实得 uses=%q", v.Uses)
	}
	for name := range jobs {
		if name == "verify" {
			continue
		}
		if !dependsOnVerify(jobs, name, map[string]bool{}) {
			t.Fatalf("job %q 的 needs 闭包里没有 verify —— 验证门挡不住它", name)
		}
	}
}

// 验证门的内容不能被悄悄掏空。
func TestCIGateCoversFullCheckSuite(t *testing.T) {
	ci := readCI(t)
	for _, want := range []string{
		"workflow_call",
		"go build ./...",
		"go vet ./...",
		"go test ./... -count=1",
		"gofmt -l",
		"GOOS=windows GOARCH=amd64 go build ./...",
		"GOOS=windows GOARCH=arm64 go build ./...",
		"bash install_test.sh",
	} {
		if !strings.Contains(ci, want) {
			t.Fatalf("ci.yml 缺检查项 %q", want)
		}
	}
}

// 签名与公证不能被摘掉。
//
// 这条与 TestEveryReleaseJobIsGatedByVerify 是同一类断言：把 codesign /
// notarytool 那几步删了，release 会跑得更快、照样出资产，只是从此发出去的
// 是未签名版本——症状出现在用户机器上（浏览器下载被 Gatekeeper 拦），
// 且从 CI 的绿色里完全看不出来。
func TestDarwinJobSignsAndNotarizes(t *testing.T) {
	jobs := releaseJobs(t)
	j, ok := jobs["build-darwin"]
	if !ok {
		t.Fatal("release.yml 缺 build-darwin job")
	}
	if !strings.HasPrefix(j.RunsOn, "macos") {
		t.Fatalf("darwin 资产必须在 macOS runner 上构建（codesign/notarytool 只在那儿有），实得 runs-on=%q", j.RunsOn)
	}
	wf := readWorkflow(t)
	for _, want := range []string{
		"--options runtime", // 硬化运行时是公证的前置条件，不加会被拒
		"notarytool submit",
		"spctl -a -t exec",
		"CGO_ENABLED", // macOS 上 CGO 默认开，开了会引入动态链接与最低系统版本约束
	} {
		if !strings.Contains(wf, want) {
			t.Fatalf("build-darwin 缺关键步骤 %q", want)
		}
	}
}
