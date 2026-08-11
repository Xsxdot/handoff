# 执行机仓库登记表 Implementation Plan（B46）

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把「项目首次落到某台执行机」从人工 ssh clone + 回填 `--repo`，变成一条显式命令 `handoff repo add`，并由 agentd 侧的登记表承载「执行机 × 仓库」映射，使日常派发不必再指定仓库路径。

**Architecture:** 执行机 agentd 的 SQLite 新增 `repos` 表（name/path/origin_url）。新增 `handoff repo add/ls/rm` 三条子命令与 `/api/repos` 三个端点；clone 由 agentd 在执行机本地执行（复用它既有的 git 调用与凭据路径，不走 ssh）。dispatch 的 `--repo` 语义扩展为「路径 / 登记名 / 省略」，解析在 agentd 侧由一个**纯函数**完成——不碰 DB、不碰 git，因此可表驱动测试并做变异检验。

**Tech Stack:** Go 1.26、cobra（CLI）、modernc.org/sqlite（纯 Go，无 cgo）、标准库 `net/http` + `http.ServeMux` 的方法路由、slog 结构化日志。

**Spec:** `docs/superpowers/specs/2026-08-10-remote-repo-registry-design.md`（遇到「为什么这么设计」先读它，不要自行重新推导）。

## Global Constraints

- 语言与注释一律中文；新文件必须有文件头注释（职责 + 边界），导出函数必须有 doc 注释（参数/返回/注意）。
- 日志一律用各包既有的 `log()`（运行时取 `slog.Default()`）或 `s.log` / `m.log`，**禁止 `fmt.Printf` 作为日志手段**。每个错误分支必须带上下文与 cause；成功路径也必须有结论日志。
- git 一律经 `gitRun(ctx, repo, args...)`（`internal/agentd/workspace.go:92`）执行，**不拼接 shell**；任何来自用户的 URL/路径参数前必须有 `--` 分隔符，防 git 参数注入。
- 错误分类一律用哨兵 + `errors.Is`，`server.go` 的 switch 靠哨兵判定，包装时必须用 `%w` 保住链路。响应体必须带可读 `error`，**不得扁平化为「操作失败」**（B45 立下的规矩）。
- SQLite 建表一律 `CREATE TABLE IF NOT EXISTS`，与 `tasks`/`events`/`tickets` 同形态。
- **不得 `git push`**，不得合并到 main，不得改 `docs/superpowers/backlog.md`（状态推进由审核者做）。
- **不得碰 `~/.handoff/`**，不得启停/重启/覆盖任何监听 7777 的 agentd——它持有正在跑的任务。真机验收必须用独立端口 + 独立 DataDir + 独立二进制 + 独立仓库副本。
- 全套闸门（每个 task 的最后一步都要跑前四条，Task 6 跑全部六条）：
  ```
  gofmt -l .
  go build ./...
  go vet ./...
  go test ./... -count=1
  go test -race ./cmd/ ./internal/agentd/ ./internal/store/ -count=1
  GOOS=windows GOARCH=amd64 go build ./...
  ```

## File Structure

| 文件 | 责任 |
|------|------|
| `internal/proto/proto.go`（改） | 新增 `Repo` 线类型——CLI / client / agentd / store 四方共用的登记条目形态 |
| `internal/agentd/reporegistry.go`（新） | **纯逻辑**：URL 归一化、路径 vs 登记名判别、dispatch 的仓库解析。不碰 DB、不碰 git、不碰 HTTP |
| `internal/agentd/reporegistry_test.go`（新） | 上述纯函数的表驱动测试 |
| `internal/store/repos.go`（新） | `repos` 表的 CRUD。与 `store.go` 分开：`store.go` 已承载三张表，再塞第四张会更难读 |
| `internal/store/repos_test.go`（新） | CRUD + UNIQUE 冲突路径 |
| `internal/store/store.go`（改） | `Open` 的 DDL 清单加 `repos` 建表；包头注释补第四张表 |
| `internal/agentd/repoadmin.go`（新） | agentd 侧的登记操作：登记已有克隆 / 克隆并登记 / 列出（带实际状态）/ 注销。会碰 store 与 git |
| `internal/agentd/server.go`（改） | `/api/repos` 三个端点 + handler；`dispatchRequest` 加 `origin_url`；`writeDispatchError` 加新哨兵分支 |
| `internal/agentd/manager.go`（改） | `DispatchReq` 加 `OriginURL`；`Dispatch` 前置块接入仓库解析 |
| `internal/config/config.go`（改） | `Config` 顶层加 `RepoRoot` |
| `internal/client/client.go`（改） | `DispatchOpts` 加 `OriginURL`；新增 `RepoAdd` / `RepoList` / `RepoRemove` |
| `cmd/repo.go`（新） | `handoff repo` 父命令 + `add` / `ls` / `rm` |
| `cmd/dispatch.go`（改） | 放开 `--repo` 必填；采集并上送 cwd 的 origin URL |
| `internal/agentd/integration_test.go`（改） | 端到端用例 |
| `README.md`（改） | 命令面与 `config.yaml` 字段说明补 `repo` 子命令与 `repo_root` |

---

### Task 1: 仓库解析纯函数与 URL 归一化

**Files:**
- Modify: `internal/proto/proto.go`（在 `Task` 结构之后追加 `Repo`）
- Create: `internal/agentd/reporegistry.go`
- Test: `internal/agentd/reporegistry_test.go`

**Interfaces:**
- Consumes: 无（本 task 是纯新增，不依赖任何前置 task）
- Produces:
  - `proto.Repo{Name, Path, OriginURL string; CreatedAt time.Time; Status string}`
  - `agentd.ErrRepoNotRegistered` / `agentd.ErrRepoAmbiguous`（`error` 哨兵）
  - `agentd.normalizeGitURL(raw string) string`
  - `agentd.looksLikePath(s string) bool`
  - `agentd.resolveRepoInput(input, originURL string, entries []proto.Repo) (string, error)`

- [ ] **Step 1: 加 `proto.Repo` 线类型**

在 `internal/proto/proto.go` 末尾追加：

```go
// Repo 是一条「执行机 × 仓库」登记：把该执行机上一个已落地的 git 仓库
// 与一个短名字绑定，使 dispatch 不必再写完整路径。
//
// 字段：
//   - Name: 登记名（每台执行机内唯一），dispatch 时可用作 --repo 的取值
//   - Path: 该执行机上仓库的绝对路径
//   - OriginURL: 仓库的 origin 地址，dispatch 省略 --repo 时据此自动匹配
//   - CreatedAt: 登记时间
//   - Status: repo ls 时**现场探得**的实际状态（"有效"/"路径不存在"/"不是 git 仓库"），
//     不落库，仅列表响应携带——它是登记与文件系统漂移的可见化手段
type Repo struct {
	Name      string    `json:"name"`
	Path      string    `json:"path"`
	OriginURL string    `json:"origin_url"`
	CreatedAt time.Time `json:"created_at"`
	Status    string    `json:"status,omitempty"`
}
```

- [ ] **Step 2: 写失败的测试**

创建 `internal/agentd/reporegistry_test.go`：

```go
package agentd

import (
	"errors"
	"testing"

	"github.com/xushixin/handoff/internal/proto"
)

// TestNormalizeGitURL 验证归一化把同一仓库的各种写法折叠成同一个串。
func TestNormalizeGitURL(t *testing.T) {
	const want = "github.com/xushixin/handoff"
	for _, raw := range []string{
		"git@github.com:xushixin/handoff.git",
		"git@github.com:xushixin/handoff",
		"https://github.com/xushixin/handoff.git",
		"https://github.com/xushixin/handoff/",
		"http://GitHub.com/xushixin/handoff.git",
		"ssh://git@github.com/xushixin/handoff.git",
		"ssh://git@github.com:22/xushixin/handoff.git",
	} {
		if got := normalizeGitURL(raw); got != want {
			t.Errorf("normalizeGitURL(%q) = %q, want %q", raw, got, want)
		}
	}
	if got := normalizeGitURL("   "); got != "" {
		t.Errorf("空白输入应归一化为空串，got %q", got)
	}
	// 不同仓库不得被折叠到一起
	if normalizeGitURL("git@github.com:a/x.git") == normalizeGitURL("git@github.com:b/x.git") {
		t.Error("不同 owner 的同名仓库被错误折叠")
	}
}

// TestLooksLikePath 验证路径与登记名的判别。
func TestLooksLikePath(t *testing.T) {
	for _, s := range []string{"/root/work/handoff", `C:\repos\x`, "C:/repos/x"} {
		if !looksLikePath(s) {
			t.Errorf("looksLikePath(%q) = false, want true", s)
		}
	}
	for _, s := range []string{"handoff", "my-repo", ""} {
		if looksLikePath(s) {
			t.Errorf("looksLikePath(%q) = true, want false", s)
		}
	}
}

// entriesFixture 是解析用例共用的登记表快照。
func entriesFixture() []proto.Repo {
	return []proto.Repo{
		{Name: "handoff", Path: "/root/work/handoff", OriginURL: "git@github.com:xushixin/handoff.git"},
		{Name: "tk", Path: "/root/work/tk", OriginURL: "https://github.com/xushixin/tk.git"},
		{Name: "handoff-2", Path: "/root/work/handoff-2", OriginURL: "https://github.com/xushixin/handoff"},
	}
}

// TestResolveRepoInput 覆盖三分支 × 命中数的全部组合。
func TestResolveRepoInput(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		originURL string
		entries   []proto.Repo
		wantPath  string
		wantErr   error
	}{
		{
			name: "含路径特征字符即当路径，不查登记表",
			input: "/some/where/else", originURL: "git@github.com:xushixin/handoff.git",
			entries: entriesFixture(), wantPath: "/some/where/else",
		},
		{
			name: "登记表为空时路径依然直通",
			input: "/some/where/else", entries: nil, wantPath: "/some/where/else",
		},
		{
			name:  "短名命中登记",
			input: "tk", entries: entriesFixture(), wantPath: "/root/work/tk",
		},
		{
			name:  "短名查不到",
			input: "nope", entries: entriesFixture(), wantErr: ErrRepoNotRegistered,
		},
		{
			name:      "省略 --repo 且 origin 唯一命中",
			originURL: "https://github.com/xushixin/tk", entries: entriesFixture(),
			wantPath: "/root/work/tk",
		},
		{
			name:      "省略 --repo 且 origin 多命中",
			originURL: "git@github.com:xushixin/handoff.git", entries: entriesFixture(),
			wantErr: ErrRepoAmbiguous,
		},
		{
			name:      "省略 --repo 且 origin 零命中",
			originURL: "git@github.com:other/thing.git", entries: entriesFixture(),
			wantErr: ErrRepoNotRegistered,
		},
		{
			name:    "省略 --repo 且 cwd 不是 git 仓库",
			entries: entriesFixture(), wantErr: errBadDispatchRequest,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveRepoInput(tt.input, tt.originURL, tt.entries)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("err = %v, want errors.Is(..., %v)", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("意外错误: %v", err)
			}
			if got != tt.wantPath {
				t.Fatalf("path = %q, want %q", got, tt.wantPath)
			}
		})
	}
}

// TestResolveRepoInputErrorsAreActionable 验证拒绝报文带得走「本机登记了什么」，
// 而不是一句干巴巴的「未登记」——这是审核者读不到执行机日志时的唯一线索。
func TestResolveRepoInputErrorsAreActionable(t *testing.T) {
	_, err := resolveRepoInput("nope", "", entriesFixture())
	for _, want := range []string{"nope", "handoff", "tk"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("报文 %q 未包含 %q", err.Error(), want)
		}
	}
	_, err = resolveRepoInput("", "git@github.com:xushixin/handoff.git", entriesFixture())
	for _, want := range []string{"handoff", "handoff-2"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("歧义报文 %q 未列出候选 %q", err.Error(), want)
		}
	}
}
```

记得在 import 块加 `"strings"`。

- [ ] **Step 3: 跑测试确认失败**

Run: `go test ./internal/agentd/ -run 'TestNormalizeGitURL|TestLooksLikePath|TestResolveRepoInput' -count=1`
Expected: 编译失败，`undefined: normalizeGitURL` / `undefined: looksLikePath` / `undefined: resolveRepoInput` / `undefined: ErrRepoNotRegistered`

- [ ] **Step 4: 写实现**

创建 `internal/agentd/reporegistry.go`：

