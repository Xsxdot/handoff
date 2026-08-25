# 台账：B156.2 breakdown 轮（2026-08-25，边干边追加）

- 分支 cards/B156.2-charter-3，HEAD=125b5c36（工作树 clean），与协调者指认的尖端一致。
- 上游状态位亲读：spec 头部 :3「已批准（用户，2026-08-25）」；contract 头部 :3-5 冻结声明+交棒 breakdown，一致。
- 协调者补充语义修正入账：cards.driver_carrier 改为「不透明载体标识」（本期只存不解释，格式定义权归 B156.3）；签名与 schema 不变，措辞落对应子卡边界。
- [亲测] `handoff graph check --view cards-B156.2-charter-2` exit=0（2026-08-26 本工作树）。
- [亲测] target.json 无 d_gateway→d_collab / d_cli→d_collab 边——契约 §2.3 两条预定声明确实未落，属实现轮欠账 #12。
- [亲读] best.json 顶层域（parent 空）13 个，d_collab 已立户（type=logic）；k_ledgerstep_* 五容器归 d_ledger——欠账 #7（NodeStep 补解析）整条在本域内，无跨域边。
- [亲读] proto.Card（internal/proto/ledger.go:15-32）无 Following/MergedInto 字段；ledger.CardView.Following（types.go:286）只在 agentd 视图投影（ledgerapi.go:149）。冻结 client 接口 GetCard→proto.Card 送不到并入态——拆解发现接缝缺口①。
- [亲读] Facade.ListActiveCards 直通 ListCards{IncludeTerminal:false}——终态卡房间无枚举数据源，而 §4 要求终态沉底可列——缺口②（与①同族）。
- [亲读] CardDrawer.tsx eventKind(:204-209) 对未知事件类型有 'system' 兜底——room_message 进卡 timeline 泛化渲染，无白名单挡死。
- [亲读] internal/proto/contract_fixture_test.go 锁 CardDetail 金样本；proto.Card 加 omitempty 新字段不影响既有金样本（缺席即不出键）。
- [亲读] web 页面容器先例：k_web_app_task/k_web_app_board/k_web_app_overlay→d_web_command；k_web_app_homedock→d_web_workbench。
- 判断：contract §3.5「CLI 直调 collab.Service 不经 agentd」与 inbox 三源编排落 gateway 存在张力（CLI 进程 Watchers 恒 0，ticket 源必失真）——列待拍板岔口三。
- [亲测] `graph resolve --doc b156.2-breakdown.md`：baseline 锚 7 条 ok/moved；5 条 Ticket 0 符号报 vanished（binding.go#Store.RebindDriver、rooms.go#Store.RecordMessageConsumed、events.go#Store.EnsureComment、collab service.go#Service.Send）——已经 `graph sym --view cards-B156.2-charter-2` 逐一验证 anchor=ok（nodesAdded 亲读在案），vanished 恰因视图未 absorb，与 finish 节点 absorb 义务互证。已记入拆解稿图覆盖债节。
- [落档] 契约文档追加「## 10. 修订记录」：澄清一 driver_carrier 不透明载体标识（协调者语义修正，签名/schema 不变）。
- 判断：欠账 #9 集成冒烟闭合方式=httptest 一发+孪生夹具（澄清三）；§8 附区实为 6 条非 5 条（澄清四，超集归属）。
- 产出物：docs/superpowers/specs/b156.2-breakdown.md（九卡 C1–C9 四段式+DAG+十条待拍板岔口+缺陷族逐族答案+真机六条），随本提交入库。
