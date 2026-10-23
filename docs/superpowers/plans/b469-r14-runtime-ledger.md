# B469 r1.4 协调者验收台账

## 偏离与自主决定

- S2 env-cut 的行分隔输出无法保证只列名称，已向用户呈合成多行值反例；用户明确批准改为真正只列名称（如 Bash `compgen -e`），原不安全写法保持人工门禁。Sol 修订 spec/plan，Luna 修代码。
- S3 用户走查指定重试按钮移到通知状态右侧、不单独占行，协调者同步 fork 原型与真实 TicketsPanel；不改变通知/原票裁决语义。
- 原型自动打开 file URL 被浏览器工具协议安全策略拒绝；没有改用 HTTP/其他浏览器绕过。用户通过本地原型反馈形态，真实页面视觉对照尚未执行，不宣称完成。

## 验证记录

- 2026-10-23：读取 B469 当前事件到 #33189，复核 #33153 C 信任级、#33180 I3–I7 与 #33182 三项用户裁决；本期不引入独立可信人类在场证明。
- 原双机证据补 `source_commit=8cdff7fcca444614c9e517acf1a08cfa82d941bb` 与入库 manifest；用 `git archive` 逐一核对原冻结清单 2067 个源文件，2067 一致、0 差异。此为旧证据归因修复，不冒充 r1.4 新代码验收。
- 按用户形态调整后，bundled Node 24 运行 TicketsPanel / TicketsOverlay / useGlobalTickets 三文件：3 files、21 tests 通过；`tsc -b` 退出 0。未声称浏览器视觉对照通过。

提交形态/旧证据补录：`git commit -m "fix(approval): align notice retry layout and trace runtime evidence to source"` 原始输出 `[codex/approval-batch-b469 cc9c04572] ...`，`9 files changed, 2118 insertions(+), 19 deletions(-)`。随后只 amend 一次收本记录，不追写 amend 后 hash。

- 真实模型首轮：隔离 production `Approver.Decide`→`OpenCode.OneShot`，deepseek/deepseek-flash，OpenCode1.18.32，23例完成/18符合/5历史help误升级（run/reply/done/continue/upgrade）；全部危险反例与compgen正确。模型给出的失败理由指向模板自指令兜底与已验证help边界不清。模型SQLite part类型只有 reasoning/step-start/step-finish/text，无tool part；禁用所有工具，不执行被裁决命令。单一provider认证只进子进程环境，实验目录扫描无明文key，无authstore副本。此轮保留为红证据，不记通过。
- 在同隔离HOME执行已验证CLI构建副本的13条历史 `--help`：13/13退出0且有Usage；只作CLI身份/帮助行为核验，不执行实际变更子命令。
- 第二轮真实模型固定同组23例：22符合，唯一 `upgrade --help` 仍误升级，理由是政策仅举dispatch而未明确已验证upgrade帮助；保留红证据。实际model DB再次无tool part。第三轮仅澄清已验证Handoff CLI纯help包含upgrade帮助，实际upgrade/替换二进制和未验证脚本不放宽；若仍失败停止措辞试探，回到政策/证据契约分析。
- PostgreSQL新鲜实跑三条 `TestB469TicketNoticePG*`：3通过、0失败、0跳过，每条独立schema、独立连接；其中same-facts外层只读预检在另一连接持全局advisory锁时仍返回，保留更新时间与退避，换责照常pending。
- Sol提供的唯一PG overlay关闭只读预检：编译通过，`TestB469TicketNoticePGIdenticalReplaySkipsGlobalWriteLock` 在line249以“identical notice replay waited on the global PG advisory write lock”真实断言红；共享树未改，撤overlay回绿待回收输出。
- 第三轮真实模型使用冻结提交 `381911e18cb3ec04e384129a229baa645a0252fa` 原生build与production Approver/OneShot：23完成、23符合、0失败。13条历史CLI help与Bash compgen获批准，9条读值/凭据/未知脚本/实际变更反例升级；模型DB的23条assistant模型均为deepseek/deepseek-flash，0 tool part，未执行被裁决命令。独立Sol已核报告并裁S2两轴通过，边界限这组隔离样本，不外推在役行为。
- PG锁变异撤overlay目标单测回绿（`pg-40f5e614`，1通过/0skip）；Sol对S3 I3/I4/I6独立变异和契约逐项复核，技术两轴通过。真实双机r1.4回归另行运行，不以旧源报告替代。
- 冻结源码三组双机回归全部成功：`r14r-7954a460` 卡路由6条完成检查；`r14c-2a54a68f` 持久游标3条；`r14v-d074a557` 权限/恢复9条。远端binary SHA与上传物一致，当前来源381911e18；独立HOME/端口和每组独占PG schema。source原生权限/ask、离线补投/重启/换驻场/不回答原票/源回答回传均按报告观测；本机stop已在phase记录，远端身份匹配停机与隧道清理均成功，无cleanup失败。schema留证据复核。
- 双机wrapper首次run-id超16字符在Harness构造阶段被拒，尚未创建schema/启动实例；改为固定13字符后运行。包装器只替换为冻结新binary/独占远端路径和同版LinkTask测试helper，不更换产品路由或伪造成功；生成报告已去除隔离token/DSN，不保存生产模型认证。

- 前端最后触及文件 `eslint src/app/task/TicketsPanel.tsx`（bundled Node24）退出0。
- 提交证据/复审/U1草稿：`git commit -m "docs(approval): record r1.4 repair validation and next-slice proposal"` 原始输出 `[codex/approval-batch-b469 5c39c94b9] ...`、`21 files changed, 4699 insertions(+), 3 deletions(-)`。随后只 amend 一次收入本记录，不追写新 hash；后续实际推送结果在卡事件留痕。
