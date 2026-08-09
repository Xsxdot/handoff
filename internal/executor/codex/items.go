// items.go —— codex ThreadItem 的结构定义与 itemId → item 的有界索引。
//
// 职责：
//   - 解析 item/started 与 item/completed 通知里的 item 本体
//   - 维护 itemId → 最近一次 item 的有界索引
//   - 把 item 渲染成 render.log 的一行人读文本
//
// 边界：
//   - 不产 handoff 事件、不做权限判据：判据在 perm.go，事件在 adapter.go
//
// 为什么需要索引：`item/fileChange/requestApproval` 的报文**没有路径**（schema 的
// 必填字段只有 itemId/threadId/turnId/startedAtMs，spec §5.4），路径只在同 itemId 的
// item 通知的 changes[].path 里。没有这张索引，写文件类权限门交出的 PermRequest
// 就没有路径，B27 的路径判据直接失效。
//
// 为什么有界：item 数量由 codex 侧决定，长任务可产出上万条。权限请求总是紧跟在
// 对应 item 之后到达，512 条窗口足够宽；无界会让内存随 item 数线性增长。
package codex

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
)

// itemIndexCap 是索引容量上限，超出后淘汰最旧条目。
const itemIndexCap = 512

// changeKind 是 fileChange 的变更类型。
//
// 注意：schema 里它是**对象** {"type":"add"|"delete"|"update"} 而不是裸字符串，
// 按字符串解析会静默得到空值，进而让 Task 5 的工具分类全部退化成 write。
type changeKind struct {
	Type string `json:"type"`
}

// fileUpdateChange 是 fileChange item 里的一条文件变更。
type fileUpdateChange struct {
	Path string     `json:"path"`
	Kind changeKind `json:"kind"`
}

// commandAction 是 commandExecution 的结构化动作（codex 自己给出的动作类型与路径）。
//
// 注意：本期**不改权限判据**（spec §9），这里只做保留与展示；它是后续替换正则
// 判据的更可靠输入。
type commandAction struct {
	Type    string `json:"type"`
	Command string `json:"command"`
	Path    string `json:"path"`
}

// threadItem 是 codex 一条 ThreadItem 的宽松视图。
//
// 注意：字段取并集而非按 type 分结构——codex 的 item 类型会增长，宽松视图让
// 未知类型也能落进索引与 render.log，不至于整条丢弃。
type threadItem struct {
	Type             string             `json:"type"`
	ID               string             `json:"id"`
	Text             string             `json:"text"`
	Command          string             `json:"command"`
	Cwd              string             `json:"cwd"`
	Status           string             `json:"status"`
	ExitCode         *int               `json:"exitCode"`
	AggregatedOutput string             `json:"aggregatedOutput"`
	CommandActions   []commandAction    `json:"commandActions"`
	Changes          []fileUpdateChange `json:"changes"`
}

// parseItemNotification 从 item/started、item/completed 的 params 里取出 item 本体。
//
// 参数：
//   - params: 通知的 params 原文
//
// 返回：
//   - item 与 true；params 不是合法 item 通知（缺 item、缺 id 或缺 type）时返回 nil, false
//
// 注意：解析失败一律返回 false 而不是半成品——半成品会让索引里存进一条没有路径
// 的 fileChange，权限门据此交出空 Paths，比查不到更危险（查不到会 fail-closed）。
func parseItemNotification(params json.RawMessage) (*threadItem, bool) {
	var env struct {
		Item *threadItem `json:"item"`
	}
	if err := json.Unmarshal(params, &env); err != nil || env.Item == nil {
		return nil, false
	}
	if env.Item.ID == "" || env.Item.Type == "" {
		return nil, false
	}
	return env.Item, true
}

// itemIndex 是 itemId → 最近一次 item 的有界索引（FIFO 淘汰）。并发安全。
type itemIndex struct {
	mu    sync.Mutex
	cap   int
	order []string
	m     map[string]*threadItem
}

// newItemIndex 建一个容量为 capacity 的索引（capacity <= 0 时退回 itemIndexCap）。
func newItemIndex(capacity int) *itemIndex {
	if capacity <= 0 {
		capacity = itemIndexCap
	}
	return &itemIndex{cap: capacity, m: map[string]*threadItem{}}
}

// put 写入或更新一条 item。
//
// 注意：同 id 重复写入只更新内容、不占新槽位——item/started 与 item/completed 会
// 对同一个 id 各投递一次，占两个槽位等于把索引窗口砍半。
func (x *itemIndex) put(it *threadItem) {
	if it == nil || it.ID == "" {
		return
	}
	x.mu.Lock()
	defer x.mu.Unlock()
	if _, exists := x.m[it.ID]; !exists {
		x.order = append(x.order, it.ID)
		for len(x.order) > x.cap {
			oldest := x.order[0]
			x.order = x.order[1:]
			delete(x.m, oldest)
		}
	}
	x.m[it.ID] = it
}

// get 取一条 item；不存在时返回 nil, false（调用方据此 fail-closed）。
func (x *itemIndex) get(id string) (*threadItem, bool) {
	x.mu.Lock()
	defer x.mu.Unlock()
	it, ok := x.m[id]
	return it, ok
}

// renderLine 把 item 渲染成 render.log 的一行人读文本。
//
// 注意：审核者 `handoff attach` 看到的就是这行，跨 executor 要同形——
// 命令带 cwd、文件变更带路径清单、模型消息取正文。
func (it *threadItem) renderLine() string {
	switch it.Type {
	case "commandExecution":
		s := "【命令】" + strings.TrimSpace(it.Command)
		if it.Cwd != "" {
			s += "  (cwd: " + it.Cwd + ")"
		}
		if it.ExitCode != nil {
			s += fmt.Sprintf("  → exit %d", *it.ExitCode)
		}
		return s
	case "fileChange":
		paths := make([]string, 0, len(it.Changes))
		for _, c := range it.Changes {
			paths = append(paths, c.Kind.Type+" "+c.Path)
		}
		return "【文件变更】" + strings.Join(paths, ", ")
	case "agentMessage":
		return strings.TrimSpace(it.Text)
	case "reasoning":
		return "【推理】" + strings.TrimSpace(it.Text)
	default:
		if s := strings.TrimSpace(it.Text); s != "" {
			return "【" + it.Type + "】" + s
		}
		return "【" + it.Type + "】"
	}
}
