// B369 配对载体 wire 形状的 Go 侧金样本与拒收面。锁两类编码：
//   - 信封版本字段与「未知版本 / 畸形载荷拒收」的跨语言演进锚；
//   - roundtrip 恒等属性（编码↘解码恒等）。
//
// TS / Kotlin / Swift 孪生金样本由移动核与壳随实现补；改形状先回 contract 节点。
package proto

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"runtime"
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

// randomPairBundle 按 seed 生成一份合法 PairBundle：1..4 台机器、每台非
// relay 即直连形态（Validate 要求恰居其一）、可选 Credential/Ticket、
// 可选非空 Relay（仅当至少一台为 relay 形态时给出）、可选非零时间。
// Token 用随机 hex（≥1 字符即可过 Validate；熵闸执法在 S2，不在 wire 层）。
func randomPairBundle(rng *rand.Rand) PairBundle {
	n := 1 + rng.Intn(4)
	b := PairBundle{Version: PairVersion}
	needRelay := false
	for j := 0; j < n; j++ {
		m := PairMachine{
			Name:  fmt.Sprintf("m%d", j),
			Token: randHex(rng, 1+rng.Intn(80)),
		}
		if rng.Intn(2) == 0 {
			m.Node = fmt.Sprintf("node-%d", j)
			needRelay = true
		} else {
			m.Addr = fmt.Sprintf("http://10.0.0.%d:%d", j+1, 7000+j)
		}
		if rng.Intn(2) == 1 {
			m.Credential = randHex(rng, 8)
		}
		if rng.Intn(2) == 1 {
			m.Ticket = &PairTicket{
				URL:       "http://localhost/console?ticket=" + randHex(rng, 6),
				ExpiresAt: randTime(rng),
			}
		}
		b.Machines = append(b.Machines, m)
	}
	if needRelay {
		b.Relay = &PairRelay{URL: "wss://relay.example/relay", Credential: "pipecred"}
	}
	if rng.Intn(2) == 1 {
		b.IssuedAt = randTime(rng)
		b.ExpiresAt = b.IssuedAt.Add(60 * time.Second)
	}
	return b
}

// randHex 返回 n 个小写十六进制字符。n<=0 时返回空串。
func randHex(rng *rand.Rand, n int) string {
	const hex = "0123456789abcdef"
	var sb strings.Builder
	for i := 0; i < n; i++ {
		sb.WriteByte(hex[rng.Intn(len(hex))])
	}
	return sb.String()
}

// randTime 返回 [1970, 2033) 区间内、秒精度的 UTC 时间。
// 秒精度避免 RFC3339 纳秒进位造成「编码再解码」不可比。
func randTime(rng *rand.Rand) time.Time {
	return time.Unix(int64(rng.Intn(2_000_000_000)), 0).UTC()
}

