package agy_test

import (
	"reflect"
	"testing"

	"github.com/Xsxdot/handoff/internal/executor"
	"github.com/Xsxdot/handoff/internal/executor/agy"
)

func TestOneShotArgvGoldenVectors(t *testing.T) {
	got, err := agy.OneShotArgv("claude-3-5-sonnet", "p", executor.OneShotLimits{})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"agy", "--model", "claude-3-5-sonnet", "-p", "p"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}

	got, err = agy.OneShotArgv("", "p", executor.OneShotLimits{})
	if err != nil {
		t.Fatal(err)
	}
	want = []string{"agy", "-p", "p"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}
