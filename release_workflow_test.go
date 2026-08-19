// release workflow 的约定测试。
//
// 为什么值得单测一个 CI 配置：资产命名与注入路径是「一处约定、多处消费」——
// workflow 产出、install.sh 消费、B54.3 的自更新是第三处。改错任何一边都不会
// 在编译期暴露，只会在真机上表现为「404 找不到资产」或「装上的二进制自称 unknown」。
// 这个测试让漂移在 go test 阶段就翻红。
package main

import (
	"os"
	"os/exec"
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

// stripYAMLComments 剔除 YAML 里首个非空白字符是 # 的整行，返回剩余文本。
//
// 为什么存在：本文件的契约断言打在 workflow 原文上，而解释性的注释里
// 常常出现与断言相同的字面量（比如「--options runtime 是公证的前置条件」
// 这行注释），于是注释自己就把断言满足了——把真正的命令删掉，测试照样绿。
// 断言只看非注释内容，这条被自己的注释架空的漏洞才堵得上。
//
// 只剔整行注释，不剔行尾 #：本仓库的 workflow 里没有行尾 # 注释，而剔行尾
// # 会误伤 shell 命令里的 #（例如 sha256sum 的 "path/*  name" 里没有，
// 但 GITHUB_ENV 的 echo "KEY=${value}" 这类行可能含 #）。
func stripYAMLComments(s string) string {
	var b strings.Builder
	for _, line := range strings.Split(s, "\n") {
		if t := strings.TrimSpace(line); t != "" && t[0] == '#' {
			continue
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return b.String()
}

// modulePathFromGoMod 读 go.mod 的 `module ` 行，返回模块路径。
func modulePathFromGoMod(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("go.mod")
	if err != nil {
		t.Fatalf("读 go.mod 失败: %v", err)
	}
	for _, line := range strings.Split(string(b), "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "module "); ok {
			return strings.TrimSpace(rest)
		}
	}
	t.Fatal("go.mod 里没有 module 行")
	return ""
}

// ldflags 的 -X 路径必须是 module path，写成 GitHub owner 会静默失效
// （构建成功、二进制自称 unknown、自动更新永远认为自己已是最新）。
// 期望值从 go.mod 派生，杜绝「模块改名后这里分叉」。
func TestWorkflowInjectsVersionAtModulePath(t *testing.T) {
	want := "-X " + modulePathFromGoMod(t) + "/internal/buildinfo.releaseVersion="
	// Count 而不是 Contains：断言「至少一次」会放走「只改对一处、别处漏改」。
	//
	// 这个数字是「workflow 里编 CLI 的地方有几处」的代理，**加构建点就要同步加**。
	// W5b-3 之前是 2（build-unix / build-darwin）；两个薄壳 job 各自也要编一份
	// CLI 嵌进壳里（那份会被释出到本平台的约定落点——Windows 是
	// %LOCALAPPDATA%\Programs\handoff\handoff.exe，其余是 ~/.local/bin/handoff——
	// 用户敲 handoff version 看到的就是它）；W5b-4 增了 build-desktop-windows
	// 薄壳 job，构建点从 4 个变 5 个，所以现在是 5：
	//
	//   build-unix · build-darwin · build-desktop-linux · build-desktop-darwin
	//   · build-desktop-windows
	//
	// 漏掉薄壳那两处的症状最阴：壳能装能跑，但它释出的 CLI 自称 unknown，
	// 自更新永远认为自己已是最新。
	const wantCount = 5
	if n := strings.Count(stripYAMLComments(readWorkflow(t)), want); n != wantCount {
		t.Fatalf("workflow 应恰好含 %d 处注入路径 %q，实得 %d 处", wantCount, want, n)
	}
}

// 资产命名是与 install.sh 的契约，模式变了两边必须一起变。
func TestWorkflowUsesAgreedAssetNaming(t *testing.T) {
	wf := stripYAMLComments(readWorkflow(t))
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
	wf := stripYAMLComments(readWorkflow(t))
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
	ci := stripYAMLComments(readCI(t))
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
	wf := stripYAMLComments(readWorkflow(t))
	for _, want := range []string{
		"--options runtime", // 硬化运行时是公证的前置条件，不加会被拒
		"notarytool submit",
		"status: Accepted", // notarytool 可能在 Invalid 时仍退 0，必须查状态串
		"Authority=Developer ID Application",
		// 裸 CLI 不能 staple 票据，但 spctl 的 `-t open` 能按 cdhash 查到它。
		// 这两条钉住的是「喂法必须是 -t open」——写成 -t exec 会被 app 类型检查
		// 挡掉，而那次失败长得像「公证没生效」，极易被再撤一次（2026-08-13 已撤过）。
		"-t open --context context:primary-signature",
		"source=Notarized Developer ID",
		"CGO_ENABLED", // macOS 上 CGO 默认开，开了会引入动态链接与最低系统版本约束
	} {
		if !strings.Contains(wf, want) {
			t.Fatalf("build-darwin 缺关键步骤 %q", want)
		}
	}
}

// 薄壳两个 job 的承重旋钮不能被悄悄改掉。
//
// 这条与 TestDarwinJobSignsAndNotarizes 同类，守的是「改错之后 CI 照样全绿、
// 只有用户遭殃」的东西。W5b-3 实现时真的踩到过其中第一条：计划写的是
// `wails3 task package GO_FLAGS="-tags embedbin ..."`，而整个 Taskfile 根本不
// 消费 GO_FLAGS——它只认 EXTRA_TAGS。传错不报错，构建照常成功，编出来的却是
// 一个 embedbin.Available() 走 stub、根本不含内嵌 CLI 的薄壳，而这要到用户
// 双击之后才暴露。--dry 实测：
//
//	GO_FLAGS=...   → go build -tags production ...
//	EXTRA_TAGS=... → go build -tags production,embedbin ...
//
// 同理 EXTRA_LDFLAGS：缺了它 embedbin.Version 为空，DecideRelease 永远判不出
// 内嵌版本，「已装的 CLI 比内嵌的旧」这条提示分支彻底失效。
func TestDesktopJobsCarryLoadBearingFlags(t *testing.T) {
	jobs := releaseJobs(t)
	lin, ok := jobs["build-desktop-linux"]
	if !ok {
		t.Fatal("release.yml 缺 build-desktop-linux job")
	}
	// AppImage 要在最老的目标发行版上构建。ubuntu-latest 会随 GitHub 滚动、
	// 某天静默抬高 glibc 基线，而这个变化不体现在任何一次提交里。
	if lin.RunsOn != "ubuntu-22.04" {
		t.Fatalf("Linux 薄壳必须锁 ubuntu-22.04（不能用 ubuntu-latest），实得 runs-on=%q", lin.RunsOn)
	}
	if dar, ok := jobs["build-desktop-darwin"]; !ok {
		t.Fatal("release.yml 缺 build-desktop-darwin job")
	} else if !strings.HasPrefix(dar.RunsOn, "macos") {
		t.Fatalf("darwin 薄壳必须在 macOS runner 上构建，实得 runs-on=%q", dar.RunsOn)
	}

	if win, ok := jobs["build-desktop-windows"]; !ok {
		t.Fatal("release.yml 缺 build-desktop-windows job")
	} else if !strings.HasPrefix(win.RunsOn, "windows") {
		t.Fatalf("windows 薄壳必须在 Windows runner 上构建（generate:syso 与 "+
			"Taskfile 里按 platforms 分支的清理命令只有原生跑才走到），实得 runs-on=%q", win.RunsOn)
	}

	wf := stripYAMLComments(readWorkflow(t))
	// 逐条带期望出现次数，而不是 Contains。
	//
	// 为什么：两个薄壳 job 各自传一次 EXTRA_TAGS/EXTRA_LDFLAGS，用 Contains 的话
	// 「只改对一处、另一处漏改」照样绿——本文件顶上的
	// TestWorkflowInjectsVersionAtModulePath 早就吃过这个亏，这里不该再犯。
	// 这条不是推演出来的：本测试第一版就是 Contains，变异复验（把 linux 那处
	// 的 EXTRA_TAGS 改回 GO_FLAGS）测试仍绿，当场证明那是一道假门。
	for _, c := range []struct {
		want string
		n    int
	}{
		// Taskfile 只认这两个变量名，传 GO_FLAGS 会被静默忽略。三个 job 各一次。
		{"EXTRA_TAGS=embedbin", 3},
		{"EXTRA_LDFLAGS=", 3},
		{"desktop/internal/embedbin.Version=", 3},
		// ARCH=amd64 不传的话 wails3 取宿主架构，runner 换代那天会静默产出一个
		// 名字叫 amd64、实际是 arm64 的资产——CI 全绿，用户下载后才发现跑不起来。
		{"ARCH=amd64", 1},
		// 装 wails3 这一步本身也要带 gtk3，否则在 22.04 上卡在准备工具阶段。仅 linux。
		{"go install -tags gtk3", 1},
		// 内嵌的那份 CLI 必须在嵌进去之前单独签名：嵌进去之后它就只是
		// go:embed 的字节块，再没有任何机会给它签名。仅 darwin。
		{"--sign \"$APPLE_SIGNING_IDENTITY\" desktop/internal/embedbin/handoff", 1},
	} {
		if got := strings.Count(wf, c.want); got != c.n {
			t.Fatalf("薄壳 job 的承重旋钮 %q 应出现 %d 次，实得 %d 次", c.want, c.n, got)
		}
	}

	// 薄壳资产必须显式列进 release：handoff-desktop_ 不匹配 handoff_*
	// （前缀后是 - 不是 _），不列就会漏出 checksums 与发布资产。
	rel, ok := jobs["release"]
	if !ok {
		t.Fatal("release.yml 缺 release job")
	}
	for _, dep := range []string{"build-desktop-linux", "build-desktop-darwin", "build-desktop-windows"} {
		if !needsSet(rel.Needs)[dep] {
			t.Fatalf("release 的 needs 里没有 %q —— 它会在薄壳 artifact 上传完成前起跑，"+
				"checksums 的通配匹配不到文件而失败，且失败是时序相关的", dep)
		}
	}
	if !strings.Contains(wf, "handoff-desktop_*") {
		t.Fatal("release job 没有显式收集 handoff-desktop_* —— 薄壳资产会漏出 checksums 与发布页")
	}
}

// 预发布 tag 必须被标成 prerelease。
//
// 又一条「删了照样绿、只有用户遭殃」：install.sh 与 agentd 自更新都认
// GitHub 的 releases/latest，而 GitHub 只把非 prerelease 的最新一版算作 latest。
// 少了这个判断，一个本意只为验证流水线的 v0.3.0-rc1 会当场成为所有用户装到、
// 所有 agentd 自更新拉到的版本。
func TestPrereleaseTagsAreNotPublishedAsLatest(t *testing.T) {
	wf := stripYAMLComments(readWorkflow(t))
	// 计数而不是 Contains：多出来的那一处若是无条件加的，正式版也会被标成
	// 预发布、永远发不出 latest——那是反方向的同一个坑，而 Contains 查不出来
	//（变异复验实测：保留条件那处、另加一处无条件的，Contains 版本仍然绿）。
	if n := strings.Count(wf, "--prerelease"); n != 1 {
		t.Fatalf("release job 里 --prerelease 应恰好出现 1 次，实得 %d 次。"+
			"0 次意味着 rc/beta tag 会被标成 Latest（install.sh 与 agentd 自更新会立刻拉到它）；"+
			"多于 1 次通常意味着有一处是无条件加的，正式版也会被标成预发布", n)
	}
	// 而且它必须是**按 tag 形态条件加上**的。
	if !strings.Contains(wf, `*-*) args+=(--prerelease) ;;`) {
		t.Fatal("--prerelease 必须按 tag 是否含连字符条件添加，否则正式版也会被标成预发布")
	}
}

// release notes 必须优先取自 CHANGELOG。
//
// 没有这条，CHANGELOG 就是个没人看也没人维护的摆设——而没人维护的文档
// 比没有更糟：它会让读者相信一份过期的事实。
func TestReleaseNotesComeFromChangelog(t *testing.T) {
	wf := stripYAMLComments(readWorkflow(t))
	for _, want := range []string{"CHANGELOG.md", "--notes-file"} {
		if !strings.Contains(wf, want) {
			t.Fatalf("release job 缺 %q —— release notes 应优先取自 CHANGELOG", want)
		}
	}
	// 抽不到时仍要能发布，否则一次格式失误会把整条发布卡死
	if !strings.Contains(wf, "--generate-notes") {
		t.Fatal("缺 --generate-notes 回落分支：CHANGELOG 抽取失败不该卡死发布")
	}
}

// CHANGELOG 必须存在且有 Unreleased 一节可供下次发布填写。
func TestChangelogExists(t *testing.T) {
	b, err := os.ReadFile("CHANGELOG.md")
	if err != nil {
		t.Fatalf("读 CHANGELOG.md 失败: %v", err)
	}
	if !strings.Contains(string(b), "## [Unreleased]") {
		t.Fatal("CHANGELOG.md 缺 [Unreleased] 一节")
	}
}

// utf8BOM 写成转义而不是字面量：Go 源码里出现字面 BOM 会被编译器直接拒收。
const utf8BOM = "\xef\xbb\xbf"

// install_test.ps1 只从磁盘跑（`powershell.exe -File`），所以它必须带 BOM。
//
// PowerShell 5.1（Windows 自带、绝大多数用户手上就是它）读 .ps1 文件时，没有
// UTF-8 BOM 就按系统 ANSI 代码页解码。中文 Windows 是 cp936/GBK，GBK 的前导
// 字节会把紧跟其后的 ASCII 字符吞掉，脚本当场变成语法错误、一行都跑不了。
//
// 这条断言必须存在的理由：CI 的 windows-latest 是 cp1252，字节一一对应不吞
// 字符，脚本照样能跑（只是中文乱码）——所以 CI 全绿也证明不了真机能跑。
// 08-13 真机（zh-CN，PowerShell 5.1）实测炸过一次，就是这么炸的。
func TestInstallTestPs1CarriesUTF8BOM(t *testing.T) {
	b, err := os.ReadFile("install_test.ps1")
	if err != nil {
		t.Fatalf("读 install_test.ps1 失败: %v", err)
	}
	if !strings.HasPrefix(string(b), utf8BOM) {
		t.Fatal("install_test.ps1 缺 UTF-8 BOM —— PowerShell 5.1 会按 ANSI 代码页" +
			"解码它，中文 Windows 上整个脚本会变成语法错误")
	}
}

// install.ps1 的规则**与上一条相反**：必须无 BOM，且必须纯 ASCII。
//
// 它的主消费方式是 README 里那条 `irm ... | iex`——`irm` 交给 `iex` 的是一个
// **字符串**，而 PowerShell 5.1 不把 U+FEFF 当空白，BOM 会粘进首个 token，
// 脚本在第一行就报「无法将 ?# 识别为 cmdlet」，且首行写什么都救不了（实测把
// 首行换成空行同样报「无法将 ? 识别为 cmdlet」）。
//
// 但它也可能被存下来当 .ps1 跑，那条路径又要求非 ASCII 内容必须有 BOM。
// 两个要求互斥，唯一同时满足的解是**不含任何非 ASCII 字节**——ASCII 在
// UTF-8 / cp936 / cp1252 下解码完全一致，于是 BOM 变得多余。
//
// 2026-08-13 真机实测（Windows Server 2025，PowerShell 5.1.26100，zh-CN，
// ANSI 代码页 936）：带 BOM 的版本经 irm|iex 跑，首行必红。
func TestInstallPs1IsBOMFreeASCII(t *testing.T) {
	b, err := os.ReadFile("install.ps1")
	if err != nil {
		t.Fatalf("读 install.ps1 失败: %v", err)
	}
	if strings.HasPrefix(string(b), utf8BOM) {
		t.Fatal("install.ps1 带了 UTF-8 BOM —— 它主要经 `irm | iex` 消费，" +
			"PS 5.1 会把 BOM 粘进首个 token，脚本第一行就挂")
	}
	for i, c := range b {
		if c > 0x7f {
			line := 1 + strings.Count(string(b[:i]), "\n")
			t.Fatalf("install.ps1 第 %d 行含非 ASCII 字节 0x%02x —— 无 BOM 时 "+
				"PS 5.1 会按 cp936 解码它，GBK 前导字节会吞掉后面的 ASCII 字符。"+
				"这个文件只能写英文（原委见其文件头）", line, c)
		}
	}
}

// TestToolRewrittenFilesPinnedToLF 钉住 .gitattributes 里那几条 eol=lf 声明。
//
// 为什么承重：go mod tidy、gofmt、wails3 generate bindings 这些工具**一律写 LF**，
// 而 Windows 上 git 默认 core.autocrlf=true 会把这些文件检出成 CRLF。于是构建
// 过程中任何一次重写都把工作树改回 LF、被 git 判成「已修改」，而 `git diff`
// 是**空的**（只有一行 "LF will be replaced by CRLF"）。症状看起来像依赖或
// 生成物变了，实际一个字符都没变。
//
// 这不是理论顾虑：2026-08-19 rc4 的 build-desktop-windows 就栽在这里——薄壳
// 编译成功、13.4MB 的 zip 都打出来了，却挂在自己末尾那道「工作区干净」检查上。
// 已在 Windows Server 2025 真机复现：干净克隆后只跑一次 go mod tidy，
// git status 立刻报 ` M desktop/go.mod`。
//
// 断言走 `git check-attr` 而不是对 .gitattributes 做字符串匹配：后者在模式
// 写错（少一层目录、通配符位置不对）时**照样绿**，那是一道假门。check-attr
// 问的是 git 自己「这个路径最终生效的属性是什么」。
func TestToolRewrittenFilesPinnedToLF(t *testing.T) {
	// 这一读**不是**多余的：断言本身走 `git check-attr` 子进程，Go 的测试缓存
	// 只登记本进程打开过的文件，看不见子进程读了什么。不读一次 .gitattributes，
	// 改它就不会让缓存失效——`go test`（不带 -count=1）会拿旧的 PASS 交差。
	//
	// 这不是推演：本用例第一版没有这一读，三条变异（删 go.mod 声明、把 bindings
	// 模式写少一层、把 go.sum 改成 -text）跑出来**全是绿的**，输出 "ok (cached)"，
	// 当场证明那是一道假门。os.ReadFile 把它登记成输入后，变异才抓得住。
	if _, err := os.ReadFile(".gitattributes"); err != nil {
		t.Fatalf("读 .gitattributes 失败: %v", err)
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("环境里没有 git，跳过（CI 上一定有）")
	}
	paths := []string{
		"go.mod", "go.sum",
		"desktop/go.mod", "desktop/go.sum",
		"desktop/frontend/bindings/github.com/wailsapp/wails/v3/internal/eventcreate.ts",
		"desktop/frontend/bindings/github.com/wailsapp/wails/v3/internal/eventdata.d.ts",
	}
	for _, p := range paths {
		out, err := exec.Command("git", "check-attr", "eol", "--", p).Output()
		if err != nil {
			t.Fatalf("git check-attr %s 失败: %v", p, err)
		}
		// 形如 "go.mod: eol: lf"
		if got := strings.TrimSpace(string(out)); !strings.HasSuffix(got, ": eol: lf") {
			t.Fatalf("%s 必须由 .gitattributes 钉成 eol=lf，实得 %q。\n"+
				"少了它，Windows 上任何一次工具重写都会让「工作区干净」检查挂在一个"+
				"内容毫无变化的文件上（git diff 为空），排错成本极高", p, got)
		}
	}
}

// macOS 薄壳的交付形态是 DMG，而组装 DMG 的调用方式是承重的。
//
// **`package:dmg` 与 `create:dmg` 不能互换。** 前者的 deps 挂着
// `package` → `build`（见 desktop/build/darwin/Taskfile.yml），会把流水线上一步
// 刚签名并装订好票据的 `.app` 整个重建掉，最后塞进 DMG 的是一个全新的、未签名
// 的 bundle。这条路上**没有任何一步会报错**：wails3 退 0、DMG 照常产出、
// job 全绿，要到用户挂载 DMG 双击被 Gatekeeper 拦住才暴露，而那时资产已经发出去了。
//
// DMG 本身也必须签 + 公证 + 装订：里面的 .app 虽已装订，但 DMG 作为下载物同样
// 带隔离属性，不装订的话首次挂载在离线机器上仍会被拦。
func TestDarwinDesktopShipsStapledDMG(t *testing.T) {
	wf := stripYAMLComments(readWorkflow(t))

	// 只看**调用行**，不看提及。报错文案里出现 package:dmg 是正当的（那正是要
	// 提醒的坑），禁的是真的去调它。第一版写成裸 Contains，被自己的 echo 文案
	// 绊倒——门要对准行为，不是对准字符串。
	var invokesDMG, invokesPackageDMG bool
	for _, line := range strings.Split(wf, "\n") {
		if !strings.Contains(line, "wails3 task") {
			continue
		}
		if strings.Contains(line, "package:dmg") {
			invokesPackageDMG = true
		}
		if strings.Contains(line, "create:dmg") {
			invokesDMG = true
		}
	}
	if invokesPackageDMG {
		t.Fatal("不得调 package:dmg：它的 deps 会重建 .app，把已签名已装订的 bundle " +
			"换成一个未签名的新 bundle，且全程不报错")
	}
	if !invokesDMG {
		t.Fatal("darwin 薄壳 job 没有调 create:dmg，不会产出 DMG")
	}
	// **带次数，不用 Contains。** bundle 那步本来就有一处 stapler staple，
	// 裸 Contains 的话「DMG 不装订」这条变异照样绿——第一版就是这么写的，
	// 变异复验当场抓出来。同一份文件顶上的
	// TestWorkflowInjectsVersionAtModulePath 早记过这个教训。
	for _, c := range []struct {
		want string
		n    int
	}{
		{"xcrun stapler staple \"$app\"", 1},   // bundle 装订
		{"xcrun stapler staple \"$dmg\"", 1},   // DMG 自己也要装订
		{"xcrun stapler validate \"$dmg\"", 1}, // 装订后立刻复核，别等用户来发现
		// 挂载产出的 DMG，复核里面那份 .app 的票据还在——这是 package:dmg
		// 重建 bundle 的运行期兜底，测试之外的第二道保险
		{"xcrun stapler validate \"$mnt/handoff-desktop.app\"", 1},
		{"notary-dmg.log", 3}, // 提交 / 查状态 / 打印失败详情，各一次
	} {
		if got := strings.Count(wf, c.want); got != c.n {
			t.Fatalf("DMG 承重步骤 %q 应出现 %d 次，实得 %d 次", c.want, c.n, got)
		}
	}
	// notarytool 在 status 为 Invalid 时仍可能退 0——两轮公证都必须查状态串，
	// 只查一轮等于放过另一轮。
	if got := strings.Count(wf, "status: Accepted"); got < 3 {
		t.Fatalf("每一轮公证都要查 'status: Accepted'（裸 CLI / bundle / DMG 共三轮），实得 %d 次", got)
	}
	// 交付的是 DMG，不再另发一份同内容的 zip
	if strings.Contains(wf, "handoff-desktop_${TAG}_darwin_arm64.zip") {
		t.Fatal("darwin 薄壳不该再发 zip：同一份 .app 出现两种资产会让用户不知道该下哪个")
	}
	if !strings.Contains(wf, "handoff-desktop_${TAG}_darwin_arm64.dmg") {
		t.Fatal("darwin 薄壳资产名必须是 .dmg")
	}
}

// TestDarwinAppCarriesRealVersion 钉住 .app 的版本号随 TAG 注入。
//
// 为什么需要这条门：Info.plist 里的版本是**签进 bundle 的静态文件**，不像
// ldflags 那样构建时必然被注入。漏掉这一步不会有任何报错——wails3 照常打包、
// 签名公证照常通过、DMG 照常产出——只有用户在访达「显示简介」里看到一个
// 假版本号。而这正是他排查「我到底装的是哪个版本」时唯一会看的地方。
//
// 断言用精确串计数而不是 Contains：Contains 会被本文件里任何一处提到
// plutil 的注释或 echo 文案蒙混过关（同一个坑本文件里已经吃过两次，见
// TestDarwinDesktopShipsStapledDMG 的注释）。
func TestDarwinAppCarriesRealVersion(t *testing.T) {
	wf := readWorkflow(t)
	plistPath := "desktop/build/darwin/Info.plist"
	want := map[string]int{
		`plutil -replace CFBundleShortVersionString -string "${TAG#v}" ` + plistPath: 1,
		`plutil -replace CFBundleVersion -string "${TAG#v}" ` + plistPath:            1,
	}
	for s, n := range want {
		if got := strings.Count(wf, s); got != n {
			t.Errorf("release.yml 里 %q 出现 %d 次，想要 %d 次", s, got, n)
		}
	}
	// 注入必须排在构建之前——排在之后等于没注入，而且同样不报错
	iInject := strings.Index(wf, `plutil -replace CFBundleShortVersionString`)
	// 当前 job 调的是 package；它的依赖会先执行 darwin build，再复制这份
	// Info.plist 组装 .app。断言对准实际调用，而不是凭旧的 task 名猜。
	iBuild := strings.Index(wf, `wails3 task package`)
	if iInject < 0 || iBuild < 0 {
		t.Fatalf("找不到注入步骤或构建步骤：inject=%d build=%d", iInject, iBuild)
	}
	if iInject > iBuild {
		t.Error("版本注入排在了 package 之后，构建读到的还是旧值")
	}

	// **改了被跟踪的文件就必须还原**，否则本 job 末尾的「确认工作区干净」必挂。
	//
	// rc11 第一次发布就是栽在这里：注入步骤本身完全正确、上面几条断言全绿，
	// 但它与另一个 step 的相互作用没人管——契约测试只看了「这一步对不对」，
	// 没看「这一步会不会把别的门弄挂」。凡是在 CI 里改仓库内被跟踪文件的步骤，
	// 都要连着问一句：这个 job 有没有干净门？
	// 断言带上 -C "$GITHUB_WORKSPACE"：还原那一步的 working-directory 是
	// desktop，而 git 的 pathspec 相对 cwd 解析，不锚定仓库根就会报
	// 「did not match any file(s) known to git」——rc11 第二次发布挂在这里。
	restore := `git -C "$GITHUB_WORKSPACE" checkout -- desktop/build/darwin/Info.plist`
	if got := strings.Count(wf, restore); got != 1 {
		t.Errorf("release.yml 里 %q 出现 %d 次，想要 1 次——"+
			"plutil 改的是仓库里被跟踪的文件，不还原，末尾的「确认工作区干净」必挂", restore, got)
	}
	iRestore := strings.Index(wf, restore)
	if iRestore < 0 {
		return
	}
	if iRestore < iBuild {
		t.Error("还原排在了 package 之前，package 组装 .app 时读到的就是被还原后的占位版本号")
	}
	// 还原必须排在那道干净门之前才有意义。取 package 之后的第一处干净检查。
	rel := strings.Index(wf[iBuild:], `out="$(git status --porcelain)"`)
	if rel < 0 {
		t.Fatal("找不到 package 之后的「确认工作区干净」步骤")
	}
	if iClean := iBuild + rel; iRestore > iClean {
		t.Error("还原排在了「确认工作区干净」之后，那道门还是会挂")
	}
}
