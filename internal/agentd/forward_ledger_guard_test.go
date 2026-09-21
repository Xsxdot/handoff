package agentd

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestForwardSourceDoesNotWriteLedger(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller 失败")
	}
	body, err := os.ReadFile(filepath.Join(filepath.Dir(thisFile), "forward.go"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	for _, needle := range []string{"attachCardBaseBranches", "SetCardBaseBranch"} {
		if strings.Contains(text, needle) {
			t.Fatalf("forward.go 是传输路径，不得写账本（含 %q）", needle)
		}
	}
}