```go
// 本文件是仓库登记的**纯逻辑**层：把「用户在 --repo 里写了什么」翻译成
// 「executor 应该在哪个目录工作」。
//
// 职责：
//   - normalizeGitURL：把同一仓库的各种 URL 写法折叠成可比对的规范串
//   - looksLikePath：判别用户输入是路径还是登记名
//   - resolveRepoInput：按「路径 / 登记名 / 省略」三分支解析出仓库路径
//
// 边界：
//   - 不碰数据库：登记条目由调用方查好后以切片传入
//   - 不碰 git、不碰文件系统：路径是否真的存在由 EnsureRepoUsable 另行判定
//   - 不碰 HTTP：错误只用哨兵表达，状态码映射在 server.go
//
// 为什么单独成文件且刻意保持纯净：这段规则是 dispatch 的必经之路，一旦错了
// 就会把任务派到错误的仓库上。纯函数才能表驱动穷举 + 变异检验。
package agentd

import (
	"errors"
	"fmt"
	"strings"

	"github.com/xushixin/handoff/internal/proto"
)

// 错误哨兵：
//   - ErrRepoNotRegistered：按名字查不到，或省略 --repo 时 origin 零命中
//   - ErrRepoAmbiguous：省略 --repo 时 origin 匹配到多条登记
//
// 两者都映射为 400（调用方先解决请求本身的问题），见 server.go 的 writeDispatchError。
var (
	ErrRepoNotRegistered = errors.New("仓库未登记")
	ErrRepoAmbiguous     = errors.New("origin 匹配到多条登记，无法自动选择")
)

// pathRunes 是「这是一个路径而不是登记名」的特征字符集合。
//
// 为什么不止 '/'：类 Unix 执行机上只判 '/' 已经够用，但 Windows 绝对路径
// C:\repos\x 既不含 '/' 也会被误判成登记名。多这两个字符可以让规则不依赖
// B37（prochost Windows 实现）的搁置状态。反向误判不成立：登记名由 origin
// 末段派生或人工指定，不会含这三个字符。
const pathRunes = `/\:`

// looksLikePath 报告 s 是否应被当作路径处理。
//
// 参数：
//   - s: 用户在 --repo 里写的原始字符串
//
// 返回：
//   - true=当路径（走今天的原有行为，不碰登记表）；false=当登记名
func looksLikePath(s string) bool {
	return strings.ContainsAny(s, pathRunes)
}

// normalizeGitURL 把 git 远程地址折叠成可比对的规范串。
//
// 参数：
//   - raw: 原始 URL，如 git@github.com:xushixin/handoff.git
//
// 返回：
//   - 规范串，如 github.com/xushixin/handoff；输入为空白时返回空串
//
// 注意：
//   - 仅用于**比对**，登记表里存的始终是原始 URL
//   - 只把首段（host）转小写：路径段在部分 git 服务端是大小写敏感的，
//     整串转小写有把两个不同仓库折叠到一起的风险
//   - 不做的事：不解析 DNS、不做 host 别名等价（github.com 与其镜像不视为同一个）
func normalizeGitURL(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	// 1) 剥 scheme
	for _, p := range []string{"ssh://", "git://", "https://", "http://"} {
		if len(s) >= len(p) && strings.EqualFold(s[:len(p)], p) {
			s = s[len(p):]
			break
		}
	}
	// 2) 剥 user@ 前缀（只在首个 '/' 之前找 '@'，避免误伤路径里的 '@'）
	if i := strings.IndexByte(s, '@'); i >= 0 {
		if j := strings.IndexByte(s, '/'); j < 0 || i < j {
			s = s[i+1:]
		}
	}
	// 3) 首个 '/' 之前的 ':' 有两种含义，分别处理：
	//    - scp-like 分隔符（github.com:owner/repo）→ 换成 '/'
	//    - 端口（github.com:22/owner/repo）→ 整段丢弃
	//    不处理的话，同一仓库的 ssh 与 https 写法永远匹配不上。
	if c := strings.IndexByte(s, ':'); c >= 0 {
		slash := strings.IndexByte(s, '/')
		if slash < 0 || c < slash {
			rest := s[c+1:]
			seg := rest
			if k := strings.IndexByte(rest, '/'); k >= 0 {
				seg = rest[:k]
			}
			if seg != "" && strings.IndexFunc(seg, func(r rune) bool { return r < '0' || r > '9' }) < 0 {
				// 纯数字=端口，连同它一起丢掉
				s = s[:c] + rest[len(seg):]
			} else {
				s = s[:c] + "/" + rest
			}
		}
	}
	// 4) 去尾部 '/' 与 '.git'（顺序不能反：形如 ".../repo.git/" 两者都要去掉）
	s = strings.TrimRight(s, "/")
	s = strings.TrimSuffix(s, ".git")
	s = strings.TrimRight(s, "/")
	// 5) 首段（host）转小写
	if i := strings.IndexByte(s, '/'); i > 0 {
		s = strings.ToLower(s[:i]) + s[i:]
	} else {
		s = strings.ToLower(s)
	}
	return s
}

// repoNames 把登记条目压成一行逗号分隔的名字串，供拒绝报文使用。
// 报文必须带得走「本机登记了什么」——远程派发时审核者读不到执行机的
// agentd.log，一句干巴巴的「未登记」等于让他去猜。
func repoNames(entries []proto.Repo) string {
	if len(entries) == 0 {
		return "（本机尚无任何登记）"
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name)
	}
	return strings.Join(names, ", ")
}

// resolveRepoInput 把用户输入解析成执行机上的仓库路径。
//
// 参数：
//   - input: 用户 --repo 的原始取值（路径 / 登记名 / 空）
//   - originURL: 审核者 cwd 的 origin 地址；cwd 不是 git 仓库时为空
//   - entries: 本机全部登记条目
//
// 返回：
//   - 解析出的仓库路径
//   - 错误：ErrRepoNotRegistered / ErrRepoAmbiguous / errBadDispatchRequest，
//     均映射 400，且报文自带可行动线索（已登记的名字或候选清单）
//
// 注意：
//   - input 含路径特征字符时**完全绕开登记表**，保持今天的行为不变
//   - 本函数不判断路径是否真的存在，那是 EnsureRepoUsable 的职责
func resolveRepoInput(input, originURL string, entries []proto.Repo) (string, error) {
	if looksLikePath(input) {
		log().Info("仓库解析：按路径直通", "input", input)
		return input, nil
	}
	if input != "" {
		for _, e := range entries {
			if e.Name == input {
				log().Info("仓库解析：登记名命中", "name", input, "path", e.Path)
				return e.Path, nil
			}
		}
		log().Warn("仓库解析被拒：登记名查不到", "name", input, "registered", repoNames(entries))
		return "", fmt.Errorf("%w: %q；本机已登记的仓库：%s（用 handoff repo ls 查看，或 handoff repo add 先落地）",
			ErrRepoNotRegistered, input, repoNames(entries))
	}
	if originURL == "" {
		log().Warn("仓库解析被拒：未给 --repo 且无 origin 可匹配")
		return "", fmt.Errorf("%w: 未指定 --repo，且当前目录不是 git 仓库，无法自动匹配已登记仓库",
			errBadDispatchRequest)
	}
	want := normalizeGitURL(originURL)
	var hits []proto.Repo
	for _, e := range entries {
		if normalizeGitURL(e.OriginURL) == want {
			hits = append(hits, e)
		}
	}
	switch len(hits) {
	case 1:
		log().Info("仓库解析：origin 唯一命中", "origin", originURL,
			"name", hits[0].Name, "path", hits[0].Path)
		return hits[0].Path, nil
	case 0:
		log().Warn("仓库解析被拒：origin 零命中", "origin", originURL, "registered", repoNames(entries))
		return "", fmt.Errorf("%w: 当前仓库 %s 尚未登记到本机；本机已登记的仓库：%s（用 handoff repo add 落地它）",
			ErrRepoNotRegistered, originURL, repoNames(entries))
	default:
		log().Warn("仓库解析被拒：origin 多命中", "origin", originURL, "candidates", repoNames(hits))
		return "", fmt.Errorf("%w: 当前仓库 %s 在本机登记了 %d 处：%s；请用 --repo <名字> 指定",
			ErrRepoAmbiguous, originURL, len(hits), repoNames(hits))
	}
}
```

- [ ] **Step 5: 跑测试确认通过**

Run: `go test ./internal/agentd/ -run 'TestNormalizeGitURL|TestLooksLikePath|TestResolveRepoInput' -count=1 -v`
Expected: 全部 PASS

- [ ] **Step 6: 变异检验（必做，不得跳过）**

把 `resolveRepoInput` 开头的三行注释掉：

```go
	// if looksLikePath(input) {
	// 	log().Info("仓库解析：按路径直通", "input", input)
	// 	return input, nil
	// }
```

Run: `go test ./internal/agentd/ -run TestResolveRepoInput -count=1`
Expected: **FAIL**，「含路径特征字符即当路径」用例报错。把失败输出原样记进报告。

如果它是**绿的**，说明这套测试没有真正锁住解析规则——**停下来报告这个事实**，不要自己想办法让它红。

恢复注释后：

Run: `go test ./internal/agentd/ -count=1 && git diff --exit-code`
Expected: PASS 且 `git diff --exit-code` 无输出（工作区干净，无残留变异）

- [ ] **Step 7: 关键节点日志自检**

`resolveRepoInput` 是 dispatch 的必经岔路，每条出口都必须留下「为什么走到这」的痕迹——远程派发时审核者读不到执行机日志之外的任何东西。逐条对照实现：

- 路径直通分支：Info「仓库解析：按路径直通」带 `input`
- 登记名命中：Info 带 `name` + `path`
- 登记名查不到：Warn 带 `name` + 已登记清单
- origin 唯一命中：Info 带 `origin` + 选中的 `name`/`path`
- origin 零命中 / 多命中：Warn 带 `origin` + 清单/候选
- 无 `--repo` 且无 origin：Warn

**成功路径也必须有日志**——只在失败时打，就分不清「解析成功走了哪条分支」和「压根没走到这」。用 `log()`（本包既有，返回 `slog.Default()`），不得用 `fmt.Printf`。

- [ ] **Step 8: 注释自检**

- `reporegistry.go` 文件头：职责（三个纯函数）+ 边界（不碰 DB/git/HTTP）+ **为什么刻意保持纯净**（可表驱动穷举 + 变异检验）
- 导出/包内关键函数各有 doc 注释：参数、返回、注意
- 「为什么」型行内注释至少覆盖三处：`pathRunes` 为什么是三个字符（Windows 绝对路径）、`normalizeGitURL` 第 3 步 `:` 的两种含义（scp 分隔符 vs 端口）、`repoNames` 为什么要把清单塞进报文（审核者读不到执行机日志）

- [ ] **Step 9: 跑闸门并提交**

```bash
gofmt -l . && go build ./... && go vet ./... && go test ./... -count=1
git add internal/proto/proto.go internal/agentd/reporegistry.go internal/agentd/reporegistry_test.go
git commit -m "feat(b46): 仓库解析纯函数与 git URL 归一化"
```

---

### Task 2: `repos` 表与 store CRUD

**Files:**
- Create: `internal/store/repos.go`
- Modify: `internal/store/store.go`（`Open` 的 DDL 清单 + 包头注释）
- Test: `internal/store/repos_test.go`

**Interfaces:**
- Consumes: `proto.Repo{Name, Path, OriginURL string; CreatedAt time.Time; Status string}`（Task 1 产出）
- Produces:
  - `store.ErrRepoDuplicate`（`error` 哨兵）
  - `(*store.Store).CreateRepo(r *proto.Repo) error`
  - `(*store.Store).ListRepos() ([]proto.Repo, error)`
  - `(*store.Store).GetRepoByName(name string) (proto.Repo, error)`（不存在时 `store.ErrNotFound`）
  - `(*store.Store).DeleteRepo(name string) error`（不存在时 `store.ErrNotFound`）
  - `(*store.Store).ActiveTasksByRepoPath(repoPath string) ([]proto.Task, error)`

- [ ] **Step 1: 写失败的测试**

创建 `internal/store/repos_test.go`：

