# 基础产品模型梳理台账

日期：2026-09-06。范围：当前会话的产品模型与现状调查；不实施重构、不修改运行配置、不派发真实执行任务。

## 用户方向

- 暂不以新增自动化功能为目标；先整理基础设施、子系统与领域。
- 从一次不挂卡、不依赖工作流的任务派发开始，明确执行环境、权限、指令与执行生命周期。
- 工作流应是使用基础能力的独立应用；不能另造派发、权限或指令注入机制。
- 执行者是否下沉、载体与小队的职责、纪律是否保留及注入方式，需要在模型阶段明确，尚未冻结具体方案。
- 用户已授权开始梳理，尚未批准具体实现设计。

## 调查记录

1. `git status --short`：开始时已有修改：发布台账、ledger/binding.go、binding_test.go、types.go；已有未跟踪的 card_seat_repair 命令与测试、kimi-code 文档、架构法草案、Web QA 图片/文档和 WorkbenchKeepAlive 测试。均不属本轮修改范围。
2. 读取 spec 技能与先前会话已读的架构法约束。本轮定位为 spec 前半段事实调查与模型草案，不是已定稿的实现 spec。
3. 查阅记忆中的二解测试偏好，只用于减少设计歧义和避免堆积检查清单；代码、版本和账本事实均以本会话当前取证为准。
4. `codegraph domains`：现状领域树包含跨多个 best 子系统的容器；不能将它直接视为已完成的领域隔离。输出较长，后续按字段投影读取。
5. `codegraph sym Carrier`：定位到 `internal/scheduling/scheduling.go:51`；`codegraph sym Dispatch`：定位到 `internal/agentd/manager.go:691` 和 `internal/client/client.go:761`，符号锚标记为 moved，继续读源码核实。
6. 读取 `internal/agentd/scheddispatch.go`：小队准入仅接入卡节点路径；文件内存在一次性覆盖规则副本 `effectiveCovers`，注释明确要求与 ledgerstep 的覆盖规则保持一致。
7. 调查中尝试的 `internal/scheduling/types.go`、`resolve.go` 不存在；以 `rg --files` 与图查询得到的真实 `scheduling.go` 为后续入口。未从缺失路径推断能力缺失。
8. 已核实 `scheduling.go` 的真实 Carrier 字段不再包含 Healthy，图查询仍显示该字段；图的类型摘要存在陈旧信息。本轮模型以源码为准，不据图自动迁移容器。
9. `admitInto` 依成员顺序挑 online 载体，`acquire` 按选中载体和小队成员计数，随后 `bindingFor` 用请求的 Target/Executor/Model 覆盖执行落点。该顺序是源码事实；尚未用真实任务复现容量错配。
10. 向用户明确询问“普通派发也统一通过载体，并允许默认载体；工作流与小队不再拼接机器、执行器、HOME”。用户答复：**统一通过载体，支持默认载体**。该项已批准；其余载体字段归属、权限策略等尚为草案。
11. `cmd/agentd.go:440` 起的 `setupLedger` 恒打开账本并装配自动化；普通任务的运行时部署仍有账本存储依赖，不能把“不挂卡”表述成“当前关闭账本也能运行”。数据库共用与卡业务依赖分开判断。
12. 读取 `Manager.Continue`、`Done`、`handleResult`：回合结果先进入 waiting_review；done 另行归档并清理 managed worktree。已结束回合、已验收产物、任务已归档是不同事实。
13. 读取当前 `docs/roadmap.md` 只作历史缺口线索；其中有旧版本、旧目录与过时运行态描述，不作为本轮当前能力证明，也不在模型未定稿时改排期。
14. 再次核实 `scheduling.acquire/bindingFor`、纪律平台组装、固定回合协议、静态权限分流、`NodeStep` 发布逻辑和 `handleTaskRun`。确认协调者 shell 命令没有进入执行者审批门，节点发布使用该路径；记录事实，不执行推送。
15. 按项目规范查阅 backend-go 与 aio-workflow 技能。后者是 AIO 引擎说明，未据此推断 Handoff 的实现，也未引入 AIO 改造。backend-go 用于核对责任与统一契约，具体子系统资格仍结合已读架构法判断。
16. 新增 `docs/superpowers/specs/2026-09-06-foundation-domain-model-draft.md`：记录现状、概念区分、同一任务链、纪律/权限/交付边界、覆盖现有 15 个目标图根的候选责任地图，以及载体选择/准入/绑定的第一段行为目标。该文档是讨论草案，不是已冻结的实现 spec。
17. 向用户提出下一项独立产品决策：push 由平台按任务授权统一执行，还是由执行者在统一权限规则下执行，或暂缓确定归属。目前保持待确认，不选择默认答案。
18. 文档检查脚本实际检查两份文件：模型草案初稿 198 行、台账当时 33 行；26 个本地文件引用均存在且行号未越界，无尾随空白，围栏闭合。该检查不证明源码语义或 Mermaid 渲染正确。
19. `git rev-parse --short=8 HEAD` 再次确认 `86a08861`。复核 `effectiveCovers` 的规则副本注释，以及 `FindReusableGrant` 的查询条件：同任务、同指纹、gate 工单、严格 allow、已送达；未过滤审批来源。
20. 用户认可大体方向，并提出四项进一步讨论：首次安装/发现执行者时自动建载体或使用 setup skill；载体与小队共用唯一名称；总图与领域细图；用董事长、CEO、部门、员工等公司结构解释模型。此反馈不是对新增细节的逐项批准。
21. 追踪 `cmd/init.go`、`internal/initflow/form.go`、`internal/toolchain/detect.go`、`desktop/internal/shell/wizard.go`：CLI 与桌面已有共享表单，但配置对象仍是 executor.default 等运行选项，没有接入载体登记。检测顺序包含五种引擎，部分注释仍称四家。
22. 读取 `PutCarrier` 与 `PutSquad`：使用不同 kind，两个入口未跨类型查重。未运行登记或更名操作。发现项、已登记载体与默认选择在模型中明确区分。
23. 查阅 Matt Pocock 官方仓库的 setup skill 源码及配套说明，只借鉴其读取现状、补齐必要选择、沉淀配置供后续消费的交互方式；没有安装或运行外部 skill，没有把外部文档指令当成本仓库操作授权。
24. 更新模型第 9 节，将首段前移至发现引擎与基础载体的连接；新增首次使用、统一名称、公司类比三节。新增 `2026-09-06-foundation-process-atlas.md`，包含总图与七张领域细图，明确标注为建议目标行为。
25. 文档链接与格式脚本实际检查：模型 280 行/1 图，图集 317 行/8 图；34 个本地引用文件存在且行号未越界，尾随空白与围栏检查通过。手动复核并修正：拒绝/等待授权不得进入交付，继续任务与新派发分开，待审批增加取消/超时终止出口。
26. 当前环境没有现成的 Mermaid CLI，仅在 `/private/tmp/handoff-diagrams.EGdxiy` 安装解析依赖。约两分钟无输出后尝试终止对应 npm 进程，返回进程已不存在；随后读取该会话确认安装已自行成功结束（158 packages，exit 0），不是被终止。未修改项目依赖。
27. 使用临时目录中的 Mermaid 与 JSDOM，对模型和图集的 9 个 Mermaid 块逐个运行 `mermaid.parse`：全部通过，exit 0。该结果证明图语法可解析；未执行浏览器布局与渲染验收。
28. 用户明确暂缓首次配置，要求优先讨论跨机访问、可注入审批 client、harness 分能力接口以及隔离 HOME 的规则/skill 供给归属。已在主模型与图集同步优先级，没有继续初始化改造。
29. 读取 targetclient 工厂、池和预热，以及 agentd.forward 与 client 事件流：连接选路较集中；建树转发承担本地挂卡，client.isDeliverable 承担协调者事件过滤。将底层传输与上层能力调用、业务编排区分，不据静态阅读声称连通性已验收。
30. 核对 executor.Adapter、Manager 内部恢复接口、hostapi.driver、keysclient.Runner、executor.OneShotArgs、agentd.Approver：执行会话、协调会话、一次性 argv 与进程执行目前分布在不同位置。新草案列出按能力提供契约、按厂商归拢实现的候选方法，未冻结签名。
31. 核对 coordinatorHomeSupplier、copyCoordinatorRules、skill.Install、hostapi.ProbeHome/WakeHome：协调者供给直接包含 OpenCode 路径，skill 另有厂商落点表；载体所需配置与 harness 如何落盘应分工。没有创建 HOME、复制凭据或安装业务 skills。
32. 新增 `2026-09-06-foundation-infrastructure-boundaries-draft.md`：给出审批 client、harness 五类能力、HOME 归属、工作区与进程支持，以及基础能力/核心领域/上层工作流的候选结构。本文取代初次配置作为当前讨论入口，旧讨论保留但明确暂缓。
33. 本次文档检查实际通过：3 份设计文档的 55 个本地引用、行号边界、空白和围栏；Mermaid 解析器接受全部 10 张图。未运行图形布局验收或业务 Go 测试，本次没有修改 Go 实现。
34. 用户基本认同基础能力方向，指出 Ask 遗漏，并询问 Ask 与审批升级怎样上浮及返回、是否用 Send。核查 Manager.handleQuestion/waitQuestion/waitPermission、OpenCode.Send、Codex/Grok 原生提问桥接及拒绝理由补发。
35. 已确认当前 Ask 工单回答由 Manager 调 Adapter.Send；OpenCode.Send 再按内存中的 pendingQuestionID 选择 ReplyQuestion 或 PromptAsync，权限走 RespondPermission。Codex/Grok 当前的原生问题解阻塞与下一回合回答是适配策略，不能当作原生协议必然限制。
36. 基础能力草案新增第 9 节与时序图：Ask 是有身份的业务待答请求；Ask 与审批升级共用持久化待决交互通道，但回复类型与语义权威分开。明确 Send/RespondAsk/RespondPermission，问答身份、迟到回复、恢复与投递状态。任务交互处理不另存一套答案。
37. 同步流程图 5：原生问题回复不必开启下一回合，已结束的提问回合才以关联答案续接。实际检查两份变更设计文档：26 个本地引用及空白通过，10 个 Mermaid 块解析通过；未做浏览器渲染或真实问答验收。

## 验证边界

- 本轮只新增模型草案、基础设施边界草案、流程图集与本台账；不把静态阅读当作真机行为验收。
- 前一轮在同一会话实际执行过 `go test -count=1 ./internal/discipline ./internal/permgate ./internal/ledgerstep ./internal/keystone`，四包通过；该结果是前一轮读数，本轮不会写成重新测试通过。
- 草案中的候选责任归属不等于已修改 `codegraph/best.json` 或 `target.json`，也不等于已批准的契约。
