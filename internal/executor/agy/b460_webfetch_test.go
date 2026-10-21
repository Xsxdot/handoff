package agy

// b460_webfetch_test.go —— B460 S4 V5：agy 的 read_url_content 结构化请求补上 URL，
// 供权限门放行审计记录抓取地址。
import (
	"encoding/json"
	"testing"

	"github.com/Xsxdot/handoff/internal/executor"
)

func TestB460AgyReadURLCarriesURL(t *testing.T) {
	_, req := permTextAndRequest("read_url_content", json.RawMessage(`{"Url":"https://go.dev/doc/"}`))
	if req == nil || req.Tool != executor.PermToolWebFetch || req.URL != "https://go.dev/doc/" {
		t.Fatalf("read_url_content 应带 URL，实得 %+v", req)
	}
	_, req = permTextAndRequest("search_web", json.RawMessage(`{"query":"go"}`))
	if req == nil || req.Tool != executor.PermToolWebFetch || req.URL != "" {
		t.Fatalf("search_web 应为 webfetch 且 URL 空，实得 %+v", req)
	}
}