```go
package store

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/xushixin/handoff/internal/proto"
)

// openTestStore 开一个临时库。
func openTestStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

// TestRepoCRUD 覆盖登记的写入、读取、列出、删除。
func TestRepoCRUD(t *testing.T) {
	st := openTestStore(t)
	now := time.Now()
	r := &proto.Repo{Name: "handoff", Path: "/root/work/handoff",
		OriginURL: "git@github.com:xushixin/handoff.git", CreatedAt: now}
	if err := st.CreateRepo(r); err != nil {
		t.Fatalf("CreateRepo: %v", err)
	}
	got, err := st.GetRepoByName("handoff")
	if err != nil {
		t.Fatalf("GetRepoByName: %v", err)
	}
	if got.Path != r.Path || got.OriginURL != r.OriginURL {
		t.Fatalf("回读不一致: %+v", got)
	}
	if got.CreatedAt.IsZero() {
		t.Error("CreatedAt 回读为零值")
	}
	list, err := st.ListRepos()
	if err != nil {
		t.Fatalf("ListRepos: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("ListRepos 返回 %d 条，want 1", len(list))
	}
	if err := st.DeleteRepo("handoff"); err != nil {
		t.Fatalf("DeleteRepo: %v", err)
	}
	if _, err := st.GetRepoByName("handoff"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("删除后 Get 应为 ErrNotFound，got %v", err)
	}
}

// TestGetRepoByNameNotFound 验证查不到时返回 ErrNotFound 而不是零值。
func TestGetRepoByNameNotFound(t *testing.T) {
	st := openTestStore(t)
	if _, err := st.GetRepoByName("nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

// TestDeleteRepoNotFound 验证删不存在的登记时报 ErrNotFound（而非静默成功）。
func TestDeleteRepoNotFound(t *testing.T) {
	st := openTestStore(t)
	if err := st.DeleteRepo("nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

// TestCreateRepoDuplicateName 验证重名冲突归到 ErrRepoDuplicate。
func TestCreateRepoDuplicateName(t *testing.T) {
	st := openTestStore(t)
	now := time.Now()
	if err := st.CreateRepo(&proto.Repo{Name: "a", Path: "/p1", OriginURL: "u1", CreatedAt: now}); err != nil {
		t.Fatalf("首次写入: %v", err)
	}
	err := st.CreateRepo(&proto.Repo{Name: "a", Path: "/p2", OriginURL: "u2", CreatedAt: now})
	if !errors.Is(err, ErrRepoDuplicate) {
		t.Fatalf("err = %v, want ErrRepoDuplicate", err)
	}
}

// TestCreateRepoDuplicatePath 验证同一路径不得被登记两次——两个名字指向同一
// 路径会同时破坏 origin 自动匹配与工作目录占用判定。
func TestCreateRepoDuplicatePath(t *testing.T) {
	st := openTestStore(t)
	now := time.Now()
	if err := st.CreateRepo(&proto.Repo{Name: "a", Path: "/p", OriginURL: "u1", CreatedAt: now}); err != nil {
		t.Fatalf("首次写入: %v", err)
	}
	err := st.CreateRepo(&proto.Repo{Name: "b", Path: "/p", OriginURL: "u2", CreatedAt: now})
	if !errors.Is(err, ErrRepoDuplicate) {
		t.Fatalf("err = %v, want ErrRepoDuplicate", err)
	}
}

// TestActiveTasksByRepoPath 验证只返回该仓库下的非终态任务。
func TestActiveTasksByRepoPath(t *testing.T) {
	st := openTestStore(t)
	now := time.Now()
	mk := func(id string, state proto.TaskState) {
		if err := st.CreateTask(&proto.Task{ID: id, RepoPath: "/root/work/handoff",
			State: state, CreatedAt: now, UpdatedAt: now}); err != nil {
			t.Fatalf("CreateTask %s: %v", id, err)
		}
	}
	mk("t-running", proto.TaskStateRunning)
	mk("t-done", proto.TaskStateCompleted)
	if err := st.CreateTask(&proto.Task{ID: "t-other", RepoPath: "/elsewhere",
		State: proto.TaskStateRunning, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("CreateTask t-other: %v", err)
	}
	tasks, err := st.ActiveTasksByRepoPath("/root/work/handoff")
	if err != nil {
		t.Fatalf("ActiveTasksByRepoPath: %v", err)
	}
	if len(tasks) != 1 || tasks[0].ID != "t-running" {
		t.Fatalf("got %+v, want 仅 t-running", tasks)
	}
	if got, _ := st.ActiveTasksByRepoPath(""); len(got) != 0 {
		t.Error("空路径应返回空切片")
	}
}
```

用到的既有符号都已核对存在：`store.Open(path) (*Store, error)`、`(*Store).Close()`、`proto.TaskStateRunning` / `proto.TaskStateCompleted`、`proto.TerminalStates`、`taskColumns` 与 `scanTaskRow(rowScanner)`（`store.go:173-189`）。

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/store/ -run 'TestRepo|TestGetRepo|TestDeleteRepo|TestCreateRepo|TestActiveTasksByRepoPath' -count=1`
Expected: 编译失败，`st.CreateRepo undefined` 等

- [ ] **Step 3: 加建表 DDL**

在 `internal/store/store.go` 的 `Open` 中，`for _, ddl := range []string{...}` 的 `tickets` 建表之后追加一项：

```go
		`CREATE TABLE IF NOT EXISTS repos (
  name TEXT PRIMARY KEY, path TEXT NOT NULL UNIQUE,
  origin_url TEXT NOT NULL, created_at TIMESTAMP NOT NULL)`,
```

并把包头注释里的「三张表」改为四张：

```go
//   - 提供任务（tasks）、事件（events）、工单（tickets）、仓库登记（repos）四张表的建表与增删改查
```

- [ ] **Step 4: 写 CRUD 实现**

创建 `internal/store/repos.go`：

```go
// 本文件是 repos 表（执行机 × 仓库登记）的持久化实现。
//
// 职责：
//   - repos 表的增（CreateRepo）、查（GetRepoByName / ListRepos）、删（DeleteRepo）
//   - 把 SQLite 的 UNIQUE 冲突翻译成 ErrRepoDuplicate 哨兵，供上层映射 409
//   - ActiveTasksByRepoPath：按仓库路径查活跃任务，供注销登记前的占用校验
//
// 边界：
//   - 不判断路径是否真的存在、是不是 git 仓库——那是 agentd 侧 EnsureRepoUsable 的事
//   - 不做名字派生、不做 URL 归一化——那是 agentd/reporegistry.go 的事
//   - 与 store.go 一致的叶子层纪律：方法错误 return 前不打日志，由调用方带上下文记录
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/xushixin/handoff/internal/proto"
)

// ErrRepoDuplicate 表示登记冲突：名字已被占用，或该路径已被另一条登记指向。
//
// 为什么路径也算冲突：两个名字指向同一路径会让 origin 自动匹配产生假歧义，
// 也会让「注销前检查占用」漏掉另一个名字下的活跃任务。
var ErrRepoDuplicate = errors.New("仓库登记冲突（名字或路径已存在）")

// repoColumns 是 repos 表的完整读取列清单，Get 与 List 共用同一份。
const repoColumns = `name, path, origin_url, created_at`

// scanRepoRow 把一行 repos 记录读成 proto.Repo。
func scanRepoRow(sc rowScanner) (proto.Repo, error) {
	var (
		r         proto.Repo
		createdAt string
	)
	if err := sc.Scan(&r.Name, &r.Path, &r.OriginURL, &createdAt); err != nil {
		return proto.Repo{}, err
	}
	r.CreatedAt = parseTime(createdAt)
	return r, nil
}

// CreateRepo 写入一条仓库登记。
//
// 参数：
//   - r: 登记条目；Name/Path/OriginURL 必须非空，CreatedAt 由调用方给定
//
// 返回：
//   - 错误：名字或路径已存在时返回包装了 ErrRepoDuplicate 的错误；其余为写库故障
func (s *Store) CreateRepo(r *proto.Repo) error {
	_, err := s.db.ExecContext(context.Background(),
		`INSERT INTO repos (name, path, origin_url, created_at) VALUES (?, ?, ?, ?)`,
		r.Name, r.Path, r.OriginURL, fmtTime(r.CreatedAt))
	if err != nil {
		// modernc.org/sqlite 的唯一约束错误文本形如
		// "constraint failed: UNIQUE constraint failed: repos.name (2067)"，
		// 没有可用的错误码常量，只能按文本判定。
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return fmt.Errorf("%w: name=%s path=%s: %v", ErrRepoDuplicate, r.Name, r.Path, err)
		}
		return fmt.Errorf("写入仓库登记 %s: %w", r.Name, err)
	}
	return nil
}

// GetRepoByName 按登记名查询单条登记。
//
// 返回：
//   - 登记条目；不存在时返回 ErrNotFound
func (s *Store) GetRepoByName(name string) (proto.Repo, error) {
	row := s.db.QueryRowContext(context.Background(),
		`SELECT `+repoColumns+` FROM repos WHERE name = ?`, name)
	r, err := scanRepoRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return proto.Repo{}, fmt.Errorf("仓库登记 %s: %w", name, ErrNotFound)
	}
	if err != nil {
		return proto.Repo{}, fmt.Errorf("查询仓库登记 %s: %w", name, err)
	}
	return r, nil
}

