// Package ledgerapi 是账本域的入站 api 门面（B156.3 §7.0）：域外读卡、读事件流、
// 写注册表实体只经此包，不得 import internal/ledger 内部。薄层——方法一一转发
// 既有 Store 实现，不改其内部；后续 B156.2 会话子系统落地时复用同一个门面包，
// 不建第二个。
//
// 命名说明：包名取 ledgerapi 而不是 internal/ledger/api，因为代码图扫描按
// 包名生成容器 id——多个域各开一个 `api` 子包会撞成同一批 k_api_* 容器。
package ledgerapi

import (
	"encoding/json"

	"github.com/Xsxdot/handoff/internal/ledger"
)

// Card / Event 是账本核心实体的门面别名：消费方只 import 本包即可拿到类型，
// 不必认识内部包路径。
type (
	Card  = ledger.Card
	Event = ledger.Event
)

// RegistryEntry 是注册表一行（载体/小队/点火队列等版本化实体）。
type RegistryEntry struct {
	Kind    string
	ID      string
	Version int
	Seq     int64 // 全表单调写入序；编制域出队 FIFO 的依据
	Body    json.RawMessage
}

// Facade 是账本能力的唯一出域窗口，包装既有 *ledger.Store。
type Facade struct {
	st *ledger.Store
}

// New 包装一个已打开的账本库。组装点构造一次后分发给各消费方，
// 不另开数据库连接。
func New(st *ledger.Store) *Facade { return &Facade{st: st} }

// Close 关闭底层账本库（透传）。
func (f *Facade) Close() error { return f.st.Close() }

// GetCard 读一张卡（透传 ledger.Store.GetCard）。
func (f *Facade) GetCard(id string) (Card, error) { return f.st.GetCard(id) }

// EventsFromAsc 按卡读事件流升序段（透传 ledger.Store.EventsFromAsc）。
func (f *Facade) EventsFromAsc(cardIDs []string, fromSeq int64, limit int) ([]Event, error) {
	return f.st.EventsFromAsc(cardIDs, fromSeq, limit)
}

// EffectiveBaseBranch 读卡的有效基线分支（透传）。
func (f *Facade) EffectiveBaseBranch(id string) (string, error) {
	return f.st.EffectiveBaseBranch(id)
}

// AddComment 在卡 timeline 追加评论（透传）。会话子系统未落地前，
// 协调者叙事以卡 note 兜底落账。
func (f *Facade) AddComment(cardID, body, kind, actor string) (Event, error) {
	return f.st.AddComment(cardID, body, kind, actor)
}

// MarkNeedsHuman 打等人标记（透传）。唤醒兜底链的终点「转等人」经此落账。
func (f *Facade) MarkNeedsHuman(cardID, reason, actor string) error {
	return f.st.MarkNeedsHuman(cardID, reason, actor)
}

// RegistryPut 以 CAS 语义写入注册表实体，返回新版本号（透传）。
func (f *Facade) RegistryPut(kind, id string, expectVersion int, body []byte, actor string) (int, error) {
	return f.st.RegistryPut(kind, id, expectVersion, body, actor)
}

// RegistryGet 读一条注册表实体（透传；不存在返回 ledger.ErrNotFound）。
func (f *Facade) RegistryGet(kind, id string) (RegistryEntry, error) {
	e, err := f.st.RegistryGet(kind, id)
	if err != nil {
		return RegistryEntry{}, err
	}
	return RegistryEntry{Kind: e.Kind, ID: e.ID, Version: e.Version, Seq: e.Seq, Body: e.Body}, nil
}

// RegistryList 按 kind 列出全部实体，seq 升序（透传）。
func (f *Facade) RegistryList(kind string) ([]RegistryEntry, error) {
	rows, err := f.st.RegistryList(kind)
	if err != nil {
		return nil, err
	}
	out := make([]RegistryEntry, 0, len(rows))
	for _, e := range rows {
		out = append(out, RegistryEntry{Kind: e.Kind, ID: e.ID, Version: e.Version, Seq: e.Seq, Body: e.Body})
	}
	return out, nil
}

// RegistryDelete 以 CAS 语义删除一条注册表实体（透传）。
func (f *Facade) RegistryDelete(kind, id string, expectVersion int, actor string) error {
	return f.st.RegistryDelete(kind, id, expectVersion, actor)
}
