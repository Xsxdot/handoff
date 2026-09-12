package agentd

// b23314_signature_lock_test.go —— B233.14 S1 缝的可红测试。
//
// 断言四件事：
//   1. Server.scheduling 字段类型是使用方接口 SchedulingClient（变异：改回
//      *scheduling.Service → 红）；
//   2. SetScheduling 参数 / Scheduling 返回类型是 SchedulingClient；
//   3. 只实现接口的 fake 可注入生产字段（不要求具体 *Service）；
//   4. gateway 生产文件不含 scheduling.New(（组装点除外；变异：server.go 加一行 → 红）。

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/scheduling"
)

func TestB23314SchedulingFieldIsInterface(t *testing.T) {
	field, ok := reflect.TypeOf(Server{}).FieldByName("scheduling")
	if !ok {
		t.Fatal("Server.scheduling 字段不存在")
	}
	if field.Type.Kind() != reflect.Interface {
		t.Fatalf("Server.scheduling 类型 = %s，want 接口 SchedulingClient", field.Type)
	}
	if want := reflect.TypeOf((*SchedulingClient)(nil)).Elem(); field.Type != want {
		t.Fatalf("Server.scheduling 接口 = %s，want %s", field.Type, want)
	}
}

func TestB23314SchedulingSignatures(t *testing.T) {
	iface := reflect.TypeOf((*SchedulingClient)(nil)).Elem()
	set, ok := reflect.TypeOf(&Server{}).MethodByName("SetScheduling")
	if !ok {
		t.Fatal("SetScheduling 方法不存在")
	}
	if set.Type.In(1) != iface {
		t.Fatalf("SetScheduling 参数 = %s，want %s", set.Type.In(1), iface)
	}
	get, ok := reflect.TypeOf(&Server{}).MethodByName("Scheduling")
	if !ok {
		t.Fatal("Scheduling 方法不存在")
	}
	if get.Type.Out(0) != iface {
		t.Fatalf("Scheduling 返回 = %s，want %s", get.Type.Out(0), iface)
	}
}

// fakeSchedulingClient 只实现 SchedulingClient（嵌入接口补足其余方法），用于
// 证明生产字段接受非 *scheduling.Service 的实现（契约 #12）。
type fakeSchedulingClient struct{ SchedulingClient }

func (fakeSchedulingClient) DefaultCarrier() (string, error) { return "fake-default", nil }

func TestB23314FieldAcceptsInterfaceOnlyFake(t *testing.T) {
	s := &Server{log: discardLogger()}
	var fake SchedulingClient = fakeSchedulingClient{}
	s.SetScheduling(fake)
	if s.scheduling == nil {
		t.Fatal("生产字段应接受只实现接口的 fake")
	}
	if _, isConcrete := s.scheduling.(*scheduling.Service); isConcrete {
		t.Fatal("注入的 fake 不应是具体 *scheduling.Service")
	}
}

func TestB23314SetSchedulingRejectsTypedNil(t *testing.T) {
	s := &Server{log: discardLogger()}
	var typedNil *scheduling.Service
	s.SetScheduling(typedNil)
	if s.scheduling != nil {
		t.Fatalf("typed-nil 必须落成 nil 接口，got %#v", s.scheduling)
	}
}

// TestB23314SchedulingNewOnlyInCmd 扫描 gateway 生产源文件，禁止出现 scheduling.New(。
// 边界：字符串扫描、剔除 // 行注释；只答「有没有绕过组装点 new」。
func TestB23314SchedulingNewOnlyInCmd(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("读取包目录失败: %v", err)
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		b, rerr := os.ReadFile(filepath.Join(".", name))
		if rerr != nil {
			t.Fatalf("读取 %s 失败: %v", name, rerr)
		}
		for i, line := range strings.Split(string(b), "\n") {
			if idx := strings.Index(line, "//"); idx >= 0 {
				line = line[:idx]
			}
			if strings.Contains(line, "scheduling.New(") {
				t.Errorf("%s:%d 出现 scheduling.New(；B233.14 起编制域具体服务只在 cmd 组装点构造",
					name, i+1)
			}
		}
	}
}