// ListRepos 返回全部登记，按名字字典序。
//
// 注意：
//   - 返回的 Status 字段恒为空——实际状态由 agentd 侧现场探测后填充
func (s *Store) ListRepos() ([]proto.Repo, error) {
	rows, err := s.db.QueryContext(context.Background(),
		`SELECT `+repoColumns+` FROM repos ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("查询仓库登记列表: %w", err)
	}
	defer rows.Close()
	var repos []proto.Repo
	for rows.Next() {
		r, err := scanRepoRow(rows)
		if err != nil {
			return nil, fmt.Errorf("读取仓库登记行: %w", err)
		}
		repos = append(repos, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历仓库登记: %w", err)
	}
	return repos, nil
}

// DeleteRepo 删除一条登记。
//
// 返回：
//   - 错误：登记不存在时返回 ErrNotFound（而非静默成功——调用方需要知道自己删错了名字）
//
// 注意：
//   - 只删登记，**不动磁盘上的仓库**
func (s *Store) DeleteRepo(name string) error {
	res, err := s.db.ExecContext(context.Background(), `DELETE FROM repos WHERE name = ?`, name)
	if err != nil {
		return fmt.Errorf("删除仓库登记 %s: %w", name, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("删除仓库登记 %s 后取影响行数: %w", name, err)
	}
	if n == 0 {
		return fmt.Errorf("仓库登记 %s: %w", name, ErrNotFound)
	}
	return nil
}

// ActiveTasksByRepoPath 返回仓库路径为 repoPath 的全部非终态任务。
//
// 参数：
//   - repoPath: 仓库绝对路径；空串返回空切片
//
// 注意：
//   - 与 ActiveTasksByWorkDir 的区别：那个按**工作目录**判定（managed worktree
//     各不相同），本方法按**仓库**判定——注销一条登记会影响这个仓库下的所有
//     任务，包括从它长出来的 managed worktree
func (s *Store) ActiveTasksByRepoPath(repoPath string) ([]proto.Task, error) {
	if repoPath == "" {
		return nil, nil
	}
	placeholders := make([]string, len(proto.TerminalStates))
	args := []any{repoPath}
	for i, st := range proto.TerminalStates {
		placeholders[i] = "?"
		args = append(args, string(st))
	}
	rows, err := s.db.QueryContext(context.Background(),
		`SELECT `+taskColumns+` FROM tasks
WHERE repo_path = ? AND state NOT IN (`+strings.Join(placeholders, ", ")+`)
ORDER BY created_at DESC`, args...)
	if err != nil {
		return nil, fmt.Errorf("查询仓库 %s 的活跃任务: %w", repoPath, err)
	}
	defer rows.Close()
	var tasks []proto.Task
	for rows.Next() {
		task, err := scanTaskRow(rows)
		if err != nil {
			return nil, fmt.Errorf("读取活跃任务行: %w", err)
		}
		tasks = append(tasks, task)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历活跃任务: %w", err)
	}
	return tasks, nil
}
```

- [ ] **Step 5: 跑测试确认通过**

Run: `go test ./internal/store/ -count=1 -v`
Expected: 新增用例全部 PASS，既有用例不受影响

- [ ] **Step 6: 日志与注释自检**

**日志**：store 是叶子层，与 `store.go` 既有方法保持同一纪律——**方法内不打日志**，错误用 `fmt.Errorf("...: %w", err)` 带足上下文（表名/名字/路径）后 return，由 agentd 侧调用方带业务上下文记录。这是刻意的一致性，不是遗漏；照 `CreateTask` / `ListTasks` 的写法办。

**注释**：
- `repos.go` 文件头：职责（CRUD + UNIQUE 冲突翻译 + 活跃任务查询）+ 边界（不判仓库有效性、不做名字派生/URL 归一化、方法内不打日志）
- 每个导出方法有 doc 注释，`DeleteRepo` 必须写明「只删登记，不动磁盘」
- 「为什么」型注释至少两处：`ErrRepoDuplicate` 为什么把路径冲突也算进去（假歧义 + 漏检占用）、`CreateRepo` 为什么靠错误文本判 UNIQUE（modernc 驱动没有可用错误码常量）
- `ActiveTasksByRepoPath` 的 doc 必须讲清它与 `ActiveTasksByWorkDir` 的区别（按仓库 vs 按工作目录）

- [ ] **Step 7: 跑闸门并提交**

```bash
gofmt -l . && go build ./... && go vet ./... && go test ./... -count=1
git add internal/store/repos.go internal/store/repos_test.go internal/store/store.go
git commit -m "feat(b46): repos 表与 store CRUD"
```

---

### Task 3: agentd 侧的登记操作

**Files:**
- Create: `internal/agentd/repoadmin.go`
- Modify: `internal/config/config.go`（`Config` 加 `RepoRoot`）
- Test: `internal/agentd/repoadmin_test.go`

**Interfaces:**
- Consumes:
  - `store.ErrRepoDuplicate`、`(*store.Store).CreateRepo/ListRepos/GetRepoByName/DeleteRepo/ActiveTasksByRepoPath`（Task 2 产出）
  - `proto.Repo`（Task 1 产出）
  - 既有：`agentd.EnsureRepoUsable(ctx, repo string) error`（`workspace.go:357`）、`agentd.gitRun(ctx, repo string, args ...string) (stdout, stderr string, err error)`（`workspace.go:92`）、`agentd.ErrRepoUnusable`、`agentd.ErrWorkdirBusy`
- Produces:
  - `agentd.ErrRepoAlreadyExists`（`error` 哨兵）
  - `agentd.RegisterRepoReq{Name, Path, URL string; Clone bool}`
  - `(*Manager).RegisterRepo(ctx context.Context, req RegisterRepoReq) (proto.Repo, error)`
  - `(*Manager).ListRepos(ctx context.Context) ([]proto.Repo, error)`
  - `(*Manager).UnregisterRepo(ctx context.Context, name string) error`
  - `agentd.repoOriginURL(ctx context.Context, repo string) (string, error)`
  - `agentd.repoNameFromURL(url string) string`

- [ ] **Step 1: 给 Config 加 RepoRoot**

在 `internal/config/config.go` 的 `Config` 结构中，`DataDir` 之后加：

```go
	// RepoRoot 是 repo add --clone 未显式指定路径时的默认落点根目录，
	// 实际落点为 RepoRoot/<登记名>。空=未配置，此时 --clone 必须显式给路径。
	//
	// 为什么放顶层而不是放进 Target：Target 是在**审核者本地**被读取的
	//（见 cmd/pull.go 的 cfg.Targets[task.Target]），放那儿会让「仓库放哪」
	// 变成审核者的本地状态，换一台审核机接管就得重配。放顶层的语义是
	// 「每台执行机自己决定它的仓库放在哪」。
	RepoRoot string
```

不要给它默认值：未配置时 `--clone` 必须显式给路径，这是有意为之的响亮失败。

- [ ] **Step 2: 写失败的测试**

创建 `internal/agentd/repoadmin_test.go`：

```go
package agentd

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// initBareOrigin 建一个可 clone 的裸仓库，返回其路径。
func initBareOrigin(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "origin.git")
	if out, err := exec.Command("git", "init", "--bare", dir).CombinedOutput(); err != nil {
		t.Fatalf("git init --bare: %v: %s", err, out)
	}
	return dir
}

// initWorkRepo 建一个带 origin 且有一个提交的工作仓库，返回其路径。
func initWorkRepo(t *testing.T, origin string) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		c := exec.Command("git", append([]string{"-C", dir}, args...)...)
		c.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	run("init")
	run("remote", "add", "origin", origin)
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "a.txt")
	run("commit", "-m", "init")
	return dir
}

// TestRepoOriginURL 验证能从仓库读出 origin。
func TestRepoOriginURL(t *testing.T) {
	origin := initBareOrigin(t)
	repo := initWorkRepo(t, origin)
	got, err := repoOriginURL(context.Background(), repo)
	if err != nil {
		t.Fatalf("repoOriginURL: %v", err)
	}
	if got != origin {
		t.Fatalf("origin = %q, want %q", got, origin)
	}
}

// TestRepoOriginURLNoRemote 验证没有 origin 的仓库报可读错误。
func TestRepoOriginURLNoRemote(t *testing.T) {
	dir := t.TempDir()
	if out, err := exec.Command("git", "init", dir).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	if _, err := repoOriginURL(context.Background(), dir); !errors.Is(err, ErrRepoUnusable) {
		t.Fatalf("err = %v, want ErrRepoUnusable", err)
	}
}

// TestRepoNameFromURL 验证登记名的缺省派生。
func TestRepoNameFromURL(t *testing.T) {
	cases := map[string]string{
		"git@github.com:xushixin/handoff.git":     "handoff",
		"https://github.com/xushixin/handoff":     "handoff",
		"https://github.com/xushixin/handoff.git": "handoff",
		"/tmp/whatever/origin.git":                "origin",
	}
	for in, want := range cases {
		if got := repoNameFromURL(in); got != want {
			t.Errorf("repoNameFromURL(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestRegisterExistingReadsOriginFromDisk 验证形态一：
// origin 由 agentd 在执行机上现读，登记名可省。
func TestRegisterExistingReadsOriginFromDisk(t *testing.T) {
	origin := initBareOrigin(t)
	repo := initWorkRepo(t, origin)
	m := newRepoTestManager(t)
	got, err := m.RegisterRepo(context.Background(), RegisterRepoReq{Path: repo})
	if err != nil {
		t.Fatalf("RegisterRepo: %v", err)
	}
	if got.OriginURL != origin {
		t.Fatalf("OriginURL = %q, want 现读的 %q", got.OriginURL, origin)
	}
	if got.Name != "origin" {
		t.Fatalf("Name = %q, want 由 origin 末段派生的 %q", got.Name, "origin")
	}
}

// TestRegisterExistingRejectsNonRepo 验证非 git 路径拒绝登记，不留空壳。
func TestRegisterExistingRejectsNonRepo(t *testing.T) {
	m := newRepoTestManager(t)
	_, err := m.RegisterRepo(context.Background(), RegisterRepoReq{Name: "x", Path: t.TempDir()})
	if !errors.Is(err, ErrRepoUnusable) {
		t.Fatalf("err = %v, want ErrRepoUnusable", err)
	}
	list, _ := m.ListRepos(context.Background())
	if len(list) != 0 {
		t.Fatalf("拒绝后不应留下登记，got %+v", list)
	}
}

// TestCloneAndRegister 验证形态二：clone 成功后才落库。
func TestCloneAndRegister(t *testing.T) {
	origin := initBareOrigin(t)
	work := initWorkRepo(t, origin)
	if out, err := exec.Command("git", "-C", work, "push", "origin", "HEAD").CombinedOutput(); err != nil {
		t.Fatalf("push: %v: %s", err, out)
	}
	m := newRepoTestManager(t)
	dest := filepath.Join(t.TempDir(), "landed")
	got, err := m.RegisterRepo(context.Background(),
		RegisterRepoReq{Name: "landed", Path: dest, URL: origin, Clone: true})
	if err != nil {
		t.Fatalf("RegisterRepo clone: %v", err)
	}
	if got.Path != dest {
		t.Fatalf("Path = %q, want %q", got.Path, dest)
	}
	if err := EnsureRepoUsable(context.Background(), dest); err != nil {
		t.Fatalf("clone 出来的目录不是可用仓库: %v", err)
	}
}

// TestCloneRefusesExistingPath 验证落点已存在时拒绝，绝不覆盖。
func TestCloneRefusesExistingPath(t *testing.T) {
	origin := initBareOrigin(t)
	m := newRepoTestManager(t)
	dest := t.TempDir() // 已存在
	_, err := m.RegisterRepo(context.Background(),
		RegisterRepoReq{Name: "x", Path: dest, URL: origin, Clone: true})
	if !errors.Is(err, ErrRepoAlreadyExists) {
		t.Fatalf("err = %v, want ErrRepoAlreadyExists", err)
	}
}

// TestCloneFailureLeavesNoRegistration 验证安全边界 3：
// clone 失败时登记表里不得有残留记录。
func TestCloneFailureLeavesNoRegistration(t *testing.T) {
	m := newRepoTestManager(t)
	dest := filepath.Join(t.TempDir(), "nope")
	_, err := m.RegisterRepo(context.Background(), RegisterRepoReq{
		Name: "x", Path: dest, URL: filepath.Join(t.TempDir(), "does-not-exist.git"), Clone: true})
	if err == nil {
		t.Fatal("clone 不存在的 URL 应当失败")
	}
	list, _ := m.ListRepos(context.Background())
	if len(list) != 0 {
		t.Fatalf("clone 失败后不应留下登记，got %+v", list)
	}
}

// TestListReposReportsStatus 验证 ls 现场探测实际状态（漂移可见化）。
func TestListReposReportsStatus(t *testing.T) {
	origin := initBareOrigin(t)
	repo := initWorkRepo(t, origin)
	m := newRepoTestManager(t)
	if _, err := m.RegisterRepo(context.Background(),
		RegisterRepoReq{Name: "ok", Path: repo}); err != nil {
		t.Fatalf("RegisterRepo: %v", err)
	}
	if err := os.RemoveAll(repo); err != nil {
		t.Fatal(err)
	}
	list, err := m.ListRepos(context.Background())
	if err != nil {
		t.Fatalf("ListRepos: %v", err)
	}
	if len(list) != 1 || list[0].Status == repoStatusOK {
		t.Fatalf("路径被删后 Status 不应为「有效」，got %+v", list)
	}
}

// TestUnregisterKeepsDiskRepo 验证安全边界 4：只删登记，不动磁盘。
func TestUnregisterKeepsDiskRepo(t *testing.T) {
	origin := initBareOrigin(t)
	repo := initWorkRepo(t, origin)
	m := newRepoTestManager(t)
	if _, err := m.RegisterRepo(context.Background(),
		RegisterRepoReq{Name: "keep", Path: repo}); err != nil {
		t.Fatalf("RegisterRepo: %v", err)
	}
	if err := m.UnregisterRepo(context.Background(), "keep"); err != nil {
		t.Fatalf("UnregisterRepo: %v", err)
	}
	if err := EnsureRepoUsable(context.Background(), repo); err != nil {
		t.Fatalf("注销登记后磁盘仓库被动过了: %v", err)
	}
}
```

`newRepoTestManager(t)` 需要造一个只带 `st` / `cfg` / `log` 的 Manager。先读 `internal/agentd/manager_test.go` 里既有的 Manager 构造 helper（`grep -n "func newTestManager\|func testManager" internal/agentd/*_test.go`），**复用它**；若它需要 executor adapter 等本 task 用不到的依赖，就在 `repoadmin_test.go` 里加一个最小构造：

```go
// newRepoTestManager 造一个只够跑仓库登记操作的 Manager（不含 executor/hub）。
func newRepoTestManager(t *testing.T) *Manager {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	return &Manager{st: st, cfg: &config.Config{}, log: slog.Default()}
}
```

- [ ] **Step 3: 跑测试确认失败**

Run: `go test ./internal/agentd/ -run 'TestRepoOrigin|TestRepoNameFromURL|TestRegister|TestClone|TestListRepos|TestUnregister' -count=1`
Expected: 编译失败，`undefined: repoOriginURL` / `undefined: RegisterRepoReq` 等

- [ ] **Step 4: 写实现**

创建 `internal/agentd/repoadmin.go`：

```go
// 本文件是 agentd 侧「执行机 × 仓库登记」的操作层。
//
// 职责：
//   - RegisterRepo：登记执行机上已有的克隆，或先 clone 再登记
//   - ListRepos：列出登记，并**现场探测**每条的实际状态（漂移可见化）
//   - UnregisterRepo：注销登记（只删记录，不动磁盘）
//
// 边界：
//   - 不做解析：dispatch 时「--repo 写的是什么」由 reporegistry.go 的纯函数决定
//   - 不做持久化细节：SQL 在 internal/store/repos.go
//   - 不删磁盘上的仓库：注销只影响登记，磁盘由人自己处置
//   - clone 在**执行机本地**执行（agentd 就跑在这台机器上），不走 ssh——
//     用的是执行机自己的 git 凭据，与 agentd 既有的 fetch 回退同一条路径
package agentd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/xushixin/handoff/internal/proto"
	"github.com/xushixin/handoff/internal/store"
)

// ErrRepoAlreadyExists 表示登记冲突或克隆落点已被占用，映射 409。
//
// 与 ErrRepoUnusable（400）的区别：那是「请求本身有问题，改了再来」，
// 这是「当前状态与请求冲突」——和 ErrDirtyWorktree / ErrWorkdirBusy 同层级。
var ErrRepoAlreadyExists = errors.New("仓库登记冲突或克隆落点已存在")

// repo ls 的状态取值。不落库，每次列出时现场探得。
const (
	repoStatusOK      = "有效"
	repoStatusMissing = "路径不存在"
	repoStatusNotRepo = "不是 git 仓库"
)

// RegisterRepoReq 是登记一个仓库的请求。
//
// 两种形态互斥：
//   - Clone=false：登记执行机上已存在的克隆，Path 必填
//   - Clone=true：先 clone 再登记，URL 必填；Path 为落点，空则用 cfg.RepoRoot/<Name>
//
// Name 可省，此时由 origin URL 末段派生。
type RegisterRepoReq struct {
	Name  string
	Path  string
	URL   string
	Clone bool
}

// repoOriginURL 读取仓库的 origin 地址。
//
// 参数：
//   - ctx: 控制 git 调用生命周期
//   - repo: 仓库路径
//
// 返回：
//   - origin 地址；仓库不可用或没有 origin 时返回包装 ErrRepoUnusable 的错误
//
// 注意：
//   - 没有 origin 的仓库拒绝登记：它永远参与不了 dispatch 省略 --repo 时的
//     origin 自动匹配，登记进来只会变成一条永远匹配不上的死记录
func repoOriginURL(ctx context.Context, repo string) (string, error) {
	out, stderr, err := gitRun(ctx, repo, "remote", "get-url", "origin")
	if err != nil {
		return "", fmt.Errorf("%w: 读取 %s 的 origin 失败: %s: %v",
			ErrRepoUnusable, repo, strings.TrimSpace(stderr), err)
	}
	url := strings.TrimSpace(out)
	if url == "" {
		return "", fmt.Errorf("%w: 仓库 %s 没有配置 origin remote", ErrRepoUnusable, repo)
	}
	return url, nil
}

// repoNameFromURL 从 git URL 末段派生缺省登记名（去掉 .git 后缀）。
//
// 例：git@github.com:xushixin/handoff.git → handoff
func repoNameFromURL(url string) string {
	s := strings.TrimRight(strings.TrimSpace(url), "/")
	s = strings.TrimSuffix(s, ".git")
	if i := strings.LastIndexAny(s, "/:"); i >= 0 {
		s = s[i+1:]
	}
	return s
}

// RegisterRepo 登记一个仓库（两种形态见 RegisterRepoReq）。
//
// 参数：
//   - ctx: 控制整组 git 调用的生命周期
//   - req: 登记请求
//
// 返回：
//   - 落库后的登记条目
//   - 错误：ErrRepoUnusable（400，路径不是仓库/无 origin/clone 失败）、
//     ErrRepoAlreadyExists（409，名字或路径已被占用/落点已存在）、
//     errBadDispatchRequest（400，参数缺失或互斥冲突）
//
// 注意：
//   - **登记在 clone 成功之后才落库**：反过来会在 clone 失败时留下一条指向
//     不存在路径的死记录
//   - clone 的落点若已存在则直接拒绝，绝不往里 clone、绝不覆盖
func (m *Manager) RegisterRepo(ctx context.Context, req RegisterRepoReq) (proto.Repo, error) {
	m.log.Info("登记仓库请求", "name", req.Name, "path", req.Path,
		"url", req.URL, "clone", req.Clone)
	if req.Clone {
		return m.cloneAndRegister(ctx, req)
	}
	return m.registerExisting(ctx, req)
}

// registerExisting 登记执行机上已存在的克隆。
func (m *Manager) registerExisting(ctx context.Context, req RegisterRepoReq) (proto.Repo, error) {
	if req.Path == "" {
		return proto.Repo{}, fmt.Errorf("%w: 登记已有仓库必须指定路径（或加 --clone 让 agentd 克隆一份）",
			errBadDispatchRequest)
	}
	if err := EnsureRepoUsable(ctx, req.Path); err != nil {
		m.log.Warn("登记被拒：路径不是可用的 git 仓库", "path", req.Path, "cause", err)
		return proto.Repo{}, err
	}
	// origin 由 agentd 在执行机上现读，而不是采信调用方上送的值：
	// 登记的是这个路径上真实存在的仓库，它的 origin 才是权威。
	origin, err := repoOriginURL(ctx, req.Path)
	if err != nil {
		m.log.Warn("登记被拒：读不到 origin", "path", req.Path, "cause", err)
		return proto.Repo{}, err
	}
	return m.persistRepo(req.Name, req.Path, origin)
}

// cloneAndRegister 先 clone 再登记。
func (m *Manager) cloneAndRegister(ctx context.Context, req RegisterRepoReq) (proto.Repo, error) {
	if req.URL == "" {
		return proto.Repo{}, fmt.Errorf("%w: --clone 需要仓库 URL（当前目录不是 git 仓库时必须显式指定）",
			errBadDispatchRequest)
	}
	if strings.HasPrefix(req.URL, "-") {
		// git 会把以 - 开头的参数解释为选项——参数注入面，与 ErrBadBaseBranch 同源。
		return proto.Repo{}, fmt.Errorf("%w: 仓库 URL 不允许以 - 开头", errBadDispatchRequest)
	}
	name := req.Name
	if name == "" {
		name = repoNameFromURL(req.URL)
	}
	if name == "" {
		return proto.Repo{}, fmt.Errorf("%w: 无法从 URL %q 派生登记名，请显式指定", errBadDispatchRequest, req.URL)
	}
	dest := req.Path
	if dest == "" {
		if m.cfg.RepoRoot == "" {
			return proto.Repo{}, fmt.Errorf("%w: 未指定落点，且执行机未配置 repo_root（在 agentd 的 config.yaml 里配它，或显式给路径）",
				errBadDispatchRequest)
		}
		dest = filepath.Join(m.cfg.RepoRoot, name)
	}
	// 落点已存在就拒绝：往一个已有目录里 clone 要么失败要么污染它，两种都不该发生。
	if _, err := os.Stat(dest); err == nil {
		m.log.Warn("克隆被拒：落点已存在", "dest", dest)
		return proto.Repo{}, fmt.Errorf("%w: 落点 %s 已存在；换一个路径，或用不带 --clone 的形态直接登记它",
			ErrRepoAlreadyExists, dest)
	} else if !errors.Is(err, os.ErrNotExist) {
		return proto.Repo{}, fmt.Errorf("%w: 探查落点 %s: %v", ErrRepoUnusable, dest, err)
	}
	parent := filepath.Dir(dest)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return proto.Repo{}, fmt.Errorf("%w: 创建落点父目录 %s: %v", ErrRepoUnusable, parent, err)
	}
	m.log.Info("开始克隆仓库", "url", req.URL, "dest", dest)
	start := time.Now()
	// gitRun 以 parent 为 cwd 执行；-- 分隔符防止 URL/路径被当成选项。
	if _, stderr, err := gitRun(ctx, parent, "clone", "--", req.URL, dest); err != nil {
		m.log.Error("克隆仓库失败", "url", req.URL, "dest", dest,
			"stderr", truncateRunes(strings.TrimSpace(stderr), 300), "cause", err)
		return proto.Repo{}, fmt.Errorf("%w: 克隆 %s 到 %s 失败: %s: %v",
			ErrRepoUnusable, req.URL, dest, strings.TrimSpace(stderr), err)
	}
	m.log.Info("克隆仓库完成", "url", req.URL, "dest", dest,
		"elapsed_ms", time.Since(start).Milliseconds())
	return m.persistRepo(name, dest, req.URL)
}

// persistRepo 把一条登记落库，并把 store 的冲突哨兵翻译成 agentd 的哨兵。
func (m *Manager) persistRepo(name, path, origin string) (proto.Repo, error) {
	if name == "" {
		name = repoNameFromURL(origin)
	}
	if name == "" {
		return proto.Repo{}, fmt.Errorf("%w: 无法派生登记名，请显式指定", errBadDispatchRequest)
	}
	r := proto.Repo{Name: name, Path: path, OriginURL: origin, CreatedAt: time.Now()}
	if err := m.st.CreateRepo(&r); err != nil {
		if errors.Is(err, store.ErrRepoDuplicate) {
			m.log.Warn("登记被拒：名字或路径已被占用", "name", name, "path", path, "cause", err)
			return proto.Repo{}, fmt.Errorf("%w: 名字 %q 或路径 %s 已被登记（handoff repo ls 查看）",
				ErrRepoAlreadyExists, name, path)
		}
		m.log.Error("登记落库失败", "name", name, "path", path, "cause", err)
		return proto.Repo{}, err
	}
	m.log.Info("仓库登记完成", "name", name, "path", path, "origin", origin)
	r.Status = repoStatusOK
	return r, nil
}

// ListRepos 列出全部登记，并现场探测每条的实际状态。
//
// 参数：
//   - ctx: 控制探测用的 git 调用生命周期
//
// 返回：
//   - 登记列表（Status 已填充）；查库失败时返回错误
//
// 注意：
//   - 探测是登记与文件系统漂移的可见化手段。探测失败不影响列出——
//     状态本身就是要报给人看的结果，不是错误
func (m *Manager) ListRepos(ctx context.Context) ([]proto.Repo, error) {
	repos, err := m.st.ListRepos()
	if err != nil {
		m.log.Error("列出仓库登记失败", "cause", err)
		return nil, err
	}
	for i := range repos {
		repos[i].Status = probeRepoStatus(ctx, repos[i].Path)
	}
	m.log.Info("列出仓库登记", "count", len(repos))
	return repos, nil
}

// probeRepoStatus 探测一条登记指向的路径当前是什么状态。
func probeRepoStatus(ctx context.Context, path string) string {
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return repoStatusMissing
	}
	if err := EnsureRepoUsable(ctx, path); err != nil {
		return repoStatusNotRepo
	}
	return repoStatusOK
}

// UnregisterRepo 注销一条登记。
//
// 参数：
//   - ctx: 上下文（当前实现不发起 git 调用，保留以对齐其余操作签名）
//   - name: 登记名
//
// 返回：
//   - 错误：登记不存在时 store.ErrNotFound（404）；路径被活跃任务占用时
//     ErrWorkdirBusy（409）
//
// 注意：
//   - **只删登记，永不删磁盘上的仓库**。磁盘上那份是不是还要留，由人自己决定；
//     handoff 不替审核者做删代码的决定
func (m *Manager) UnregisterRepo(ctx context.Context, name string) error {
	r, err := m.st.GetRepoByName(name)
	if err != nil {
		m.log.Warn("注销登记失败：登记不存在", "name", name, "cause", err)
		return err
	}
	tasks, err := m.st.ActiveTasksByRepoPath(r.Path)
	if err != nil {
		m.log.Error("注销登记前查活跃任务失败", "name", name, "path", r.Path, "cause", err)
		return err
	}
	if len(tasks) > 0 {
		ids := make([]string, 0, len(tasks))
		for _, t := range tasks {
			ids = append(ids, t.ID)
		}
		m.log.Warn("注销登记被拒：仓库被活跃任务占用",
			"name", name, "path", r.Path, "tasks", strings.Join(ids, ","))
		return fmt.Errorf("%w: 仓库 %s 上还有 %d 个活跃任务（%s）；先 done 或 stop 它们",
			ErrWorkdirBusy, r.Path, len(tasks), strings.Join(ids, ", "))
	}
	if err := m.st.DeleteRepo(name); err != nil {
		m.log.Error("注销登记落库失败", "name", name, "cause", err)
		return err
	}
	m.log.Info("仓库登记已注销（磁盘仓库未动）", "name", name, "path", r.Path)
	return nil
}
```

`truncateRunes` 已存在于 `internal/agentd/workspace.go`，直接用。

- [ ] **Step 5: 跑测试确认通过**

Run: `go test ./internal/agentd/ -count=1 -v`
Expected: 新增用例全部 PASS，既有用例不受影响

- [ ] **Step 6: 关键节点日志自检**

本 task 是全 B46 唯一会**改变执行机磁盘**的地方（clone 会落文件），日志密度按外部调用标准来。逐条对照实现：

- 进入 `RegisterRepo`：Info 带 `name`/`path`/`url`/`clone` 四个入参
- **外部调用前后各一条**：`git clone` 前 Info「开始克隆仓库」带 `url`+`dest`，成功后 Info「克隆仓库完成」带 `elapsed_ms`，失败 Error 带截断后的 git `stderr` + cause
- 每个拒绝分支各一条 Warn 且带原因：路径不是仓库、读不到 origin、落点已存在、名字/路径被占用、仓库被活跃任务占用（后者要带上占用它的 task id 列表——审核者拿到就能直接去 `done` 它们）
- 成功出口各一条 Info：「仓库登记完成」带 `name`/`path`/`origin`、「仓库登记已注销（磁盘仓库未动）」、「列出仓库登记」带 `count`
- 落库失败 Error 带 cause

`gitRun` 自己已经打了「git 调用 / 完成 / 失败」，所以本层日志讲的是**业务意图**（在登记什么、为什么拒），不要重复 git 的参数细节。

- [ ] **Step 7: 注释自检**

- `repoadmin.go` 文件头：职责（三个操作）+ 边界（不做解析、不写 SQL、**不删磁盘仓库**、clone 在执行机本地跑不走 ssh）
- 每个导出符号有 doc 注释；`RegisterRepoReq` 的 doc 必须讲清两种形态的互斥与各自必填项
- 「为什么」型注释至少四处，对应 spec §7 的四条安全边界：登记前先 `EnsureRepoUsable`、落点已存在为什么直接拒而不是往里 clone、**为什么先 clone 成功再落库**（反过来会留下指向不存在路径的死记录）、`UnregisterRepo` 为什么永不删磁盘（不替审核者做删代码的决定）
- `ErrRepoAlreadyExists` 的注释要说明它与 `ErrRepoUnusable` 的分层差异（409 状态冲突 vs 400 请求非法）
- `RepoRoot` 的配置注释要说明**为什么放 Config 顶层而不是 Target**（Target 在审核者本地被读，放那儿会让「仓库放哪」变成审核者的本地状态）

- [ ] **Step 8: 跑闸门并提交**

```bash
gofmt -l . && go build ./... && go vet ./... && go test ./... -count=1
git add internal/agentd/repoadmin.go internal/agentd/repoadmin_test.go internal/config/config.go
git commit -m "feat(b46): agentd 侧仓库登记操作与 repo_root 配置"
```

---

### Task 4: `/api/repos` 端点与 client 方法

**Files:**
- Modify: `internal/agentd/server.go`（路由表 + 三个 handler + 错误映射）
- Modify: `internal/client/client.go`（三个方法）
- Test: `internal/agentd/integration_test.go`（追加用例）

**Interfaces:**
- Consumes: `(*Manager).RegisterRepo(ctx, RegisterRepoReq) (proto.Repo, error)`、`(*Manager).ListRepos(ctx) ([]proto.Repo, error)`、`(*Manager).UnregisterRepo(ctx, name string) error`、`agentd.ErrRepoAlreadyExists`、`agentd.ErrRepoUnusable`、`agentd.ErrWorkdirBusy`、`store.ErrNotFound`（均由 Task 1–3 产出）
- Produces:
  - `(*client.Client).RepoAdd(ctx context.Context, opts client.RepoAddOpts) (*proto.Repo, error)`
  - `client.RepoAddOpts{Name, Path, URL string; Clone bool}`
  - `(*client.Client).RepoList(ctx context.Context) ([]proto.Repo, error)`
  - `(*client.Client).RepoRemove(ctx context.Context, name string) error`

- [ ] **Step 1: 写失败的集成测试**

在 `internal/agentd/integration_test.go` 末尾追加（沿用该文件里既有的测试服务器构造 helper——先 `grep -n "func newTestServer\|httptest.NewServer" internal/agentd/integration_test.go` 看它叫什么，下面的 `newTestServer(t)` 按实际名字替换）：

```go
// TestRepoAPIAddListRemove 走完整 HTTP 面：登记 → 列出 → 注销。
func TestRepoAPIAddListRemove(t *testing.T) {
	srv, _ := newTestServer(t)
	origin := initBareOrigin(t)
	repo := initWorkRepo(t, origin)

	var added proto.Repo
	postJSON(t, srv, "/api/repos", map[string]any{"name": "r1", "path": repo}, http.StatusOK, &added)
	if added.OriginURL != origin {
		t.Fatalf("OriginURL = %q, want %q", added.OriginURL, origin)
	}

	var list []proto.Repo
	getJSON(t, srv, "/api/repos", http.StatusOK, &list)
	if len(list) != 1 || list[0].Name != "r1" || list[0].Status != repoStatusOK {
		t.Fatalf("列表不符: %+v", list)
	}

	deleteReq(t, srv, "/api/repos/r1", http.StatusOK)
	getJSON(t, srv, "/api/repos", http.StatusOK, &list)
	if len(list) != 0 {
		t.Fatalf("注销后仍有 %d 条", len(list))
	}
}

// TestRepoAPIRejectsNonRepoWithReadableReason 验证非 git 路径 → 400 且带 git 原文，
// 不被扁平化成「操作失败」（B45 立下的规矩）。
func TestRepoAPIRejectsNonRepoWithReadableReason(t *testing.T) {
	srv, _ := newTestServer(t)
	body := postRaw(t, srv, "/api/repos",
		map[string]any{"name": "x", "path": t.TempDir()}, http.StatusBadRequest)
	if !strings.Contains(body, "not a git repository") {
		t.Fatalf("响应体未带 git 原文: %s", body)
	}
}

// TestRepoAPICloneIntoExistingPathConflicts 验证落点已存在 → 409。
func TestRepoAPICloneIntoExistingPathConflicts(t *testing.T) {
	srv, _ := newTestServer(t)
	origin := initBareOrigin(t)
	postRaw(t, srv, "/api/repos",
		map[string]any{"name": "x", "path": t.TempDir(), "url": origin, "clone": true},
		http.StatusConflict)
}

// TestRepoAPIRemoveMissing 验证注销不存在的登记 → 404。
func TestRepoAPIRemoveMissing(t *testing.T) {
	srv, _ := newTestServer(t)
	deleteReq(t, srv, "/api/repos/nope", http.StatusNotFound)
}
```

`postJSON` / `getJSON` / `deleteReq` / `postRaw` 若该文件里没有等价 helper，就在测试文件里补上最小实现（构造带 `Authorization: Bearer <token>` 的请求、断言状态码、解码响应体）。**不要**为了让测试通过而放宽状态码断言。

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/agentd/ -run TestRepoAPI -count=1`
Expected: FAIL——`/api/repos` 返回 404（路由尚未注册）

- [ ] **Step 3: 注册路由**

在 `internal/agentd/server.go` 的路由表（`mux.HandleFunc("GET /ws/events", s.handleEvents)` 之前）追加：

```go
	mux.HandleFunc("POST /api/repos", s.handleRepoAdd)
	mux.HandleFunc("GET /api/repos", s.handleRepoList)
	mux.HandleFunc("DELETE /api/repos/{name}", s.handleRepoRemove)
```

- [ ] **Step 4: 写 handler 与错误映射**

在 `internal/agentd/server.go` 里 `writeDispatchError` 附近追加：

```go
// repoAddRequest 是 POST /api/repos 的请求体。
//
// 两种形态：clone=false 时 path 必填（登记已有克隆）；clone=true 时 url 必填
// （先克隆再登记），path 为落点、可省（省时用 agentd 配置的 repo_root）。
type repoAddRequest struct {
	Name  string `json:"name"`
	Path  string `json:"path"`
	URL   string `json:"url"`
	Clone bool   `json:"clone"`
}

// handleRepoAdd 登记一个仓库（必要时先克隆）。
func (s *Server) handleRepoAdd(w http.ResponseWriter, r *http.Request) {
	s.log.Info("repo add 请求", "method", r.Method, "path", r.URL.Path)
	if s.mgr == nil {
		s.log.Warn("repo add 请求到达但 manager 未注入", "remote_addr", r.RemoteAddr)
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "manager 未就绪"})
		return
	}
	var req repoAddRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		s.log.Warn("repo add 请求体解析失败", "err", err)
		writeJSON(w, http.StatusBadRequest,
			map[string]string{"error": "请求体必须是 JSON {name, path, url, clone}"})
		return
	}
	repo, err := s.mgr.RegisterRepo(r.Context(), RegisterRepoReq{
		Name: req.Name, Path: req.Path, URL: req.URL, Clone: req.Clone})
	if err != nil {
		s.writeRepoError(w, req.Name, err)
		return
	}
	s.log.Info("repo add 完成", "name", repo.Name, "path", repo.Path)
	writeJSON(w, http.StatusOK, repo)
}

