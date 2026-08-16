# B83 init 交互重做 + 协调者更名 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把 `handoff init` 改成 huh 选择式向导，监听拆成 bind / 广告地址，彻底删掉 `update.*` 配置项，产品可见面「审核者」改成「协调者」。

**Architecture:** `Load` 先剥顶层 `update` 再 `KnownFields` 解码，旧文件不砖、Save 永远不再写出。init 经 `prompter` 接口问答：测试走脚本化实现，TTY 走 huh。广告 IP 是可测纯函数，只进配对片段。更名只动活文件，不动协议字段和历史文档。

**Tech Stack:** Go 1.26、cobra、gopkg.in/yaml.v3、charmbracelet/huh、现有 `internal/toolchain` / `internal/config`

**Spec:** [docs/superpowers/specs/2026-08-13-init-ux-and-coordinator-rename-design.md](../specs/2026-08-13-init-ux-and-coordinator-rename-design.md)

---

## File Structure

| 文件 | 职责 |
|---|---|
| `internal/config/config.go` | 删 `Update`；Load 剥键 + 回写 |
| `internal/config/config_test.go` | 旧文件能 Load、回写后无 `update` |
| `cmd/agentd.go` | 去掉 `WarnDeprecated` |
| `cmd/advertise.go` | `listAdvertiseAddrs` + `advertiseAddr` |
| `cmd/advertise_test.go` | IP 过滤 / 排序 / 配对 addr |
| `cmd/prompter.go` | `prompter` 接口 + `scriptedPrompter` |
| `cmd/init_huh.go` | huh 实现 |
| `cmd/init.go` | 新问答流程、env 提示、可重跑提示 |
| `cmd/init_test.go` | 按新答案序列改写 |
| `cmd/permission_mcp.go` | reviewer → coordinator |
| `README.md` / `skills/handoff/SKILL.md` | 可见面更名；删 update 配置段 |

不新增 `internal/ui`。`handoff upgrade` 整条不动。

---

### Task 1: 删除 `update.*` 配置项

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/config/config_test.go`
- Modify: `cmd/agentd.go`（删 `cfg.WarnDeprecated(logger)` 及旁边注释）
- Modify: `cmd/init.go`（先只删 `update.auto` / `update.interval` 两问，让现有 init 测试少两行答案后仍能跑；完整向导在 Task 4）
- Modify: `cmd/init_test.go`（删对 `cfg.Update` 的断言；`execAnswers` 去掉两行 update）

- [ ] **Step 1: 把旧测试改成「剥键」契约，确认它们现在会红**

删掉 / 改写 `internal/config/config_test.go` 里这些用例：

- `TestUpdateDefaults`、`TestUpdateExplicit`、`TestUpdateIntervalMustBePositiveWhenAuto`、`TestUpdateIntervalNotCheckedWhenAutoOff`、`TestWarnDeprecatedFiresOnNonDefault`、`TestWarnDeprecatedSilentOnDefault` —— **整段删除**
- `TestUnknownFieldMessageListsUpdate` 改名为 `TestUnknownFieldMessageOmitsUpdate`，断言报错**不含** `update{auto,interval}`
- `TestLoadAcceptsDeprecatedUpdateKeys` 改成：

```go
func TestLoadAcceptsDeprecatedUpdateKeys(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(p, []byte("listen: 127.0.0.1:7777\ntoken: tk\nupdate:\n  auto: false\n  interval: 12h\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(p)
	if err != nil {
		t.Fatalf("含 update 键的旧配置必须能加载: %v", err)
	}
	if cfg.Token != "tk" {
		t.Fatalf("剥键不得伤 token，得到 %q", cfg.Token)
	}
	body, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "update") {
		t.Fatalf("Load 必须回写并丢掉 update 段:\n%s", body)
	}
}

func TestLoadFreshFileHasNoUpdateKey(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	if _, err := config.Load(p); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "update") {
		t.Fatalf("首次生成的配置不得含 update:\n%s", body)
	}
}

