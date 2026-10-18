package p2p

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io"
	"math/rand"
	"reflect"
	"strings"
	"testing"
)

// W1：魔数等长且不同于 preview-raw 魔数（listener 一次 Peek 三分的前提）。
func TestSignalMagicShape(t *testing.T) {
	if SignalMagic != "handoff-p2p-ice-sig-v1" {
		t.Fatalf("SignalMagic = %q", SignalMagic)
	}
	const previewRaw = "handoff-preview-raw-v1"
	if len(SignalMagic) != len(previewRaw) || SignalMagic == previewRaw {
		t.Fatalf("魔数须与 previewRawMagic 等长且不同: %d vs %d", len(SignalMagic), len(previewRaw))
	}
}

var goldenDesc = Description{Ufrag: "uf", Pwd: "pw", Candidates: []string{"c1"}}

// 金样本：请求与两种应答的精确字节（F1 W2/W3/W5/W6/W7）。
func TestSignalGolden(t *testing.T) {
	cases := []struct {
		name  string
		write func(io.Writer) error
		hex   string
	}{
		{"request", func(w io.Writer) error { return WriteRequest(w, goldenDesc) },
			hex.EncodeToString([]byte(SignalMagic)) + "00000033" +
				hex.EncodeToString([]byte(`{"v":1,"ufrag":"uf","pwd":"pw","candidates":["c1"]}`))},
		{"accept", func(w io.Writer) error { return WriteAccept(w, goldenDesc) },
			hex.EncodeToString([]byte(SignalMagic)) + "0000003d" +
				hex.EncodeToString([]byte(`{"v":1,"ok":true,"ufrag":"uf","pwd":"pw","candidates":["c1"]}`))},
		{"reject", func(w io.Writer) error { return WriteReject(w, RejectBusy) },
			hex.EncodeToString([]byte(SignalMagic)) + "00000022" +
				hex.EncodeToString([]byte(`{"v":1,"ok":false,"reject":"busy"}`))},
	}
	for _, c := range cases {
		var buf bytes.Buffer
		if err := c.write(&buf); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if got := hex.EncodeToString(buf.Bytes()); got != c.hex {
			t.Fatalf("%s golden 不符\n got %s\nwant %s", c.name, got, c.hex)
		}
	}
}

func frame(body string) []byte {
	var b bytes.Buffer
	b.WriteString(SignalMagic)
	var n [4]byte
	binary.BigEndian.PutUint32(n[:], uint32(len(body)))
	b.Write(n[:])
	b.WriteString(body)
	return b.Bytes()
}

// W2/W3/W4/W14 请求侧拒收表。
func TestReadRequestRejects(t *testing.T) {
	cases := []struct {
		name string
		in   []byte
		code string
	}{
		{"bad magic", append([]byte("handoff-p2p-ice-sig-v9"), 0, 0, 0, 2, '{', '}'), RejectBadRequest},
		{"zero length", frame(""), RejectBadRequest},
		{"too long", func() []byte {
			b := []byte(SignalMagic)
			return append(b, 0, 0, 0x40, 0x01)
		}(), RejectBadRequest},
		{"not json", frame("xx"), RejectBadRequest},
		{"missing v", frame(`{"ufrag":"a","pwd":"b","candidates":[]}`), RejectBadRequest},
		{"v2", frame(`{"v":2,"ufrag":"a","pwd":"b","candidates":[]}`), RejectUnsupportedVersion},
		{"empty ufrag", frame(`{"v":1,"ufrag":"","pwd":"b","candidates":[]}`), RejectBadRequest},
		{"empty pwd", frame(`{"v":1,"ufrag":"a","pwd":"","candidates":[]}`), RejectBadRequest},
		{"too many candidates", frame(`{"v":1,"ufrag":"a","pwd":"b","candidates":[` +
			strings.TrimSuffix(strings.Repeat(`"c",`, 33), ",") + `]}`), RejectBadRequest},
		{"truncated", frame(`{"v":1`)[:len(SignalMagic)+5], RejectBadRequest},
	}
	for _, c := range cases {
		_, err := ReadRequest(bytes.NewReader(c.in))
		var re *RequestError
		if !errors.As(err, &re) || re.Code != c.code {
			t.Fatalf("%s: err=%v want code %s", c.name, err, c.code)
		}
	}
}