// handleRepoList 列出全部仓库登记（含现场探得的实际状态）。
func (s *Server) handleRepoList(w http.ResponseWriter, r *http.Request) {
	s.log.Info("repo list 请求", "method", r.Method, "path", r.URL.Path)
	if s.mgr == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "manager 未就绪"})
		return
	}
	repos, err := s.mgr.ListRepos(r.Context())
	if err != nil {
		s.writeRepoError(w, "", err)
		return
	}
	if repos == nil {
		repos = []proto.Repo{} // 空列表要序列化成 []，不是 null
	}
	s.log.Info("repo list 完成", "count", len(repos))
	writeJSON(w, http.StatusOK, repos)
}

// handleRepoRemove 注销一条仓库登记（只删登记，不动磁盘）。
func (s *Server) handleRepoRemove(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	s.log.Info("repo remove 请求", "name", name)
	if s.mgr == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "manager 未就绪"})
		return
	}
	if err := s.mgr.UnregisterRepo(r.Context(), name); err != nil {
		s.writeRepoError(w, name, err)
		return
	}
	s.log.Info("repo remove 完成", "name", name)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// writeRepoError 把仓库登记操作的失败映射为 HTTP 状态码与可读原因。
//
// 映射规则（与 writeDispatchError 同一套哲学：调用方拿到就能行动）：
//   - store.ErrNotFound → 404：登记名不存在
//   - ErrRepoAlreadyExists → 409：名字/路径已被占用，或克隆落点已存在——
//     与 ErrDirtyWorktree/ErrWorkdirBusy 同为状态冲突
//   - ErrWorkdirBusy → 409：注销时仓库仍被活跃任务占用
//   - ErrRepoUnusable / errBadDispatchRequest → 400：请求本身的问题
//     （路径不是仓库、没有 origin、clone 失败、参数缺失）
//   - 其余 → 500
func (s *Server) writeRepoError(w http.ResponseWriter, name string, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		s.log.Warn("仓库登记操作被拒：登记不存在", "name", name, "cause", err)
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
	case errors.Is(err, ErrRepoAlreadyExists):
		s.log.Warn("仓库登记操作被拒：已存在", "name", name, "cause", err)
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
	case errors.Is(err, ErrWorkdirBusy):
		s.log.Warn("仓库登记操作被拒：被活跃任务占用", "name", name, "cause", err)
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
	case errors.Is(err, ErrRepoUnusable), errors.Is(err, errBadDispatchRequest):
		s.log.Warn("仓库登记操作被拒：请求非法", "name", name, "cause", err)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
	default:
		s.log.Error("仓库登记操作失败", "name", name, "cause", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "仓库登记操作失败"})
	}
}
```

如果 `server.go` 尚未 import `"github.com/xushixin/handoff/internal/store"`，补上。

- [ ] **Step 5: 写 client 方法**

在 `internal/client/client.go` 的 `Dispatch` 之后追加：

```go
// RepoAddOpts 是 RepoAdd 的参数。
//
// 两种形态：Clone=false 时 Path 必填（登记执行机上已有的克隆）；
// Clone=true 时 URL 必填（让 agentd 克隆一份），Path 为落点、可省。
type RepoAddOpts struct {
	Name  string
	Path  string
	URL   string
	Clone bool
}

