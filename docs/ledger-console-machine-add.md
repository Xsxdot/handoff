# 控制台新增/删除开发机 — 实现 ledger

状态机：task 完成后追加一行，含 commit 范围；修复轮次单独追加。以本文件 + git log 恢复现场。

## 起点

- 分支：feat/console-machine-add
- 基点：8251901b docs(plan): 控制台新增开发机 + 首次配置单页表单两份实现计划
- 环境：Go 1.26、React + TypeScript + Vitest

## Task 1: 配置快照原子化 — DONE

- commit: 4b1cab25
- 审查：spec PASS / quality PASS
- 实现者偏离：Rollback 用例改用「普通文件作父路径组件」构造不可写（config.Save 会 MkdirAll 自建父目录，原「父目录不存在」写法必挂）；既有 _test.go 三处编译修正（&Server{cfg:…} 字面量 → cfg.Store）
- minor（留终审 triage）：
  - machineadmin_test.go: `_ = os.Remove("")` 死代码（源自 plan 原文）
  - machineadmin_test.go 缺文件头职责注释

## Task 2: 增删开发机的领域逻辑 — DONE

- commit: 447c875e
- 审查：spec PASS / quality PASS
- 实现者偏离：newAdminServer 补 StallTimeout: 2*time.Hour（config.validate 要求 StallTimeout>0，TestAddAndRemoveMachine 的 config.Load 重读需要）
- minor（留终审 triage）：machineadmin_test.go:152 失败断言 `%+v` 打印整结构含 Token（仅测试失败输出，非运行期日志）

## Task 3: POST /api/machines（含可达性探测）— DONE

- commit: 4685c1ce
- 审查：spec PASS / quality PASS
- 实现者偏离：newTestAgentdEnvWithCfg 加 config.Save + SetConfigPath（swapConf 需要 cfgPath；审查认定最小且不影响既有用例）
- minor（留终审 triage）：TestAddMachineUnreachableRejected 断言偏弱（只查含 "error" 且 len>=20，没断言「探测 127.0.0.1:1 失败」原文）；postMachine 忽略 Marshal/NewRequest 错误
- 旁证（非本分支引入）：全包 -race 出现 TestPtyWSAttachToExitedSession 既有竞态（Task 2 commit 上复验同样复现，与本 task 无关，不修）

## Task 4: DELETE /api/machines/{name} — DONE

- commit: 757befbe
- 审查：spec PASS / quality PASS（无 FAIL）

## Task 5: 前端 API 层 — DONE

- commit: 3e582ed0
- 审查：spec PASS / quality PASS（无 FAIL；观察：新测试用 spyOn 而既有 helper 用 stubGlobal，风格不统一，non-blocking）

## Task 6: 机器页的新增与删除 — DONE

- commit: 2b1e31c9
- 审查：spec PASS / quality PASS（无 FAIL）
- 实现者偏离：补第 5 个删除流程用例（理由「避免 no-unused-vars」不成立但用例本身正当，覆盖 spec 要求 5）；观察：JSX 注释「编辑已有机器时该字段为空即表示不改」在新增表单语境下表述不准确（minor）

## Task 7: 端到端验收 — DONE

- 全量测试：`go test ./... -count=1` 全部 ok（33 包）
- 前端：`npx vitest run` 49 文件 533 用例全 PASS；`npx tsc --noEmit` 无错
- agentd -race：本分支机器相关用例全 PASS（TestConfSnapshot|TestSwapConf|TestValidateAddMachine|TestAddAndRemoveMachine|TestAddMachine|TestDeleteMachine|TestProbeMachines 等）
- **既有竞态（与本分支无关）**：`go test ./internal/agentd/ -race -count=1` 全包时 TestPtyWSAttachToExitedSession 报 DATA RACE——已在基点 8251901b 上复验同样复现（temp worktree 验证），涉及 pty_ws_test/hub_test/pty_api/hostguard，本分支从未触碰这些文件。不修。
- token 泄漏自检：`grep -rn "Token" internal/agentd/machineadmin.go internal/agentd/machines.go | grep -iE "log\.|Info\(|Warn\(|Error\("` 无输出 ✓

### 真机走查（本机起两个真实 agentd：remote 127.0.0.1:18001 / local 127.0.0.1:18002，走实际 HTTP API）

1. POST /api/machines 加真实可达机器（remotebox=127.0.0.1:18001 + remote-tok-123 + user=me）→ 200，列表出现 remotebox 且 reachable=true ✓
2. config.yaml 落盘含 remotebox（addr/token/user）✓
3. token 不回显：响应体与 agentd 日志均无 remote-tok-123 ✓
4. 重启 local agentd → remotebox 仍在且 reachable=true（重启不丢）✓
5. DELETE /api/machines/remotebox → 200，列表与 config.yaml 均无；再删 → 404「开发机不存在」✓
6. 错误路径：addr=127.0.0.1:1 不 force → 400，error 带「探测 127.0.0.1:1 失败：…connect: connection refused」原文，机器未落库（列表仍 1 台）✓
7. force=true → 200，ghost 落库且 reachable=false、error 带拒连原文 ✓

注：走查通过 HTTP API 驱动（等价前端每次点击的网络路径），未开真实浏览器 UI。前端组件行为由 13 个 MachinesPage 单测覆盖。

### 终审（整分支 diff 8251901b..HEAD）

- 裁决：PASS-with-findings（架构/并发/契约/token 全链路核对通过，7 项低风险修缮）
- 一次性修复 commit 227db63e（5 文件，7 项全修）：
  1. 删 machineadmin_test 死代码 `os.Remove("")`
  2. machineadmin_test 补文件头注释
  3. 失败断言脱敏（%+v 不再打印 Token）
  4. TestAddMachineUnreachableRejected 钉死「探测 127.0.0.1:1 失败」原文
  5. postMachine/deleteMachine 处理 Marshal/NewRequest 错误
  6. client.NoRedirect() 副本（CheckRedirect=ErrUseLastResponse）——新增开发机探测不跟随重定向（计划约束 #8）；未动共享 client，逐字段构造避免 copylocks
  7. 前端令牌注释订正
- 范围复审：FAIL 1 项——修复后注释写「后端不落盘」与事实矛盾（token 随 config.Target 落盘）。协调者直接订正为如实表述并验证前端全绿（78393c71）。评审意见认为表述应指向「配置落盘、不回显、不写日志」。
- 未采纳项（Won't fix）：Task5 测试 spyOn/stubGlobal 风格不统一；校验类 400 也显示「仍然保存」（force 不绕过校验，无害）
- 既有竞态 TestPtyWSAttachToExitedSession（基点即存在，本分支不碰相关文件，不修）

## 完成

全部 7 个 task 完成 + 终审通过。分支终点 HEAD。
