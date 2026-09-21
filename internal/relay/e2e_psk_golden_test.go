package relay_test

import (
	"bytes"
	"encoding/hex"
	"testing"

	"github.com/Xsxdot/handoff/internal/relay"
)

// TestDerivePSKGoldenVector 锁住 relay E2E PSK 派生。本卡不改握手字节；跨实现一致性靠这支金样本，不靠纸面「逐字节一致」。
func TestDerivePSKGoldenVector(t *testing.T) {
	salt := bytes.Repeat([]byte{1}, 32)
	got, err := relay.DerivePSKForTest("tok", "acc1", "devbox", salt)
	if err != nil {
		t.Fatal(err)
	}
	want, err := hex.DecodeString("f9b8a53c508f2adac984c2bd61fb3c059b30ed379c2556b64b0dd8b6833d0250")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("PSK = %x, want %x", got, want)
	}
}