// RepoAdd 在目标 agentd 上登记一个仓库（必要时先克隆）。
//
// 注意：
//   - 路径不是 git 仓库/没有 origin/克隆失败返回 400 错误（报文含 git 原文）
//   - 名字或路径已被登记、克隆落点已存在返回 409 错误
func (c *Client) RepoAdd(ctx context.Context, opts RepoAddOpts) (*proto.Repo, error) {
	resp, err := c.do(ctx, http.MethodPost, "/api/repos", map[string]any{
		"name": opts.Name, "path": opts.Path, "url": opts.URL, "clone": opts.Clone,
	})
	if err != nil {
		return nil, fmt.Errorf("repo add 请求: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, c.httpError("repo add", resp)
	}
	var repo proto.Repo
	if err := json.NewDecoder(resp.Body).Decode(&repo); err != nil {
		return nil, fmt.Errorf("解析 repo add 响应: %w", err)
	}
	return &repo, nil
}

// RepoList 列出目标 agentd 上的全部仓库登记（含实际状态）。
func (c *Client) RepoList(ctx context.Context) ([]proto.Repo, error) {
	resp, err := c.do(ctx, http.MethodGet, "/api/repos", nil)
	if err != nil {
		return nil, fmt.Errorf("repo list 请求: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, c.httpError("repo list", resp)
	}
	var repos []proto.Repo
	if err := json.NewDecoder(resp.Body).Decode(&repos); err != nil {
		return nil, fmt.Errorf("解析 repo list 响应: %w", err)
	}
	return repos, nil
}

// RepoRemove 注销一条仓库登记。
//
// 注意：
//   - 只删登记，**不删磁盘上的仓库**
//   - 登记不存在返回 404 错误；仓库仍被活跃任务占用返回 409 错误
func (c *Client) RepoRemove(ctx context.Context, name string) error {
	resp, err := c.do(ctx, http.MethodDelete, "/api/repos/"+name, nil)
	if err != nil {
		return fmt.Errorf("repo remove 请求: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return c.httpError("repo remove", resp)
	}
	return nil
}
```

- [ ] **Step 6: 跑测试确认通过**

Run: `go test ./internal/agentd/ -run TestRepoAPI -count=1 -v && go test ./... -count=1`
Expected: 全部 PASS

- [ ] **Step 7: 日志与注释自检**

**日志**（HTTP 层用 `s.log`）：
- 每个 handler 入口 Info 带 `method` + `path`（`handleRepoRemove` 带 `name`）
- 每个 handler 成功出口 Info 带结论（`name`/`path`，list 带 `count`）
- 请求体解析失败 Warn 带 err；manager 未就绪 Warn 带 `remote_addr`
- `writeRepoError` 每个分支各自记一条：4xx 用 Warn（是调用方的问题），500 用 Error 带 cause

**client 层不打日志**——它是给 CLI 用的库，错误经 `fmt.Errorf` 带上下文返回，由 CLI 决定怎么呈现，与 `Dispatch` 一致。

**注释**：
- `repoAddRequest` 的 doc 讲清两种形态；`writeRepoError` 的 doc 必须有完整的**状态码映射表注释**（与 `writeDispatchError` 同形态）
- `RepoAddOpts` / 三个 client 方法各有 doc，`RepoRemove` 必须写明「只删登记，不删磁盘」
- 「为什么」型注释：`handleRepoList` 里空列表为什么要显式转成 `[]proto.Repo{}`（否则序列化成 `null`，CLI 侧无法区分「没有登记」和「字段缺失」）

- [ ] **Step 8: 跑闸门并提交**

```bash
gofmt -l . && go build ./... && go vet ./... && go test ./... -count=1
git add internal/agentd/server.go internal/agentd/integration_test.go internal/client/client.go
git commit -m "feat(b46): /api/repos 端点与 client 方法"
```

---

### Task 5: `handoff repo` 子命令

**Files:**
- Create: `cmd/repo.go`
- Test: `cmd/repo_test.go`

**Interfaces:**
- Consumes: `client.RepoAddOpts{Name, Path, URL string; Clone bool}`、`(*client.Client).RepoAdd/RepoList/RepoRemove`（Task 4 产出）；既有的 `cmd.TargetEndpoint() (addr, token string, err error)`
- Produces: `handoff repo add|ls|rm` 三条子命令；`cmd.localOriginURL() string`

- [ ] **Step 1: 写失败的测试**

创建 `cmd/repo_test.go`：

```go
package cmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestLocalOriginURL 验证从 cwd 读 origin；不是 git 仓库时返回空串而不是报错。
func TestLocalOriginURL(t *testing.T) {
	dir := t.TempDir()
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(old) })
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	if got := localOriginURL(); got != "" {
		t.Fatalf("非 git 目录应返回空串，got %q", got)
	}
	if out, err := exec.Command("git", "init", dir).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	want := filepath.Join(dir, "fake-origin.git")
	if out, err := exec.Command("git", "-C", dir, "remote", "add", "origin", want).CombinedOutput(); err != nil {
		t.Fatalf("remote add: %v: %s", err, out)
	}
	if got := localOriginURL(); got != want {
		t.Fatalf("localOriginURL() = %q, want %q", got, want)
	}
}

// TestRepoAddRequiresPathOrClone 验证两种形态都没给时本地即报错，不发请求。
func TestRepoAddRequiresPathOrClone(t *testing.T) {
	repoAddPath, repoAddClone, repoAddURL = "", false, ""
	err := validateRepoAddFlags()
	if err == nil {
		t.Fatal("既没给路径也没给 --clone 时应当报错")
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./cmd/ -run 'TestLocalOriginURL|TestRepoAddRequires' -count=1`
Expected: 编译失败，`undefined: localOriginURL` / `undefined: validateRepoAddFlags`

- [ ] **Step 3: 写实现**

创建 `cmd/repo.go`：

```go
// 本文件实现 handoff repo 子命令族：把一个项目显式落到某台执行机上，
// 并维护「执行机 × 仓库」的登记，使日常 dispatch 不必再写仓库路径。
//
// 职责：
//   - repo add：登记执行机上已有的克隆，或让 agentd 克隆一份再登记
//   - repo ls：列出登记，并显示每条的实际状态（登记与磁盘漂移时看得见）
//   - repo rm：注销登记
//
// 边界：
//   - 不自己 ssh、不自己 clone：克隆由执行机上的 agentd 执行，用它自己的 git 凭据
//   - 不删磁盘上的仓库：rm 只删登记
//   - 不做解析：dispatch 时 --repo 怎么解释是 agentd 的事
package cmd

import (
	"fmt"
	"os/exec"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"github.com/xushixin/handoff/internal/client"
)

var (
	repoAddPath  string
	repoAddURL   string
	repoAddClone bool
)

// localOriginURL 读当前目录仓库的 origin 地址；不是 git 仓库或没有 origin 时返回空串。
//
// 与 localHeadCommit 同源同 caveat：取的是 **cwd** 的信息，因此 cwd 必须是
// 你要落地的那个仓库。
func localOriginURL() string {
	out, err := exec.Command("git", "remote", "get-url", "origin").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// validateRepoAddFlags 校验 repo add 的两种形态互斥且齐备。
//
// 返回：
//   - 错误：两种形态都没给时返回可读提示（本地拦下，不浪费一次往返）
func validateRepoAddFlags() error {
	if !repoAddClone && repoAddPath == "" {
		return fmt.Errorf("需要二选一：--path <执行机上已有仓库的路径>，或 --clone（让 agentd 克隆一份）")
	}
	return nil
}

// repoCmd 是 repo 子命令族的父命令。
var repoCmd = &cobra.Command{
	Use:   "repo",
	Short: "管理执行机上的仓库登记（落地项目、列出、注销）",
}

// repoAddCmd 登记一个仓库。
//
// 使用方式：
//
//	handoff repo add [名字] --path /root/work/handoff --target devbox
//	handoff repo add [名字] --clone [--url <URL>] [--path <落点>] --target devbox
var repoAddCmd = &cobra.Command{
	Use:   "add [名字]",
	Short: "把一个仓库登记到执行机（可让 agentd 克隆一份）",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := validateRepoAddFlags(); err != nil {
			return err
		}
		addr, token, err := TargetEndpoint()
		if err != nil {
			return err
		}
		name := ""
		if len(args) == 1 {
			name = args[0]
		}
		url := repoAddURL
		if repoAddClone && url == "" {
			// 与 dispatch 取基线同源：默认拿 cwd 的 origin
			url = localOriginURL()
			if url == "" {
				return fmt.Errorf("--clone 需要仓库 URL：当前目录不是 git 仓库（或没有 origin），请用 --url 指定")
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "克隆源取自当前目录的 origin: %s\n", url)
		}
		repo, err := client.New(addr, token).RepoAdd(cmd.Context(), client.RepoAddOpts{
			Name: name, Path: repoAddPath, URL: url, Clone: repoAddClone,
		})
		if err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "已登记 %s → %s\n", repo.Name, repo.Path)
		fmt.Fprintf(cmd.ErrOrStderr(), "此后可用 --repo %s 派发，或在该仓库目录里直接省略 --repo\n", repo.Name)
		return nil
	},
}

// repoLsCmd 列出登记。
var repoLsCmd = &cobra.Command{
	Use:   "ls",
	Short: "列出执行机上的仓库登记（含实际状态）",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		addr, token, err := TargetEndpoint()
		if err != nil {
			return err
		}
		repos, err := client.New(addr, token).RepoList(cmd.Context())
		if err != nil {
			return err
		}
		if len(repos) == 0 {
			fmt.Fprintln(cmd.OutOrStdout(), "（该执行机上还没有任何仓库登记，用 handoff repo add 落地一个）")
			return nil
		}
		tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "名字\t路径\t状态\torigin")
		for _, r := range repos {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", r.Name, r.Path, r.Status, r.OriginURL)
		}
		return tw.Flush()
	},
}

