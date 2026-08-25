// keystone 半段（B156.3 门面归一轮补入）：keystone 域经 keysclient.LedgerView
// 端口消费的两条账本能力。逐方法转调既有 Store 实现，不含任何业务判断——
// 唤醒决策与兜底链的规则归 d_keystone（B156.3 spec §7.1）。
//
// 跨卡约束：本文件与 registry.go 是与 B156.2 商定的新增落点，api.go 一个
// 字符不动；两份门面已归一，internal/ledgerapi 不复存在。
package api

// EffectiveBaseBranch 读卡的有效基线分支（透传 Store.EffectiveBaseBranch，
// internal/ledger/relations.go）。开场评估与重建四步的新鲜度判据。
func (f *Facade) EffectiveBaseBranch(id string) (string, error) {
	return f.st.EffectiveBaseBranch(id)
}

// MarkNeedsHuman 打等人标记（透传 Store.MarkNeedsHuman，
// internal/ledger/events.go）。唤醒兜底链终点「转等人」经此落账。
func (f *Facade) MarkNeedsHuman(cardID, reason, actor string) error {
	return f.st.MarkNeedsHuman(cardID, reason, actor)
}
