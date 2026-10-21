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
