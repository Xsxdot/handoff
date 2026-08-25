# B248：`/bin/bash -lc` 绕过黑名单硬拦

**级别：L1（快道）** —— plan 增量为零（末尾「实现决定」三行即全部实现决定），验收一眼可核（一个表驱动测试）。
**状态：已批准**（2026-08-25，用户在 spec 对话中批准）

---

## 问题陈述

`internal/permgate` 的内置黑名单 8 条硬规则，对当前主力执行器 **codex 一条都拦不住**。

codex 上报的权限命令原文形如 `/bin/bash -lc '<真命令>'`（`internal/executor/codex/perm.go:86` 原样透传 `a.Command`）。linux-01 三天 862 条 codex 权限原文里 **858 条是这个形状**，codex 占同期 269 个任务里的 171 个（63%）。

真代码验证（在 `internal/permgate` 包内调 `Gate.judgeCommand` 真身，非复刻正则）：

```
/bin/bash -lc 'rm -rf /root/x'                wrapper=false  => consult
/bin/bash -c  'rm -rf /root/x'                wrapper=true   => escalate
rm -rf /root/x                                wrapper=false  => escalate
/bin/bash -lc 'git reset --hard abc123'                      => consult
/bin/bash -lc 'sudo systemctl restart nginx'                 => consult
/bin/bash -lc 'git push --force origin main'                 => consult
```

生产证据：`agentd.log` 的 `权限判定：交审批者` 理由分布为「黑名单未命中 1030」+「**仅引号内字面量命中黑名单，降级交审批者裁决 8**」，后者 8 条全部出自 codex 任务，内容正是 `rm -rf` 与 `git reset --hard`。同期 opencode 的 7 条同类 `rm -rf`（无包装器）全部被硬拦升级人工。

## 根因

三重巧合，缺一不成立（判定链见 `internal/permgate/blacklist.go:133` 的 `judgeCommand`）：

1. `StripQuoted` 把单引号内的整条真命令清空 → 剥离后是 `/bin/bash -lc ''`，`g.match(StripQuoted(s))` 不命中，走不到 `blacklist.go:148` 的「剥离后仍命中 → Escalate」；
2. `execWrapperRx`（`blacklist.go:71`）两段都要求 `\s-c\b` / `\s-[ceE]\b`，而 **`-lc` 是组合短选项**，`-` 后紧跟的是 `l` 不是 `c` → `HasExecWrapper` 为 false → 跳过 `blacklist.go:150-159` 的整个硬拦分支；
3. 于是落到 `blacklist.go:160` 的 `if hit` → **Consult**。

后果：codex 侧的全部安全判断单点压在廉价模型上。而实测该模型对一字不差的同一条命令有约 **12% 的判定翻转率**（`commit --amend` 家族 escalate 49 / approve 7），这个单点并不可靠。

`-lc` 在 `internal/permgate` 全部测试里**零覆盖**。

## 方案

**选定：放宽 `execWrapperRx` 第一段的选项匹配，使其识别组合短选项。**

第一段是「解释器直调」段，已由 `\b(sh|bash|zsh|dash|ksh|env)\b` 限定前缀，在这段里放宽不会波及无关命令。

**弃选一：同时放宽第三段（通用执行标志段）。** 第三段没有 shell 名前缀保护，放宽成 `-[a-z]*[ceE]` 会让 `gcc -static`、`find . -exec` 这类命中（`static`/`exec` 都以 `c` 结尾）。虽然 `blacklist.go:64` 的注释指出包装器只在「黑名单已命中」时才参与升级、有一层安全垫，但本卡是安全修复，不应顺手扩大误伤面。codex 的形态由第一段完全覆盖，第三段维持原样。

**弃选二：改由 codex adapter 在上报前剥掉 `/bin/bash -lc` 外壳。** 这把判据的正确性押在「每个 adapter 都记得归一化」上，四个 adapter 各一份，正是 `internal/permgate` 包注释开头声明要避免的漂移（「判据分散到 adapter 里，就会重演各家一套」）。且它治标——换一个包装器形态又漏。

## 实现决定

1. **改哪个文件**：`internal/permgate/blacklist.go` 的 `execWrapperRx`，第一段 `\s-c\b` 放宽为 `\s-[a-z]*c[a-z]*\b`（认 `-lc` / `-cl` / `-lic` 等任意含 `c` 的组合短选项）；第二、三段一字不动。同步更新该变量上方的注释，写明组合短选项这一形态与它的来源。
2. **补测试**：`internal/permgate/blacklist_test.go` 增表驱动用例，覆盖 `-lc` / `-cl` / `-lic` / `-c` 四种形态 × `rm -rf` / `git reset --hard` / `sudo` / `git push --force` 四条规则，断言一律 `Escalate`；并保留一条 `gcc -static` / `git log -c` 的反向用例，断言不因本次放宽被误升级。
3. **怎么验收**：`go test ./internal/permgate/ -count=1` 全绿；且改动前后各跑一次，改动前新用例必须**红**（证明它确实罩住了这个缺陷，不是装饰性断言）。

## 测试决定（接缝清单）

一条缝，落在 `HasExecWrapper`（`internal/permgate/blacklist.go`）——存量导出符号，调用方 `judgeCommand` 同文件可 grep 核验。表驱动用例穿过这条缝，同时经 `Gate.judgeCommand` 断言最终裁决，不只断言正则本身。

## Out of Scope

- **不动黑名单的 8 条规则本身**（增删规则是 B249 的事，且方向相反——本卡收紧、B249 放宽）。
- **不动第三段通用执行标志匹配**（见弃选一）。
- **不改 codex adapter 的上报形态**（见弃选二）。
- **不碰 `selfcmd.go` 的自指令白名单**（实测有 3 次把只读的 `handoff graph resolve` 误升级，归 B249）。

## 备注：与 B249 的落地顺序

B249（降噪，放宽判据）与本卡（收紧判据）改同一个包。**本卡必须先落地或与 B249 同轮落地**——先放宽后收紧的中间态是净减安全。
