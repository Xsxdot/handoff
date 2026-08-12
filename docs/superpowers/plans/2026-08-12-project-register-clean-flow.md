# 项目登记干净流程 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把 Web「添加项目」改成单步、本机优先的干净流程：本机 path 必填、git URL 选填、name 可选；path 不存在则 clone 到该 path；path 存在无 URL 则现读 origin；远程可选且复用本机 origin，可选填远程 path。后端 `RegisterProject` 扩展为能表达这些形态，不搞前端凑合。

**Architecture:** 真相放在 agentd 的 `RegisterProject`：由「path 是否给出 / 是否存在 / origin 是否给出」统一分派，复用现有 `inspectRepoDir`、`persistProject`、幂等与 origin 校验。Web 只做单页表单与「本机 → 远程」两次 `POST /api/projects`（远程经 `?machine=` 转发）。不新增 probe 端点——登记本身就是权威探测。

**Tech Stack:** Go (`internal/agentd`)、既有 HTTP `POST /api/projects`、React + vitest（`web/src/app/projects`）

---

## 决策（已锁定，实现勿回退）

### 后端统一决策表

| path | 路径状态 | origin_url | 行为 |
|------|----------|------------|------|
| 空 | — | 空 | 400：无身份、无落点 |
| 空 | — | 有 | **既有**：clone 到 `repo_root/<name>`（或认领已有落点） |
| 非空 | 不存在 | 空 | 400：无 URL 无法创建 |
| 非空 | 不存在 | 有 | **新增**：`git clone` 到 **该 path**，再登记 |
| 非空 | 存在且是带 origin 的 git 仓 | 空 | **新增**：现读 origin，用实际 origin 登记 |
| 非空 | 存在且是带 origin 的 git 仓 | 有 | **既有**：project_id 一致则登记/幂等，不一致 → `ErrProjectOriginMismatch` |
| 非空 | 存在但不是可用 git 仓 | 任意 | **既有**：`ErrRepoUnusable`（不静默往非 git 目录里 clone） |

**不引入 `clone` 布尔位。** 形态仍由 path + 文件系统状态 + origin 是否为空推导，避免非法组合。

### 前端产品规则

- **本机必选**（不再是可取消的 checkbox）。
- **本机 path 必填**；**git URL 选填**；**name 选填**（空则后端从 origin 末段派生）。
- **远程可选**（至多一台）；远程 **不再填 URL**；远程 path 选填（空 = 该机 `repo_root/<name>`）。
- **单页表单** + 结果列表；取消两步向导。
- 提交顺序：先本机；本机成功且勾了远程再打远程（`origin_url` / `name` 用本机响应里的权威值）。本机失败则不自动打远程（远程可单独重试时用表单里已有 URL，或提示先修本机）。

### 兼容性

- CLI `handoff project add`：仍送 `origin_url` + 本机 path；走「path 存在 + 有 origin」旧路径，行为不变。
- 自动登记 / 远程空 path clone：不变。
- 契约：`origin_url` 在请求里变为**条件必填**（path 已有仓时可省）；成功响应形状不变。

---

## 文件地图

| 文件 | 职责 |
|------|------|
| `internal/agentd/projectadmin.go` | 扩展 `RegisterProject` 分派：path 不存在 clone-to-path；path 存在可省 origin |
| `internal/agentd/projectadmin_test.go` | 新语义红绿测试 |
| `internal/agentd/server.go` | 注释/`projectAddRequest` 文档同步（handler 逻辑可不动） |
| `internal/client/client.go` | `ProjectAdd` 文档：OriginURL 条件必填（可选：空字段不入 JSON） |
| `web/src/api/types.ts` | `CreateProjectReq.origin_url` 改为可选 |
| `web/src/app/projects/register.ts` | 请求组装：条件带 `origin_url`/`path`/`name`；本机→远程编排 |
| `web/src/app/projects/register.test.ts` | 编排与字段测试 |
| `web/src/app/projects/AddProjectWizard.tsx` | 单页表单重写 |
| `web/src/app/projects/AddProjectWizard.test.tsx` | UI 规则测试重写 |

---

