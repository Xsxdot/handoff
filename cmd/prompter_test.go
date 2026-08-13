// scriptedPrompter 的行为测试。
//
// 覆盖空行 / EOF 取默认、Value 精确匹配、1-based 下标、Confirm 的 y/n。
// 不测 huh，不测写配置。
package cmd

import (
	"io"
	"strings"
	"testing"
)

func TestScriptedSelectTakesDefaultOnEmpty(t *testing.T) {
	p := newScriptedPrompter(strings.NewReader("\n"), io.Discard)
	got, err := p.Select("角色", []promptOption{{Value: "executor", Label: "执行机"}, {Value: "coordinator", Label: "协调者"}}, "executor")
	if err != nil || got != "executor" {
		t.Fatalf("got %q err %v", got, err)
	}
}

func TestScriptedSelectMatchesValue(t *testing.T) {
	p := newScriptedPrompter(strings.NewReader("coordinator\n"), io.Discard)
	got, err := p.Select("角色", []promptOption{{Value: "executor", Label: "执行机"}, {Value: "coordinator", Label: "协调者"}}, "executor")
	if err != nil || got != "coordinator" {
		t.Fatalf("got %q err %v", got, err)
	}
}

func TestScriptedSelectTakesOneBasedIndex(t *testing.T) {
	p := newScriptedPrompter(strings.NewReader("2\n"), io.Discard)
	got, err := p.Select("角色", []promptOption{{Value: "executor", Label: "执行机"}, {Value: "coordinator", Label: "协调者"}}, "executor")
	if err != nil || got != "coordinator" {
		t.Fatalf("got %q err %v", got, err)
	}
}

func TestScriptedEOFTakesDefault(t *testing.T) {
	p := newScriptedPrompter(strings.NewReader(""), io.Discard)
	got, err := p.Input("模型", "x")
	if err != nil || got != "x" {
		t.Fatalf("got %q err %v", got, err)
	}
}

func TestScriptedConfirmYes(t *testing.T) {
	p := newScriptedPrompter(strings.NewReader("y\n"), io.Discard)
	got, err := p.Confirm("托管", false)
	if err != nil || !got {
		t.Fatalf("got %v err %v", got, err)
	}
}

func TestScriptedConfirmEmptyTakesDefault(t *testing.T) {
	p := newScriptedPrompter(strings.NewReader("\n"), io.Discard)
	got, err := p.Confirm("托管", true)
	if err != nil || !got {
		t.Fatalf("got %v err %v", got, err)
	}
}
