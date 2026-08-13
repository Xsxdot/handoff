// handoff init 的 CLI 行为测试。
//
// 交互经 rootCmd.SetIn 喂脚本化答案，tty 判定经 initStdinIsTTY 缝控制，
// TTY 构造经 newInteractivePrompter 换成脚本化，因此测试既能覆盖交互
// 分支也不需要真的终端（huh 要真 TTY，CI 上会挂）。
package cmd

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xushixin/handoff/internal/config"
	"github.com/xushixin/handoff/internal/toolchain"
)

// runInit 跑一次 init：answers 是按行喂给 stdin 的答案，tty 控制是否走交互分支。
// 顺带用 fake 桩掉 newServiceManager——init 的托管追问默认答 y，不桩的话任何
// 「角色=执行机、答案用完」的用例都会真的往这台机器装 launchd/systemd 单元。
func runInit(t *testing.T, cfgPath string, tty bool, answers string) (string, error) {
	t.Helper()
	return runInitWith(t, cfgPath, tty, answers, nil)
}

// runInitWith 同 runInit，但用给定 fake 桩 newServiceManager（nil 时用默认空 fake）。
//
// 需要自定 fake（如 installErr）或要断言托管行为的用例传自己的 fake；
// 其余用例一律走 runInit 的默认空 fake，保证不真装任何服务。
func runInitWith(t *testing.T, cfgPath string, tty bool, answers string, f *fakeManager) (string, error) {
	t.Helper()
	resetFlags(t)
	if f == nil {
		f = &fakeManager{}
	}
	withFakeManager(t, f)

	// go test 二进制在编译缓存里，resolveSpec 会当临时文件拒掉，
	// 执行机答 y 的托管用例就走不到 fake.Install。
	oldExe := osExecutable
	osExecutable = func() (string, error) { return "/usr/local/bin/handoff", nil }
	t.Cleanup(func() { osExecutable = oldExe })

	oldTTY := initStdinIsTTY
	initStdinIsTTY = func() bool { return tty }
	t.Cleanup(func() { initStdinIsTTY = oldTTY })

	// 测试不得走 huh：CI 没有真终端，huh 会挂死。生产 RunE 用
	// newInteractivePrompter；这里一律换成脚本化，按行吃 SetIn 的答案。
	oldP := newInteractivePrompter
	newInteractivePrompter = func(in io.Reader, out io.Writer) prompter {
		return newScriptedPrompter(in, out)
	}
	t.Cleanup(func() { newInteractivePrompter = oldP })

	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	rootCmd.SetErr(&buf)
	rootCmd.SetIn(strings.NewReader(answers))
	rootCmd.SetArgs([]string{"init", "--config", cfgPath})
	t.Cleanup(func() {
		rootCmd.SetArgs(nil)
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)
		rootCmd.SetIn(nil)
	})
	err := rootCmd.ExecuteContext(context.Background())
	return buf.String(), err
}

// cancelPrompter 每个问题都立刻取消。用来钉死「取消不写盘」：
// 半截答案绝不能 Save，否则会留下一份只配了一半的配置。
type cancelPrompter struct{}

func (cancelPrompter) Select(string, []promptOption, string) (string, error) {
	return "", errPromptCanceled
}
func (cancelPrompter) Input(string, string) (string, error) {
	return "", errPromptCanceled
}
func (cancelPrompter) Confirm(string, bool) (bool, error) {
	return false, errPromptCanceled
}

// runInitProduction 走生产 TTY 构造缝，不换成脚本化。取消用例用它
// 替换 newInteractivePrompter；其余用例必须走 runInitWith。
func runInitProduction(t *testing.T, cfgPath string) (string, error) {
	t.Helper()
	resetFlags(t)
	withFakeManager(t, &fakeManager{})

	oldTTY := initStdinIsTTY
	initStdinIsTTY = func() bool { return true }
	t.Cleanup(func() { initStdinIsTTY = oldTTY })

	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	rootCmd.SetErr(&buf)
	rootCmd.SetIn(strings.NewReader(""))
	rootCmd.SetArgs([]string{"init", "--config", cfgPath})
	t.Cleanup(func() {
		rootCmd.SetArgs(nil)
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)
		rootCmd.SetIn(nil)
	})
	err := rootCmd.ExecuteContext(context.Background())
	return buf.String(), err
}

