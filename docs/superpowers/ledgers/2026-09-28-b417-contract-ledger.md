# B417 contract 节点台账（2026-09-28）

本文件记本节点实验过程与原始读数；结论文本在 `docs/superpowers/specs/b417-contract.md`。
卡 B417（Android/iOS 原生移动壳），有效基线 `cards/B417-spec`。

## 0. 开工状态位

- spec 头部状态行：`**状态：已批准（2026-09-28）**` —— 复核一致，无需回写。
- `git rev-parse HEAD` = `git rev-parse origin/cards/B417-spec` = `a9ba59b5e5f52bb6e1d29265c24ba3c5dcb9172b`；工作树 clean。
- 依赖 B392 已在基线：`10ccde73 merge(B392): mobile bind 生产接线`。

## 1. 环境读数（决定 Ticket 0 可行性）

```
$ uname -a
Linux handoff 6.8.0-138-generic #138-Ubuntu SMP ... x86_64 GNU/Linux
$ which java kotlinc gradle xcodebuild swift adb
（无输出：均未安装）
$ echo "ANDROID_HOME=$ANDROID_HOME ANDROID_NDK_HOME=$ANDROID_NDK_HOME"
ANDROID_HOME= ANDROID_NDK_HOME=
```

**结论**：本工作树是 linux（无 JDK/Xcode/Android SDK），**Kotlin/Swift 壳骨架无法在此编译**。
这正是 spec「备注·执行机约束」预判的风险（`docs/superpowers/specs/b417.md:180-183`）——壳工程
实现必须落到装了 Xcode/NDK 的 macOS 执行机。故本节点把「壳编译期 API 形状」用钉版
gobind 在 linux 上生成并冻结（见 §2），壳工程骨架/编译/直通竖切列欠账（contract §8）。

## 2. gobind 生成面（本轮亲自跑通）

`mobile/go.mod` 带 `tool golang.org/x/mobile/cmd/gobind`（`mobile/go.mod:38`），故不装 $PATH 也能生成：

```
$ cd mobile && go tool gobind -lang=objc -outdir $TMPDIR/gen/objc ./bind
objc_exit=0
$ ls $TMPDIR/gen/objc/src/gobind
Bind.objc.h  Bind_darwin.m  Universe.objc.h ...
$ cd mobile && go tool gobind -lang=java -outdir $TMPDIR/gen/java ./bind
java_exit=0
$ find $TMPDIR/gen/java -name '*.java'
.../java/bind/Bind.java  .../java/bind/Machine.java  .../java/go/Seq.java
```

生成面（逐字，供 contract §3 引用）：

- ObjC 包级函数是 C 函数（默认前缀 = `strings.Title(pkg.Name())` = `Bind`，
  见 `golang.org/x/mobile@v0.0.0-20260908204917-8b95e45f8d3e/bind/genobjc.go:101-106`）：
  `BindClose`、`BindMachineAt(long)`、`BindMachineCount(void)`、`BindOrigin`、
  `BindPair`、`BindSessionCookie`、`BindSwitchMachine`；错误经 `NSError**` 返回。
- Java 类 `bind.Bind`，静态方法 camelCase + `throws Exception`：
  `close()`、`machineAt(long)`、`machineCount()`、`origin(String)`、`pair(String)`、
  `sessionCookie(String)`、`switchMachine(String)`；`bind.Machine` 有
  `getName()/getOrigin()/getOnline()`。
- 生成器命名规则出处：`bind/genjava.go:808-855`（`genFuncSignature`，`throws Exception`）、
  `bind/genjava.go:317`（字段 `get%s`）、`bind/genobjc.go:540-566`（`asFunc`）、
  `bind/genobjc.go:1114`（结构体 `@property`）。

**可执行冻结**：新增 `mobile/bind/shell_api_golden_test.go`，用 `go tool gobind` 生成
两语言面并逐行断言上列签名（见 §4 跑出的绿）。

## 3. 图查询与覆盖

`codegraph` 已装（`/usr/local/bin/codegraph`）。查证命中/未命中：

