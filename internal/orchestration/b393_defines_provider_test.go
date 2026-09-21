// b393_defines_provider_test.go —— B393 MINOR：definesProvider 的判据边界。
//
// 职责：锁住「裸壳 vs 有定义」的判据不被注释或无关字符串值误触——只有 JSON
// 对象键位置的 "provider"/"model" 才算定义。
// 边界：只测未导出纯函数；真实供给端到端见 b393_provider_config_test.go。
package orchestration

import "testing"

// TestB393DefinesProviderBoundaries 锁 MINOR：字节扫描不得把注释或字符串值里
// 出现的 "provider"/"model" 当成定义。
//
// 红（当前 HEAD）：bytes.Contains 命中断言注释里的 "provider" 与字符串值里的
// "model"，裸壳被误判为「已有定义」→ 投影跳过 → 重建仍 ProviderModelNotFound。
// 绿：注释剥离 + 键位置判定后，只有真实对象键才为 true。
// 变异：判据退回 bytes.Contains → 复红。
func TestB393DefinesProviderBoundaries(t *testing.T) {
	cases := []struct {
		name string
		body string
		want bool
	}{
		{"裸壳", `{"$schema":"https://opencode.ai/config.json"}`, false},
		{"行注释提provider", "{\n  // TODO: 以后补 \"provider\" 块\n  \"$schema\":\"x\"\n}", false},
		{"块注释提model", "{\n  /* \"model\": \"x\" */\n  \"$schema\":\"x\"\n}", false},
		{"字符串值是model", `{"description":"model"}`, false},
		{"字符串值是provider", `{"note":"provider"}`, false},
		{"真实provider键", `{"provider":{"commandcode":{}}}`, true},
		{"真实model键", `{"model":"commandcode/x"}`, true},
		{"provider键前有注释", "{\n  // 说明\n  \"provider\": {}\n}", true},
	}
	for _, tc := range cases {
		if got := definesProvider([]byte(tc.body)); got != tc.want {
			t.Fatalf("%s: definesProvider=%v, want %v\nbody=%s", tc.name, got, tc.want, tc.body)
		}
	}
}
