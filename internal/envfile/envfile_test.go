package envfile

import (
	"strings"
	"testing"
)

// fixedLookup 造一个确定的外部环境，避免测试依赖真实 os.Environ。
func fixedLookup(m map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) {
		v, ok := m[k]
		return v, ok
	}
}

func TestParse(t *testing.T) {
	cases := []struct {
		name  string
		input string
		outer map[string]string
		want  []KV
	}{
		{
			name:  "注释与空行被跳过",
			input: "# 注释\n\n   # 缩进后的注释\nA=1\n",
			want:  []KV{{"A", "1"}},
		},
		{
			name:  "export 前缀可选",
			input: "export A=1\nexport\tB=2\nC=3\n",
			want:  []KV{{"A", "1"}, {"B", "2"}, {"C", "3"}},
		},
		{
			name:  "单引号字面量不展开",
			input: "A='literal $B here'\n",
			outer: map[string]string{"B": "x"},
			want:  []KV{{"A", "literal $B here"}},
		},
		{
			name:  "双引号去引号后展开",
			input: `A="v=${B}"` + "\n",
			outer: map[string]string{"B": "x"},
			want:  []KV{{"A", "v=x"}},
		},
		{
			name:  "无引号展开 $VAR 与 ${VAR}",
			input: "A=$B-${B}\n",
			outer: map[string]string{"B": "x"},
			want:  []KV{{"A", "x-x"}},
		},
		{
			name:  "PATH 自引用取外部环境",
			input: "PATH=${PATH}:/usr/local/go/bin\n",
			outer: map[string]string{"PATH": "/usr/bin:/bin"},
			want:  []KV{{"PATH", "/usr/bin:/bin:/usr/local/go/bin"}},
		},
		{
			name:  "文件内前置键优先于外部环境",
			input: "B=inner\nA=${B}\n",
			outer: map[string]string{"B": "outer"},
			want:  []KV{{"B", "inner"}, {"A", "inner"}},
		},
		{
			name:  "未定义变量展开为空串",
			input: "A=[${NOPE}]\n",
			want:  []KV{{"A", "[]"}},
		},
		{
			name:  "值的首尾空白被 trim",
			input: "A=   spaced   \n",
			want:  []KV{{"A", "spaced"}},
		},
		{
			name:  "值里的 # 不是注释",
			input: "A=http://host/a#b\n",
			want:  []KV{{"A", "http://host/a#b"}},
		},
		{
			name:  "CRLF 行尾",
			input: "A=1\r\nB=2\r\n",
			want:  []KV{{"A", "1"}, {"B", "2"}},
		},
		{
			name:  "重复键后者覆盖前者且保持首次位置",
			input: "A=1\nB=2\nA=3\n",
			want:  []KV{{"A", "3"}, {"B", "2"}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, _, err := Parse(strings.NewReader(tc.input), fixedLookup(tc.outer))
			if err != nil {
				t.Fatalf("Parse 意外失败: %v", err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("条数不符: got %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("第 %d 条: got %v, want %v", i, got[i], tc.want[i])
				}
			}
		})
	}
}

func TestParseReportsDuplicateKeys(t *testing.T) {
	_, dups, err := Parse(strings.NewReader("A=1\nB=2\nA=3\n"), nil)
	if err != nil {
		t.Fatalf("Parse 意外失败: %v", err)
	}
	if len(dups) != 1 || dups[0] != "A" {
		t.Fatalf("重复键应为 [A], got %v", dups)
	}
}

func TestParseErrors(t *testing.T) {
	cases := []struct {
		name      string
		input     string
		wantInErr string
	}{
		{name: "缺等号", input: "A=1\nJUST_A_WORD\n", wantInErr: "第 2 行"},
		{name: "键名以数字开头", input: "1BAD=x\n", wantInErr: "第 1 行"},
		{name: "键名含连字符", input: "BAD-KEY=x\n", wantInErr: "第 1 行"},
		{name: "键名为空", input: "=x\n", wantInErr: "第 1 行"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := Parse(strings.NewReader(tc.input), nil)
			if err == nil {
				t.Fatal("期望报错，实际成功")
			}
			if !strings.Contains(err.Error(), tc.wantInErr) {
				t.Errorf("错误应含 %q，实际 %q", tc.wantInErr, err.Error())
			}
		})
	}
}

func TestParseRejectsOversizedFile(t *testing.T) {
	big := strings.Repeat("A=1\n", maxEnvFileSize)
	_, _, err := Parse(strings.NewReader(big), nil)
	if err == nil {
		t.Fatal("超限文件应报错")
	}
	if !strings.Contains(err.Error(), "64") {
		t.Errorf("错误应提到大小上限，实际 %q", err.Error())
	}
}