// W4：未知字段忽略（前向兼容）。
func TestReadRequestIgnoresUnknownFields(t *testing.T) {
	d, err := ReadRequest(bytes.NewReader(frame(`{"v":1,"ufrag":"a","pwd":"b","candidates":["x"],"future":42}`)))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(d, Description{Ufrag: "a", Pwd: "b", Candidates: []string{"x"}}) {
		t.Fatalf("got %+v", d)
	}
}

// W8/W9/W10：发起方对应答的判定。
func TestReadAnswerClassification(t *testing.T) {
	cases := []struct {
		name        string
		in          []byte
		unsupported bool
		code        string
	}{
		{"eof", nil, true, ""},
		{"partial magic eof", []byte(SignalMagic[:10]), true, ""},
		{"yamux-ish bytes", []byte{0, 1, 0, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}, true, ""},
		{"disabled", frame(`{"v":1,"ok":false,"reject":"disabled"}`), true, RejectDisabled},
		{"unsupported_version", frame(`{"v":1,"ok":false,"reject":"unsupported_version"}`), true, RejectUnsupportedVersion},
		{"busy", frame(`{"v":1,"ok":false,"reject":"busy"}`), false, RejectBusy},
		{"bad_request", frame(`{"v":1,"ok":false,"reject":"bad_request"}`), false, RejectBadRequest},
		{"unknown code", frame(`{"v":1,"ok":false,"reject":"weird"}`), false, "weird"},
	}
	for _, c := range cases {
		_, err := ReadAnswer(bytes.NewReader(c.in))
		if c.unsupported {
			if !errors.Is(err, ErrPeerUnsupported) {
				t.Fatalf("%s: err=%v want ErrPeerUnsupported", c.name, err)
			}
			continue
		}
		var re *RejectedError
		if !errors.As(err, &re) || re.Code != c.code || errors.Is(err, ErrPeerUnsupported) {
			t.Fatalf("%s: err=%v want RejectedError %s", c.name, err, c.code)
		}
	}
}

// encode∘decode roundtrip（含空候选与随机内容）。
func TestSignalRoundtrip(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	for i := 0; i < 200; i++ {
		d := Description{Ufrag: randStr(r, 1+r.Intn(16)), Pwd: randStr(r, 1+r.Intn(32))}
		for j := r.Intn(MaxCandidates + 1); j > 0; j-- {
			d.Candidates = append(d.Candidates, randStr(r, 1+r.Intn(80)))
		}
		var buf bytes.Buffer
		if err := WriteRequest(&buf, d); err != nil {
			t.Fatal(err)
		}
		got, err := ReadRequest(&buf)
		if err != nil || !descEqual(got, d) {
			t.Fatalf("request roundtrip %d: %v %+v != %+v", i, err, got, d)
		}
		buf.Reset()
		if err := WriteAccept(&buf, d); err != nil {
			t.Fatal(err)
		}
		got, err = ReadAnswer(&buf)
		if err != nil || !descEqual(got, d) {
			t.Fatalf("answer roundtrip %d: %v", i, err)
		}
	}
}

func descEqual(a, b Description) bool {
	return a.Ufrag == b.Ufrag && a.Pwd == b.Pwd && len(a.Candidates) == len(b.Candidates) &&
		(len(a.Candidates) == 0 || reflect.DeepEqual(a.Candidates, b.Candidates))
}

func randStr(r *rand.Rand, n int) string {
	const alpha = "abcdefghijklmnopqrstuvwxyz0123456789 :./+"
	b := make([]byte, n)
	for i := range b {
		b[i] = alpha[r.Intn(len(alpha))]
	}
	return string(b)
}
