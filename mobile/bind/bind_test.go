package bind

import (
	"reflect"
	"testing"

	"github.com/Xsxdot/handoff/internal/mobilecore"
)

func TestBindPairForwardsToCore(t *testing.T) {
	fc := newFakeCore()
	defer swapCore(fc)()
	if err := Pair(`{"v":1}`); err != nil {
		t.Fatalf("Pair: %v", err)
	}
	if fc.pairCalls != 1 || fc.lastBundleBytes != len(`{"v":1}`) {
		t.Fatalf("Pair 未如实转发: calls=%d bytes=%d", fc.pairCalls, fc.lastBundleBytes)
	}
	// 配对结果经 MachineCount / MachineAt 读回（B386：列表不能用 Go slice）。
	if n := MachineCount(); n != 1 {
		t.Fatalf("MachineCount 应反映核侧登记数: got=%d", n)
	}
	m := MachineAt(0)
	if m == nil || m.Name != "devbox" || !m.Online || m.Origin == "" {
		t.Fatalf("MachineAt(0) 结果漂移: %+v", m)
	}
	if m := MachineAt(1); m != nil {
		t.Fatalf("越界索引应返回 nil，实得 %+v", m)
	}
	if m := MachineAt(-1); m != nil {
		t.Fatalf("负索引应返回 nil，实得 %+v", m)
	}
}

func TestBindPairSurfacesError(t *testing.T) {
	fc := newFakeCore()
	fc.pairErr = errBoom
	defer swapCore(fc)()
	if err := Pair(`{}`); err == nil {
		t.Fatal("核侧错误必须上抛，不得吞成 nil")
	}
}

func TestBindOriginSurfacesError(t *testing.T) {
	fc := newFakeCore()
	fc.originErr = errBoom
	defer swapCore(fc)()
	if _, err := Origin("devbox"); err == nil {
		t.Fatal("Origin 核侧错误必须上抛")
	}
}

// TestBindSessionCookieAndSwitchAreOneJarToOneMachine 锁三事：切机清旧罐、
// 一罐只装一机、取 cookie 经绑定导出面。入口 = SessionCookie/SwitchMachine。
func TestBindSessionCookieAndSwitchAreOneJarToOneMachine(t *testing.T) {
	fs := newFakeSessions()
	defer swapSessions(fs)()

	originA, err := SwitchMachine("devbox")
	if err != nil {
		t.Fatalf("切到 devbox: %v", err)
	}
	if originA == "" {
		t.Fatal("切机应返回目标 loopback 源")
	}
	cookieA, err := SessionCookie("devbox")
	if err != nil || cookieA != "sess-devbox" {
		t.Fatalf("取 devbox cookie: value=%q err=%v（绑定面必须返回核侧 cookie，非 origin）", cookieA, err)
	}

	originB, err := SwitchMachine("lanbox")
	if err != nil {
		t.Fatalf("切到 lanbox: %v", err)
	}
	if originB == originA {
		t.Fatalf("切机应换到另一 loopback 源: %q", originB)
	}
	if fs.switchedFrom != "devbox" {
		t.Fatalf("切机应清掉旧机罐: switchedFrom=%q", fs.switchedFrom)
	}
	// 清罐后旧机 cookie 不可再用（切机前请求它必须失败）。
	if _, err := fs.SessionCookie("devbox"); err == nil {
		t.Fatal("切机后旧机 cookie 仍可取——一罐只装一机的语义失守")
	}
	cookieB, err := SessionCookie("lanbox")
	if err != nil || cookieB != "sess-lanbox" || cookieB == cookieA {
		t.Fatalf("lanbox cookie 应与 devbox 不同: a=%q b=%q err=%v", cookieA, cookieB, err)
	}
}

// TestBindSessionNotPairedFailsClosed：用显式注入的空真实 Core（无任何配对）
// 锁绑定导出面对未配对机器的失败闭合，不依赖包级 liveCore 的空态（B392 P3=A）。
func TestBindSessionNotPairedFailsClosed(t *testing.T) {
	empty := mobilecore.New(nil, log)
	defer empty.Close()
	defer swapSessions(newCoreSessions(empty))()

	if v, err := SessionCookie("devbox"); err == nil || v != "" {
		t.Fatalf("未配对取 cookie 必须 (\"\", err): value=%q err=%v", v, err)
	}
	if v, err := SwitchMachine("devbox"); err == nil || v != "" {
		t.Fatalf("未配对切机必须 (\"\", err): value=%q err=%v", v, err)
	}
}

func TestBindMachineCountAndAtForwardToCore(t *testing.T) {
	fc := newFakeCore()
	defer swapCore(fc)()
	if n := MachineCount(); n != 1 {
		t.Fatalf("MachineCount 未如实转发: %d", n)
	}
	m := MachineAt(0)
	if m == nil || m.Name != "devbox" {
		t.Fatalf("MachineAt 未如实转发: %+v", m)
	}
}

func TestBindCloseForwardsToCore(t *testing.T) {
	fc := newFakeCore()
	defer swapCore(fc)()
	if err := Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	fc.closeErr = errBoom
	if err := Close(); err == nil {
		t.Fatal("Close 核侧错误必须上抛")
	}
}

// TestBindResultTypesAreGomobileSafe 锁跨语言映射边界：绑定面回给壳的结构体字段
// 只能是 gomobile 支持的基本类型（string/bool）。**切片一律不许**——gomobile 的
// `isSupported` 对 slice 只放行 []byte，用 []T 表达列表会被静默跳过（B386 的现场）。
func TestBindResultTypesAreGomobileSafe(t *testing.T) {
	for _, typ := range resultTypes() {
		for i := 0; i < typ.NumField(); i++ {
			f := typ.Field(i)
			if !f.IsExported() {
				t.Fatalf("%s.%s 未导出，gomobile 不可见", typ.Name(), f.Name)
			}
			switch f.Type.Kind() {
			case reflect.String, reflect.Bool:
			default:
				t.Fatalf("%s.%s 类型 %s 不是 gomobile 可绑定的 string/bool（列表请用 MachineCount+MachineAt）",
					typ.Name(), f.Name, f.Type)
			}
		}
	}
}
