# B176 实现计划（实况记录）：回合末正文送达裁决解析器

> 2026-08-22。**成因说明**：与 B175 同形态（B182），plan 环节执行者（task
> 48b4ceb8@linux-01）直接完成了实现（cards/B176-charter @ c37fe616）。协调者
> 逐行审阅 diff 后按实况回填本文档，作为 implement 门附件与审阅对账基准。

Spec：docs/superpowers/specs/2026-08-22-b176-verdict-final-text.md

## 设计决定

1. **正文尾窗随 Result 传递**：`executor.Result` 增 `FinalText`；
   `turn.FinalText`（`FinalTextLimit = 16Ki rune`，复用 `TailRunes`）取正文
   尾部——裁决块按契约在报文尾部，截断从头部丢，块保全。render.log 仍是
   完整取证来源，FinalText 只是传输边界的有界投影。
2. **四家 adapter finish 路径接线**：codex/grok/opencode/claudecode 的成功
   收尾都带 `FinalText: turn.FinalText(text)`；各家收尾日志补 `final_text_len`。
3. **completed payload 增量可选字段**：`FinalText *string json:"final_text,omitempty"`
   ——指针区分「字段缺席（旧 adapter）」与「显式空」；无正文时省略字段，
   旧消费者忽略未知字段，滚动升级两个方向都兼容。
4. **wire 层优先新字段、显式空 fail-closed**：`finalMessageFromEvents` 有
   `final_text` 即用（空串报错转人工，不静默退回摘要），缺席回落 `summary`
   （历史事件与旧 agentd 兼容）。
5. **ParseTrailer 先剥完整裁决块再扫收尾行**：块在 trailer 之后（codex 真机
   形态）时，收尾行解析不再被裁决 JSON 遮蔽；不完整的块不剥，维持 fail-closed。
6. **模板契约出新版**：review/impl 两个 verdict 契约改为「块写正文、位置
   前后均可、收尾行只带简短 summary」；保留 legacy 契约原文作**存量识别器**，
   `EnsureDefaultTemplates` 只对逐字段等于旧出厂定义的模板追加 v2，用户改过
   的同名模板不覆盖（模板不可变纪律）。

## 触及文件

- internal/executor/executor.go —— Result.FinalText
- internal/executor/turn/text.go / protocol.go —— FinalText 尾窗、ParseTrailer 剥块
- internal/executor/{codex,grok,opencode,claudecode}/adapter.go —— 四家接线
- internal/agentd/manager.go —— completedPayload 增量字段
- internal/ledgerstep/wire.go —— finalMessageFromEvents 优先级
- internal/ledger/templates.go —— 契约 v2 + legacy 识别升级

## 测试映射（spec 接缝清单 → 用例）

1. wire 表驱动：`TestFinalMessageFromEventsPrefersOptionalFinalText`
   （优先/回落/显式空报错）；端到端 `...PreservesVerdictAfterTrailer`
2. 四家 adapter：各自 finish 用例断言 FinalText 含 verdict 块且超长尾截断到限
3. 截断保全：`TestFinalTextKeepsVerdictAtTail`
4. 收尾行位置宽容：`TestParseTrailer` 增「块在 trailer 前/后」两例
5. payload 增量性：`TestCompletedPayloadCarriesFinalTextAsOptionalField`
   （含无正文省略字段）
6. 模板升级：`TestVerdictTemplateContractUpgradeCreatesNewVersion`
   （追加 v2、保留 v1、幂等、不覆盖用户版）

## 已知边界

- `charter-default` 模板是账本数据（非代码播种），代码侧升级机制不触达——
  合并后由协调者 `template put` 出 v2 更新措辞；旧措辞在新传输下仍可工作
  （块位置已宽容），只是审阅类「块塞 summary」的指令过时。
- turn/protocol.go 与 ledgerstep/verdict.go 各持一份 verdict 块正则（包依赖
  方向使然），两处漂移风险记为已知，改块语法时需同步。
- 真机验收依赖 linux-01 部署新 agentd 后跑一轮 Verdict 环节目击自动路由，
  部署前记「未验」，账挂本卡。