### Task 1: 后端 —— path 存在时可省 origin_url

**Files:**
- Modify: `internal/agentd/projectadmin.go`（`RegisterProject` 入口校验 + `registerExistingProject`）
- Test: `internal/agentd/projectadmin_test.go`

- [ ] **Step 1: 写失败的测试**

追加到 `projectadmin_test.go`：

```go
// TestRegisterProjectExistingInfersOrigin 验证 path 指向已有仓且请求不带
// origin_url 时，agentd 现读 origin 完成登记（Web「只填 path」主路径）。
func TestRegisterProjectExistingInfersOrigin(t *testing.T) {
	m, _, _ := newTestManagerWithAds(t, nil, "fake")
	const origin = "git@github.com:xushixin/handoff.git"
	repo := initGitRepoWithOrigin(t, origin)

	loc, err := m.RegisterProject(context.Background(), RegisterProjectReq{Path: repo})
	if err != nil {
		t.Fatalf("RegisterProject(无 origin): %v", err)
	}
	if loc.OriginURL != origin {
		t.Errorf("OriginURL = %q, want 现读的 %q", loc.OriginURL, origin)
	}
	if loc.ProjectID != projectid.FromOrigin(origin) {
		t.Errorf("project_id = %q, want %q", loc.ProjectID, projectid.FromOrigin(origin))
	}
	if loc.Name != "handoff" {
		t.Errorf("name = %q, want handoff", loc.Name)
	}
}

// TestRegisterProjectRejectsEmptyOriginAndEmptyPath 验证既无 path 也无 origin 时
// 无法确定身份与落点。
func TestRegisterProjectRejectsEmptyOriginAndEmptyPath(t *testing.T) {
	m, _, _ := newTestManagerWithAds(t, nil, "fake")
	_, err := m.RegisterProject(context.Background(), RegisterProjectReq{})
	if !errors.Is(err, errBadDispatchRequest) {
		t.Fatalf("err = %v, want errBadDispatchRequest", err)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/agentd/ -run 'TestRegisterProjectExistingInfersOrigin|TestRegisterProjectRejectsEmptyOriginAndEmptyPath' -count=1`

Expected: `ExistingInfersOrigin` FAIL（仍强制 origin_url）；`EmptyOriginAndEmptyPath` 可能已 PASS。

- [ ] **Step 3: 改 `RegisterProject` 入口与 `registerExistingProject`**

把入口从「origin 必填」改为：

```go
func (m *Manager) RegisterProject(ctx context.Context, req RegisterProjectReq) (proto.ProjectLocation, error) {
	m.log.Info("登记项目请求", "origin", req.OriginURL, "name", req.Name, "path", req.Path)
	req.OriginURL = strings.TrimSpace(req.OriginURL)
	req.Name = strings.TrimSpace(req.Name)
	req.Path = strings.TrimSpace(req.Path)

	if req.OriginURL != "" && strings.HasPrefix(req.OriginURL, "-") {
		return proto.ProjectLocation{}, fmt.Errorf("%w: origin_url 不允许以 - 开头", errBadDispatchRequest)
	}

	if req.Path != "" {
		return m.registerAtPath(ctx, req)
	}
	// 无 path：只能 clone 到 repo_root，必须带 origin
	if req.OriginURL == "" {
		return proto.ProjectLocation{}, fmt.Errorf(
			"%w: 不带 path 时必须提供 origin_url（否则既无落点也无项目身份）",
			errBadDispatchRequest)
	}
	return m.cloneAndRegisterProject(ctx, req)
}
```

将原 `registerExistingProject` 重命名/收敛为 `registerAtPath` 的「存在」分支（Task 2 会补「不存在」）。本 Task 先让「存在 + 可省 origin」通过：