// 取消必须返回错误且不得改已有配置。
//
// 为什么不走 runInit：runInitWith 会把 newInteractivePrompter 换成脚本化，
// 空答案取默认并 Save，正好掩盖「取消仍写盘」的 bug。
func TestInitCanceledDoesNotWrite(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(p, []byte("listen: 127.0.0.1:7777\ntoken: keep\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// 先直接钉 askAll：任一问返回取消就立刻停。
	cfg, err := config.Load(p)
	if err != nil {
		t.Fatalf("预载配置: %v", err)
	}
	if _, _, err := askAll(io.Discard, cancelPrompter{}, cfg, nil, true); !errors.Is(err, errPromptCanceled) {
		t.Fatalf("askAll 取消应得 errPromptCanceled，得到 %v", err)
	}

	oldP := newInteractivePrompter
	newInteractivePrompter = func(in io.Reader, out io.Writer) prompter {
		return cancelPrompter{}
	}
	t.Cleanup(func() { newInteractivePrompter = oldP })

	if _, err := runInitProduction(t, p); err == nil {
		t.Fatal("取消应返回错误")
	} else if !errors.Is(err, errPromptCanceled) {
		t.Fatalf("取消应得 errPromptCanceled，得到 %v", err)
	}
	body, rerr := os.ReadFile(p)
	if rerr != nil {
		t.Fatal(rerr)
	}
	if !strings.Contains(string(body), "token: keep") {
		t.Fatalf("取消不得改文件:\n%s", body)
	}
}

// loadCfg 读回写盘的配置。
func loadCfg(t *testing.T, p string) *config.Config {
	t.Helper()
	cfg, err := config.Load(p)
	if err != nil {
		t.Fatalf("回读配置: %v", err)
	}
	return cfg
}

// 非 tty 时一问不问，只探测 + 写出厂默认，并明确告诉用户下一步。
//
// why：init 会被 install.sh 经管道调起（curl … | bash），那种场景下 stdin
// 被脚本占着。问了也没人答，卡住比不问糟得多。
func TestInitNonInteractiveWritesDefaults(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	out, err := runInit(t, p, false, "")
	if err != nil {
		t.Fatalf("init 不应报错: %v", err)
	}
	if !strings.Contains(out, "未交互配置") {
		t.Errorf("非 tty 时应提示未交互配置:\n%s", out)
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("配置应被写出来: %v", err)
	}
	cfg := loadCfg(t, p)
	if cfg.Listen != "127.0.0.1:7777" {
		t.Errorf("listen 应为出厂默认，得到 %q", cfg.Listen)
	}
	if cfg.Token == "" {
		t.Error("token 应被生成")
	}
}

// 探测表必须打印，且四家都在。
func TestInitPrintsDetectionTable(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	out, _ := runInit(t, p, false, "")
	for _, n := range []string{"opencode", "claude", "grok", "codex"} {
		if !strings.Contains(out, n) {
			t.Errorf("探测表缺 %s:\n%s", n, out)
		}
	}
}

// 交互下全部回车（取默认）：应写出一份合法配置，且角色默认能走通。
func TestInitInteractiveAllDefaults(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	// 30 个空行足够覆盖所有提问，多余的不会被读
	out, err := runInit(t, p, true, strings.Repeat("\n", 30))
	if err != nil {
		t.Fatalf("init 不应报错: %v\n%s", err, out)
	}
	cfg := loadCfg(t, p)
	if cfg.Listen == "" || cfg.Token == "" {
		t.Fatalf("配置不完整: %+v", cfg)
	}
}

// 幂等：已有配置时，每一问的默认值取当前值，全回车即原样保持。
//
// why 这条是 init 能当「改配置工具」用的前提。若默认值退回出厂值，
// 用户重跑一次 init 就会把 listen、token、targets 全部冲掉。
func TestInitIsIdempotent(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	body := "listen: 0.0.0.0:7788\ntoken: keepme\nrepo_root: /srv/repos\nexecutor:\n  default: grok\n"
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := runInit(t, p, true, strings.Repeat("\n", 30)); err != nil {
		t.Fatalf("init: %v", err)
	}
	cfg := loadCfg(t, p)
	if cfg.Listen != "0.0.0.0:7788" {
		t.Errorf("listen 被冲掉了: %q", cfg.Listen)
	}
	if cfg.Token != "keepme" {
		t.Errorf("token 被冲掉了: %q", cfg.Token)
	}
	if cfg.RepoRoot != "/srv/repos" {
		t.Errorf("repo_root 被冲掉了: %q", cfg.RepoRoot)
	}
	if cfg.Executor.Default != "grok" {
		t.Errorf("executor.default 被冲掉了: %q", cfg.Executor.Default)
	}
}

// 显式回答要被采纳：角色 executor，监听走 custom 再手填。
func TestInitAcceptsAnswers(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	answers := strings.Join([]string{
		"executor",
		"", // 默认执行者
		"", // 模型
		"custom",
		"0.0.0.0:7799",
		"", // repo_root
		"", // 审批链
	}, "\n") + "\n" + strings.Repeat("\n", 10)
	if _, err := runInit(t, p, true, answers); err != nil {
		t.Fatalf("init: %v", err)
	}
	if got := loadCfg(t, p).Listen; got != "0.0.0.0:7799" {
		t.Fatalf("listen=%q，期望采纳输入的 0.0.0.0:7799", got)
	}
}

// 末尾必须打印本机 token 与现成的配对片段——协调者机要靠它配 targets。
func TestInitPrintsPairingSnippet(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	out, err := runInit(t, p, true, "executor\n"+strings.Repeat("\n", 29))
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	if !strings.Contains(out, "targets:") {
		t.Errorf("应打印现成的配对 yaml 片段:\n%s", out)
	}
	if !strings.Contains(out, loadCfg(t, p).Token) {
		t.Error("配对片段里应含本机 token")
	}
}

// 工具落在「本次补全新增的目录」里时，探测表要说清楚这件事。
//
// why：这是整个 B71 里用户唯一能直接看到的价值——它同时回答了「为什么我 shell 里
// which opencode 找不到、handoff 却说就绪」和「重启之后还灵不灵」。
func TestPrintDetectionExplainsAugmentedDir(t *testing.T) {
	var buf bytes.Buffer
	rs := []toolchain.Result{
		{Name: "opencode", State: toolchain.StateReady, Path: "/home/u/.opencode/bin/opencode"},
	}

	printDetection(&buf, rs, []string{"/home/u/.opencode/bin"})

	out := buf.String()
	if !strings.Contains(out, "/home/u/.opencode/bin") {
		t.Errorf("应点名那个补出来的目录，实得:\n%s", out)
	}
	if !strings.Contains(out, "不在你的 PATH 里") {
		t.Errorf("应说明该目录不在用户 PATH 里，实得:\n%s", out)
	}
	if !strings.Contains(out, "自动补上") {
		t.Errorf("应说明 agentd 启动时会自动补上，实得:\n%s", out)
	}
}

// 工具本来就在用户 PATH 上时不加这句——不是每一行都要挂个说明。
func TestPrintDetectionQuietWhenDirAlreadyOnPath(t *testing.T) {
	var buf bytes.Buffer
	rs := []toolchain.Result{
		{Name: "opencode", State: toolchain.StateReady, Path: "/usr/local/bin/opencode"},
	}

	printDetection(&buf, rs, []string{"/home/u/.opencode/bin"})

	if strings.Contains(buf.String(), "不在你的 PATH 里") {
		t.Errorf("目录本来就在 PATH 上时不该加说明，实得:\n%s", buf.String())
	}
}

// 没探到的工具（Path 为空）不该匹配上任何补全目录。
func TestPrintDetectionQuietForMissingTool(t *testing.T) {
	var buf bytes.Buffer
	rs := []toolchain.Result{{Name: "grok", State: toolchain.StateMissing}}

	printDetection(&buf, rs, []string{"/home/u/.grok/bin"})

	if strings.Contains(buf.String(), "不在你的 PATH 里") {
		t.Errorf("没装的工具不该带补全说明，实得:\n%s", buf.String())
	}
}

// execAnswers 是「角色=执行机」那条问答路径的答案脚本，末尾一项是托管追问。
//
// 顺序对应 askAll：角色 / 默认执行者 / 模型 / 监听（Select） / repo_root /
// 审批链执行者 / 托管追问。空行=取默认值。
//
// 监听 Select 预选不是 custom 时没有后续 Input；custom 预选会多吃一行，
// 那种用例不要用本函数，自己把 Input 空行写进去。
func execAnswers(installAnswer string) string {
	return strings.Join([]string{
		"executor",
		"", // 默认执行者
		"", // 模型
		"", // 监听（首次预选 all）
		"", // repo_root
		"", // 审批链 = 不启用
		installAnswer,
	}, "\n") + "\n"
}

// 执行机上答 y：init 必须真的把 agentd 托管起来（而不是只打一行提示）。
//
// why：托管是「机器重启后 agentd 还回得来」的唯一保障。B71 现场那台就是因为
// 这一步只是最后一行提示，从没被执行过，重启后 PATH 全靠运气。
func TestInitInstallsServiceWhenAccepted(t *testing.T) {
	f := &fakeManager{}
	p := filepath.Join(t.TempDir(), "config.yaml")

	out, err := runInitWith(t, p, true, execAnswers("y"), f)
	if err != nil {
		t.Fatalf("init 不应报错: %v", err)
	}
	if f.installed == nil {
		t.Fatalf("答 y 必须真的调 Install，实得输出:\n%s", out)
	}
}

// 答 n：不装，但要留下可直接复制的命令与「不托管的后果」。
func TestInitSkipsServiceWhenDeclined(t *testing.T) {
	f := &fakeManager{}
	p := filepath.Join(t.TempDir(), "config.yaml")

	out, err := runInitWith(t, p, true, execAnswers("n"), f)
	if err != nil {
		t.Fatalf("init 不应报错: %v", err)
	}
	if f.installed != nil {
		t.Error("答 n 时不该调 Install")
	}
	if !strings.Contains(out, "handoff service install") {
		t.Errorf("答 n 时要留下可复制的命令，实得:\n%s", out)
	}
	if !strings.Contains(out, "重启") {
		t.Errorf("要说清不托管的后果（重启后不会自己回来），实得:\n%s", out)
	}
}

// 托管失败不能让 init 失败：配置已经写盘了，为一个附属动作退非零，
// 用户会以为配置没保存。
func TestInitSurvivesServiceInstallFailure(t *testing.T) {
	f := &fakeManager{installErr: errors.New("launchctl 挂了")}
	p := filepath.Join(t.TempDir(), "config.yaml")

	out, err := runInitWith(t, p, true, execAnswers("y"), f)
	if err != nil {
		t.Fatalf("托管失败不应让 init 报错，实得: %v", err)
	}
	if !strings.Contains(out, "launchctl 挂了") {
		t.Errorf("失败真因必须回显，实得:\n%s", out)
	}
	if _, statErr := os.Stat(p); statErr != nil {
		t.Errorf("配置必须已经写盘: %v", statErr)
	}
}

// 协调者机不追问托管——那台机器上根本不跑 agentd。
func TestInitDoesNotAskServiceForCoordinator(t *testing.T) {
	f := &fakeManager{}
	p := filepath.Join(t.TempDir(), "config.yaml")

	answers := strings.Join([]string{"coordinator", "", ""}, "\n") + "\n" // 角色 + sync + 结束配对
	out, err := runInitWith(t, p, true, answers, f)
	if err != nil {
		t.Fatalf("init 不应报错: %v", err)
	}
	if f.installed != nil {
		t.Errorf("协调者机不该装服务，实得输出:\n%s", out)
	}
}

// 角色=执行机、其余全默认作答时，托管追问的默认值 y 必须真的走安装路径。
//
// why：这是「答案脚本用完（EOF）、其余问题全取默认」的真实形态——B71 现场那台
// 正是全程默认。桩掉 manager 不等于不测这条路径，只是不真装。
func TestInitAllDefaultsInstallsService(t *testing.T) {
	f := &fakeManager{}
	p := filepath.Join(t.TempDir(), "config.yaml")

	answers := "executor\n" + strings.Repeat("\n", 8)
	if _, err := runInitWith(t, p, true, answers, f); err != nil {
		t.Fatalf("init 不应报错: %v", err)
	}
	if f.installed == nil {
		t.Error("全默认作答时托管追问取默认 y，必须真的调 Install")
	}
}

// 首次执行机：文件事先不存在，监听回车应预选所有网卡。
//
// why 必须 stat-before-Load：Load 会把缺文件写成 127.0.0.1:7777，事后再也
// 分不清「出厂」和「用户选过仅本机」。
func TestInitFirstRunExecutorListenDefaultsToAll(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatal("测试前提：配置文件必须事先不存在")
	}
	if _, err := runInit(t, p, true, execAnswers("n")); err != nil {
		t.Fatalf("init: %v", err)
	}
	if got := loadCfg(t, p).Listen; got != "0.0.0.0:7777" {
		t.Fatalf("首次执行机 listen=%q，期望 0.0.0.0:7777", got)
	}
}

// 文件已在且 listen 是仅本机：重跑回车必须保住 loopback，不能因为角色是执行机就改成 all。
func TestInitRerunKeepsLoopbackListen(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	body := "listen: 127.0.0.1:7777\ntoken: keepme\n"
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := runInit(t, p, true, execAnswers("n")); err != nil {
		t.Fatalf("init: %v", err)
	}
	if got := loadCfg(t, p).Listen; got != "127.0.0.1:7777" {
		t.Fatalf("重跑后 listen=%q，期望仍是 127.0.0.1:7777", got)
	}
}

// 手填残值必须 round-trip：0.0.0:7777 预选手填，空 Input 原样保留。
func TestInitRerunKeepsCustomListen(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	body := "listen: 0.0.0:7777\ntoken: keepme\n"
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	// custom 预选之后还有一档 Input，比 execAnswers 多一个空行
	answers := strings.Join([]string{
		"executor",
		"", // 默认执行者
		"", // 模型
		"", // 监听 Select → custom
		"", // 监听 Input → 保留 0.0.0:7777
		"", // repo_root
		"", // 审批链
		"n",
	}, "\n") + "\n"
	if _, err := runInit(t, p, true, answers); err != nil {
		t.Fatalf("init: %v", err)
	}
	if got := loadCfg(t, p).Listen; got != "0.0.0:7777" {
		t.Fatalf("重跑后 listen=%q，期望仍是 0.0.0:7777", got)
	}
}

// 审批链空选（不启用）→ approver.executor 为空，且不会再问模型把后续答案错位。
func TestInitApproverEmptyDisables(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	if _, err := runInit(t, p, true, execAnswers("n")); err != nil {
		t.Fatalf("init: %v", err)
	}
	if got := loadCfg(t, p).Approver.Executor; got != "" {
		t.Fatalf("审批链空选后 Approver.Executor=%q，期望空串", got)
	}
}

// 用户可见输出用「协调者」「默认」，不再出现「审核者」「缺省」。
func TestInitWordingCoordinatorNotReviewer(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	out, err := runInit(t, p, true, execAnswers("n"))
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	if !strings.Contains(out, "协调者") {
		t.Errorf("输出应含「协调者」:\n%s", out)
	}
	if !strings.Contains(out, "默认") {
		t.Errorf("输出应含「默认」:\n%s", out)
	}
	if strings.Contains(out, "审核者") {
		t.Errorf("输出不应再含「审核者」:\n%s", out)
	}
	if strings.Contains(out, "缺省") {
		t.Errorf("输出不应再含「缺省」:\n%s", out)
	}
}

// 探测区打通用 env 提示，不再写 codex 那三段代理专文。
func TestInitPrintsGenericEnvHint(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	out, err := runInit(t, p, true, execAnswers("n"))
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	if !strings.Contains(out, "~/.handoff/env/") {
		t.Errorf("应打印通用 env 提示:\n%s", out)
	}
	if !strings.Contains(out, "init 不创建、不修改这些文件") {
		t.Errorf("env 提示应说明 init 不碰这些文件:\n%s", out)
	}
	if strings.Contains(out, "failed to refresh available models") {
		t.Errorf("不应再出现 codex 代理专文:\n%s", out)
	}
}

// 写盘后提醒可随时重跑。
func TestInitPrintsRerunHint(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	out, err := runInit(t, p, true, execAnswers("n"))
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	if !strings.Contains(out, "可随时重跑") {
		t.Errorf("写盘后应提示可随时重跑:\n%s", out)
	}
}

// 监听选所有网卡时，配对片段的 addr 用探到的可达 IP，而不是占位符。
func TestInitPairingUsesDetectedIP(t *testing.T) {
	old := interfaceAddrs
	interfaceAddrs = func() ([]net.Addr, error) {
		return []net.Addr{&net.IPNet{IP: net.ParseIP("100.73.1.2"), Mask: net.CIDRMask(32, 32)}}, nil
	}
	t.Cleanup(func() { interfaceAddrs = old })

	p := filepath.Join(t.TempDir(), "config.yaml")
	out, err := runInit(t, p, true, execAnswers("n"))
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	if !strings.Contains(out, "100.73.1.2:7777") {
		t.Errorf("配对片段应含探到的 100.73.1.2:7777，实得:\n%s", out)
	}
	if strings.Contains(out, "<本机IP>") {
		t.Errorf("有可达 IP 时不该再打占位符:\n%s", out)
	}
}

// 监听三档：显式选 all / loopback 写入对应地址。
func TestInitListenSelectAll(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	answers := strings.Join([]string{"executor", "", "", "all", "", "", "n"}, "\n") + "\n"
	if _, err := runInit(t, p, true, answers); err != nil {
		t.Fatalf("init: %v", err)
	}
	if got := loadCfg(t, p).Listen; got != "0.0.0.0:7777" {
		t.Fatalf("选 all 后 listen=%q", got)
	}
}

func TestInitListenSelectLoopback(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	answers := strings.Join([]string{"executor", "", "", "loopback", "", "", "n"}, "\n") + "\n"
	if _, err := runInit(t, p, true, answers); err != nil {
		t.Fatalf("init: %v", err)
	}
	if got := loadCfg(t, p).Listen; got != "127.0.0.1:7777" {
		t.Fatalf("选 loopback 后 listen=%q", got)
	}
}

// 没装的执行者也必须出现在选项里——探测不阻断选择。
func TestExecutorOptionsIncludeMissing(t *testing.T) {
	rs := []toolchain.Result{
		{Name: "opencode", State: toolchain.StateReady},
		{Name: "claude", State: toolchain.StateAuthUnknown},
		{Name: "grok", State: toolchain.StateMissing},
		{Name: "codex", State: toolchain.StateNoCreds},
	}
	opts := executorOptions(rs)
	if len(opts) != 4 {
		t.Fatalf("应列出四家，得到 %d", len(opts))
	}
	byName := map[string]string{}
	for _, o := range opts {
		byName[o.Value] = o.Label
	}
	if !strings.Contains(byName["grok"], "没装") {
		t.Errorf("没装的 grok 应留在列表并旁注状态，得到 %q", byName["grok"])
	}
	if !strings.Contains(byName["opencode"], "就绪") {
		t.Errorf("就绪项 Label 应含状态，得到 %q", byName["opencode"])
	}
}

func TestListenPreset(t *testing.T) {
	cases := []struct {
		listen  string
		existed bool
		isExec  bool
		want    string
	}{
		{"127.0.0.1:7777", false, true, listenAll},
		{"127.0.0.1:7777", true, true, listenLoopback},
		{"0.0.0.0:7777", true, true, listenAll},
		{"[::]:7777", true, true, listenAll},
		{"0.0.0:7777", true, true, listenCustom},
		{"0.0.0.0:7788", true, true, listenCustom},
		{"192.168.1.9:7788", true, true, listenCustom},
	}
	for _, tc := range cases {
		if got := listenPreset(tc.listen, tc.existed, tc.isExec); got != tc.want {
			t.Errorf("listenPreset(%q, existed=%v, exec=%v)=%q, want %q",
				tc.listen, tc.existed, tc.isExec, got, tc.want)
		}
	}
}
