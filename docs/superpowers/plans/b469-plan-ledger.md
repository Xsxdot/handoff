# B469 批次 plan 台账

2026-10-23；状态：出稿完成，待独立协调者审查。基线 `a79b42867`；计划 [b469-plan.md](b469-plan.md)。只编辑本计划与本台账，不实现、不提交、不运行产品验收。

## 输入与核对

- 全文读取 plan、architecture-law、defect-families、instrumenting-code skill；读取批准 spec r1.0 与 b469-approval-batch-ledger.md。最初按 spec-ledger 命名查找未命中，随即读取 spec 实际链接的台账；没有自造输入。
- 核对 `internal/hostapi/hostapi.go` 与 `driver.go`：目前仅 opencode，RunTurn为已冻结入口；driver只解析text/session/error，无权限交互回传面。CLI是否支持所需权限握手未验证，明确放入P0。
- 核对 `internal/approval/authority.go`：Judge依赖真实task查上下文；不能直接喂协调者假task，须提取共同评估能力。
- 核对 `internal/orchestration/approver_chain.go`：现有共享审批链注册表、读超时和不存在/读取失败区分为复用先例，不等同用户授权规则。
- 核对 `internal/ledger/registry_pg_test.go`：已有CAS/版本测试；不据此声称跨机撤销已验证。
- 核对 TicketsPanel、agentd handlers、cmd/reply.go：已有人工gate回答入口；新增保存与管理必须接真实入口。
- `prototypes/base/`存在；未发现codegraph与项目CLAUDE/AGENTS文件，计划记录无图基础。

## 拆解决定（提案）

P0先核CLI交互与可信管理来源；S1真实工单生命周期为首故事，规则权威随真实消费者落地；再S2、S3/双入口S4，四卡分别验收，串行共享分支，不造技术子卡。CLI握手失败只阻塞S3/整体收口，规则写入身份不能证明则阻塞持久授权，不妨碍S2。

未把actor当授权管理员、stdin当权限协议、error当permission事实、单机测试当跨机验证；撤销与投递并发语义列稿首待拍板。全局范围及五类明确限制覆盖是用户已批决定，不重复提问。

## 自检与待审

计划已覆盖故事映射、五格行为闭环、责任范围、依赖类别、失败传播、日志/注释步骤、序列化与安全反例、真实环境清单、条件冻结、UI最小原型、权限和交回物。全部待决集中稿首，作者未自批。

后续由协调者审查并逐项回写裁决；未完成P0，未声明任何实现/真机通过。

## 2026-10-23 提交记录

用户要求提交并推送。四卡已共同挂 plan:docs/superpowers/plans/b469-plan.md；技术提案状态保持待协调者拍板。
`git diff --cached --check`：退出 0，无输出。
`git commit -m 'docs(B469): record shared approval batch spec and plan'` 原始输出：
```
[codex/approval-batch-b469 60cbc8642] docs(B469): record shared approval batch spec and plan
 4 files changed, 252 insertions(+)
 create mode 100644 docs/superpowers/plans/b469-plan-ledger.md
 create mode 100644 docs/superpowers/plans/b469-plan.md
 create mode 100644 docs/superpowers/specs/b469-approval-batch-ledger.md
 create mode 100644 docs/superpowers/specs/b469-approval-batch-spec.md
```
按台账纪律仅 amend 一次纳入本记录；最终提交号以 Git 为准。仅文档，未运行代码测试。

## 2026-10-23 r1.1 重写记录

本节取代前文r1.0拆解结论，不抹除历史。按B469 #33001/#33005与四卡最新裁决重写plan，故事基线改为spec r1.1；当前仅文档提案，未实现、未运行P0/服务/测试、未修改生产配置。

- 三线改为先U2/S2，再U1/S1+任务S4，再U3原执行者工单收件；U2规范化为规则硬依赖。原hostapi自身权限桥移除。
- 稿首六项集中技术推荐与拍板边界；解析器、可信确认、人工同任务复用、责任归属/准入均未冒充用户已批。
- U1补完整分类/求值顺序、不可覆盖、不可变事务审计、FindReuse撤销/期限、四类原因/版本去重/历史命中与原nl样本。
- U3承载原任务工单；push/pull契约分别保持，无归属needs_human；等人不占30min回合/准入，实际行为列P0真机。
- P0/U4全部隔离账本与实例；两机也禁生产共享账本。Runner/RunTurn已有冻结显式引用，卡席位/驻场共用路径回归。
- 全量C/I/M逐项响应与源码纠正集中在[spec台账](../specs/b469-approval-batch-ledger.md)的“#33001响应矩阵”，本计划对各项给实施/验证责任，避免两份结论漂移。

