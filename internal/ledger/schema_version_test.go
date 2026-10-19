// schema 版本防漏升（spec ledger-schema-version S4）。
//
// 职责：把两方言 DDL 列表的指纹钉在 ledgerSchemaVersion 上。PG Open 在库内标记
// ≥ 代码版本时跳过全部 DDL，改了 DDL 却不升版本号，已在旧版本标记上的库永远
// 吃不到新 DDL——本测试把这个遗漏当场翻红。
// 边界：只钉 ddlStatements 两支；Go 侧迁移（ensureSchema 里的 ALTER/回填）测不到，
// 靠 ensureSchema 头注释与审查。
package ledger

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
)

// pinnedDDLFingerprints 版本号 → 该版本的 DDL 指纹。升版本时追加一行，旧行保留作历史。
var pinnedDDLFingerprints = map[int]string{
	1: "d84335016f3b445a7f1fd0fbdf4c3429ba4a1efc9a0eb6b464d64b0ef350c011",
}

func ddlFingerprint() string {
	h := sha256.New()
	h.Write([]byte(strings.Join(ddlStatements(true), "\x00")))
	h.Write([]byte{0xff})
	h.Write([]byte(strings.Join(ddlStatements(false), "\x00")))
	return hex.EncodeToString(h.Sum(nil))
}

func TestSchemaVersionPinsDDL(t *testing.T) {
	got := ddlFingerprint()
	want, ok := pinnedDDLFingerprints[ledgerSchemaVersion]
	if !ok || got != want {
		t.Fatalf("DDL 指纹 %s 与 ledgerSchemaVersion=%d 钉住的 %q 不符：改了 DDL 就把 ledgerSchemaVersion 加 1，"+
			"并在 pinnedDDLFingerprints 追加新版本号 → 新指纹", got, ledgerSchemaVersion, want)
	}
}