func TestLoadStripUpdateDoesNotBlockOnSaveFailure(t *testing.T) {
	// 目录改成只读后再 Load：剥键在内存完成，Save 失败不得让 Load 返回 error
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(p, []byte("listen: 127.0.0.1:7777\ntoken: tk\nupdate:\n  auto: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	if _, err := config.Load(p); err != nil {
		t.Fatalf("回写失败不得阻断启动: %v", err)
	}
}
```

`cmd/init_test.go`：删 `TestInitNonInteractiveWritesDefaults` 里对 `cfg.Update.Auto` 的断言。`execAnswers` 去掉 `update.auto` / `update.interval` 两行（托管追问往前挪两档）。

- [ ] **Step 2: 跑测试确认失败**

```
go test ./internal/config/ -count=1 -run 'TestLoadAcceptsDeprecatedUpdateKeys|TestUnknownFieldMessage|TestUpdate'
```

期望：`TestLoadAcceptsDeprecatedUpdateKeys` 在回写断言处失败（现在会把 update 解出来并写回去）；`TestUpdateDefaults` 若还在会因没了字段编不过。

- [ ] **Step 3: 实现剥键 + 删字段**

`internal/config/config.go`：

- 从 `Config` 删 `Update` 字段；删整个 `UpdateConfig` 类型和 `WarnDeprecated`
- `Load` 初始字面量删 `Update: ...`
- `validate` 删 `update.interval` 那段
- 新增：

```go
// stripDeprecatedTopLevel 删掉已废弃的顶层键，返回剥过的 yaml 和是否剥到了东西。
//
// 为什么在 KnownFields 之前做：v0.1.x 写过 update 段的机器升级后，
// 直接严格解码会拒启动。剥掉再解码，旧文件能起，其它未知键仍硬拒。
func stripDeprecatedTopLevel(b []byte) (out []byte, stripped bool, err error) {
    var root yaml.Node
    if err := yaml.Unmarshal(b, &root); err != nil {
        return nil, false, err
    }
    docs := root.Content
    if root.Kind == yaml.DocumentNode && len(root.Content) == 1 {
        docs = root.Content[0].Content
        if root.Content[0].Kind == yaml.MappingNode {
            stripped = removeMapKey(root.Content[0], "update")
        }
    } else if root.Kind == yaml.MappingNode {
        stripped = removeMapKey(&root, "update")
        docs = nil
    }
    if !stripped {
        return b, false, nil
    }
    out, err = yaml.Marshal(&root)
    return out, true, err
}
```

`removeMapKey` 在 MappingNode 的 Content（key/value 成对）里找到 `update` 并切掉那一对。只剥顶层，不要走进 `targets` / `env`。

`Load` 的已有文件分支：

```go
cleaned, stripped, serr := stripDeprecatedTopLevel(b)
if serr != nil {
    return nil, fmt.Errorf("解析配置 %s: %w", path, serr)
}
if stripped {
    log().Warn("配置 update 段已废弃，已忽略并将从文件删除", "path", path)
    b = cleaned
}
if uerr := decodeStrict(b, cfg); uerr != nil { ... }
// validate 之后、return 之前：
if stripped && !firstRun {
    if werr := save(path, cfg); werr != nil {
        log().Error("删除废弃 update 段后回写失败", "path", path, "cause", werr)
        // 不 return：内存已经干净，启动必须成功
    } else {
        log().Info("已从配置文件删除废弃 update 段", "path", path)
    }
}
```

`decodeStrict` 错误清单去掉 `update{auto,interval}`。

`cmd/agentd.go` 删 `cfg.WarnDeprecated(logger)` 及「字段保留只为了旧配置」注释。

`cmd/init.go` 删 askAll 里 7–8 两问（`update.auto` / interval）。

- [ ] **Step 4: 跑测试确认通过**

```
go test ./internal/config/ ./cmd/ -count=1
```

期望：全绿。`cmd` 里若还有 `cfg.Update` 引用会编不过，一并删掉。

- [ ] **Step 5: 加日志与注释**

- `stripDeprecatedTopLevel` / `removeMapKey` 写清「为什么在 KnownFields 之前」
- Load 剥键 Warn、回写 Info / 失败 Error（失败不阻断）已在 Step 3
- 新函数导出则补参数 / 返回 / 注意；不导出也要有职责说明

- [ ] **Step 6: Commit**

```
git add internal/config/config.go internal/config/config_test.go cmd/agentd.go cmd/init.go cmd/init_test.go
git commit -m "fix(config): 删除 update.*，旧文件剥键后仍能启动"
```

---

### Task 2: 广告地址枚举

**Files:**
- Create: `cmd/advertise.go`
- Create: `cmd/advertise_test.go`

- [ ] **Step 1: 写失败的测试**

```go
package cmd

func TestListAdvertiseAddrsFiltersAndOrders(t *testing.T) {
    old := interfaceAddrs
    interfaceAddrs = func() ([]net.Addr, error) {
        return []net.Addr{
            &net.IPNet{IP: net.ParseIP("127.0.0.1"), Mask: net.CIDRMask(8, 32)},
            &net.IPNet{IP: net.ParseIP("169.254.1.1"), Mask: net.CIDRMask(16, 32)},
            &net.IPNet{IP: net.ParseIP("10.0.0.8"), Mask: net.CIDRMask(24, 32)},
            &net.IPNet{IP: net.ParseIP("100.73.238.21"), Mask: net.CIDRMask(32, 32)},
            &net.IPNet{IP: net.ParseIP("fe80::1"), Mask: net.CIDRMask(64, 128)},
        }, nil
    }
    t.Cleanup(func() { interfaceAddrs = old })

    got := listAdvertiseAddrs()
    if len(got) != 2 || !got[0].Equal(net.ParseIP("100.73.238.21")) || !got[1].Equal(net.ParseIP("10.0.0.8")) {
        t.Fatalf("应先 Tailscale 再其它 IPv4，得到 %v", got)
    }
}

func TestAdvertiseAddrAllInterfacesUsesFirst(t *testing.T) {
    old := interfaceAddrs
    interfaceAddrs = func() ([]net.Addr, error) {
        return []net.Addr{&net.IPNet{IP: net.ParseIP("100.73.1.2"), Mask: net.CIDRMask(32, 32)}}, nil
    }
    t.Cleanup(func() { interfaceAddrs = old })
    if got := advertiseAddr("0.0.0.0:7777"); got != "100.73.1.2:7777" {
        t.Fatalf("got %q", got)
    }
}

func TestAdvertiseAddrLoopbackStaysLocal(t *testing.T) {
    if got := advertiseAddr("127.0.0.1:7777"); got != "127.0.0.1:7777" {
        t.Fatalf("got %q", got)
    }
}

func TestAdvertiseAddrSpecificIPKept(t *testing.T) {
    if got := advertiseAddr("192.168.1.9:7788"); got != "192.168.1.9:7788" {
        t.Fatalf("got %q", got)
    }
}

func TestAdvertiseAddrNoAddrsFallsBack(t *testing.T) {
    old := interfaceAddrs
    interfaceAddrs = func() ([]net.Addr, error) { return nil, nil }
    t.Cleanup(func() { interfaceAddrs = old })
    if got := advertiseAddr("0.0.0.0:7777"); got != "<本机IP>:7777" {
        t.Fatalf("got %q", got)
    }
}
```

- [ ] **Step 2: 跑测试确认失败**

```
go test ./cmd/ -count=1 -run TestAdvertise
```

期望：未定义 `listAdvertiseAddrs` / `advertiseAddr` / `interfaceAddrs`。

- [ ] **Step 3: 实现**

`cmd/advertise.go`：

```go
// 广告地址：配对片段里的 addr。不是 listen。
// 探到的 IP 只出现在这里——绑到某一张网卡会让 127.0.0.1 连不上，
// DHCP / Tailscale 一变 agentd 也起不来。

var interfaceAddrs = net.InterfaceAddrs

func listAdvertiseAddrs() []net.IP { /* 排除 loopback、169.254/16、fe80::/10；只要 IPv4；100.64/10 排前 */ }

func advertiseAddr(listen string) string {
    host, port, err := net.SplitHostPort(listen)
    if err != nil {
        port = "7777"
        host = listen
    }
    switch host {
    case "0.0.0.0", "::", "":
        ips := listAdvertiseAddrs()
        if len(ips) == 0 {
            return "<本机IP>:" + port
        }
        return net.JoinHostPort(ips[0].String(), port)
    default:
        return net.JoinHostPort(host, port)
    }
}
```

`printPairing` 改用 `advertiseAddr(cfg.Listen)`，删掉旧 `pairAddr` 或让它转调。

- [ ] **Step 4: 跑测试确认通过**

```
go test ./cmd/ -count=1 -run 'TestAdvertise|TestInitPrintsPairing'
```

- [ ] **Step 5: 加日志与注释**

- `advertiseAddr` 选中哪条 / 退化到占位符：Info / Warn（`slog`，不要打进 init 的 stdout）
- 文件头：职责 + 「不决定 listen」边界
- 中文注释写清为什么探到的 IP 不进 listen

- [ ] **Step 6: Commit**

```
git add cmd/advertise.go cmd/advertise_test.go cmd/init.go
git commit -m "feat(init): 配对 addr 用探到的可达 IP，不写进 listen"
```

---

### Task 3: `prompter` 接口 + 脚本化实现

**Files:**
- Create: `cmd/prompter.go`
- Create: `cmd/prompter_test.go`
- Modify: `cmd/init.go`（问答改走接口；本 task **先不改问题集合**，只换通道。问题集合在 Task 4）

本 task 的验收：现有 `go test ./cmd/ -run TestInit` 在把 `ask*` 换成 `prompter` 后仍然全绿。TTY 分支暂时仍用脚本化实现（读 `cmd.In`），huh 在 Task 5 再接。

- [ ] **Step 1: 写接口与脚本化实现的单测**

```go
func TestScriptedSelectTakesDefaultOnEmpty(t *testing.T) {
    p := newScriptedPrompter(strings.NewReader("\n"), io.Discard)
    got, err := p.Select("角色", []promptOption{{Value: "executor", Label: "执行机"}, {Value: "coordinator", Label: "协调者"}}, "executor")
    if err != nil || got != "executor" {
        t.Fatalf("got %q err %v", got, err)
    }
}

func TestScriptedSelectMatchesValue(t *testing.T) {
    p := newScriptedPrompter(strings.NewReader("coordinator\n"), io.Discard)
    got, err := p.Select("角色", []promptOption{{Value: "executor", Label: "执行机"}, {Value: "coordinator", Label: "协调者"}}, "executor")
    if err != nil || got != "coordinator" {
        t.Fatalf("got %q err %v", got, err)
    }
}

func TestScriptedEOFTakesDefault(t *testing.T) {
    p := newScriptedPrompter(strings.NewReader(""), io.Discard)
    got, err := p.Input("模型", "x")
    if err != nil || got != "x" {
        t.Fatalf("got %q err %v", got, err)
    }
}
```

- [ ] **Step 2: 跑测试确认失败**

```
go test ./cmd/ -count=1 -run TestScripted
```

- [ ] **Step 3: 实现 `cmd/prompter.go` 并把现有 `ask*` 改成对 prompter 的调用**

```go
type promptOption struct {
    Value string
    Label string
}

type prompter interface {
    Select(title string, options []promptOption, def string) (string, error)
    Input(title, def string) (string, error)
    Confirm(title string, def bool) (bool, error)
}
```

`scriptedPrompter`：空行 / EOF → 默认；`Select` 先按 Value 精确匹配，再按「阿拉伯数字下标」（1-based）匹配——这样 Task 4 之前现有 `answers := "1\n"` 的测试不用改。`Confirm` 认 y/yes/n。

`askAll` 签名加 `p prompter`。`init` 的 TTY 与测试一律先构造 `newScriptedPrompter(cmd.InOrStdin(), out)`。

本 task **不要**改问题内容。

- [ ] **Step 4: 跑测试确认通过**

```
go test ./cmd/ -count=1
```

- [ ] **Step 5: 加注释**

- 文件头：职责（问答通道）+ 边界（不写配置、不探测）
- 为什么 Select 同时认 Value 和 1-based 下标：过渡期保住现有答案脚本，Task 4 改成 Value 后下标仍可作为逃生

- [ ] **Step 6: Commit**

```
git add cmd/prompter.go cmd/prompter_test.go cmd/init.go
git commit -m "refactor(init): 问答抽成 prompter 接口，测试走脚本化实现"
```

---

### Task 4: 新问答流程

**Files:**
- Modify: `cmd/init.go`
- Modify: `cmd/init_test.go`

- [ ] **Step 1: 按新契约改测试（先让旧测试红）**

关键改动：

- 角色 Value：`executor` / `coordinator` / `both`。测试答案用这些词，不再用 `1`/`2`/`3` 当主路径（下标仍可用，但新用例写 Value）。
- `execAnswers`：

```go
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
```

- `TestInitAcceptsAnswers`：角色 `executor`，监听选 `custom` 再输入 `0.0.0.0:7799`，或直接手填路径。按实现：Select 听取值 `loopback`/`all`/`custom`，`custom` 之后再 `Input`。
- `TestInitDoesNotAskServiceForReviewer` 改名为 `TestInitDoesNotAskServiceForCoordinator`，答案 `"coordinator\n"` + 若干空行（sync + 结束配对）。
- 新增 spec §5 的用例：
  - 首次执行机 + 监听回车 → `0.0.0.0:7777`（文件事先不存在；`runInit` 前不要写文件）
  - 文件已在且 listen=`127.0.0.1:7777` + 角色 executor + 监听回车 → 仍 loopback
  - 手填 `0.0.0:7777` round-trip
  - 审批链选空 Value → `approver.executor==""`
  - 输出含「协调者」「默认」、不含「审核者」「缺省」
  - 输出含通用 env 提示、不含 `failed to refresh available models`
  - 输出含「可随时重跑」
  - 注入 `interfaceAddrs` 后配对片段含 `100.73.1.2:7777`

- [ ] **Step 2: 跑测试确认失败**

```
go test ./cmd/ -count=1 -run TestInit
```

- [ ] **Step 3: 重写 `askAll` / `printDetection` / `printPairing`**

严格按 spec §4.2–4.5：

- `stat` 配置文件是否事先存在，**再** `Load`。首次 + 角色含执行机 → 监听预选 `all`。
- 角色 Select 三个选项。
- 执行者 Select 四家，Label 带 `toolchain` 状态。
- 监听 Select：`loopback` / `all` / `custom`；`custom` 再 Input。
- 审批链 Select：第一项 Value `""` Label `不启用（权限直接找人）` + 四家。
- 删 codex 专文；探测表下打通用 env 提示。
- 结尾：「init 可随时重跑，默认取当前配置，一路回车即保持不变。」
- `printPairing` 文案「贴到协调者机」。
- huh 取消（本 task 脚本化不会取消）不写盘——写盘仍在全部问完之后。

`warnIfNotReady` 保留。`maybeInstallService` 文案里的「审核者机」改「协调者机」（完整更名在 Task 6，这里碰到就改）。

- [ ] **Step 4: 跑测试确认通过**

```
go test ./cmd/ -count=1
```

- [ ] **Step 5: 加日志与注释**

- Info：tty=true/false、写盘路径与角色
- 监听预选为什么看「文件事先是否存在」：出厂和「仅本机」都是 `127.0.0.1:7777`，只能靠 stat
- 中文注释：探到的 IP 不进 listen

- [ ] **Step 6: Commit**

```
git add cmd/init.go cmd/init_test.go
git commit -m "feat(init): 选择式向导，监听三档，删除更新问答"
```

---

### Task 5: TTY 接 huh

**Files:**
- Create: `cmd/init_huh.go`
- Modify: `cmd/init.go`（TTY 且非测试时用 huh）
- Modify: `go.mod` / `go.sum`

- [ ] **Step 1: 写「取消不写盘」测试**

给 `prompter` 加测试缝：init 用的构造函数是 `newInteractivePrompter` 包级 var，测试替换成永远返回 `errPromptCanceled` 的假实现。

```go
func TestInitCanceledDoesNotWrite(t *testing.T) {
    p := filepath.Join(t.TempDir(), "config.yaml")
    old := os.WriteFile // 先写一份已有配置
    if err := os.WriteFile(p, []byte("listen: 127.0.0.1:7777\ntoken: keep\n"), 0o600); err != nil {
        t.Fatal(err)
    }
    oldNew := newInteractivePrompter
    newInteractivePrompter = func(in io.Reader, out io.Writer) prompter {
        return cancelPrompter{}
    }
    t.Cleanup(func() { newInteractivePrompter = oldNew })

    _, err := runInit(t, p, true, "")
    if err == nil {
        t.Fatal("取消应返回错误")
    }
    body, _ := os.ReadFile(p)
    if !strings.Contains(string(body), "token: keep") {
        t.Fatalf("取消不得改文件:\n%s", body)
    }
}
```

`runInit` TTY=true 时走 `newInteractivePrompter`。现在它还指向脚本化，接入 huh 后测试仍替换缝，不必起真终端。

- [ ] **Step 2: 跑测试确认失败**（缝还不存在，或取消路径还不返回错）

- [ ] **Step 3: 实现 huh**

```
go get github.com/charmbracelet/huh
```

`cmd/init_huh.go`：`huhPrompter` 三个方法各建一个 huh 控件。`Select` 用 `huh.NewSelect[string]`，Options 的 value 是 `promptOption.Value`，title 是 Label。Ctrl-C / `huh.ErrUserAborted` 译成 `errPromptCanceled`。

`init` TTY 分支：`p := newInteractivePrompter(cmd.InOrStdin(), out)`，生产赋值 `func(...){ return newHuhPrompter() }`。`runInit` 里测试若没替换缝、TTY=true，也走 huh——会在 CI 非 TTY 下挂。所以 **`runInit` 必须继续喂脚本化**，不要在测试里走 huh：

```go
// runInitWith 里 TTY 时用 newScriptedPrompter(strings.NewReader(answers), &buf)
// 生产 cmd 的 RunE 里 TTY 用 newInteractivePrompter
```

把测试和生产的构造彻底分开，取消用例单独替换 `newInteractivePrompter` 并调用一条走生产构造的辅助函数，或直接测 `askAll(..., cancelPrompter{})`。

推荐：`askAll` 在任何 Select/Input/Confirm 返回 `errPromptCanceled` 时立即 return err；`RunE` 见到错不 `Save`。单测直接调 `askAll` + cancelPrompter，不绕整棵 cobra。

- [ ] **Step 4: 跑测试确认通过**

```
go test ./cmd/ ./internal/config/ -count=1
go test ./... -count=1
```

`GOOS=windows go build ./...` 必须过。

- [ ] **Step 5: 加注释**

- 文件头：huh 只服务 TTY；失败不写盘的原因
- huh 错误如何译成取消

- [ ] **Step 6: Commit**

```
git add cmd/init_huh.go cmd/init.go cmd/init_test.go go.mod go.sum
git commit -m "feat(init): TTY 向导改用 huh，取消不写盘"
```

---

### Task 6: 产品可见面更名 + README

**Files:**
- Modify: `README.md`（审核者→协调者；删 `update.auto` 配置段，改成「升级用 handoff upgrade，无定时自动更新、无对应配置项」）
- Modify: `skills/handoff/SKILL.md`（通篇审核者→协调者；标题「以协调者身份」）
- Modify: 当前源码里面向用户的「人工审核者」「审核者机」：`internal/agentd/approver.go`、`internal/agentd/manager.go`、`internal/config/config.go` 注释、`internal/permgate/permgate.go`、`internal/proto/proto.go` 注释、`cmd/permission_mcp.go` 的 `Ask the handoff reviewer` → `coordinator`
- 测试里钉死「审核者」字符串的改掉
- **不要改** `waiting_review`、`approvalsReviewer`、`docs/superpowers/` 里旧 spec/plan/review

- [ ] **Step 1: 先加/改会失败的文案断言**

- MCP 描述若有测试钉 `reviewer`，改钉 `coordinator`
- `TestInit*` 已在 Task 4 要求不含「审核者」

跑一遍 grep 列出活文件（`*.go`、`README.md`、`skills/handoff/SKILL.md`）里剩余的「审核者」，本 task 清掉它们（注释也清）。历史文档不动。

- [ ] **Step 2: 改文案**

逐处替换。日志 `权限升级人工审核者` → `权限升级人工协调者`（这是用户/运维会 grep 的）。

- [ ] **Step 3: 跑测试**

```
go test ./... -count=1
gofmt -l .
go vet ./...
```

- [ ] **Step 4: 加注释**

碰到改名处如需说明「为什么不叫审核者」，一句就够：用户把审核者理解成 code review，协调者才是派发与盯任务的那一端。不要在每个文件重复。

- [ ] **Step 5: Commit**

```
git add README.md skills/handoff/SKILL.md cmd/permission_mcp.go internal/agentd/approver.go internal/agentd/manager.go internal/config/config.go internal/permgate/permgate.go internal/proto/proto.go
git commit -m "docs: 产品可见面审核者更名为协调者，去掉 update 配置说明"
```

---

## Self-Review

| Spec 条款 | Task |
|---|---|
| D1 huh | 5 |
| D2 prompter | 3 |
| D3 监听三档 | 4 |
| D4 广告 IP | 2 |
| D5 更名范围 | 6 |
| D6 删除 update.* | 1 |
| D7 探测不阻断 | 4（沿用 warnIfNotReady） |
| D8 托管追问 | 4（maybeInstallService 不动） |
| 非 TTY | 4（现有路径保留） |
| 可重跑提示 | 4 |
| 通用 env 提示 | 4 |
| 取消不写盘 | 5 |
| 日志 / 注释 | 每 task 都有对应 step |

无 TBD。`prompter` 方法名在 Task 3 定义，后续任务沿用。