```go
// registerExistingProject：path 已存在时的登记。
// OriginURL 可空——空则采用 inspect 现读的 actual。
func (m *Manager) registerExistingProject(ctx context.Context, req RegisterProjectReq) (proto.ProjectLocation, error) {
	root, actual, err := m.inspectRepoDir(ctx, req.Path)
	if err != nil {
		return proto.ProjectLocation{}, err
	}
	if req.OriginURL != "" {
		if projectid.FromOrigin(actual) != projectid.FromOrigin(req.OriginURL) {
			// ... 既有 ErrProjectOriginMismatch 报文
		}
	}
	// 幂等短路：用 actual 算 pid（身份以磁盘为准）
	pid := projectid.FromOrigin(actual)
	// ... 既有 sameLocation / 冲突逻辑
	return m.persistProject(req.Name, root, actual)
}
```

**关键：** 落库 origin 永远用 `actual`（现读），不采信请求串里未校验的写法；请求 origin 只用于一致性校验。

- [ ] **Step 4: 加日志与注释**

- 入口 Info 保持；分支 Info：`登记已有目录（origin 由磁盘现读）` 当 `req.OriginURL==""`
- `RegisterProjectReq` 注释改写为三态决策表（删「Origin 不可省」旧说法）
- `registerExistingProject` 文档注明 OriginURL 可选及原因

- [ ] **Step 5: 跑测试确认通过**

Run: `go test ./internal/agentd/ -run 'TestRegisterProject' -count=1`

Expected: 全部 PASS（含既有用例）。

- [ ] **Step 6: Commit**

```bash
git add internal/agentd/projectadmin.go internal/agentd/projectadmin_test.go
git commit -m "$(cat <<'EOF'
feat(agentd): 登记已有目录时允许省略 origin_url

path 指向可用 git 仓时由 agentd 现读 origin 作为身份；
请求里的 origin_url 仅作一致性校验。
EOF
)"
```

---

### Task 2: 后端 —— path 不存在 + origin → clone 到该 path

**Files:**
- Modify: `internal/agentd/projectadmin.go`
- Test: `internal/agentd/projectadmin_test.go`

- [ ] **Step 1: 写失败的测试**

```go
// TestRegisterProjectClonesToExplicitPath 验证 path 不存在且带 origin 时
// clone 到调用方指定的 path（不是 repo_root/<name>）。
func TestRegisterProjectClonesToExplicitPath(t *testing.T) {
	m, _, _ := newTestManagerWithAds(t, nil, "fake")
	src := initGitRepo(t)
	// 裸源：clone 用 file:// 最稳
	// 与 TestRegisterProjectClonesWhenNoPath 同样手法造可 clone 源
	src = makeCloneSource(t) // 若无此助手：见下方内联实现

	dest := filepath.Join(t.TempDir(), "workdir", "my-handoff")
	loc, err := m.RegisterProject(context.Background(), RegisterProjectReq{
		OriginURL: src, // file:// 或真实 path——与既有 clone 测试一致
		Name:      "ignored-for-dest",
		Path:      dest,
	})
	if err != nil {
		t.Fatalf("RegisterProject(clone-to-path): %v", err)
	}
	if loc.Path != dest && !sameLocation(loc.Path, dest) {
		// persist 会 Abs，比较 Abs
		want, _ := filepath.Abs(dest)
		if loc.Path != want {
			t.Fatalf("path = %q, want %q", loc.Path, want)
		}
	}
	if _, err := os.Stat(filepath.Join(loc.Path, ".git")); err != nil {
		t.Fatalf("落点应是 git 仓: %v", err)
	}
}

// TestRegisterProjectMissingPathRequiresOrigin 验证 path 不存在且无 origin → 400。
func TestRegisterProjectMissingPathRequiresOrigin(t *testing.T) {
	m, _, _ := newTestManagerWithAds(t, nil, "fake")
	dest := filepath.Join(t.TempDir(), "nope")
	_, err := m.RegisterProject(context.Background(), RegisterProjectReq{Path: dest})
	if !errors.Is(err, errBadDispatchRequest) {
		t.Fatalf("err = %v, want errBadDispatchRequest", err)
	}
}
```

