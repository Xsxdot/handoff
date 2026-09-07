package cmd

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestCLIExecutionCommandsDoNotHandRollHTTP(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller 失败")
	}
	dir := filepath.Dir(thisFile)
	files := []string{"dispatch.go", "wait.go", "reply.go", "continue.go", "stop.go"}
	needles := []string{".HTTPClient()", "http.NewRequest", "client.NewRelay("}
	for _, name := range files {
		body, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		text := string(body)
		for _, n := range needles {
			if strings.Contains(text, n) {
				t.Fatalf("%s 生产路径不得手拼 HTTP / 取裸客户端（含 %q）", name, n)
			}
		}
	}
}
