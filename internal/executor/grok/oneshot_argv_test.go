package grok_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/executor"
	"github.com/Xsxdot/handoff/internal/executor/grok"
)

func TestOneShotArgvGoldenVectors(t *testing.T) {
	cases := []struct {
		name   string
		model  string
		prompt string
		limits executor.OneShotLimits
		want   []string
	}{
		{
			name:   "caller low effort with model",
			model:  "grok-4.5",
			prompt: "p",
			limits: executor.OneShotLimits{Effort: executor.EffortLow},
			want:   []string{"grok", "--effort", "low", "-m", "grok-4.5", "-p", "p"},
		},
		{
			name:   "caller low effort without model",
			prompt: "p",
			limits: executor.OneShotLimits{Effort: executor.EffortLow},
			want:   []string{"grok", "--effort", "low", "-p", "p"},
		},
		{
			name:   "no effort is not default low",
			model:  "grok-4.5",
			prompt: "p",
			want:   []string{"grok", "-m", "grok-4.5", "-p", "p"},
		},
		{
			name:   "no effort no model",
			prompt: "p",
			want:   []string{"grok", "-p", "p"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := grok.OneShotArgv(c.model, c.prompt, c.limits)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("got %v want %v", got, c.want)
			}
			for i, a := range got {
				if a == "-p" && i > 0 && got[i-1] == "--effort" {
					t.Fatal("--effort must not sit immediately after being parsed as -p value; -p is last flag")
				}
			}
			if c.limits.Effort == executor.EffortLow {
				effortAt, pAt := -1, -1
				for i, a := range got {
					if a == "--effort" {
						effortAt = i
					}
					if a == "-p" {
						pAt = i
					}
				}
				if effortAt < 0 || pAt < 0 || effortAt > pAt {
					t.Fatalf("--effort must precede -p: %v", got)
				}
			}
		})
	}
}

func TestOneShotArgvRejectsUnknownEffort(t *testing.T) {
	_, err := grok.OneShotArgv("", "p", executor.OneShotLimits{Effort: "high"})
	if err == nil || !strings.Contains(err.Error(), "high") {
		t.Fatalf("want unknown effort error, got %v", err)
	}
}
