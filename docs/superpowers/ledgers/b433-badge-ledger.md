# B433 角标回归修复证据（2026-10-11）

## 身份与根因

- 修复树：`/Users/xushixin/.codex/worktrees/b432-current-ios/handoff`，基线 main `18c5c5fd`；
  复用已附加且干净的 B432 工作树，main 不混入未验修改。
- 用户反馈五个裁决均答复，手机 build 43203 的图标仍为 5；新鲜 `handoff room inbox` count=0。
- 原 fanout 只分类新增项，忽略 `decision_answered`/已处理工单；原 APNs Send 恒附 alert/sound。
- B432 终态不能直接退回 implement（官方 CLI 明确拒绝）；根因与验收遗漏已落 B432 comment，
  新卡 B433 绑定本会话，原卡未绕门/改库。修复涉及当前本机 APNs 真机验证，留本地执行。

## 红色反馈与调整

- `/private/tmp/b432-clear-badge-red.log`：旧实现 `TestAnsweredDecisionsRefreshBadgeToZero`
  第一条答复后等待投递超时；`TestAPNsBadgeOnlyClearsWithoutAlert` 实际 HTTP 载荷带
  `alert:{title:""}` 与 `sound:"default"`，断言失败；测试正常编译，exit 1。
- `/private/tmp/b433-other-sources-red.log`：已答复/作废工单钩子均无角标投递，测试失败。
- 提及初始夹具误由被提及用户本人发，Send 已立即消费提及；改为真实群主发后发现
  MarkRead 仅更新 unread 游标，并不消费收件箱提及。未改变该既有契约，撤去 read handler 改动，
  改测用户回复的真实消费路径。
- `/private/tmp/b433-consumed-mention-red.log`：真实回复后 inbox count=0，旧钩子没有
  badge-only 投递，等待超时，exit 1。这是修正后的行为红窗，夹具红不作功能红证据。

## 实现与局部验证

- `OnLedgerEvent` 处理裁决答复、needs_cleared、控制台用户回复事件；`OnTaskEvent`
  处理 ticket_answered/tickets_voided。既有分类仍只负责新增待办，未知事件不默认弹通知。
- 新内部 badge_update 模式复用同一 fanout 队列/设备身份/计数器；在 deliver 时取最新
  collectInboxItems 数量。APNs 完全省略 alert/sound，badge=0 不省略，新增待办原载荷不变。
- `/private/tmp/b433-badge-green.log`：`go test ./internal/agentd -run
  'Push|APNs|APNS|HandledTicket|ReplyConsumesMention|AnsweredDecisions' -count=1` exit 0。
  五条真实裁决逐条答复验证 5→4→3→2→1→0，工单钩子与回复消费提及均通过。
- 全量 `go build ./...`、`go vet ./internal/agentd`、`git diff --check` exit 0。
- `/private/tmp/b433-badge-mutation.log`：唯一 `decision_answered` 判断加 false，测试正常
  编译，五裁决测试第一条答复超时，exit 1；finally 已恢复原字节。变异确实有牙。
- codegraph 未覆盖 PushFanout，沿 B432 已落 roadmap 的图覆盖债，不伪造视图。

## 待完成

全量集成、race 与静态检查正在执行；尚未部署/真机验证，不宣告处理后角标已修好。
公共 App 的 APNs 私钥应由官方服务持有；本卡不建设公共推送网关或分发 `.p8`。

## 集成与部署结果

- 修复提交 `5fc63bf8`；`go test ./... -count=1` exit 0，65 包通过（`/private/tmp/b433-full-tests.log`）。
- Push/APNs/处理事件的 race 测试 exit 0（`/private/tmp/b433-push-race.log`）；`go vet ./...`
  exit 0；全部 tracked Go `gofmt -l` 为空；diff check 通过。
- `OUT=/private/tmp/b433-handoff ./scripts/build-deploy.sh` exit 0，产物内嵌 Web；version
  验证 revision=`5fc63bf84d0f5a9cf72ca8efd2a0accbcc7a6d61`、modified=false。
- SuperDev `list_services` 返回清单无本机 handoff agentd 的受管 deployment；经现有官方
  `handoff service restart` 重载 launchd，PID 84583→36243，17:55:48 agentd 启动。
  `handoff status --json` 新鲜 reachable=true、degraded=false、agentd revision=5fc63bf8。
  旧 CLI 二进制备份 `/private/tmp/b433-handoff-before-reload`；配置、密钥与手机 App 未改。
- 尚待真实 iPhone 对处理后归零的反馈；main 仍为 18c5c5fd，修复分支未合并/推送。
- 17:56:45 经官方 `handoff decision open` 创建测试裁决 #22，挂 B433，正文「B433 角标归零复验」，
  选项「已处理」。测试前新鲜 inbox=0；已请用户在 App 答复后返回桌面确认无角标、无多余横幅/声音。
  未代答该裁决，未将等待反馈记成真机通过。
