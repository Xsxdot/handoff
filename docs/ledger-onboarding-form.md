# B113 台账：首次配置改单页表单

范围：6 个 task + 整分支终审。分支 feat/onboarding-single-page-form。
恢复现场以本 ledger + git log 为准。

## 进度

- 2026-08-17 Task 1（先录 CLI 提问金样）完成，commit 3945cda8。审查双 APPROVED。三份金样与 AskAll 现状逐行核对一致：coordinator 无执行机提问（角色→sync.auto→配对循环）、executor 无 sync.auto（角色→默认执行者→模型→监听→repo_root→审批链）、both 执行机段后接协调者段；监听预选 def=all（首次执行机 + cfgExisted=false 翻档）正确；`out|` 与 `select|`/`input|`/`confirm|` 交错顺序真实。全量 `go test ./...` 全绿、gofmt 空。未发现需要修复的问题。

- 2026-08-17 Task 2（字段描述表）完成，commit c0536f0f。审查双 APPROVED，历经 1 轮修复：初版 Advanced 标记与设计 spec §5.2 不符（我给的指令把 listen 标成 Advanced、漏标 executor_default/repo_root，与「顶部常显=角色+监听地址」矛盾），已修复为 spec 权威定义（role/listen_preset/listen 常显；executor_default/executor_model/repo_root/approver_executor/approver_model/sync_auto 进高级设置）。实现者偏离（Apply 的 `ans, ok := answers[f.Key]; if !ok { continue }` 前置）经裁决合理：键缺失=前端没提交该字段，不校验不写回，保住逐字测试且不引入越界值落盘风险。Option 已加 json tag。标题与选项标签逐字核对全过、金样未受影响、全量测试绿。Minor 记账 1 条：M1 设计 spec §4.3/§5.2 的 Advanced 集合与我初版指令冲突，我按 spec 裁定修正（非实现者过错）。

- 2026-08-17 Task 3（AskAll 改为按表消费）完成，commit 86f467d7。审查双 APPROVED，但先经一次**计划缺陷上报与裁决**：Task 2 字段表把监听两字段放到了执行者之前，按 Task 3「提问顺序即 Form 切片顺序」执行会让 CLI 提问顺序从「角色→默认执行者→模型→监听→…」变成「角色→监听→默认执行者→模型→…」，并导致 cmd 两个按位置喂答案的测试（TestInitAcceptsAnswers、TestInitListenSelectLoopback）失败——与全局约束「CLI 顺序逐字不变」矛盾。上报协调者，裁决按 **A 方案**：字段表顺序改为与改造前 AskAll 逐字一致（role→executor_default→executor_model→listen_preset→listen→repo_root→approver_executor→approver_model→sync_auto）；金样不重录、只删远程配对两行（coordinator/both 各删 2 行，executor 未动）；cmd 两测试不动必须照常过；桌面版面按 advanced 分组不受影响。实现者当时把金样误重录（含监听挪位），我 git checkout 还原后手工改顺序+只删配对两行，金样测试全过、用例名集合 diff 为空、全量绿。Minor 记账 1 条：M2 旧 askListen 的 `slog.Debug("init 监听预选")` 随函数删除消失（属预期删除）。

- 2026-08-17 Task 4（桌面 Go 侧一次性交表）完成，commit b19a5e5c。审查双 APPROVED，历经 1 轮修复：初版把 `waitAnswers` 收到的 nil（畸形 wizard-submit 载荷）直接交给 `shell.ApplyAnswers`——Apply 对 nil map 全 continue 返回 nil，随后 config.Save 静默落盘默认配置并宣告成功，违背「结构不符按取消处理不落盘」；修复为 startWizard 收答案分支判 nil 即 return（按取消），waitAnswers 的 `ch <- nil` 补 select/default 防阻塞。实现者偏离均合理：waitAnswers 加 app 参数（app 是 main 局部变量）、保留 releaseEmbedded 的 wizard-notice（DecisionNotifyOutdated 独立产出）、测试用 shell. 前缀（shell_test 包）。shell 包不 import Wails 核对通过、删除项（eventPrompter/Question/Transport/NewEventPrompter/noticeWriter/NewNoticeWriter/wizAnswers/wailsTransport/wizard-answer 注册）无残留、5 个关键节点日志齐全、成功路径不静默。Minor 记账 2 条：M3 前端仍在听 wizard-notice 但新协议只有 wizard-form/submit/error，releaseEmbedded 的「检测到旧版本」提示会在 Task 5 重写前端后失去接收者而静默丢弃（留给终审 triage）；M4 waitAnswers 的 `fmt.Sprint(v)` 把非字符串值也硬转字符串（前端只发字符串值，够用）。