```
$ codegraph --repo . sym n_agentd_sessionCookie  → 命中 internal/agentd/authroutes.go:343（d_gateway）
$ codegraph --repo . sym Pair / MachineCount / MachineAt / Origin / SessionCookie / SwitchMachine
  → 全部「不在图中」（近似候选仅 n_cmd_printPairing）
$ codegraph --repo . sym Core.Activate  → 不在图中
```

- `mobile/` 是**图外**：扫描配方显式排除 `mobile` 前缀（`scripts/codegraph-rescan/main.go:231`），
  故 `mobile/bind` 全部符号不在图，走 file#Symbol 锚（resolve 接受为 "moved"）。
- `internal/mobilecore` 属 `d_transport_channel`（`scripts/codegraph-rescan/main.go:2140`），
  但其符号只存在于视图 `codegraph/diffs/cards-B369-charter.json`（B369 冻结时 29 节点），
  baseline 未吸收；B392 新增的 `Core.Activate/Session/ActiveMachine`、`mobilecore.SessionCookie`
  **从未入图** → 记图覆盖债（contract §1.4）。

图健康（pre-existing，非本卡引入）：

```
$ codegraph --repo . validate
issues: ["[cards-B272-charter] 新增容器 k_dropdir_fn 引用不存在的基线领域 d_coordination_api",
         "[cards-B374-charter] 新增容器 k_collab_model 已存在于基线，containersAdded 只接受新容器"]
Error: 发现 2 个完整性问题、0 个失鲜节点   exit=1
$ codegraph --repo . check  → fails=0（本卡零 Go 边，无新增违规）
```

## 4. 本轮跑过的命令（原始结果）

| 命令 | 结果 |
| --- | --- |
| `go build ./...`（根模块） | exit 0 |
| `cd mobile && go test ./bind/...` | `ok github.com/Xsxdot/handoff/mobile/bind 0.093s` |
| `go test ./internal/mobilecore/...` | `ok github.com/Xsxdot/handoff/internal/mobilecore 0.023s` |
| `cd mobile && go tool gobind -lang=objc -outdir … ./bind` | exit 0（生成 Bind.objc.h） |
| `cd mobile && go tool gobind -lang=java -outdir … ./bind` | exit 0（生成 bind/Bind.java、bind/Machine.java） |
| `cd mobile && go build -o $TMPDIR/gobind golang.org/x/mobile/cmd/gobind` | exit 0 |
| `PATH=$TMPDIR:$PATH go test ./bind/ -run TestGomobileSurfaceHasNoSkips -v` | PASS（7.56s，未 skip） |
| `cd mobile && go test ./bind/ -run TestBindGeneratedShellAPIGolden -v` | PASS（7.23s） |
| 变异：把 golden 里 `pair` 的 `throws Exception` 删掉重跑 | `FAIL … 生成面缺少冻结签名："public static void pair(String bundleJSON);"`；还原即绿 |
| `codegraph validate` | 2 个 pre-existing issue（B272/B374 视图），baseline 非本卡引入 |
| `codegraph check` | fails=0 |

## 5. 判断与放弃的尝试

- **不落 Kotlin/Swift 壳骨架**：本机无工具链，落非编译通过的骨架违反 contract 纪律
  「骨架编译必须通过」。spec 结构本身即「壳工程子卡从零起」（`docs/superpowers/specs/b417.md:24-27`），
  故壳骨架归 macOS 子卡；本节点以**生成面 golden 测试**替代其编译期冻结。
- **不改 `target.json`/`best.json`**：本卡零 Go 符号、壳图外，无跨域契约条目可加；
  「未引入新符号则合法无视图，不造空文件」（contract 纪律）。
- **未建原型站**：承 spec「实现决定」的知情决定（`docs/superpowers/specs/b417.md:130-133`）。
- **未碰 handoff CLI、未起任何 executor/子任务**。

## 6. 提交事实（历史读数）

```
$ git add docs/superpowers/specs/b417-contract.md \
          docs/superpowers/ledgers/2026-09-28-b417-contract-ledger.md \
          mobile/bind/shell_api_golden_test.go
$ git commit -m "docs(B417): contract 冻结壳↔核接缝与两端共享不变式 + 生成面 golden"
e845e218 docs(B417): contract 冻结壳↔核接缝与两端共享不变式 + 生成面 golden
```

（本段落随同一提交 amend 收口；amend 会换 hash——git 的事实，不 chasing。）

