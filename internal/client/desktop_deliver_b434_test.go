// desktop_deliver_b434_test.go —— B434.6：Client.DesktopDeliver 与 StatusCode 的线契约
// （契约 §3.9 POST /api/desktop/deliver、§3.9.1、r12 第 5 条，client 一侧）。
//
// 职责：httptest 伪收件机锁请求形状（恰一次 POST、路径、Bearer、转交头随 MarkForwarded、
// 体只搬三键且键序与 V 的 fixture 一致）、200 回执逐字段解码、非 2xx 不重试、StatusCode
// 对 HTTP 状态错误与非 HTTP 错误的两种投影。
// 边界：收件机行为（守卫、渲染、零出站）与发起端处置归 internal/agentd 的 G3 测试。
package client_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/Xsxdot/handoff/internal/client"
	"github.com/Xsxdot/handoff/internal/proto"
)

// b434DeliverKeyOrder 返回 JSON 对象顶层键的出现顺序。
func b434DeliverKeyOrder(t *testing.T, body []byte) []string {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(body))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		t.Fatalf("须为 JSON 对象: %v %s", err, body)
	}
	var keys []string
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			t.Fatalf("读键: %v", err)
		}
		keys = append(keys, tok.(string))
		var skip json.RawMessage
		if err := dec.Decode(&skip); err != nil {
			t.Fatalf("读值: %v", err)
		}
	}
	return keys
}

// 恰一次 POST /api/desktop/deliver，带 Bearer 与（MarkForwarded 后的）X-Handoff-Forwarded: 1；
// 体恰为 req 的三键、键序等于 V 冻结的 DesktopDeliverReq fixture；200 体三键逐字解码（opened:false
// 不丢）。
func TestB434DesktopDeliverPostsRequest(t *testing.T) {
	var calls int
	var gotMethod, gotPath, gotAuth, gotFwd string
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		gotMethod, gotPath = r.Method, r.URL.Path
		gotAuth, gotFwd = r.Header.Get("Authorization"), r.Header.Get("X-Handoff-Forwarded")
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"handled_by":"mac-02","written":true,"opened":false}`)
	}))
	defer srv.Close()
	req := proto.DesktopDeliverReq{CLISession: "cli-1", DesktopSession: "local_d1", Card: "B42"}
	resp, err := client.New(srv.URL, testToken).MarkForwarded().DesktopDeliver(context.Background(), req)
	if err != nil {
		t.Fatalf("DesktopDeliver: %v", err)
	}
	want := proto.DesktopDeliverResp{HandledBy: "mac-02", Written: true, Opened: false}
	if resp == nil || *resp != want {
		t.Fatalf("回执 = %+v, want %+v", resp, want)
	}
	if calls != 1 || gotMethod != http.MethodPost || gotPath != "/api/desktop/deliver" {
		t.Fatalf("请求形状 = %d 次 %s %s", calls, gotMethod, gotPath)
	}
	if gotAuth != "Bearer "+testToken || gotFwd != "1" {
		t.Fatalf("Authorization = %q, X-Handoff-Forwarded = %q", gotAuth, gotFwd)
	}
	var sent proto.DesktopDeliverReq
	if err := json.Unmarshal(gotBody, &sent); err != nil || sent != req {
		t.Fatalf("请求体 = %s（%v）, want %+v", gotBody, err, req)
	}
	fixture, err := os.ReadFile(filepath.Join("..", "..", "web", "src", "api", "testdata", "DesktopDeliverReq.json"))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := b434DeliverKeyOrder(t, gotBody), b434DeliverKeyOrder(t, fixture); !reflect.DeepEqual(got, want) {
		t.Fatalf("请求体键序 %v ≠ fixture %v", got, want)
	}
}

// 非 2xx：只发一次（不重试——重试会把「信已落盘但响应丢了」变成两封拉起信）；StatusCode 取出
// 对端状态码，ServerErrorText 取出 error 原文，两个投影同源于一个错误。
func TestB434DesktopDeliverNon2xxOnceWithStatus(t *testing.T) {
	for _, status := range []int{http.StatusConflict, http.StatusServiceUnavailable, http.StatusInternalServerError} {
		var calls int
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls++
			w.WriteHeader(status)
			_, _ = io.WriteString(w, `{"error":"收件机原文"}`)
		}))
		_, err := client.New(srv.URL, testToken).DesktopDeliver(context.Background(), proto.DesktopDeliverReq{Card: "B42"})
		srv.Close()
		if err == nil {
			t.Fatalf("%d：非 2xx 应返回错误", status)
		}
		if calls != 1 {
			t.Fatalf("%d：应只发一次，得 %d 次", status, calls)
		}
		if code, ok := client.StatusCode(err); !ok || code != status {
			t.Fatalf("StatusCode = %d ok=%v, want %d", code, ok, status)
		}
		if text, ok := client.ServerErrorText(err); !ok || text != "收件机原文" {
			t.Fatalf("ServerErrorText = %q ok=%v", text, ok)
		}
	}
}

// StatusCode 对非 HTTP 状态错误（连接失败、普通错误、nil）返回 ok=false，调用方按原错误处理。
func TestB434StatusCodeNonHTTP(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	addr := srv.URL
	srv.Close()
	_, connErr := client.New(addr, testToken).DesktopDeliver(context.Background(), proto.DesktopDeliverReq{Card: "B42"})
	if !errors.Is(connErr, client.ErrUnreachable) {
		t.Fatalf("前提：连接失败应带 ErrUnreachable，得 %v", connErr)
	}
	for name, err := range map[string]error{"连接失败": connErr, "普通错误": errors.New("x"), "nil": nil} {
		if code, ok := client.StatusCode(err); ok || code != 0 {
			t.Fatalf("%s：StatusCode = %d ok=%v, want 0 false", name, code, ok)
		}
	}
}
