package opencode_test

import (
	"reflect"
	"testing"

	"github.com/Xsxdot/handoff/internal/executor"
	"github.com/Xsxdot/handoff/internal/executor/opencode"
)

func TestOneShotArgvGoldenVectors(t *testing.T) {
	got, err := opencode.OneShotArgv("m1", "p", executor.OneShotLimits{})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"opencode", "run", "-m", "m1", "p"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}

	got, err = opencode.OneShotArgv("", "p", executor.OneShotLimits{})
	if err != nil {
		t.Fatal(err)
	}
	want = []string{"opencode", "run", "p"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}