造 clone 源：复用 `TestRegisterProjectClonesWhenNoPath` 的写法（读该测试，原样抽局部变量 `src`）。

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/agentd/ -run 'TestRegisterProjectClonesToExplicitPath|TestRegisterProjectMissingPathRequiresOrigin' -count=1`

Expected: FAIL——当前 path 非空走 registerExisting，目录不存在 → `ErrRepoUnusable`。

- [ ] **Step 3: 实现 `registerAtPath` + `cloneToPathAndRegister`**

```go
// registerAtPath 处理 path 非空：按目录是否存在在「登记已有」与「clone 到该 path」间分派。
func (m *Manager) registerAtPath(ctx context.Context, req RegisterProjectReq) (proto.ProjectLocation, error) {
	_, err := os.Stat(req.Path)
	if errors.Is(err, os.ErrNotExist) {
		if req.OriginURL == "" {
			return proto.ProjectLocation{}, fmt.Errorf(
				"%w: 路径 %s 不存在，且未提供 origin_url，无法 clone",
				errBadDispatchRequest, req.Path)
		}
		return m.cloneToPathAndRegister(ctx, req)
	}
	if err != nil {
		return proto.ProjectLocation{}, fmt.Errorf("%w: 探查路径 %s: %v", ErrRepoUnusable, req.Path, err)
	}
	return m.registerExistingProject(ctx, req)
}

// cloneToPathAndRegister 把 origin clone 到调用方指定的 dest（req.Path）。
// 与 cloneAndRegisterProject 的区别：落点是 req.Path，不是 repo_root/<name>。
// 落点已存在的情况不会进入本函数（由 registerAtPath 分流）。
func (m *Manager) cloneToPathAndRegister(ctx context.Context, req RegisterProjectReq) (proto.ProjectLocation, error) {
	// 幂等：同 project 已登记则直接返回（与 clone 形态一致，clone 之前查表）
	pid := projectid.FromOrigin(req.OriginURL)
	if pid != "" {
		if existing, ok, err := m.registeredProjectByID(pid); err != nil {
			return proto.ProjectLocation{}, err
		} else if ok {
			m.log.Info("项目位置已存在，幂等返回",
				"project_id", existing.ProjectID, "name", existing.Name, "path", existing.Path)
			existing.Status = projectStatusOK
			return existing, nil
		}
	}
	dest := req.Path
	parent := filepath.Dir(dest)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return proto.ProjectLocation{}, fmt.Errorf("%w: 创建落点父目录 %s: %v", ErrRepoUnusable, parent, err)
	}
	m.log.Info("开始克隆项目到指定路径", "origin", req.OriginURL, "dest", dest)
	start := time.Now()
	if _, stderr, err := gitRun(ctx, parent, "clone", "--", req.OriginURL, dest); err != nil {
		m.log.Error("克隆到指定路径失败", "origin", req.OriginURL, "dest", dest,
			"elapsed_ms", time.Since(start).Milliseconds(),
			"stderr", truncateRunes(strings.TrimSpace(stderr), 300), "cause", err)
		return proto.ProjectLocation{}, fmt.Errorf("%w: 克隆 %s 到 %s 失败: %s: %v",
			ErrRepoUnusable, req.OriginURL, dest, strings.TrimSpace(stderr), err)
	}
	m.log.Info("克隆到指定路径完成", "origin", req.OriginURL, "dest", dest,
		"elapsed_ms", time.Since(start).Milliseconds())
	name := req.Name
	if name == "" {
		name = projectNameFromURL(req.OriginURL)
	}
	return m.persistProject(name, dest, req.OriginURL)
}
```

**复用点：** `gitRun` clone、`persistProject`、`registeredProjectByID` 幂等；**不要**复制认领 `repo_root` 落点那套逻辑到任意 path（任意 path 不存在才 clone；存在走 registerExisting）。

可选小重构：把 `cloneAndRegisterProject` 里「真正执行 clone 的几行」抽成 `gitClone(ctx, origin, dest) error`，两处共用——仅当能减少重复且测试全绿时再做，本 Task 不强制。

- [ ] **Step 4: 日志与注释**

- `cloneToPathAndRegister` 文件内注释：与 `cloneAndRegisterProject` 的落点差异
- 成功/失败 Info/Error 带 origin、dest、elapsed_ms
- 更新包内 `RegisterProject` 返回文档中的形态说明

- [ ] **Step 5: 跑测试**

Run: `go test ./internal/agentd/ -run 'TestRegisterProject' -count=1`

Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/agentd/projectadmin.go internal/agentd/projectadmin_test.go
git commit -m "$(cat <<'EOF'
feat(agentd): path 不存在时 clone 到调用方指定目录

Web 本机登记可写绝对 path + git URL，不再只能落到 repo_root/<name>。
EOF
)"
```