- 2026-08-17 Task 5（前端单页表单）完成，commit 89475f45。审查双 APPROVED。wizard.ts 重写为单页表单：类型与 Go 蛇形键名逐字对齐、match/defaultOf/visible/roleMatches 与 Go 侧 form.go 逐行语义一致、advanced 分组进默认折叠的「高级设置」、touched 保护监听预设翻档（切角色→未手动改过的 listen_preset 自动翻 all）、提交 payload 带全部可见字段（折叠区没碰过的带 defaultOf）、wizard-error 不清空已填、wizard-done 换文案。实现者决策均合理：文本输入不触发重绘（避免每敲一字丢焦点）、监听翻档靠通用 touched 规则无字段名特判、form+submit 按钮+submitted 哨兵封死双发。style.css 最小追加。仓库 TS4.9 解析不了 tsconfig 的 TS5 选项是既有问题（W5b-1 遗留），用临时 TS5 跑 tsc --noEmit 零错误。构建成功、git 只含两个源文件。Minor 记账 3 条：M5 redraw 重建 `<details>` 不带 open，展开高级设置后改任一控件会重新折叠（spec 只要求默认折叠，可接受）；M6 Notice 渲染在控件上方（spec 写「字段下方说明」，两种读法皆通，Windows 角色警告放选择前更醒目）；M7 旧 wizard-notice 事件在 Go 侧 main.go:250 仍 emit（releaseEmbedded 的旧版本提示），前端已不再监听，静默丢弃——与 M3 同源，终审 triage。

## 真机走查（Task 6）

- 2026-08-17 本执行环境无 GUI 会话（无窗口服务器/辅助功能授权），**Step 3 真机双击走查与 Step 4 控制台配对回归无法在本环境完成**，记为**未决**，需在协调者有授权的机器上人工走查。按 W5b-2 先例（"不要留成已完成"）。
  - 待走查项：①首次配置页四家 executor 探测与登录态正确；②默认值已填、不改直接点完成走通；③切角色时区块显隐正确、无 executor 的机器上协调者改执行机时监听预设自动翻「所有网卡」（手动改过则不翻）；④「高级设置」折叠状态下提交后配置里那些字段仍是默认值；⑤完成后进入控制台；⑥页面无远程配对段。
  - 另注意：完成向导会走 EnsureRunning，launchd label 固定值（B71）会与运行中的 agentd 抢 label/DataDir，**不在执行机上重跑完成路径**。
- Step 2 判据复验（SIGKILL 不留痕）已在执行机完成：`wails3 task build` 出新薄壳 + 根模块 `go build` 新 CLI 到临时目录；`PATH=$FIX:$PATH HOME=$TH ./bin/handoff-desktop` → 8s 后 `kill -9`：日志 `existing=/private/.../tmp.iAMkOEg8zj/handoff`（确认解析到新构建而非已安装 CLI）、`$TH/.handoff` **整个不存在**、进程已退出。**判据通过**。未覆盖 `~/.local/bin/handoff`（未带 embedbin tag 的构建 releaseEmbedded 走 use-existing 分支，安全）。
- Step 1 全量测试：根模块 `go test ./...` 与 `cd desktop && go test ./...` 全绿，`gofmt -l .` 两模块空输出。