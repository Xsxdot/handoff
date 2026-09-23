# B380 integrate 台账（2026-09-23）

执行者：charter:integrate 节点。依据：integrate 纪律块 + 协调者回答工单 6ed714a7（P1=甲 确认，全量与契约对照就在本分支做）。
分支 `cards/B380-charter-5` @ 起手 HEAD `814ef046`。基线 `cards/B233.1-charter-7`（合并目标，本节点不碰）。
环境：go1.26.1 linux/amd64 · codegraph 在 PATH（`/usr/local/bin/codegraph`）· TMPDIR=`/root/.handoff/tmp/0dfd828d`。
产出：`docs/superpowers/reviews/2026-09-23-b380-integrate-report.md` + 本台账 + `internal/client/delivery.go` 头注顺手修。

## 一、环境与合分支（亲跑）

```
$ git branch --show-current && git rev-parse HEAD
cards/B380-charter-5
814ef046b84781a76756431ba43194d49de3489b

$ git merge-base --is-ancestor cards/B380-charter-4 HEAD && echo yes
yes

$ git log --oneline HEAD..cards/B380-review-1   # 空
$ git log --oneline HEAD..cards/B380-review-2   # 空
# review 分支与 HEAD 同 hash，无未合提交

$ codegraph --repo . views   # 无 cards-B380-*
（views 12 个，无 B380）
```

合分支结论：P1=甲 无子系统扇出，集成分支即工作分支；并入合并目标留协调者。

## 二、全量核算（亲跑原始读数）

```
$ go build ./...
BUILD_EXIT=0

$ go test ./... -count=1 > "$TMPDIR/b380_full_go_test.txt"
FULL_TEST_EXIT=1
ok_pkgs 63
notest_pkgs 6
fail_pkgs ['FAIL\tgithub.com/Xsxdot/handoff/internal/client\t14.870s']
fail_tests ['--- FAIL: TestProductionHTTPClientCallersAreGatewayOnly (0.02s)']
失败原文：
  execution_test.go:173: 生产代码不得自取 HTTPClient 拼请求（只允许复用共享传输的转发路径：
  agentd/forward*.go 服务端 + mobilecore/{core,proxy}.go 客户端侧）: internal/agentd/drop.go
```

归因：contract §177 已知既有红（B272 遗留 drop.go），基线即红、非本卡引入、不修。红窗恰 1 支无漂移。

三包子集（单包避争抢，协调者指示）：

```
$ go test ./internal/ledger/ -run TestOpenTicketsTerminal -count=1
ok  	github.com/Xsxdot/handoff/internal/ledger	0.557s

$ go test ./internal/orchestration/ -run TestB380 -count=1
ok  	github.com/Xsxdot/handoff/internal/orchestration	0.255s

$ go test ./internal/approval/ -run TestB380 -count=1
ok  	github.com/Xsxdot/handoff/internal/approval	0.132s
```

web（环境性失败，本卡零触 web）：

```
$ cd web && npx vitest run
Startup Error: Cannot find package '@tailwindcss/vite' imported from …/vite.config.ts
VITEST_EXIT=1
（web/node_modules 不存在）

$ cd web && npm run typecheck
sh: 1: tsc: not found
TYPECHECK_EXIT=127

$ git diff --name-only e5e428a5..HEAD -- web/ | wc -l
0
```

→ 未验证（缺依赖），非测试红；披露报告 §2.2。

## 三、契约对照（亲跑）

```
$ codegraph --repo . check
CHECK_EXIT=0   fails=0  warns=41
bestCoverage: assignedContainers 338 = viewContainers 338  crossDomainEdges 1254

$ codegraph --repo . validate
VALIDATE_EXIT=1
issues 恰 2 条：
  [cards-B272-charter] 新增容器 k_dropdir_fn 引用不存在的基线领域 d_coordination_api
  [cards-B374-charter] 新增容器 k_collab_model 已存在于基线，containersAdded 只接受新容器
（与 contract §6 逐字一致；他分支存量；views 无 B380）
```

B380 无分支视图（合法：`git diff --stat e5e428a5..HEAD` 仅改函数体与文档，无新符号）。

contract §1 逐条亲读（C-1a/b/c、C-2、C-3 十项）——全部在位，明细见报告 §3.2。契约错配暴露数 **0**。

resolve：