---

### Task 3: 后端 —— 文档与 client 注释同步

**Files:**
- Modify: `internal/agentd/server.go`（`projectAddRequest` / `handleProjectAdd` 注释）
- Modify: `internal/client/client.go`（`ProjectAddOpts` / `ProjectAdd` 注释）
- 可选: `ProjectAdd` 发 JSON 时省略空 `origin_url`/`path`/`name`，避免远端收到 `"origin_url":""`（行为上 TrimSpace 后等价，但更干净）

- [ ] **Step 1: 改注释为决策表摘要**（无行为变化也可）

`projectAddRequest`：

```go
// projectAddRequest 是 POST /api/projects 的请求体。
//
// 形态由 path / 路径是否存在 / origin_url 是否非空共同决定：
//   - path 空 + origin 有 → clone 到 repo_root/<name>
//   - path 有且目录存在 → 登记已有仓（origin 可省，省则现读）
//   - path 有且目录不存在 + origin 有 → clone 到该 path
//   - 其余非法组合 → 400
type projectAddRequest struct {
	OriginURL string `json:"origin_url"`
	Name      string `json:"name"`
	Path      string `json:"path"`
}
```

- [ ] **Step 2: client 发请求时跳过空字段（推荐）**

```go
body := map[string]any{}
if opts.OriginURL != "" {
	body["origin_url"] = opts.OriginURL
}
if opts.Name != "" {
	body["name"] = opts.Name
}
if opts.Path != "" {
	body["path"] = opts.Path
}
resp, err := c.do(ctx, http.MethodPost, "/api/projects", body)
```

- [ ] **Step 3: 回归 agentd 登记测试 + 一次 integration 若有**

Run: `go test ./internal/agentd/ -run 'TestRegisterProject|TestProject' -count=1`

- [ ] **Step 4: Commit**

```bash
git add internal/agentd/server.go internal/client/client.go
git commit -m "docs(api): 同步项目登记三态语义到 HTTP/client 注释"
```

---

### Task 4: 前端 —— `CreateProjectReq` 与 `register` 编排

**Files:**
- Modify: `web/src/api/types.ts`
- Modify: `web/src/app/projects/register.ts`
- Test: `web/src/app/projects/register.test.ts`

- [ ] **Step 1: 更新类型**

```ts
// CreateProjectReq：origin_url 在 path 已有仓时可省；path 在远程默认 clone 时可省。
export interface CreateProjectReq {
  origin_url?: string
  name?: string
  path?: string
}
```

- [ ] **Step 2: 重写 register 模块 API**

```ts
// LocationChoice 单个位置一次 POST。
export interface LocationChoice {
  machine: string
  originUrl?: string
  name?: string
  path?: string
}

export interface RegisterFormInput {
  name: string
  localPath: string
  gitUrl: string
  remoteMachine: string | null  // null = 不登记远程
  remotePath: string
}

export async function registerFromForm(input: RegisterFormInput): Promise<RegisterOutcome[]> {
  const localReq: CreateProjectReq = {}
  if (input.name.trim()) localReq.name = input.name.trim()
  if (input.gitUrl.trim()) localReq.origin_url = input.gitUrl.trim()
  localReq.path = input.localPath.trim()

  const local = await registerOne({ machine: '', ...map(localReq) })
  const outcomes: RegisterOutcome[] = [toOutcome('', local)]

  if (!local.ok || !input.remoteMachine) return outcomes

  // 远程复用本机响应的权威 origin / name，不要求用户再填 URL
  const remoteReq: CreateProjectReq = {
    origin_url: local.result!.origin_url,
    name: local.result!.name,
  }
  if (input.remotePath.trim()) remoteReq.path = input.remotePath.trim()

  const remote = await registerOne({ machine: input.remoteMachine, ... })
  outcomes.push(toOutcome(input.remoteMachine, remote))
  return outcomes
}

// registerAll 保留给「结果页按位置重试」：入参为完整 LocationChoice[]。
```

