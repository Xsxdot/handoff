# B156.2.9 台账（charter 节点起，边干边追加）

> 卡：B156.2.9 纪律层文本修订（欠账 #10，非代码）。分支 cards/B156.2.9-charter。
> 本文件随节点追加：[plan] / [implement] / [review] 各记各的，同批提交。

## plan 节点（2026-08-26）

- [plan] 开工现场：分支 cards/B156.2.9-charter，HEAD=b4231fb5，工作树干净；`git merge-base` 实测本分支起点==origin/feature/b156-workbench 尖端（b4231fb5，本地无同名本地分支、只有远程跟踪引用）——「有效基线分支」事实核清。
- [plan] 范围裁决入账：协调者裁岔口七方案 A——交付物=SKILL.md 三节修订 + discipline 具名资源新版本的仓内种子文本与写入步骤文档；运行期版本写入归协调者合入后单独执行（向用户报备）；验收止于仓内一致性。
- [plan] 基线红证：`grep -c "^### 升级简报六段契约"|^### 收口摘要与偏差上报义务|^### 换绑与重建四步 skills/handoff/SKILL.md` 三项各=0；关键词「升级简报/收口摘要/重建四步/执行中偏差如实记入/六段」在 SKILL.md 全部 0 次——三节现状确实不存在，判据能红。
- [plan] 插入锚实测：`## 会话恢复：从零接管` 在 SKILL.md 唯一命中 ：530；新节插它之前。标题计数基线：`^## `=17、`^### `=17。
- [plan] 判据更正一次（判据先在基线跑的价值实证）：初选三条 spec 引文锚「自主消化的不许消失，这是安全网执法点」「换绑写入按当前绑定 CAS 校验」「填补级偏差账的完备性来源」grep -F 均=0——它们在 spec 里被粗体标记+换行劈开（:103-104、:191-192、:262-263），属跨行串不能当保真锚；已换成单行安全锚「这是安全网执法点」「当前绑定 CAS 校验」「账的完备性来源」，复测=1。
- [plan] 最终保真锚清单 21 条逐一 grep -F 实测 spec 全部=1（清单落 plan §2）。
- [plan] 运行期机制查证（代码出处，非记忆）：`cmd/discipline.go:65` get 打印 `<name> v<N>` 首行、`:79` put 读文件全文、`:88/:92` PutDiscipline 与返回 JSON；`internal/ledger/disciplines.go:43-67` 版本=max+1 只插不改、`:20` 64KiB 上限、`:1-5` 派发自动取最新版并记 DispatchSnapshot。本机不实跑 handoff discipline（执行者纪律禁 CLI，非 graph 子命令）——命令行为依据=源码出处，标「未真机验证，归协调者执行时以 cmp 回读兜底」。
- [plan] 具名资源点名依据：b229-breakdown.md:177 实测七个名字 charter-plan/implement/contract/review/breakdown/recon/integrate；b229 P3 先例「只点名 charter-implement，不扩围」+contract §3.8/§7#10 单数措辞——D2 决策：义务只进 charter-implement，扩围归协调者另裁。
- [plan] 种子形态决策（D3）：运行期正文不在仓内（协调者侧账本），自造整块正文=虚构输入（纪律禁止）；种子=逐字片段+锚点+合并步骤+cmp 回读，每字皆有着落。此为「无占位符」正当出口的自我声明。
- [plan] 台账边界待办（补充四）评估结论：折入本卡 Fragment B，不独立成卡——同文本面、同批次交付，独立成卡反而多一次运行期版本写入；理由与措辞落 plan D4 与种子文件。
- [plan] 叙事文体缺口显式化：contract §3.8 列有「叙事文体」但协调者本轮四来源不含 spec §5 文体段——按「照抄别自创」不纳入，Out-of-scope 显式列出，不静默带走。
- [plan] 计划落盘：docs/superpowers/plans/b156.2.9-plan.md（339 行；决策 D1–D5、Consumes/Produces 接口表、T1/T2 全文级任务、AC-1～8 验收看板、缺陷族/五项检查/三查全节）。
- [plan] 自检抓缺二（自审的价值实证）：①保真锚「读卡（字段/附件/验收判据）」会被 T1 草文的粗体号劈成 **读卡**（字段…，grep -F 必红——换成单行锚「（字段/附件/验收判据）」，spec 与 plan §3 文块双测=1；②§2 证据行错字 disciplies→disciplines。
- [plan] 终态机械自检：21 条保真锚在 spec 与 plan §3 草文双测 21/21 pass；占位符扫描仅自声明行命中（预期）；零宽空格清零；代码围栏 6 对全部配平；S1/S2 字符串跨 T1/T2/验收命令一致。
- [plan] 红线自守：本节点零实现（SKILL.md 未动、disciplines/ 未建——那是实现节点的事）、零 handoff CLI 写调用、零派发、未切分支未改 git 配置。
