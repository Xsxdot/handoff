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
