// 注册表半段（B156.3 门面归一轮补入）：编制域经 schedclient.Registry 端口
// 消费的四个版本化 KV 原语。逐方法转调 internal/ledger/registry.go 的既有
// Store 实现 + RegistryEntry→本包 DTO 映射，不含任何业务判断——载体/小队的
// 规则归 d_scheduling（B156.3 spec §7.1）。
//
// 跨卡约束：本文件与 keystone.go 是与 B156.2 商定的新增落点，api.go 一个
// 字符不动；两份门面已归一，internal/ledgerapi 不复存在。
package api

import (
	"encoding/json"

	"github.com/Xsxdot/handoff/internal/ledger"
)

// RegistryEntry 是注册表一行（载体/小队/点火队列等版本化实体）的门面 DTO。
// 字段与 ledger.RegistryEntry 一一对应：Seq 是全表单调写入序，Body 是写入方
// 序列化好的 JSON——账本不解释其内容。
type RegistryEntry struct {
	Kind    string
	ID      string
	Version int
	Seq     int64
	Body    json.RawMessage
}

// Put 以 CAS 语义写入注册表实体，返回新版本号（透传 Store.RegistryPut）。
// expectVersion 必须等于当前版本；0 表示必须不存在（新建）。
func (f *Facade) Put(kind, id string, expectVersion int, body []byte, actor string) (int, error) {
	return f.st.RegistryPut(kind, id, expectVersion, body, actor)
}

// Get 读一条注册表实体（透传；不存在返回 ledger.ErrNotFound 包装错误）。
func (f *Facade) Get(kind, id string) (RegistryEntry, error) {
	e, err := f.st.RegistryGet(kind, id)
	if err != nil {
		return RegistryEntry{}, err
	}
	return registryWire(e), nil
}

// List 按 kind 列出全部实体，seq 升序（透传）。
func (f *Facade) List(kind string) ([]RegistryEntry, error) {
	rows, err := f.st.RegistryList(kind)
	if err != nil {
		return nil, err
	}
	out := make([]RegistryEntry, 0, len(rows))
	for _, e := range rows {
		out = append(out, registryWire(e))
	}
	return out, nil
}

// Delete 以 CAS 语义删除一条注册表实体（透传）。expectVersion 必须等于当前
// 版本；删除前先 Get 取版本。
func (f *Facade) Delete(kind, id string, expectVersion int, actor string) error {
	return f.st.RegistryDelete(kind, id, expectVersion, actor)
}

// registryWire 账本注册表行 → 门面 DTO。
func registryWire(e ledger.RegistryEntry) RegistryEntry {
	return RegistryEntry{Kind: e.Kind, ID: e.ID, Version: e.Version, Seq: e.Seq, Body: e.Body}
}