// TestPairBundleRoundTripProperty 是一条属性顶一族手写用例：对随机构造的
// 合法 bundle，断言 Encode∘Decode∘Encode 逐字节恒等，且关键字段与可空语义
// （Relay/Ticket 的 nil-ness、Credential 的有无、Node/Addr 的择一）守恒。
//
// 为什么不直接 DeepEqual：time.Time 零值的 Location 是 nil，而 JSON 解码
// "0001-01-01T00:00:00Z" 得到 UTC，DeepEqual 会把两者误判为不等。故时间用
// .Equal、其余逐字段比。
func TestPairBundleRoundTripProperty(t *testing.T) {
	rng := rand.New(rand.NewSource(0xB369))
	for i := 0; i < 500; i++ {
		b := randomPairBundle(rng)
		raw, err := EncodePairBundle(b)
		if err != nil {
			t.Fatalf("轮 %d 编码失败: %v bundle=%+v", i, err, b)
		}
		got, err := DecodePairBundle(raw)
		if err != nil {
			t.Fatalf("轮 %d 解码失败: %v raw=%s", i, err, raw)
		}
		again, err := EncodePairBundle(got)
		if err != nil {
			t.Fatalf("轮 %d 再编码失败: %v", i, err)
		}
		if raw != again {
			t.Fatalf("轮 %d roundtrip 逐字节漂移:\n in=%s\nout=%s", i, raw, again)
		}
		if (b.Relay == nil) != (got.Relay == nil) {
			t.Fatalf("轮 %d Relay nil-ness 漂移: %s", i, raw)
		}
		if b.Relay != nil && (b.Relay.URL != got.Relay.URL || b.Relay.Credential != got.Relay.Credential) {
			t.Fatalf("轮 %d Relay 字段漂移: %s", i, raw)
		}
		if len(b.Machines) != len(got.Machines) {
			t.Fatalf("轮 %d 机器数漂移: %s", i, raw)
		}
		for j := range b.Machines {
			wm, gm := b.Machines[j], got.Machines[j]
			if wm.Name != gm.Name || wm.Token != gm.Token || wm.Node != gm.Node ||
				wm.Addr != gm.Addr || wm.Credential != gm.Credential {
				t.Fatalf("轮 %d 机器[%d]字段漂移: %s", i, j, raw)
			}
			if (wm.Ticket == nil) != (gm.Ticket == nil) {
				t.Fatalf("轮 %d 机器[%d] Ticket nil-ness 漂移（缺失 vs 零值）: %s", i, j, raw)
			}
			if wm.Ticket != nil && (wm.Ticket.URL != gm.Ticket.URL || !wm.Ticket.ExpiresAt.Equal(gm.Ticket.ExpiresAt)) {
				t.Fatalf("轮 %d 机器[%d] Ticket 字段漂移: %s", i, j, raw)
			}
		}
		if !b.IssuedAt.Equal(got.IssuedAt) || !b.ExpiresAt.Equal(got.ExpiresAt) {
			t.Fatalf("轮 %d 时间字段漂移: %s", i, raw)
		}
	}
}

// TestPairBundleJSONKeysMatchWire 键集逐键断言：编码产物每个对象的键必须
// 恰好等于契约 §3.1 声明的集合（多键/少键/改名都红）。这是跨语言解码方
// 逐键一致的可判锚（契约条 10），T2 的共享金样本以此规范化字节为准。
func TestPairBundleJSONKeysMatchWire(t *testing.T) {
	raw, err := EncodePairBundle(pairFixture())
	if err != nil {
		t.Fatal(err)
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &top); err != nil {
		t.Fatal(err)
	}
	assertExactKeys(t, "bundle", top, []string{"v", "relay", "machines", "issued_at", "expires_at"})

	var relay map[string]json.RawMessage
	if err := json.Unmarshal(top["relay"], &relay); err != nil {
		t.Fatal(err)
	}
	assertExactKeys(t, "relay", relay, []string{"url", "credential"})

	var machines []map[string]json.RawMessage
	if err := json.Unmarshal(top["machines"], &machines); err != nil {
		t.Fatal(err)
	}
	if len(machines) != 3 {
		t.Fatalf("金样本应有 3 台机器，实得 %d", len(machines))
	}
	// 0：relay 形态 + ticket；1：直连形态；2：relay 形态无 ticket。
	assertExactKeys(t, "machines[0]", machines[0], []string{"name", "token", "node", "ticket"})
	assertExactKeys(t, "machines[1]", machines[1], []string{"name", "token", "addr"})
	assertExactKeys(t, "machines[2]", machines[2], []string{"name", "token", "node"})

	var ticket map[string]json.RawMessage
	if err := json.Unmarshal(machines[0]["ticket"], &ticket); err != nil {
		t.Fatal(err)
	}
	assertExactKeys(t, "ticket", ticket, []string{"url", "expires_at"})
}

// assertExactKeys 断言 m 的键集与 want 完全相同（顺序无关，多/少/替均失败）。
func assertExactKeys(t *testing.T, label string, m map[string]json.RawMessage, want []string) {
	t.Helper()
	if len(m) != len(want) {
		t.Fatalf("%s 键数漂移: got %v want %v", label, keysOf(m), want)
	}
	for _, k := range want {
		if _, ok := m[k]; !ok {
			t.Fatalf("%s 缺键 %q: got %v want %v", label, k, keysOf(m), want)
		}
	}
}

