# W5a agentd 用 go:embed 托管前端 — 执行 ledger

任务：31b7593f-738f-446f-ae4d-e3a671a7571e
分支：feat/w5a-embed-frontend
基线：5a4031be（docs(plan,backlog): W5a 实现计划）
计划：docs/superpowers/plans/2026-08-16-w5a-agentd-embed-frontend.md

## 进度

- 2026-08-16 Task 1（internal/webui 包与 build tag 双实现）完成，commit c0d3b9bf。审查直接 APPROVED。Minor 记账 3 条：M1 webui.go 未逐字点名「FS()/Embedded() 声明位置在 embed.go 与 stub.go」；M2 stub.go:26 显式接口类型标注略冗余；M3 embed.go:23 注释措辞严格说 fs.Sub 对非法路径也报错但字面量 "dist" 断言成立。
- 2026-08-16 Task 2（SPA handler）完成，commit 70091cf7。审查直接 APPROVED。Minor 记账 4 条：M4 TestSPARejectsTraversal 恒绿不咬（NewRequest 不规范化 `..`，MapFS 自身 ValidPath 也会挡穿越；实现三重防御无碍）；M5 webhandler.go:65 `name == "."` 死分支；M6 serveIndex 区分 ErrNotExist 与其他错误双文案轻微多做但更可诊断；M7 spaGet 返回 body 未 Close 且 ReadAll 忽略错误。
- 2026-08-16 Task 3（挂进路由栈 + 启动日志）完成，commit 355cc376。**发现计划缺陷并偏离**：计划字面机制 `mux.Handle("/", SPA)` 会让未注册的 `/api/*` 落到 SPA 回落成 200 HTML（协调者已独立实测复现），违反计划自己承重的「/api 未命中绝不回落 HTML」。实现者改为：全部 `/api`/`/ws` 路由移入无兜底的 `api` 子 mux，外层 `mux` 做前缀分派（`/api/`、`/ws/` → api，`/` → SPA），SPA 仍挂内层 mux（s.auth 之后），/console 仍在 root。实测 /api 未命中保持 404、方法错配保持 405（既有 TestRevealRouteRejectsGet 等仍绿）。变异验证 4 条：①注释掉 SPA 注册行 → TestConsoleRouteRegistered+TestDeepLinkRouteFallsBack 红；②注册目标 mux→root 按字面不可行（root 先于声明引用是编译错，挪到声明后是重复 "/" panic），计划变异 2 本身不成立，记 M8；③serveIndex 缓存头改 immutable → TestSPAIndexIsNoCache 红；④去掉方法判断 → TestSPARejectsNonGet 红。审查 APPROVED（审查员独立复现确认：计划机制不仅吞 /api 未命中、连方法错配也吞成 200 HTML；改后机制 404/405 完整恢复）。Minor 记账 6 条：M8 计划变异 2 不可行；M9 webhandler.go 注释措辞「精确模式抢走」与真实机制（前缀分派）不符；M10 TestUnknownAPIPathStaysJSON 命名理想化（实际 text/plain 404 非 JSON）；M11 中间层 mux 命名泛；M12 TestConsoleTicketRouteNotShadowed 敏感性弱（只咬复合情形）；M13 裸 /api、/ws 尾斜杠行为从 404 变 307→404（净效果一致）。
- 2026-08-16 Task 4（未鉴权 HTML 说明页）完成，commit 0a68a48b。审查直接 APPROVED（Accept 头大小写分析：strings.Contains 对真实输入全对，`Text/HTML` 大写漏判方向是 fail-safe 降级 JSON，无真实浏览器发大写 media type；q 值参数命中属无害边角）。变异验证 4 条逐条红且咬对用例：①wantsHTML 恒 true → TestUnauthenticatedJSONStaysJSON；②恒 false → 两个 HTML 用例；③HTML 分支改 200 → 两个 HTML 用例；④只改 sess==nil 出口 → 仅 TestUnauthenticatedHTMLWhenTokenUnset 红。Minor 记账 3 条：M14 Accept 大写漏判（fail-safe 可选 ToLower 加固）；M15 text/html;q=0 仍命中（无害边角）；M16 Cache-Control: no-store 无测试钉死（三用例均不断言）。

## 变异验证记录

（Task 3 四条、Task 4 五条，逐条记红/绿与失败用例，不写「已验证」）

## Minor 记账

- M1: webui.go 未逐字点名「FS()/Embedded() 声明位置在 embed.go 与 stub.go」，语义被「为什么两份实现」覆盖，建议后续补一句。
- M2: stub.go:26 `var stubFS fs.FS = ...` 显式接口标注略冗余。
- M3: embed.go:23 注释措辞，字面量 "dist" 断言成立无误导。
- M4: TestSPARejectsTraversal 恒绿不咬（NewRequest 不规范化 `..`，MapFS 自身 ValidPath 也会挡穿越），实现三重防御无碍，建议断言改为「返回 index.html 内容」。
- M5: webhandler.go:65 `name == "."` 死分支（URL.Path 恒以 `/` 开头），防御性代码无碍。
- M6: serveIndex 区分 ErrNotExist 与其他错误双文案，轻微多做但更可诊断。
- M7: spaGet 返回 body 未 Close 且 ReadAll 忽略错误，httptest 场景无实际泄漏。
- M8: 计划 Task 3 变异 2（注册目标 mux→root）按字面不可行：注册处 root 先于声明是编译错；挪到 root 声明后又是重复 "/" panic。ServeMux 里 `GET /console` 靠模式精确度天然胜过 "/"，变异 2 在任何设计下都不成立。
- M9: webhandler.go:9-10 注释「/api、/ws 由路由层用更精确的模式抢走（精确前缀优先）」措辞与真实机制（Task 3 的 /api/、/ws/ 前缀分派）不符，建议对齐。
- M10: TestUnknownAPIPathStaysJSON 命名理想化——实际 body 是原生 text/plain 404 非 JSON，断言正确。
- M11: server.go:317 中间层 mux 命名泛（相对 root/api），靠注释承载职责。
- M12: TestConsoleTicketRouteNotShadowed 敏感性弱——/console 被删且 SPA 仍在 auth 内时无 cookie 请求 401 仍非 200，测试照样绿；只咬复合情形。
- M13: 裸 /api、/ws（无尾斜杠）行为从直接 404 变 307→/api/→404，净效果一致；/console/ 带尾斜杠经 SPA 回落 200 HTML（在 auth 内无暴露面）。
- M14: server.go:414 wantsHTML 对 `Accept: Text/HTML`（大写）漏判，fail-safe 方向降级 JSON，可选 ToLower 加固。
- M15: `text/html;q=0` 仍命中（理论上不应接受），无害边角。
- M16: authpage_test.go 三用例均不断言 Cache-Control: no-store，该头无变异防护。
