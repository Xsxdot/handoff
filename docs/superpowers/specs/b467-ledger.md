# B467 台账

## spec

- 2026-10-23 熟练工 code-investigate（基线 8a31ad33）结论：Q1 key 路径只解析 usage 窗口、仓内无 200 样本（需真机）；Q2 只有 `opencode auth logout <provider>` 可按条目删，codex/grok/claude 登出清整份，agy 无登出；Q3 弹层 `.up-main` 376px、内容区 329px、三窗口最小 334px → 月换行；「使用详情」`StatusBar.tsx:238`，底栏名 `:214`。
- 2026-10-23 §探测 mac-pro 主 HOME `auth.json` 条目 `opencode-go`（type api），只打印键名与类型的脚本：
  - 第 1 次 Python 缺省 UA → `status 403`，Cloudflare 错误体（`.cloudflare_error` 等），无效样本。
  - 第 2 次 UA 改 `Go-http-client/1.1`（与 handoff 的 Go 客户端一致，`internal/quota/opencodego.go` 未设 UA）→ `status 200`，键：`.usage.rolling.{status str, percent int, resetsAt str}`、`.usage.weekly.{同}`、`.usage.monthly.{同}`。无账号/组织/邮箱字段 → 第 4 条不做。
- 2026-10-23 原型 fork `prototypes/b467-accounts/`（自 base 8a31ad33，不入库）：账号卡「删除」→「忽略」、页底「已忽略」折叠区与恢复、底栏去名字（title 悬停）、弹层去「使用详情」、`.up-main` 376→392px（原型取值，实现由 plan 定）。
- 2026-10-23 原型浏览器核验（browser-worker，Chrome 经 127.0.0.1 临时静态服务）：8/8 符合——每卡「忽略」无「删除」；已忽略区收起/展开/恢复；被引用账号拒绝文案；忽略后底栏与弹层消失；底栏段内无名字（title 有）；弹层只剩「管理账号…」；.up-main 392px 时 og-team 三窗口 offsetTop 同为 121；settings 页无 error。
- 2026-10-23 用户批准 spec r1（「两条都按推荐，批准，cx-0jr3 删掉」）；B464 残余 cx-0jr3 已删、账号 codex:858e29a5d44e DELETE 200。
