package claudecode_test

import (
	"reflect"
	"testing"

	"github.com/Xsxdot/handoff/internal/executor"
	"github.com/Xsxdot/handoff/internal/executor/claudecode"
)

func TestOneShotArgvGoldenVectors(t *testing.T) {
	got, err := claudecode.OneShotArgv("haiku", "p", executor.OneShotLimits{})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"claude", "-p", "--model", "haiku", "p"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}

	got, err = claudecode.OneShotArgv("", "p", executor.OneShotLimits{})
	if err != nil {
		t.Fatal(err)
	}
	want = []string{"claude", "-p", "p"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}