// keysOf 返回 map 键列表，仅用于失败信息。
func keysOf(m map[string]json.RawMessage) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	return ks
}

// TestPairBundleRejectsMalformedSentinelExact 拒绝面双重分型：每条畸形载荷
// 必须 ErrPairMalformed 且**不得**同时被 ErrPairVersion 命中；版本错误反之。
// 壳侧按 sentinel 分支渲染，不得解析文案，故两类错误必须互斥可判（契约条 3/4）。
func TestPairBundleRejectsMalformedSentinelExact(t *testing.T) {
	malformed := map[string]string{
		"不可解 JSON":    `not-json`,
		"无机器登记":       `{"v":1,"machines":[]}`,
		"机器名空":        `{"v":1,"machines":[{"name":"","token":"t","addr":"http://x"}]}`,
		"token 空":     `{"v":1,"machines":[{"name":"m","token":"","addr":"http://x"}]}`,
		"双形态自相矛盾":     `{"v":1,"relay":{"url":"wss://r","credential":"c"},"machines":[{"name":"m","token":"t","node":"n","addr":"http://x"}]}`,
		"两形态皆空":       `{"v":1,"machines":[{"name":"m","token":"t"}]}`,
		"relay 形态缺端点": `{"v":1,"machines":[{"name":"m","token":"t","node":"n"}]}`,
	}
	for name, raw := range malformed {
		_, err := DecodePairBundle(raw)
		if !errors.Is(err, ErrPairMalformed) {
			t.Fatalf("%s：应返回 ErrPairMalformed，得到 %v", name, err)
		}
		if errors.Is(err, ErrPairVersion) {
			t.Fatalf("%s：畸形载荷不得同时被判为版本错误: %v", name, err)
		}
	}
	versions := map[string]string{
		"未知版本 v=999": `{"v":999,"machines":[{"name":"m","token":"t","addr":"http://x"}]}`,
		"缺省版本 v=0":   `{"machines":[{"name":"m","token":"t","addr":"http://x"}]}`,
	}
	for name, raw := range versions {
		_, err := DecodePairBundle(raw)
		if !errors.Is(err, ErrPairVersion) {
			t.Fatalf("%s：应返回 ErrPairVersion，得到 %v", name, err)
		}
		if errors.Is(err, ErrPairMalformed) {
			t.Fatalf("%s：版本错误不得同时被判为畸形: %v", name, err)
		}
	}
}

// pairingFixturePath 返回共享金样本在仓内的绝对路径 mobile/bind/pairing_fixture.json。
// 用 runtime.Caller(0) 从本测试文件推仓根（与 contract_fixture_test.go#fixtureDir 同款），
// 不依赖 go test 的 cwd。
func pairingFixturePath(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("无法定位测试文件自身路径")
	}
	return filepath.Join(filepath.Dir(thisFile), "..", "..", "mobile", "bind", "pairing_fixture.json")
}

// TestPairingFixtureMatchesCanonicalBytes 跨语言共享金样本护栏：仓内
// mobile/bind/pairing_fixture.json 必须逐字节等于 EncodePairBundle(pairFixture())
// 加一个换行。壳（Kotlin/Swift）侧用同一份文件做孪生解码断言（P5=A 壳工程
// 不进本仓，真机清单 #1）；本测试是「Go 编」与「壳解」之间唯一的机内链环——
// 它红了说明 Go 侧 wire 形状漂移而共享样本未同步（或反之）。
func TestPairingFixtureMatchesCanonicalBytes(t *testing.T) {
	raw, err := EncodePairBundle(pairFixture())
	if err != nil {
		t.Fatal(err)
	}
	canonical := raw + "\n"
	stored, err := os.ReadFile(pairingFixturePath(t))
	if err != nil {
		t.Fatalf("读取共享金样本失败（T2 是否已落文件？）: %v", err)
	}
	if !bytes.Equal(stored, []byte(canonical)) {
		t.Errorf("共享金样本漂移（改 wire 形状请同步刷新该文件）:\n--- stored ---\n%s\n--- canonical ---\n%s", stored, canonical)
	}
}
