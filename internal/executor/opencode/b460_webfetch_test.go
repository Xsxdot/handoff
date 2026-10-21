package opencode

// b460_webfetch_test.go —— B460 S4 V5：opencode 的 webfetch 权限请求产出结构化载荷
// （Tool=webfetch + URL）。testdata/perm_webfetch.json 目前是**合成样本**（字段形状按
// opencode webfetch 工具的 ask 调用推断：metadata.url + patterns[0]），真机样本（U1）
// 由协调者 R4a 取得后替换本文件引用的 testdata 并复核字段名。
import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Xsxdot/handoff/internal/executor"
)

func TestB460PermissionAskedWebFetchStructured(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "perm_webfetch.json"))
	if err != nil {
		t.Fatalf("读 webfetch 载荷样本: %v", err)
	}
	a, r := newAdapterWithRunForTest(t)
	a.mapPermissionAsked(r, raw)
	ev := <-r.EventsForTest()
	if ev.Perm == nil || ev.Perm.Tool != executor.PermToolWebFetch || ev.Perm.URL != "https://go.dev/doc/" {
		t.Fatalf("webfetch 应结构化为 Tool=webfetch + URL，实得 %+v", ev.Perm)
	}
}

func TestB460PermissionAskedWebFetchPatternsFallback(t *testing.T) {
	a, r := newAdapterWithRunForTest(t)
	a.mapPermissionAsked(r, []byte(`{"id":"p1","permission":"webfetch","patterns":["https://example.com/a"]}`))
	ev := <-r.EventsForTest()
	if ev.Perm == nil || ev.Perm.Tool != executor.PermToolWebFetch || ev.Perm.URL != "https://example.com/a" {
		t.Fatalf("无 metadata.url 时应退回 patterns[0]，实得 %+v", ev.Perm)
	}
}

// TestB460PermissionAskedWebFetchNoURLNil 去掉 URL 字段（且 patterns 不是 http(s) 地址）→
// Perm=nil：不伪造空壳（executor.go 纪律），交 manager fail-closed 升级人工。
func TestB460PermissionAskedWebFetchNoURLNil(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "perm_webfetch.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	delete(m["metadata"].(map[string]any), "url")
	m["patterns"] = []string{"*"}
	stripped, _ := json.Marshal(m)
	a, r := newAdapterWithRunForTest(t)
	a.mapPermissionAsked(r, stripped)
	ev := <-r.EventsForTest()
	if ev.Perm != nil {
		t.Fatalf("取不到 URL 时必须 Perm=nil，实得 %+v", ev.Perm)
	}
}
