# 拖拽覆盖终端关闭：独立审查记录

阶段：bug 源码修复；基线c34cad96；计划docs/superpowers/plans/2026-10-15-terminal-drop-replace.md。实现gpt-6-luna，独立只读审查 /root/review_drop_replace。以下为施工中首轮风险，最终结论待冻结后复核。

## Important R1：替换请求期间其他关闭入口覆盖确认目标

首轮源码：beforeCloseTab仅检查groupCloseRef（当时Shell.tsx:459），killHomeSession同样仅检查groupCloseRef（当时:748）。拖A覆盖B，确认后DELETE(B) pending；关闭C可setClosingPty/closingDirtyFile/closingHome覆盖原确认目标。B成功回调setClosingPty(null)会抹掉C提示；或B失败后下次确认可能删C而dropReplaceRef仍指B。此风险需真实入口deferred DELETE回归，核对后由实现者修复。尚未核销。

非protectedTarget新drop本身不创建确认，允许无关布局操作可沿用已有target/source身份再核约束；若影响本次源或目标则废弃原拖放，不应用陈旧continuation。此项不要求全局锁所有布局操作，但任何关闭入口不得覆盖in-flight确认。

## Important R2：确认期间来源从未打开变为已打开导致会话重复

初始DRAG_SESSION_MIME的S未打开时创建kind:new request，request.source=null；确认期间其他入口将S开到另一组，旧isCurrent仅核对target，确认后apply直接placeSource新session tab，绕过原本findSessionTab的去重，违反既有一会话一tab不变量。协调者确认此边界属于源意图变化；允许废弃此次拖放并告知重拖，不要求自动转成移动。已交实现核验并补回归，待核销。

首轮独立实跑：npm test -- src/app/shell/Shell.test.tsx -t '拖拽覆盖中央终端'，1 passed/144 skipped，2.38s；确认/取消、DELETE失败保留、重试成功由mock路径验证，不代表真机。实际drop全部api.place调用已统一guard，纯layout与TerminalTab普通卸载保活未变。日志需补drop cancel与源/目标机器/会话关联，已反馈。

## 冻结后独立审查与承重变异

独立review冻结版亲跑Shell/Workbench两文件191项通过（15.61s）、diffcheck通过；无未修Critical/Important产品缺陷，R1/R2自动化核销。源码架构：placeSource仍纯布局、普通卸载保活，Shell接入onBeforeReplace，DELETE前后消费isCurrent；无wire字段或新增持久化。维度表由reviewer工具回传，日志与注释、既有API契约检查通过；无客户端运行证据。

协调者变异1：唯一targetTab判定取反，typecheck exit0，真实Shell中央确认及Workbench边缘不应guard两用例同时断言红，test exit1（2failed/189skipped），日志/private/tmp/handoff-5160-drop-mutation-replacement.log。变异2：唯一sourceTab身份检查改成sourceTab && Boolean(0)，typecheck exit0，4文件232项仍全绿（14.27s），证明现有tab来源变化分支缺承重测试，不能以原套件称该边界已验。生产文件已finally按原SHA恢复；已退回同一gpt-6-luna补生产消费入口回归，暂不作最终验收通过。

## 来源身份测试缺口核销

gpt-6-luna仅补Shell生产拖放回归「文件来源在替换确认等待期间改变时不删除终端目标，也不覆盖两边内容」。独立review亲跑1passed/150skipped（2.37s），确认真实Shell→Workbench标题dragstart/drop→修改来源draft→确认→零DELETE、两侧内容保留。

协调者重复同一source身份变异：唯一if(sourceTab)改为sourceTab && Boolean(0)，typecheck exit0，新用例断言红（缺预期未结束终端alert，1failed/150skipped，3.53s）；finally原字节恢复，SHA256=29488f4d9a8b68d8dfa9300417e3e89f5b333bbcbfafd4f32446037ae1256504。证据/private/tmp/handoff-5160-drop-mutation-source-r2.log。该承重证据缺口核销；source.machine/位置、target.machine/content等各组合不声称逐一真机实测，身份字段保护另有源码核验。最终原版全量回绿待协调者ledger。

## 最终维度裁决（源码与自动化阶段）

| 轴 | 维度 | 裁决与证据 |
| --- | --- | --- |
| 目标 | 故事与验收覆盖 | 已跑确认/取消/失败重试、草稿、远端、关闭竞争、确认前后目标移位、已有来源draft变化、新session去重与不替换路径。Shell真实入口及Workbench正式组件回调接缝；具体测试见两测试文件新增describe/case。未跑客户端整链。 |
| 目标 | 原型/流程基准 | 按计划复用既有确认框，无新增页面形态；实际替换才确认，边缘/空/自身不触发，满列回落触发。 |
| 目标 | scope drift | 无；改动限Shell/Workbench及测试，无后台回收、持久化或API变更。 |
| 架构与证据 | 架构与实际生产路径 | WorkbenchPage纯placeSource投影判定→Shell注入onBeforeReplace→现有machine-aware DELETE→身份再核→placement；tabs/useWorkbench与普通卸载未增加资源副作用。 |
| 架构与证据 | 测试有牙 | 两类可编译语义变异已红、源码原字节恢复后全Web144文件/1877项绿（56.28s）；来源证据缺口已补。 |
| 架构与证据 | 日志与注释 | 入口/确认/取消/DELETE前后/失败/失效/完成有资源和机器上下文；导出WorkbenchReplacementRequest与投影职责有文档注释，独立review已核。 |
| 架构与证据 | 序列化边界 | 本轮无新wire/持久化字段；请求类型只用于组件内部回调。 |
| 架构与证据 | 既有契约 | 既有终端DELETE与404幂等、卸载保活规则未变，无新增独立冻结对象。 |

无未解决Critical/Important源码finding。部署与客户端行为尚未验证，不以自动化阶段通过替代最终设备证据。

## 本机部署后的行为核销

协调者在实际嵌入Web服务c34cad96ac4e+dirty5上用IAB真实鼠标drag点验：取消后后台清单和原PID保留；确认后旧会话/PID均消失，新终端显示；清理测试资源后3个原有会话全部保留。该证据补齐本机浏览器到真实PTY结束的整链；原生桌面与手机的逐项拖放仍未运行，不冒充对应设备验收。详见ledger部署段。