// repoRmCmd 注销一条登记。
var repoRmCmd = &cobra.Command{
	Use:   "rm <名字>",
	Short: "注销一条仓库登记（只删登记，不删磁盘上的仓库）",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		addr, token, err := TargetEndpoint()
		if err != nil {
			return err
		}
		if err := client.New(addr, token).RepoRemove(cmd.Context(), args[0]); err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "已注销登记 %s（磁盘上的仓库未动）\n", args[0])
		return nil
	},
}

func init() {
	repoAddCmd.Flags().StringVar(&repoAddPath, "path", "",
		"执行机上的仓库路径；--clone 时为落点（省略则用执行机配置的 repo_root/<名字>）")
	repoAddCmd.Flags().StringVar(&repoAddURL, "url", "",
		"克隆源 URL（仅与 --clone 连用；省略则取当前目录的 origin）")
	repoAddCmd.Flags().BoolVar(&repoAddClone, "clone", false,
		"让 agentd 在执行机上克隆一份，而不是登记已有的克隆")
	repoCmd.AddCommand(repoAddCmd, repoLsCmd, repoRmCmd)
	rootCmd.AddCommand(repoCmd)
}
```

若 `--target` 是根命令的持久 flag（`TargetEndpoint()` 直接读它），则无需在本文件重复声明；先 `grep -n "target" cmd/root.go` 确认。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./cmd/ -count=1 -v`
Expected: 全部 PASS

- [ ] **Step 5: 手工确认命令树**

Run: `go run . repo --help && go run . repo add --help`
Expected: 三条子命令与三个 flag 的帮助文本正常显示

- [ ] **Step 6: 输出与注释自检**

**输出（CLI 层的「日志」是给人看的 stdout/stderr，不是 slog）**：
- 结果走 `cmd.OutOrStdout()`，提示与旁注走 `cmd.ErrOrStderr()`——这样 `handoff repo ls` 的输出可以直接管道给别的命令而不被提示污染
- `--clone` 自动取 cwd 的 origin 时**必须在 stderr 说明取自哪里**（与 `dispatch` 打「基线 <短号>」同一条纪律：隐式取值必须可见，否则派错仓库时没人知道值是哪来的）
- `repo add` 成功后在 stderr 提示「此后可用 `--repo <名字>` 派发，或省略 `--repo`」——这是本次特性的落点，不提示等于白做
- `repo rm` 成功后必须明说「磁盘上的仓库未动」
- `repo ls` 空列表时给出下一步动作，而不是打印空表

**注释**：
- `repo.go` 文件头：职责（三条子命令）+ 边界（不自己 ssh/clone、不删磁盘、不做解析）
- 每个 cobra 命令变量有 doc 注释；`repoAddCmd` 的 doc 带两种形态的用法示例
- `localOriginURL` 的 doc 必须写明它与 `localHeadCommit` 同源同 caveat（取的是 **cwd** 的信息）
- flag 的 help 文本本身就是用户唯一能读到的说明，必须写清 `--path` 在两种形态下的不同含义

- [ ] **Step 7: 跑闸门并提交**

```bash
gofmt -l . && go build ./... && go vet ./... && go test ./... -count=1
git add cmd/repo.go cmd/repo_test.go
git commit -m "feat(b46): handoff repo add/ls/rm 子命令"
```

---

### Task 6: 接进 dispatch，全套闸门与真机验收

**Files:**
- Modify: `internal/agentd/manager.go`（`DispatchReq` 加 `OriginURL`；`Dispatch` 前置块接入解析）
- Modify: `internal/agentd/server.go`（`dispatchRequest` 加 `origin_url`；透传；`writeDispatchError` 加两个哨兵分支）
- Modify: `internal/client/client.go`（`DispatchOpts` 加 `OriginURL`；请求体加键）
- Modify: `cmd/dispatch.go:83`（放开 `--repo` 必填）与请求构造处（上送 origin）
- Modify: `README.md`（命令面与 `config.yaml` 字段说明）
- Test: `internal/agentd/integration_test.go`（追加用例）