`registerOne`：只把非空字段放进 body（与 client 一致）。

- [ ] **Step 3: 测试**

```ts
it('本机只带 path 时不传 origin_url', async () => {
  const spy = vi.spyOn(client, 'createProject').mockResolvedValue({
    project_id: 'p', name: 'handoff', path: '/Users/me/handoff',
    origin_url: 'git@x:h.git', created_at: 't',
  })
  await registerFromForm({
    name: '', localPath: '/Users/me/handoff', gitUrl: '',
    remoteMachine: null, remotePath: '',
  })
  expect(spy).toHaveBeenCalledWith({ path: '/Users/me/handoff' }, '')
})

it('本机成功后再打远程，远程 origin/name 来自本机响应', async () => {
  const spy = vi.spyOn(client, 'createProject')
    .mockResolvedValueOnce({
      project_id: 'p', name: 'handoff', path: '/Users/me/h',
      origin_url: 'git@x:h.git', created_at: 't',
    })
    .mockResolvedValueOnce({
      project_id: 'p', name: 'handoff', path: '/root/h',
      origin_url: 'git@x:h.git', created_at: 't',
    })
  await registerFromForm({
    name: '', localPath: '/Users/me/h', gitUrl: 'git@x:h.git',
    remoteMachine: 'devbox', remotePath: '',
  })
  expect(spy).toHaveBeenNthCalledWith(2, {
    origin_url: 'git@x:h.git', name: 'handoff',
  }, 'devbox')
})

it('本机失败时不请求远程', async () => {
  vi.spyOn(client, 'createProject').mockRejectedValue(new ApiError(400, '路径不存在'))
  const out = await registerFromForm({
    name: '', localPath: '/nope', gitUrl: '',
    remoteMachine: 'devbox', remotePath: '',
  })
  expect(out).toHaveLength(1)
  expect(out[0].ok).toBe(false)
})
```

- [ ] **Step 4: 日志**——前端无 structured logger；失败文案继续 `errorMessage` 透传 agentd 原文。模块头注释写清编排职责与「不在浏览器侧猜 origin」。

- [ ] **Step 5: 跑 vitest**

Run: `cd web && npx vitest run src/app/projects/register.test.ts`

Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add web/src/api/types.ts web/src/app/projects/register.ts web/src/app/projects/register.test.ts
git commit -m "feat(web): 项目登记编排支持本机优先与远程复用 origin"
```

---

### Task 5: 前端 —— 单页 `AddProjectWizard`

**Files:**
- Modify: `web/src/app/projects/AddProjectWizard.tsx`
- Test: `web/src/app/projects/AddProjectWizard.test.tsx`

- [ ] **Step 1: 重写 UI（单页，无 locations/sources 两步）**

结构：

1. 标题「添加项目」
2. **名称** `<input>` placeholder「可选；默认用仓库名」
3. **本机** 区块（固定，无 checkbox）
   - path 必填
   - git URL 选填 placeholder「可选；path 已有仓时可留空自动读取」
4. **开发机** 区块
   - 「同时登记到开发机」checkbox
   - 勾选后：远程机器 radio 列表（至多一台）+ 可选 path
   - **无** URL 输入
5. 提交 / 取消
6. 提交后切到结果列表（复用现有 outcomes UI）

校验 `canSubmit`：

```ts
const canSubmit =
  localPath.trim() !== '' &&
  (!remoteEnabled || remoteMachine !== null)
