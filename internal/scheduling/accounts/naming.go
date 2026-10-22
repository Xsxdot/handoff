// naming.go —— 账号 id、登录 id 与自动起名（B464 plan Q2/L1/L4/L5）。
//
// 职责：
//   - AccountID：由 kind（+ provider）+ 身份短指纹确定性导出，registry 主键即去重键——两台
//     机器同时发现同一身份 = 对同一 id 的 expect=0 竞争，一方 409 后重读即归并；
//   - LoginID：「机器 × HOME × kind × provider × 来源」槽位的确定性哈希，重启不变、URL 安全；
//   - autoName：邮箱 label 优先，否则 `<kind>-<指纹前 4 hex>`（第三方 key 为
//     `<provider>-key-<4>`），撞名加 `-2`、`-3`…；
//   - isAccountID：区分账号 id 与名字（名字不含 `:`，plan Q15）。
//
// 边界：只做纯计算；身份材料从不进入本包——入参只有 quota 算好的 `sha256:<hex12>` 指纹。
package accounts

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

// fingerprintHex 取 `sha256:<hex>` 的十六进制部分；形状不对返回空串。
func fingerprintHex(fp string) string {
	hexPart, ok := strings.CutPrefix(strings.TrimSpace(fp), "sha256:")
	if !ok || len(hexPart) < 4 {
		return ""
	}
	for _, r := range hexPart {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			return ""
		}
	}
	return hexPart
}

// AccountID 返回账号的确定性 id（plan L1）：`<kind>:<hex12>`，第三方 key 为
// `apikey:<provider>:<hex12>`。指纹形状不对返回空串（调用方按「不是可登记身份」处理）。
func AccountID(kind, provider, fingerprint string) string {
	h := fingerprintHex(fingerprint)
	if h == "" || strings.TrimSpace(kind) == "" {
		return ""
	}
	if kind == KindAPIKey {
		if strings.TrimSpace(provider) == "" {
			return ""
		}
		return KindAPIKey + ":" + provider + ":" + h
	}
	return kind + ":" + h
}

// LoginID 返回登录槽位的确定性 id（plan L5）：`login-<16 hex>`，只含 [a-z0-9-]。
// 身份不参与——同一 HOME 重新登录另一个账号是同一条登录改挂账号（plan §3）。
func LoginID(machine, home, kind, provider, source string) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{machine, home, kind, provider, source}, "\x00")))
	return "login-" + hex.EncodeToString(sum[:])[:16]
}

// isAccountID 报告 ref 是账号 id 还是名字：名字禁止含 `:`（plan L4），id 必含。
func isAccountID(ref string) bool { return strings.Contains(ref, ":") }

// baseName 是自动名的基干（未加撞名后缀）。label 只在不含 `:`/`/` 时可用作名字。
func baseName(kind, provider, label, fingerprint string) string {
	if l := strings.TrimSpace(label); l != "" && !strings.ContainsAny(l, ":/") {
		return l
	}
	h := fingerprintHex(fingerprint)
	if len(h) > 4 {
		h = h[:4]
	}
	if kind == KindAPIKey {
		return provider + "-key-" + h
	}
	return kind + "-" + h
}

// autoName 在已占用名字集合里为新账号挑名字：基干可用即用，否则 `-2`、`-3`…（plan Q2）。
func autoName(kind, provider, label, fingerprint string, taken map[string]bool) string {
	base := baseName(kind, provider, label, fingerprint)
	if !taken[base] {
		return base
	}
	for i := 2; ; i++ {
		if name := fmt.Sprintf("%s-%d", base, i); !taken[name] {
			return name
		}
	}
}

// CompatibleKinds 返回执行器 CLI 可用的账号 kind（spec S6）：opencode → opencode-go 与第三方
// API key；codex/grok/agy/claude 一一对应；其余 CLI 无兼容 kind（只能选「无账号 · 主 HOME」）。
// cli 可带路径，按 basename 判。
func CompatibleKinds(cli string) []string {
	base := cli
	if i := strings.LastIndexAny(base, `/\`); i >= 0 {
		base = base[i+1:]
	}
	switch base {
	case "opencode":
		return []string{KindOpencodeGo, KindAPIKey}
	case KindCodex, KindGrok, KindAgy, KindClaude:
		return []string{base}
	default:
		return nil
	}
}

// Compatible 报告登录 kind 是否可被该 CLI 的载体选用。
func Compatible(cli, kind string) bool {
	for _, k := range CompatibleKinds(cli) {
		if k == kind {
			return true
		}
	}
	return false
}
