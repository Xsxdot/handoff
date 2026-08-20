package wire

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

// TestDataRoundTrip 数据帧原样往返，一个字节都不能变。
func TestDataRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	payload := []byte("\x1b[31mhello\x00\xff world\x1b[0m")
	if err := WriteData(&buf, payload); err != nil {
		t.Fatalf("WriteData: %v", err)
	}
	kind, data, ctrl, err := ReadFrame(&buf)
	if err != nil {
		t.Fatalf("ReadFrame: %v", err)
	}
	if kind != KindData || ctrl != nil {
		t.Fatalf("kind=%d ctrl=%v，期望数据帧", kind, ctrl)
	}
	if !bytes.Equal(data, payload) {
		t.Fatalf("data = %q，期望 %q", data, payload)
	}
}

// TestControlRoundTrip 控制帧的每个字段都要活着回来。
func TestControlRoundTrip(t *testing.T) {
	code := 3
	var buf bytes.Buffer
	in := Control{
		Type: CtrlStatResp, BytesOut: 12345, Foreground: true, Attached: 2,
		Cols: 120, Rows: 40, ExitCode: &code, ProtoVersion: ProtoVersion,
	}
	if err := WriteControl(&buf, in); err != nil {
		t.Fatalf("WriteControl: %v", err)
	}
	kind, _, ctrl, err := ReadFrame(&buf)
	if err != nil {
		t.Fatalf("ReadFrame: %v", err)
	}
	if kind != KindControl || ctrl == nil {
		t.Fatalf("kind=%d ctrl=%v，期望控制帧", kind, ctrl)
	}
	if ctrl.Type != CtrlStatResp || ctrl.BytesOut != 12345 || !ctrl.Foreground ||
		ctrl.Attached != 2 || ctrl.Cols != 120 || ctrl.Rows != 40 {
		t.Fatalf("ctrl = %+v", *ctrl)
	}
	if ctrl.ExitCode == nil || *ctrl.ExitCode != 3 {
		t.Fatalf("exit_code = %v，期望 3", ctrl.ExitCode)
	}
}

// TestExitCodeNilStaysNil 退出码的三态不能被 0 冒充。
func TestExitCodeNilStaysNil(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteControl(&buf, Control{Type: CtrlStatResp}); err != nil {
		t.Fatalf("WriteControl: %v", err)
	}
	_, _, ctrl, err := ReadFrame(&buf)
	if err != nil {
		t.Fatalf("ReadFrame: %v", err)
	}
	if ctrl.ExitCode != nil {
		t.Fatalf("exit_code = %v，期望 nil", *ctrl.ExitCode)
	}
}

// TestUnknownFieldsIgnored 未知字段忽略。
func TestUnknownFieldsIgnored(t *testing.T) {
	var buf bytes.Buffer
	body := []byte(`{"type":"stat_resp","bytes_out":7,"future_field":{"a":1}}`)
	buf.WriteByte(KindControl)
	buf.Write([]byte{byte(len(body) >> 24), byte(len(body) >> 16), byte(len(body) >> 8), byte(len(body))})
	buf.Write(body)

	_, _, ctrl, err := ReadFrame(&buf)
	if err != nil {
		t.Fatalf("未知字段不该让解码失败: %v", err)
	}
	if ctrl.BytesOut != 7 {
		t.Fatalf("bytes_out = %d，期望 7", ctrl.BytesOut)
	}
}

// TestOversizeRejected 超长帧当场拒绝，不先分配再失败。
func TestOversizeRejected(t *testing.T) {
	var buf bytes.Buffer
	buf.WriteByte(KindData)
	buf.Write([]byte{0xff, 0xff, 0xff, 0xff})
	_, _, _, err := ReadFrame(&buf)
	if err == nil {
		t.Fatal("期望拒绝超长帧")
	}
	if !strings.Contains(err.Error(), "长度") {
		t.Fatalf("错误应说明是长度问题: %v", err)
	}
}

// TestTruncatedFrameIsEOF 半截帧要能与「干净结束」区分开。
func TestTruncatedFrameIsEOF(t *testing.T) {
	var buf bytes.Buffer
	buf.WriteByte(KindData)
	buf.Write([]byte{0, 0, 0, 10})
	buf.Write([]byte("abc"))
	_, _, _, err := ReadFrame(&buf)
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("err = %v，期望 io.ErrUnexpectedEOF", err)
	}
}

// TestCleanEOF 连接干净关闭时给 io.EOF。
func TestCleanEOF(t *testing.T) {
	_, _, _, err := ReadFrame(bytes.NewReader(nil))
	if !errors.Is(err, io.EOF) {
		t.Fatalf("err = %v，期望 io.EOF", err)
	}
}
