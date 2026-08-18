// Windows 实现的测试：Task Scheduler XML 内容与 schtasks 调用序列都在这里钉住。
//
// 全部经缝注入，不真的调 schtasks、不真的写 %LOCALAPPDATA%——测试跑完机器上
// 不会多出任何计划任务。本文件不带 build tag：windows.go 全平台编译
// （同 launchd.go / systemd.go），测试因此在 mac/Linux 上也能跑。
package service

import (
	"strings"
	"testing"
	"unicode/utf16"
)

// fromUTF16LE 把 toUTF16LE 的产物解回字符串，供断言 XML 内容用。
func fromUTF16LE(b []byte) string {
	if len(b) >= 2 && b[0] == 0xFF && b[1] == 0xFE {
		b = b[2:]
	}
	codes := make([]uint16, 0, len(b)/2)
	for i := 0; i+1 < len(b); i += 2 {
		codes = append(codes, uint16(b[i])|uint16(b[i+1])<<8)
	}
	return string(utf16.Decode(codes))
}

// XML 的内容是这条路上最容易写错又最难发现的东西，逐项钉住。
// 四项承重配置任一缺失都会静默失效，测试是唯一的防线。
func TestWindowsTaskXMLContent(t *testing.T) {
	m := &windowsManager{log: testLogger()}
	body := m.taskXML(Spec{
		BinPath:    `C:\Users\u\.local\bin\handoff.exe`,
		ConfigPath: `C:\Users\u\.handoff\config.yaml`,
		LogPath:    `C:\Users\u\.handoff\agentd.log`,
	}, `WIN-B37\Administrator`)

	for _, want := range []string{
		"<Interval>PT1M</Interval>",
		"<MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy>",
		"<LogonTrigger>",
		"<DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries>",
		"<StopIfGoingOnBatteries>false</StopIfGoingOnBatteries>",
		`<Command>C:\Users\u\.local\bin\handoff.exe</Command>`,
		"<Arguments>agentd --config C:\\Users\\u\\.handoff\\config.yaml</Arguments>",
		"<ExecutionTimeLimit>PT0S</ExecutionTimeLimit>",
		"<UserId>WIN-B37\\Administrator</UserId>",
		"<LogonType>S4U</LogonType>",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("XML 缺少 %q:\n%s", want, body)
		}
	}

	if strings.Contains(strings.ToLower(body), "cmd.exe") {
		t.Error("XML 不得套 cmd.exe：D8 实测它让 /end 只杀外层，agentd 孙进程原样活着，管理器视图与现实分叉")
	}
}

// schtasks /Create /XML 要求 UTF-16LE；这条断言钉住编码与 BOM。
func TestWindowsXMLIsUTF16LEWithBOM(t *testing.T) {
	got := toUTF16LE("<Task>")
	if len(got) < 2 || got[0] != 0xFF || got[1] != 0xFE {
		t.Fatalf("缺少 UTF-16LE BOM，前两字节=%v", got[:min(2, len(got))])
	}
	if s := fromUTF16LE(got); s != "<Task>" {
		t.Fatalf("往返不一致：%q", s)
	}
	if len(got) != 14 {
		t.Fatalf("UTF-16LE 长度应为 14，实际 %d", len(got))
	}
}

// XML 特殊字符必须转义，否则路径里一个 & 就让 schtasks 拒绝整个文件。
func TestWindowsTaskXMLEscapes(t *testing.T) {
	m := &windowsManager{log: testLogger()}
	body := m.taskXML(Spec{BinPath: `C:\a&b\handoff.exe`}, "u")
	if strings.Contains(body, `C:\a&b\handoff.exe`) {
		t.Error("路径里的 & 未转义，schtasks 会拒绝整个 XML")
	}
	if !strings.Contains(body, "&amp;") {
		t.Errorf("未见转义后的 &amp;:\n%s", body)
	}
}
