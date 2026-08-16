package agentd

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"
)

// spaTestFS 模拟一份 vite 产物：index.html + 一个带 hash 的 JS + 一个静态图。
// 用 fstest.MapFS 而不是真实 dist：测试不能依赖「先跑过 npm run build」。
func spaTestFS() fstest.MapFS {
	return fstest.MapFS{
		"index.html":              &fstest.MapFile{Data: []byte("<!doctype html><div id=root></div>")},
		"assets/app-a1b2c3d4.js":  &fstest.MapFile{Data: []byte("console.log(1)")},
		"assets/app-a1b2c3d4.css": &fstest.MapFile{Data: []byte("body{}")},
		"favicon.svg":             &fstest.MapFile{Data: []byte("<svg/>")},
	}
}

func spaGet(t *testing.T, h http.Handler, path string) *http.Response {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec.Result()
}

// 命中真实文件时必须原样伺服，不能回落。
func TestSPAServesRealFile(t *testing.T) {
	h := newSPAHandler(spaTestFS(), testLogger(t))
	resp := spaGet(t, h, "/assets/app-a1b2c3d4.js")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("状态码 = %d，want 200", resp.StatusCode)
	}
	b, _ := io.ReadAll(resp.Body)
	if string(b) != "console.log(1)" {
		t.Errorf("内容 = %q，want 真实文件内容", b)
	}
}

// 深链接（客户端路由）必须回落 index.html，否则刷新页面就 404。
func TestSPAFallsBackToIndexForDeepLink(t *testing.T) {
	h := newSPAHandler(spaTestFS(), testLogger(t))
	for _, path := range []string{"/", "/tasks", "/tasks/abc-123", "/projects/x/machines/y"} {
		resp := spaGet(t, h, path)
		if resp.StatusCode != http.StatusOK {
			t.Errorf("%s 状态码 = %d，want 200", path, resp.StatusCode)
			continue
		}
		b, _ := io.ReadAll(resp.Body)
		if string(b) != "<!doctype html><div id=root></div>" {
			t.Errorf("%s 未回落到 index.html，实际 = %q", path, b)
		}
	}
}

// index.html 必须 no-cache：否则换版后浏览器拿着旧 index 去引用
// 已经不存在的 hash 资源，表现为白屏，且用户清缓存前无法自愈。
func TestSPAIndexIsNoCache(t *testing.T) {
	h := newSPAHandler(spaTestFS(), testLogger(t))
	for _, path := range []string{"/", "/tasks/abc"} {
		got := spaGet(t, h, path).Header.Get("Cache-Control")
		if got != "no-cache" {
			t.Errorf("%s 的 Cache-Control = %q，want no-cache", path, got)
		}
	}
}

// 带 hash 的资源可以长缓存：文件名变了内容才变，这是 vite 的产物契约。
func TestSPAHashedAssetIsImmutable(t *testing.T) {
	h := newSPAHandler(spaTestFS(), testLogger(t))
	got := spaGet(t, h, "/assets/app-a1b2c3d4.js").Header.Get("Cache-Control")
	if got != "public, max-age=31536000, immutable" {
		t.Errorf("hash 资源的 Cache-Control = %q，want 长缓存", got)
	}
}

// 不带 hash 的静态文件不能长缓存——它换了内容名字不变。
func TestSPAUnhashedAssetIsNotImmutable(t *testing.T) {
	h := newSPAHandler(spaTestFS(), testLogger(t))
	got := spaGet(t, h, "/favicon.svg").Header.Get("Cache-Control")
	if got == "public, max-age=31536000, immutable" {
		t.Errorf("favicon.svg 不带 hash，不该被长缓存")
	}
}

// 非 GET/HEAD 不该被 SPA 吞掉——那多半是打错了路由的写请求，
// 回落一个 200 的 HTML 会让调用方以为成功了。
func TestSPARejectsNonGet(t *testing.T) {
	h := newSPAHandler(spaTestFS(), testLogger(t))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/tasks", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST 状态码 = %d，want 405", rec.Code)
	}
}

// 目录穿越必须被拒。fs.FS 本身不接受 .. ，但回落逻辑不能把它变成 200
// 直达真实文件。断言「回落成 index.html 而非真实文件」：只有这个断言能咬住
// 「穿越没被挡、直接伺服了真实文件」的回归。
func TestSPARejectsTraversal(t *testing.T) {
	h := newSPAHandler(spaTestFS(), testLogger(t))
	resp := spaGet(t, h, "/../../etc/passwd")
	b, _ := io.ReadAll(resp.Body)
	if string(b) == "console.log(1)" {
		t.Fatal("穿越请求拿到了真实文件")
	}
	if string(b) != "<!doctype html><div id=root></div>" {
		t.Errorf("穿越请求未回落 index.html，实际 = %q", b)
	}
}

// index.html 缺失是 stub 都不该出现的状态，但一旦出现必须是 500 而不是空 200：
// 空 200 会让浏览器显示白页，运维完全看不出发生了什么。
func TestSPAMissingIndexIs500(t *testing.T) {
	h := newSPAHandler(fstest.MapFS{}, testLogger(t))
	resp := spaGet(t, h, "/")
	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("index 缺失时状态码 = %d，want 500", resp.StatusCode)
	}
}
