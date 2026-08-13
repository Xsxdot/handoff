# 参与贡献

欢迎 issue 与 PR。这份文档只讲**怎么把改动跑通并提上来**，设计与用法看
[README](README.md)。

## 先说一句期望管理

这是业余时间维护的项目，PR 的响应可能不快。改动大一点的话，**先开 issue 聊一下
再动手**——避免你写完了才发现方向不对。小修（拼写、报错文案、文档）直接提 PR
就行。

## 本地跑起来

只需要 Go（版本以 `go.mod` 的 `go` 行为准，CI 也读同一处）。

```bash
git clone https://github.com/Xsxdot/handoff.git
cd handoff
go build ./...
go test ./... -count=1
```

> 这个仓库的历史里有已封存的桌面端原型（含 `node_modules`），完整 clone 约
> 800MB。只想改代码的话 `git clone --depth 1 https://github.com/Xsxdot/handoff.git`
> 就够了。

想装到自己机器上试：

```bash
go build -o ~/.local/bin/handoff .
```

**别把开发中的二进制装成托管服务再忘了它**——`handoff service install` 装的是
launchd/systemd 托管，会被自动拉起。开发时用 `handoff agentd` 前台跑更省事。

## 提交前必须过的门

CI 跑的就是下面这几条，本地先过一遍能省一轮往返：

```bash
go build ./...
go vet ./...
go test ./... -count=1
gofmt -l $(git ls-files '*.go')      # 必须无输出
GOOS=windows GOARCH=amd64 go build . # Windows 交叉编译不能断
bash install_test.sh                 # 安装脚本单测
```

`install.ps1` 的单测（`install_test.ps1`）在 CI 里跑两遍：**PowerShell 5.1**
（Windows 自带的那个）和 PowerShell 7。没有 Windows 机器也没关系，提上来 CI 会跑。

### 两个 .ps1 文件的编码规则是**相反**的，别顺手统一

- `install.ps1` **必须无 BOM 且纯 ASCII**：它主要经 `irm ... | iex` 消费，
  而 PS 5.1 不把 U+FEFF 当空白，BOM 会粘进首个 token，脚本第一行就报错。
- `install_test.ps1` **必须带 UTF-8 BOM**：它只从磁盘跑，无 BOM 时 PS 5.1 会按
  系统 ANSI 代码页解码，中文 Windows 上整个脚本会被解析成语法错误。

两条各有一个 Go 测试钉着（`release_workflow_test.go`），改错了会变红。原委写在
`install.ps1` 的文件头注释里。

## 代码约定

- **注释与日志用中文**，跟现有代码保持一致。
- 新文件顶部写「职责 + 边界」；导出函数写清参数、返回、注意事项。
- 注释解释**为什么**，不复述代码在做什么。踩过的坑写进注释里——这个仓库的注释
  里有大量「某年某月实测……」的记录，那是有意的：它们防的是后人把一个看似多余的
  防御删掉。
- 日志用 `log/slog`，**不要 `fmt.Printf`**。错误分支要带上下文（哪个任务、哪个
  文件、根因是什么）。
- 关键节点要有日志：进入关键操作、外部调用前后、状态变更、错误分支、成功退出。
  一个在生产上看不见的函数等于没写完。

## 测试约定

- 修 bug 请**先写一条会红的测试**，再修。CHANGELOG 里那些「实测……」的条目，
  背后基本都有一条这样的测试。
- 测试不要依赖开发机的环境。这个仓库真被这个咬过：几条用例隐含假设了
  「跑测试的机器是 macOS」「`~/.handoff` 里已有配置」，开发机全绿、干净的 CI
  机器全红。平台相关的分支请把 `runtime.GOOS` 之类做成可注入的包级变量。
- 别写会真睡几秒的测试——退避、超时这类间隔做成可在测试里覆盖的包级变量。

## 提交信息

Conventional Commits，正文用中文：

```
fix(release): 公证校验门补回来，spctl 对裸 CLI 要用 -t open

上一版把 spctl 整个撤了，理由写的是「对裸 CLI 根本不适用」——那句结论下早了。
不适用的只是 `-t exec`……
```

常用 type：`feat` / `fix` / `docs` / `test` / `refactor` / `chore`。
scope 用包名或子系统（`agentd`、`release`、`install.ps1` 之类）。

**正文比标题重要**：说清「为什么这么改」和「怎么发现的」，而不是复述 diff。

## 用户可见的改动要记 CHANGELOG

`CHANGELOG.md` 是**承重**的：release workflow 按 tag 抽取对应小节作为 GitHub
Release 的说明。改动如果用户能感知到（行为变了、报错文案变了、修了会咬人的 bug），
往 `[Unreleased]` 下加一条，写清**用户会遇到的现象**，不是内部实现。

## 发布（维护者）

推 `v*` tag 触发 `release.yml`：跑同一套验证门 → 六平台构建 → macOS 签名与公证
→ 算 checksums → 建 Release。Apple 签名 secrets 缺失时**硬失败**，不会产出未签名
资产。
