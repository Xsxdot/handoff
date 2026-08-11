# handoff Web 控制台（前端地基）

agentd 托管的 Web 控制台的前端脚手架。本任务只做地基：用**真实的 cookie 会话**
在 dev server 上证明三件事——`/api/status` 拿到版本与状态、`/api/tasks` 拿到任务
列表、对任一任务开 `/ws/events` 收到至少一条事件。不做看板、不做任务详情。

## 版本（2026-08-11 实际选定）

| 依赖 | 版本 | 说明 |
|---|---|---|
| React | ^19.1.0 | 模板自带 |
| Vite | ^6.3.5（build 时解析到 6.4.3） | `npm create vite@6`（react-ts 模板） |
| TypeScript | ~5.8.3 | 模板自带 |
| Tailwind | ^4.3.3 | v4，用 `@tailwindcss/vite` 插件，无需 postcss 配置 |
| shadcn/ui | 用 registry 抄录（new-york / neutral） | 未用 `npx shadcn init`：手动写 `components.json` 与 `src/index.css` 主题变量，`components/ui/{button,card,badge}.tsx` 由 `ui.shadcn.com/r/styles/new-york/<名>.json` 抄入 |
| lucide-react | ^1.31.0 | 图标 |
| vitest | ^4.1.10 | 测试框架；`@testing-library/react`、`jsdom` 已装供后续组件测试 |
| ws / @types/ws | ^8 | 仅验收脚本用（Node 侧 WS 客户端，devDependencies） |

`package-lock.json` 已提交，重装用 `npm ci`。

## 开发流程（鉴权闭环怎么走）

1. **起 agentd**。本任务一律用一次性实例（独立 datadir + 独立端口），别碰机器上
   正在跑的那个（同一 DataDir 起第二个会被单实例文件锁挡下）。先给一份能直接
   复制粘贴的最小配置——字段名是 `datadir`（**不是** `data_dir`，strict 解码器
   会把后者当场拒掉）：
   ```
   cat > /tmp/agentd-smoke.yaml <<'EOF'
   listen: 127.0.0.1:7788
   token: smoketest-token-00000000000000000000
   datadir: /tmp/agentd-smoke-data
   EOF
   go build -o /tmp/agentd-smoke . && /tmp/agentd-smoke agentd --config /tmp/agentd-smoke.yaml
   ```
2. **拿 ticket URL**：
   ```
   /tmp/agentd-smoke console --config /tmp/agentd-smoke.yaml --print-url
   # → http://127.0.0.1:7788/console?ticket=<t>
   ```
3. **把端口从 agentd 的换成 5173 打开**：
   ```
   open http://localhost:5173/console?ticket=<t>
   ```
   浏览器经 vite 反代把 `/console` 转给 agentd → agentd 原子消费 ticket、
   `Set-Cookie: handoff_session=…`（host-only、不按端口隔离）→ 302 到 `/` →
   回到 5173 的 `/` 已是登录态，后续 `/api` 与 `/ws` 都带 cookie。
4. **正常开发**：`npm run dev`，页面三个区块分别验证 status / tasks / WS。

agentd 地址可配：`AGENTD_URL=http://127.0.0.1:7788 npm run dev`（默认就是 7777；
上例一次性实例在 7788，就要带上这个环境变量）。

## 反代（`vite.config.ts`）

三个前缀转给 agentd：`/api`、`/console`（HTTP）、`/ws`（WS 升级，`ws: true`）。
**刻意不加 `changeOrigin`**：agentd 的 Host 白名单与 coder/websocket 的默认
Origin 校验都要求 Host 原样转发（`localhost:5173` 在两个白名单里），改写 Host
会让 WS 握手因 `Origin ≠ Host` 被 403。

## 契约 fixture（前后端共同基准）

- **Go 侧** `internal/proto/contract_fixture_test.go`：把 `internal/proto` 每个
  对外类型的代表性样本 `json.MarshalIndent` 成 `web/src/api/testdata/<类型>.json`，
  并**逐字节断言**与已存文件一致。字段改名/类型变化/omitempty/时间格式变化都会
  当场变红。显式刷新：`go test ./internal/proto/ -run TestContractFixtures -update`。
- **前端侧** `web/src/api/contract.test.ts`（vitest）：import 同一批 JSON，断言
  TS 类型能解析且关键字段齐全（编译期 + 运行期双保险）。
- 为什么不用代码生成器：要钉住的是**实际序列化结果**（`omitempty` 缺键、时间
  格式、指针字段是 null 还是缺席），那是代码生成器覆盖不到的。
- 改线格式的纪律：Go 结构体 → `-update` 刷新 fixture → 同步 TS 类型与契约测试，
  任何一步漏改都有测试变红。

## 目录

```
src/
  api/          类型定义（types.ts）、fetch 客户端（client.ts）、WS 客户端（ws.ts）
    testdata/   Go 侧测试生成的契约 fixture（勿手改；改走 -update）
  app/          页面区块：StatusSection / TaskSection / EventSection / ErrorBanner
  components/ui/ shadcn/ui 生成物（button / card / badge）
  lib/utils.ts  cn() 类名合并
scripts/
  verify-auth-loop.mjs  真实 agentd 的鉴权闭环冒烟（见下）
```

## 脚本与测试

```bash
npm run dev        # 起 dev server（默认反代 127.0.0.1:7777）
npm test           # vitest（契约 fixture 断言）
npm run typecheck  # tsc -b
npm run lint       # eslint（shadcn 组件自身带 fast-refresh 告警，属预期）
```

### 鉴权闭环冒烟（真实 agentd，不用 mock）

```bash
go build -o /tmp/agentd-smoke . && /tmp/agentd-smoke agentd --config /tmp/agentd-smoke.yaml &
AGENTD_URL=http://127.0.0.1:7788 npm run dev &
node scripts/verify-auth-loop.mjs \
  "$(/tmp/agentd-smoke console --config /tmp/agentd-smoke.yaml --print-url | sed 's/:7788/:5173/')"
```

`/tmp/agentd-smoke.yaml` 用上面「开发流程」第 1 步那份最小配置（listen /
token / datadir 三行，字段名 `datadir`）。

脚本证明：`/console` 兑换 → 302 + `Set-Cookie: handoff_session` → 无 cookie 401 →
带 cookie 的 `/api/status`、`/api/tasks` 200 → WS 握手带 cookie 被接受。实例没有
任务时显式输出「无任务，跳过 WS 验证」，不假装成功；有任务但连接后 10s 内既无
事件也不关闭时，明确以非零退出报「已连上但 10s 内既没收到事件也没被关闭」——
验收脚本绝不静默挂死。

## 硬约束

- 不做 `go:embed`：前端产物暂不嵌入二进制（那是「打包与发布」任务的事）。
- 布局只用 flex/grid + 相对单位，不写死像素宽度——移动端适配是后续任务，
  现在守住这条就能让它变成调整而不是重写。
- 不打印 token / ticket / cookie 明文；凭据只由浏览器在 cookie 里携带。