**Interfaces:**
- Consumes: `agentd.resolveRepoInput(input, originURL string, entries []proto.Repo) (string, error)`、`agentd.ErrRepoNotRegistered`、`agentd.ErrRepoAmbiguous`（Task 1）；`(*store.Store).ListRepos() ([]proto.Repo, error)`（Task 2）
- Produces: 无（终结 task）

- [ ] **Step 1: 写失败的集成测试**

在 `internal/agentd/integration_test.go` 追加：

```go
// TestDispatchResolvesRegisteredShortName 验证短名派发落到登记的路径上。
func TestDispatchResolvesRegisteredShortName(t *testing.T) {
	srv, _ := newTestServer(t)
	origin := initBareOrigin(t)
	repo := initWorkRepo(t, origin)
	postJSON(t, srv, "/api/repos", map[string]any{"name": "r1", "path": repo},
		http.StatusOK, &proto.Repo{})

	var task proto.Task
	postJSON(t, srv, "/api/tasks",
		map[string]any{"repo": "r1", "prompt": "干活", "new_worktree": true},
		http.StatusOK, &task)
	if task.RepoPath != repo {
		t.Fatalf("RepoPath = %q, want 登记的 %q", task.RepoPath, repo)
	}
}

// TestDispatchAutoSelectsByOrigin 验证省略 repo 时按 origin 唯一命中自动选中。
func TestDispatchAutoSelectsByOrigin(t *testing.T) {
	srv, _ := newTestServer(t)
	origin := initBareOrigin(t)
	repo := initWorkRepo(t, origin)
	postJSON(t, srv, "/api/repos", map[string]any{"name": "r1", "path": repo},
		http.StatusOK, &proto.Repo{})

	var task proto.Task
	postJSON(t, srv, "/api/tasks",
		map[string]any{"repo": "", "origin_url": origin, "prompt": "干活", "new_worktree": true},
		http.StatusOK, &task)
	if task.RepoPath != repo {
		t.Fatalf("RepoPath = %q, want %q", task.RepoPath, repo)
	}
}

// TestDispatchAmbiguousOriginListsCandidates 验证多命中 → 400 且报文列出候选。
func TestDispatchAmbiguousOriginListsCandidates(t *testing.T) {
	srv, _ := newTestServer(t)
	origin := initBareOrigin(t)
	r1, r2 := initWorkRepo(t, origin), initWorkRepo(t, origin)
	postJSON(t, srv, "/api/repos", map[string]any{"name": "a", "path": r1}, http.StatusOK, &proto.Repo{})
	postJSON(t, srv, "/api/repos", map[string]any{"name": "b", "path": r2}, http.StatusOK, &proto.Repo{})

	body := postRaw(t, srv, "/api/tasks",
		map[string]any{"origin_url": origin, "prompt": "干活", "new_worktree": true},
		http.StatusBadRequest)
	for _, want := range []string{"a", "b"} {
		if !strings.Contains(body, want) {
			t.Errorf("报文 %q 未列出候选 %q", body, want)
		}
	}
}

// TestDispatchUnregisteredNameLists 验证短名查不到 → 400 且报文带已登记清单。
func TestDispatchUnregisteredNameLists(t *testing.T) {
	srv, _ := newTestServer(t)
	origin := initBareOrigin(t)
	repo := initWorkRepo(t, origin)
	postJSON(t, srv, "/api/repos", map[string]any{"name": "known", "path": repo},
		http.StatusOK, &proto.Repo{})
	body := postRaw(t, srv, "/api/tasks",
		map[string]any{"repo": "unknown", "prompt": "干活", "new_worktree": true},
		http.StatusBadRequest)
	if !strings.Contains(body, "known") {
		t.Fatalf("报文未列出已登记的仓库: %s", body)
	}
}

// TestDispatchAbsolutePathStillWorks 验证老用法（完整路径）行为完全不变。
func TestDispatchAbsolutePathStillWorks(t *testing.T) {
	srv, _ := newTestServer(t)
	origin := initBareOrigin(t)
	repo := initWorkRepo(t, origin)
	var task proto.Task
	postJSON(t, srv, "/api/tasks",
		map[string]any{"repo": repo, "prompt": "干活", "new_worktree": true},
		http.StatusOK, &task)
	if task.RepoPath != repo {
		t.Fatalf("RepoPath = %q, want %q", task.RepoPath, repo)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/agentd/ -run 'TestDispatchResolves|TestDispatchAuto|TestDispatchAmbiguous|TestDispatchUnregistered' -count=1`
Expected: FAIL——短名被当成路径直接交给 git，报「不是 git 仓库」

- [ ] **Step 3: 接上 DispatchReq 与解析**

`internal/agentd/manager.go` 的 `DispatchReq` 中，在 `BaseCommit` 之后追加：

```go
	// OriginURL 是审核者 cwd 仓库的 origin 地址，用于 Repo 省略时按 origin
	// 自动匹配本机登记；cwd 不是 git 仓库时为空。
	OriginURL string
	// Repo 的语义（B46 起）：路径 / 登记名 / 空三态，由 resolveRepoInput 解析。
```

在 `Dispatch` 方法的最前面（任何使用 `req.Repo` 之前）插入解析：

```go
	// B46：--repo 三态解析（路径 / 登记名 / 空）。放在最前面：后面所有前置校验
	// （仓库可用性、工作目录占用、基线决议）都要拿到真实路径才有意义。
	entries, err := m.st.ListRepos()
	if err != nil {
		m.log.Error("dispatch 前置：读取仓库登记失败", "cause", err)
		return nil, err
	}
	resolvedRepo, err := resolveRepoInput(req.Repo, req.OriginURL, entries)
	if err != nil {
		return nil, err
	}
	req.Repo = resolvedRepo
```

`Dispatch` 的签名是 `func (m *Manager) Dispatch(ctx context.Context, req DispatchReq) (*proto.Task, error)`——`req` 是值传参，就地改写它不会影响调用方。

- [ ] **Step 4: 接上 HTTP 与 client 透传**

`internal/agentd/server.go` 的 `dispatchRequest` 追加：

```go
	// OriginURL 是审核者 cwd 仓库的 origin，用于 repo 省略时自动匹配登记（B46）。
	OriginURL string `json:"origin_url"`
```

`handleDispatch` 构造 `DispatchReq` 处追加 `OriginURL: req.OriginURL,`。

`writeDispatchError` 的 switch 中，在 `ErrRepoUnusable` 分支之后追加：

```go
	case errors.Is(err, ErrRepoNotRegistered):
		s.log.Warn("dispatch 被拒：仓库未登记", "repo", repo, "cause", err)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
	case errors.Is(err, ErrRepoAmbiguous):
		s.log.Warn("dispatch 被拒：origin 匹配到多条登记", "repo", repo, "cause", err)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
```

同时把该函数上方的映射规则注释补两行：

```go
//   - ErrRepoNotRegistered / ErrRepoAmbiguous → 400：--repo 给的登记名查不到，
//     或省略 --repo 时 origin 匹配到多条/零条——报文自带本机已登记清单或候选
//     清单，审核者拿到即可行动（换名字，或先 handoff repo add）
```

`internal/client/client.go` 的 `DispatchOpts` 追加 `OriginURL string`，`Dispatch` 的请求体 map 追加 `"origin_url": opts.OriginURL,`。

- [ ] **Step 5: 放开 CLI 的 `--repo` 必填并上送 origin**

`cmd/dispatch.go` 中删掉这三行（约 83 行）：

```go
		if dispatchRepo == "" {
			return fmt.Errorf("--repo 必须指定任务仓库路径")
		}
```

改为一句注释说明为什么不再本地拦：

```go
		// B46：--repo 可以是路径、登记名，也可以省略（由 agentd 按 cwd 的 origin
		// 匹配本机登记）。三态都要查登记表才能判，所以拦截点下沉到 agentd——
		// 好处是拒绝报文能带上「这台机器上登记了什么」，本地拦只能说一句「必填」。
```

在构造 `client.DispatchOpts` 处追加 `OriginURL: localOriginURL(),`（`localOriginURL` 由 Task 5 提供）。

同时更新 `--repo` 的 flag 说明：

```go
	dispatchCmd.Flags().StringVar(&dispatchRepo, "repo", "",
		"任务仓库：执行机上的完整路径，或 handoff repo add 登记过的名字；省略则按当前目录的 origin 自动匹配登记")
```

- [ ] **Step 6: 跑测试确认通过**

Run: `go test ./... -count=1`
Expected: 全部 PASS，含 Task 6 新增的五条用例

- [ ] **Step 7: 变异检验（必做）**

把 Step 3 插入的 `req.Repo = resolvedRepo` 一行注释掉。

Run: `go test ./internal/agentd/ -run 'TestDispatchResolvesRegisteredShortName|TestDispatchAutoSelectsByOrigin' -count=1`
Expected: **FAIL**——解析算了但没用上，任务仍落在原始输入上。把失败输出记进报告。

恢复后 `go test ./... -count=1 && git diff --exit-code`，确认全绿且工作区干净。

- [ ] **Step 8: 日志、注释与文档自检**

**日志**：
- `Dispatch` 里读登记失败 → Error 带 cause；解析本身的日志由 `resolveRepoInput` 打（Task 1 已覆盖），此处不重复
- `writeDispatchError` 两个新分支各一条 Warn，带 `repo` + cause

**注释**：
- `DispatchReq.OriginURL` 与 `dispatchRequest.OriginURL` 各有字段注释，说明它是**审核者 cwd 的** origin、cwd 不是 git 仓库时为空
- `Dispatch` 里插入的解析块要有「为什么放在最前面」的注释（后面所有前置校验都要拿到真实路径才有意义）
- `cmd/dispatch.go` 删掉的必填检查处**必须留一句注释说明为什么不再本地拦**（三态都要查登记表才能判，且拒绝报文要带上「这台机器上登记了什么」）——否则下一个读到这里的人会以为是漏删
- `writeDispatchError` 上方的映射表注释补两行新哨兵

**文档**：`README.md` 里描述 CLI 命令面与 `config.yaml` 字段的地方，补上 `handoff repo add/ls/rm` 与 `repo_root`，并更新 `--repo` 的说明（现在是「路径 / 登记名 / 可省略」）。一条用户唯一入口的新命令不出现在 README 里，等于没做。

- [ ] **Step 9: 跑全套六条闸门**

逐条跑，把实际输出记进报告：

```bash
gofmt -l .
go build ./...
go vet ./...
go test ./... -count=1
go test -race ./cmd/ ./internal/agentd/ ./internal/store/ -count=1
GOOS=windows GOARCH=amd64 go build ./...
```

- [ ] **Step 10: 真机验收（devbox 隔离实例）**

**红线**：不得启停/覆盖监听 7777 的 agentd，不得碰 `~/.handoff/`。验收实例必须用独立端口、独立 DataDir、独立二进制、独立仓库副本；清理时只按验收二进制的完整路径精确匹配来 kill，**不得 `pkill -f agentd`**。

依次验证并记录实际输出：

1. `handoff repo add` 形态一（登记已有克隆）→ 登记成功，`origin_url` 是那个路径上仓库的真实 origin
2. `handoff repo add --clone` → 仓库真的出现在落点，`handoff repo ls` 显示「有效」
3. `--clone` 落点已存在 → 409，报文可读
4. 把登记指向的目录改名 → `handoff repo ls` 显示「路径不存在」（漂移可见）
5. `handoff dispatch --repo <短名>` → 任务真的跑起来，`handoff show` 的 `repo_path` 是登记的路径
6. 在本地项目目录里省略 `--repo` 派发 → 自动命中，`stderr` 无异常
7. 未登记的短名派发 → 400 且报文列出已登记清单
8. `handoff repo rm` 一个仍有活跃任务的仓库 → 409；`done` 掉任务后再 `rm` → 成功，且**磁盘上的仓库还在**

- [ ] **Step 11: 提交**

```bash
git add internal/agentd/manager.go internal/agentd/server.go internal/agentd/integration_test.go internal/client/client.go cmd/dispatch.go README.md
git commit -m "feat(b46): dispatch 接入仓库登记解析，--repo 支持登记名与省略"
```

---

## 完工报告要包含

1. 六个 task 各自的 commit sha 与一句话说明。
2. 两处变异检验的输出：Task 1 Step 6 与 Task 6 Step 7 的注入后 FAIL 原文、恢复后 PASS 与 `git diff --exit-code` 结果。
3. 六条闸门命令各自的实际输出。
4. 真机验收八项的实际输出。
5. 任何偏离计划的地方及原因。**没有偏离就明确写「无偏离」**，不要含糊带过。
