package termseq

import (
	"reflect"
	"testing"
)

func TestSummarizeInFocusAndWheel(t *testing.T) {
	got := SummarizeIn([]byte("\x1b[O\x1b[I\x1b[<64;10;4M\x1b[<64;10;4M\x1b[<65;10;5M"))
	want := []string{"focus-out", "focus-in", "wheel-up×2", "wheel-down×1"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v want %#v", got, want)
	}
}

func TestSummarizeInIgnoresOrdinaryKeys(t *testing.T) {
	if notes := SummarizeIn([]byte("ls -al\r")); len(notes) != 0 {
		t.Fatalf("ordinary keys leaked: %#v", notes)
	}
}

func TestSummarizeOutMouseAndFocusModes(t *testing.T) {
	raw := []byte("\x1b[?1049h\x1b[?1000;1002;1003;1006h\x1b[?1004h\x1b[?1000l")
	got := SummarizeOut(raw)
	want := []string{"1049h", "1000h", "1002h", "1003h", "1006h", "1004h", "1000l"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v want %#v", got, want)
	}
}

func TestSummarizeOutIgnoresUnrelatedCSI(t *testing.T) {
	if notes := SummarizeOut([]byte("\x1b[2J\x1b[Hhello")); len(notes) != 0 {
		t.Fatalf("unrelated CSI leaked: %#v", notes)
	}
}
