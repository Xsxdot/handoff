// kinds.go —— 账号 kind 规则表（B464 段 2，plan Q6 分支 b、Q11）。
//
// 职责：每个 kind 一处写清两条规则，发现器、探测器、读面推导、段 3 的新增登录校验都从这里取：
//   - 是否支持额度探测：claude 与第三方 API key 不支持（状态 na，阈值与间隔缺席，plan Q11）；
//   - 在哪些平台上只认主 HOME：claude 在 darwin 上登录存在钥匙串里、同一台 Mac 所有 HOME
//     共用一份（用户 2026-10-22 裁决「先按共用处理」），非主 HOME 不发现为 claude 登录；linux
//     每个 HOME 独立。平台由调用方注入（GOOS），本包不读 runtime。
//
// 边界：纯规则，无 IO。未知 kind 按「支持探测、不限 HOME」处理（kind 词表校验在别处）。
package accounts

import "slices"

type kindRule struct {
	probe        bool     // 支持额度探测
	mainHomeOnly []string // 这些 GOOS 上只认主 HOME
}

var kindRules = map[string]kindRule{
	KindOpencodeGo: {probe: true},
	KindCodex:      {probe: true},
	KindGrok:       {probe: true},
	KindAgy:        {probe: true},
	KindClaude:     {mainHomeOnly: []string{"darwin"}},
	KindAPIKey:     {},
}

// SupportsProbe 报告 kind 是否支持额度探测（false = 账号状态恒为 na）。
func SupportsProbe(kind string) bool {
	r, ok := kindRules[kind]
	return !ok || r.probe
}

// LoginMainHomeOnly 报告在 goos 平台上该 kind 的登录是否只认所在机主 HOME（plan Q6 分支 b：
// darwin 上的 claude）。发现器据此不在非主 HOME 认 claude 登录；新增登录（段 3）据此在 darwin
// 选 claude 时只允许主 HOME 并说明原因。
func LoginMainHomeOnly(kind, goos string) bool {
	return slices.Contains(kindRules[kind].mainHomeOnly, goos)
}