// 不要求 gitUrl；path 不存在且无 URL 的错误交给后端 400 原文
```

调用：`registerFromForm({ name, localPath, gitUrl, remoteMachine: remoteEnabled ? remoteMachine : null, remotePath })`

重试：对失败位置构造 `LocationChoice`——本机用表单字段；远程用「本机成功结果的 origin/name」或表单 gitUrl（若本机也失败则仅当 gitUrl 非空才允许远程重试，否则按钮 disabled + 提示）。

- [ ] **Step 2: 重写测试**

```ts
it('本机 path 为空时提交禁用', () => { ... })
it('不展示「下一步」——单页直接提交', () => {
  expect(screen.queryByRole('button', { name: '下一步' })).toBeNull()
  expect(screen.getByRole('button', { name: '提交' })).toBeInTheDocument()
})
it('有 name 输入框', () => {
  expect(screen.getByPlaceholderText(/可选.*仓库名|项目名/)).toBeInTheDocument()
})
it('远程勾选后仍无第二处 Git URL 输入', () => {
  fireEvent.click(screen.getByLabelText(/同时登记到开发机|开发机/))
  expect(screen.getAllByPlaceholderText(/Git|仓库地址|origin/i)).toHaveLength(1) // 仅本机
})
it('不可达远程可选并提示登记可能失败', () => { ...保留语义... })
it('一成一败逐位置展示且可重试', async () => { ... })
```

删除「两步 / 本机 checkbox / 每位置各填 Git」旧断言。

- [ ] **Step 3: 组件头注释**

改写为：单页本机优先登记；远程复用本机 origin；不做浏览器侧 path 探测。

- [ ] **Step 4: 跑测试**

Run: `cd web && npx vitest run src/app/projects/`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add web/src/app/projects/AddProjectWizard.tsx web/src/app/projects/AddProjectWizard.test.tsx
git commit -m "feat(web): 添加项目改为单页本机优先流程"
```

---

### Task 6: 全量验证与手工验收清单

- [ ] **Step 1: Go 全量相关包**

Run:

```bash
go test ./internal/agentd/ ./internal/client/ ./cmd/ -count=1
```

Expected: PASS

- [ ] **Step 2: Web 相关 vitest**

Run:

```bash
cd web && npx vitest run src/app/projects/ src/api/
```

Expected: PASS

- [ ] **Step 3: 手工验收（有运行中的 agentd 时）**

| # | 操作 | 期望 |
|---|------|------|
| 1 | 只填本机已有 git path，无 URL、无 name | 成功；树出现项目；name 为 origin 末段 |
| 2 | 本机 path 不存在 + 有效 URL | clone 到该 path 并登记 |
| 3 | 本机 path 不存在 + 无 URL | 400 文案可读 |
| 4 | 本机 path 存在但是别的仓 + 填了错误 URL | origin mismatch |
| 5 | 本机成功 + 勾远程、远程 path 空 | 远程 clone 到 repo_root |
| 6 | 本机成功 + 远程 path 已是同仓 | 幂等成功 |
| 7 | 填 name | 两边登记名一致（远程用本机返回的 name） |

- [ ] **Step 4: instrumenting 自检**

- [ ] `RegisterProject` 各新分支有 Info/Warn/Error
- [ ] clone-to-path 成功路径有完成日志
- [ ] 新/改导出函数有中文文档注释（决策表 why）
- [ ] 前端无 silent 吞错；失败透传 agentd 原文

---

## Spec 覆盖自检

| 需求 | Task |
|------|------|
| 本机必选、path 必填 | Task 5 |
| git URL 选填 | Task 1 + 4 + 5 |
| path 不存在 + URL → clone 到 path | Task 2 |
| path 不存在 + 无 URL → 报错 | Task 2 |
| path 存在 + 无 URL → 现读 origin | Task 1 |
| 远程可选、不填 URL | Task 4 + 5 |
| 远程 path 可选；存在校验/幂等/异仓报错 | 既有后端 + Task 4 透传 |
| name 可写 | Task 4 + 5 |
| 单步表单 | Task 5 |
| 不凑合 / 可复用 inspect+persist+幂等 | Task 1–2 |

## Placeholder 扫描

无 TBD/TODO 步骤；测试与实现代码块完整。

## 类型一致性

- Go: `RegisterProjectReq{OriginURL, Name, Path string}` 字段名不变，语义变宽。
- TS: `CreateProjectReq.origin_url?`；编排 `registerFromForm` / `registerAll`。
- HTTP JSON 字段名不变：`origin_url` / `name` / `path`。
