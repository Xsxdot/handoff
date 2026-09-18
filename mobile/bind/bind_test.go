package bind

import (
	"reflect"
	"testing"

	"github.com/Xsxdot/handoff/internal/mobilecore"
)

func TestBindPairForwardsToCore(t *testing.T) {
	fc := newFakeCore()
	defer swapCore(fc)()
	res, err := Pair(`{"v":1}`)
	if err != nil {
		t.Fatalf("Pair: %v", err)
	}
	if fc.pairCalls != 1 || fc.lastBundleBytes != len(`{"v":1}`) {
		t.Fatalf("Pair 未如实转发: calls=%d bytes=%d", fc.pairCalls, fc.lastBundleBytes)
	}
	if len(res.Machines) != 1 || res.Machines[0].Name != "devbox" || !res.Machines[0].Online {
		t.Fatalf("Pair 结果漂移: %+v", res)
	}
	var _ mobilecore.PairResult = res
}

func TestBindPairSurfacesError(t *testing.T) {
	fc := newFakeCore()
	fc.pairErr = errBoom
	defer swapCore(fc)()
	if _, err := Pair(`{}`); err == nil {
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

// TestBindSessionFailIsClosed：S2 未接线时不得静默成功（空 cookie + error）。
func TestBindSessionFailIsClosed(t *testing.T) {
	if _, err := SessionCookie("devbox"); err == nil {
		t.Fatal("核侧会话入口未接线时必须返回错误，不得静默成功")
	}
	if _, err := SwitchMachine("devbox"); err == nil {
		t.Fatal("核侧会话入口未接线时切机必须返回错误")
	}
}

func TestBindMachineNamesForwardsToCore(t *testing.T) {
	fc := newFakeCore()
	defer swapCore(fc)()
	got := MachineNames()
	if len(got) != 1 || got[0] != "devbox" {
		t.Fatalf("MachineNames 未如实转发: %v", got)
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

// TestBindResultTypesAreGomobileSafe 锁跨语言映射边界：绑定面返回的结构体字段
// 只能是 string/bool（gomobile 对 time.Time/error/接口支持有限，不可绑定）。
func TestBindResultTypesAreGomobileSafe(t *testing.T) {
	for _, typ := range resultTypes() {
		for i := 0; i < typ.NumField(); i++ {
			f := typ.Field(i)
			if !f.IsExported() {
				t.Fatalf("%s.%s 未导出，gomobile 不可见", typ.Name(), f.Name)
			}
			switch f.Type.Kind() {
			case reflect.String, reflect.Bool:
			case reflect.Slice:
				if f.Type.Elem().Kind() != reflect.Struct {
					t.Fatalf("%s.%s 切片元素非结构体: %s", typ.Name(), f.Name, f.Type)
				}
			default:
				t.Fatalf("%s.%s 类型 %s 不是 gomobile 可绑定的 string/bool/结构体切片",
					typ.Name(), f.Name, f.Type)
			}
		}
	}
}