本轮完成标准：四文档范围/故事/依赖/提案状态一致、diff检查；不是承重实验或实现验收通过。#32980与孤儿会话待协调者核状态，未宣称已关闭。

## 协调者复核与提交（2026-10-23）

协调者通读 r1.1；另由 gpt-6.1-sol 只读独立复核，对照 #33001/#33005 及其余三卡，未发现新的范围/行为缺陷。技术稿首仍待拍板；未实施或运行承重实验。
`git diff --check` 退出0，无输出。
`git commit -m 'docs(B469): revise approval spec and plan from review'` 原始输出：
```
[codex/approval-batch-b469 175cc1959] docs(B469): revise approval spec and plan from review
 5 files changed, 222 insertions(+), 137 deletions(-)
```
按台账纪律 amend 一次收进本记录；最终提交号以Git为准。

## 2026-10-23 r1.2 修订与待复审

输入：B469 #33034复审、#33043用户「都按推荐，批准」及四卡#33044–33046。spec r1.2已批准；plan r1.2待Claude主会话复审拍板，不自批。完整逐项响应见spec台账r1.2矩阵。

Q1固定合法卡席位→任务发起会话驻场者→人本人；驻场print/desktop独立于卡push/pull。执行机未挂卡工单发布/镜像、寻址房间通知及session wait候选/门铃接线为S3本期承诺；独立投递记录使S3不依赖规则线B。通知归属不收紧回答CAS。Q2部署工具闭集+机器环境；Q3审批控制面例外及人工reuse不收紧贯穿正文/验收/冻结。

补help/env历史样本采集/缺失边界与危险反例；默认Info候选日志与隔离codex多环实验；权威UTC及跨机偏移反例；spec第一版数据归属/依赖表在首次wire前对齐。历史r1.1待审与顺序由当前正文覆盖，未宣称样本已齐或实验通过。

本轮仅修改原四文档，未运行P0/服务/实现或提交。文档检查结果由协调者收口记录。

### r1.2 协调者复核与提交

协调者通读变更；独立gpt-6.1-sol按#33034/#33043做反例复核，无新增阻塞。澄清全部候选均不可达再needs_human、通知不限制回答、规则派生复用与人工来源分开。spec已批准，plan仍待Claude主会话复审；未运行实现实验。
`git diff --check` 退出0，无输出。
提交原始输出：
```
[codex/approval-batch-b469 e74c7e0a6] docs(B469): apply second review and approved r1.2 decisions
 4 files changed, 106 insertions(+), 44 deletions(-)
```
本记录仅amend一次纳入，最终hash以Git为准。

## 2026-10-23 r1.3 已拍板

终审#33054与最终#33059用户「按推荐，批准」已落实，plan状态已拍板。部署覆盖为空并落roadmap，upgrade/换二进制不可覆盖；独立寻址系统通知与B435现役contract批准补丁一致；完整卡祖先路由保持。默认Warn可见、无HANDOFF_LOG_LEVEL实验、原改名help样本及边界文件补齐。

顺序P0→U2/S2与U3/S3并行→U1→U4；S2自身review/finish后可单独合入但操作仍须用户授权。U2仅减少自指令误命中，permgate全量+变异；P0认证不足阻塞S1写入回用户。三条新增原子冻结见正文，准确kind/wire/守卫留实施前冻结，未改源码或跑实验。本节取代r1.2待复审状态，不覆写历史。

### r1.3 协调者复核（2026-10-23）

协调者通读本轮diff，并核logx默认Warn、hostapi wakeRoundLogLevel、build-deploy只构建、wakeconsumer结构行过滤与卡路由原义。独立sol已核源码，但r1.3文档复核回合因模型容量不足失败，未形成最终独立审查结论；不沿用上一轮“无阻塞”结论。协调者对照#33054/#33059完成本轮文档核对，技术批准来源为卡上已记录拍板。
`git diff --check`退出0，无输出；本轮只改文档，未运行产品测试/实验。

提交原始输出：
```
[codex/approval-batch-b469 a84462fdd] docs(B469): finalize approved r1.3 approval plan and contract
 6 files changed, 99 insertions(+), 37 deletions(-)
```
按纪律amend一次收进记录，最终提交号以Git为准。