```
$ codegraph --repo . resolve --doc docs/superpowers/specs/b380-contract.md → exit 0
$ codegraph --repo . resolve --doc docs/superpowers/specs/b380-breakdown.md → exit 0
```

图覆盖债（sym 亲跑，未命中后回落 grep，不以 grep 顶替查图）：

```
$ codegraph sym OpenTickets           → Error: 不在图中；近似候选 []
$ codegraph sym EventTypeTicketAnswered → Error: 不在图中
$ codegraph sym encodeCardWaitSnapshot → Error: 不在图中
$ codegraph sym AnswerTicket          → 命中 n_store_Store_AnswerTicket（非 orchestration 方法）
$ codegraph sym Manager.approvePermission / Client.consult / Hub.Publish / WaitDeliveryPolicy → 命中
```

棘轮：无提请（无接缝直调下降；target/baseline/best 零改动）。

## 四、生产闭环对账（grep/sed 亲读）

报告 §4 七行五格逐行核。关键原始 grep：

```
三写入点：facade.go:127 / manager.go:2577 / approval/client.go:430（无第四处）
C-2：taskstate.go:188 case evTicketsVoided, "completed", "failed", "archived"
消费方：cmd/card_wait.go:357 OpenTickets；ledgerapi.go:198 OpenTicketCounts
WaitDeliveryPolicy：delivery.go:29 ticket_answered → false
mirrorSkip：仅 Progress/ApproverDecision/ApproverDisabled（不含 ticket_answered）
生产装配：approval_client.go:37 Hub: m.hub
镜像链：client.go:1669「策略在应用谓词，传输必须仍见到该帧」
handlers.go:1451 先订阅后补发
VoidTicketsWithAudit：manager.go:3491/3616、contracts.go:242
tickets_voided：ticketvoid.go:9 不 Publish + grep 无 Publish 调用
S1 断言：bash $TMPDIR/b380_s1_assert_integrate.sh → B380_S1_ASSERT_OK exit 0
```

## 五、接缝缺陷与就地修

缺陷 #1（本轮新发现）：`internal/client/delivery.go:17` 头注仍写「审计类在服务端只入库不 Publish，实时流本就见不到」——对 B380 后 `ticket_answered` 失真（contract C-3 只改了 proto.go 注记，漏了同族头注）。

就地修（一行注释，行为零变）：

```
$ go build ./internal/client/ → 0
$ go test ./internal/client/ -run 'TestWait|TestFollow|TestDelivery' -count=1
ok  	github.com/Xsxdot/handoff/internal/client	8.856s
$ gofmt -l internal/client/delivery.go → 空
$ git diff internal/client/delivery.go   # 仅 3 行注释替换
```

缺陷 #2-#5：见报告 §五（client 基线红 / web 依赖 / README.zh-CN 残余 / spec 落点）——披露或有归属，不另修。

## 六、回旋镖

见报告 §六。要点：已知红 2 条预测全命中无漂移；S1 断言命中；README.zh-CN 残余命中；web 环境为预测未覆盖项（环境披露）；真机 6 条零执行符合预期。

## 七、交棒（孤儿标记）

**下一步：acceptance**。真机 6 条全部未执行（报告 §七逐条「未执行」）；web vitest/typecheck 未验证。并入主线/absorb/归档留协调者。

## 八、自检

1. 有没有把没亲自跑到结果的命令写成结论？**无**——build/全量/三包/check/validate/resolve/sym/断言脚本/grep 闭环均亲跑贴原文；web 与真机标「未验证/未执行」。
2. 这一轮碰过 handoff CLI 或起过新 executor？**否**——只用 git/go/codegraph/grep/文件工具。

## 提交事实（历史读数）

```
$ git add internal/client/delivery.go \
    docs/superpowers/ledgers/2026-09-23-b380-integrate-ledger.md \
    docs/superpowers/reviews/2026-09-23-b380-integrate-report.md
$ git commit -m "integrate(B380): 集成收口——全量/契约对照/生产闭环对账 + delivery 头注顺手修 + 报告台账"
```

最终 hash 以交付报文为准（台账不 chase hash；收口判据 = 受控三文件入库、工作树干净、父提交仍为原 implement `814ef046` 等价内容）。

> 过程注记：首收口时并行提交竞态曾把本台账误 amend 进 implement 提交、且 integrate 提交漏台账——已 reset 回原 implement 底重做单提交，本段为修复后终稿。
