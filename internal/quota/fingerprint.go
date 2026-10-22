// fingerprint.go —— 凭据身份短指纹（B453 plan Q13）。
//
// 语义是「是不是同一个账号」，不是「是不是同一个 token」：各适配器传入不随 token
// 刷新而变的身份值（opencode-go 的 API key、codex 的 account_id、grok 的 user_id/email、
// agy 的登录身份），这里只存 sha256 前 12 个十六进制字符，不可逆、不是凭据的子串。
package quota

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// Fingerprint 返回 `sha256:<前 12 位十六进制>`；身份值为空返回空串（取不到 = 不比对）。
func Fingerprint(identity string) string {
	identity = strings.TrimSpace(identity)
	if identity == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(identity))
	return "sha256:" + hex.EncodeToString(sum[:])[:12]
}

// Identifier 是能离线（不出网、不刷新）从凭据文件取身份值的适配。检测链用它在载体 HOME
// 里算指纹，与账号读数指纹比对（spec S4）。agy 的身份只在 `/usage` 输出里，不实现它。
type Identifier interface {
	Identity(home, entry string) (string, error)
}

// CredentialFingerprint 按 kind 在 home 下读凭据身份并返回短指纹（plan Q13 同一取材）。
// 未注册 kind 返回 unsupported_kind；适配没有离线身份来源返回空串（不比对）；凭据缺失/
// 形状不对返回 *Failure（文案只带路径与条目名，不含凭据值）。
func (r *Registry) CredentialFingerprint(kind, home, entry string) (string, error) {
	a, ok := r.adapters[kind]
	if !ok {
		return "", fail(CategoryUnsupportedKind, "类型 %q 的凭据指纹尚未支持", kind)
	}
	id, ok := a.(Identifier)
	if !ok {
		return "", nil
	}
	identity, err := id.Identity(home, entry)
	if err != nil {
		return "", err
	}
	return Fingerprint(identity), nil
}
