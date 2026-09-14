// B369 配对载体 wire 形状的 Go 侧金样本与拒收面。锁两类编码：
//   - 信封版本字段与「未知版本 / 畸形载荷拒收」的跨语言演进锚；
//   - roundtrip 恒等属性（编码↘解码恒等）。
//
// TS / Kotlin / Swift 孪生金样本由移动核与壳随实现补；改形状先回 contract 节点。
package proto

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func pairFixture() PairBundle {
	exp := time.Unix(1700000000, 0).UTC()
	return PairBundle{
		Version:   PairVersion,
		Relay:     &PairRelay{URL: "wss://relay.example/relay", Credential: "pipecred"},
		IssuedAt:  exp,
		ExpiresAt: exp.Add(60 * time.Second),
		Machines: []PairMachine{
			{
				Name: "devbox", Token: strings.Repeat("ab", 32), Node: "devbox",
				Ticket: &PairTicket{URL: "http://localhost/console?ticket=x", ExpiresAt: exp.Add(60 * time.Second)},
			},
			// 直连形态（同机两态切换：story 7 的 LAN target）。
			{Name: "lan", Token: strings.Repeat("cd", 32), Addr: "http://10.0.0.9:7777"},
			// 离线机：无 ticket = 部分 bundle，不整单失败。
			{Name: "offline", Token: strings.Repeat("ef", 32), Node: "offline"},
		},
	}
}

func TestPairBundleEnvelopeVersionKey(t *testing.T) {
	// 信封版本键必须在线且恒为 PairVersion——跨语言解码方先读它。
	raw, err := EncodePairBundle(pairFixture())
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatal(err)
	}
	if got["v"] != float64(PairVersion) {
		t.Fatalf("信封版本键 v 应为 %d: %s", PairVersion, raw)
	}
	for _, key := range []string{"relay", "machines", "issued_at", "expires_at"} {
		if _, ok := got[key]; !ok {
			t.Fatalf("顶层缺键 %q: %s", key, raw)
		}
	}
}

func TestPairBundleRoundTrip(t *testing.T) {
	// roundtrip 恒等（一条属性顶一族手写用例）。
	want := pairFixture()
	raw, err := EncodePairBundle(want)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodePairBundle(raw)
	if err != nil {
		t.Fatal(err)
	}
	// time.Time 编解码后单调时钟剥离，DeepEqual 会误判；逐字段比对关键载荷。
	if got.Version != want.Version || got.Relay.URL != want.Relay.URL ||
		got.Relay.Credential != want.Relay.Credential || len(got.Machines) != len(want.Machines) {
		t.Fatalf("roundtrip 漂移: want=%+v got=%+v", want, got)
	}
	if !got.IssuedAt.Equal(want.IssuedAt) || !got.ExpiresAt.Equal(want.ExpiresAt) {
		t.Fatalf("时间字段 roundtrip 漂移: %+v", got)
	}
}

func TestPairBundleRejectsUnknownVersion(t *testing.T) {
	// 未知版本必须拒收，且错误可按 errors.Is 判定（跨语言演进锚）。
	raw := `{"v":999,"machines":[{"name":"m","token":"t","addr":"http://x"}]}`
	if _, err := DecodePairBundle(raw); !errors.Is(err, ErrPairVersion) {
		t.Fatalf("未知版本应返回 ErrPairVersion，得到 %v", err)
	}
	// 缺省版本（v=0）同样拒收。
	if _, err := DecodePairBundle(`{"machines":[{"name":"m","token":"t","addr":"http://x"}]}`); !errors.Is(err, ErrPairVersion) {
		t.Fatalf("缺省版本应返回 ErrPairVersion，得到 %v", err)
	}
}

func TestPairBundleRejectsMalformed(t *testing.T) {
	cases := map[string]string{
		"不可解 JSON":    `not-json`,
		"无机器登记":       `{"v":1,"machines":[]}`,
		"机器名空":        `{"v":1,"machines":[{"name":"","token":"t","addr":"http://x"}]}`,
		"token 空":     `{"v":1,"machines":[{"name":"m","token":"","addr":"http://x"}]}`,
		"双形态自相矛盾":     `{"v":1,"relay":{"url":"wss://r","credential":"c"},"machines":[{"name":"m","token":"t","node":"n","addr":"http://x"}]}`,
		"两形态皆空":       `{"v":1,"machines":[{"name":"m","token":"t"}]}`,
		"relay 形态缺端点": `{"v":1,"machines":[{"name":"m","token":"t","node":"n"}]}`,
	}
	for name, raw := range cases {
		if _, err := DecodePairBundle(raw); !errors.Is(err, ErrPairMalformed) {
			t.Fatalf("%s：应返回 ErrPairMalformed，得到 %v", name, err)
		}
	}
}
