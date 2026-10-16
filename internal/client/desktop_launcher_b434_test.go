// desktop_launcher_b434_test.go —— B434.2：Client.DesktopLauncherOpen 与 ServerErrorText
// 的线契约（契约 §3.9 POST desktop-launch、§3.10、条 253/257–259 的 client 一侧）。
//
// 职责：httptest 伪 agentd 按契约 §3.9 形状作答（G3 端点未落地期的唯一对侧），锁请求形状
// （POST、路径、体 {}、Bearer）、200 体逐字段解码、非 2xx 错误原文投影。
// 边界：端点行为（空座前置、无拉起器 needs_human）归 G3 的 internal/agentd 测试。
package client_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Xsxdot/handoff/internal/client"
	"github.com/Xsxdot/handoff/internal/proto"
)

// 恰一次 POST /api/cards/{id}/coordinator/desktop-launch，体 {}，带 Bearer；200 体三键逐字解码。
func TestB434DesktopLauncherOpenPostsEmptyObject(t *testing.T) {
	var calls int
	var gotMethod, gotPath, gotBody, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		gotMethod, gotPath, gotAuth = r.Method, r.URL.Path, r.Header.Get("Authorization")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"launcher":"local_l1","machine":"","handled_by":"mac-01"}`)
	}))
	defer srv.Close()
	resp, err := client.New(srv.URL, testToken).DesktopLauncherOpen(context.Background(), "B42")
	if err != nil {
		t.Fatalf("DesktopLauncherOpen: %v", err)
	}
	want := proto.DesktopLauncherOpenResp{Launcher: "local_l1", Machine: "", HandledBy: "mac-01"}
	if resp == nil || *resp != want {
		t.Fatalf("回执 = %+v, want %+v", resp, want)
	}
	if calls != 1 || gotMethod != http.MethodPost || gotPath != "/api/cards/B42/coordinator/desktop-launch" {
		t.Fatalf("请求形状 = %d 次 %s %s", calls, gotMethod, gotPath)
	}
	if gotBody != `{}` {
		t.Fatalf("请求体 = %q, want {}", gotBody)
	}
	if gotAuth != "Bearer "+testToken {
		t.Fatalf("Authorization = %q", gotAuth)
	}
}

// 非 2xx：ServerErrorText 取 JSON error 原文；非 JSON / 无 error 键取原始体；空体给状态码；
// 连接失败 ok=false 且错误链带 ErrUnreachable。
func TestB434DesktopLauncherOpenErrorText(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		want   string
	}{
		{"409 无拉起器", http.StatusConflict, `{"error":"无拉起器，请在桌面端开一场并登记"}`, "无拉起器，请在桌面端开一场并登记"},
		{"502 非 JSON", http.StatusBadGateway, "bad gateway\n", "bad gateway"},
		{"409 无 error 键", http.StatusConflict, `{"detail":"x"}`, `{"detail":"x"}`},
		{"500 空体", http.StatusInternalServerError, "", "agentd 返回状态码 500"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer srv.Close()
			_, err := client.New(srv.URL, testToken).DesktopLauncherOpen(context.Background(), "B42")
			if err == nil {
				t.Fatal("非 2xx 应返回错误")
			}
			got, ok := client.ServerErrorText(err)
			if !ok || got != tc.want {
				t.Fatalf("ServerErrorText = %q ok=%v, want %q", got, ok, tc.want)
			}
		})
	}
	srv := httptest.NewServer(http.NotFoundHandler())
	dead := srv.URL
	srv.Close()
	_, err := client.New(dead, testToken).DesktopLauncherOpen(context.Background(), "B42")
	if _, ok := client.ServerErrorText(err); ok || !errors.Is(err, client.ErrUnreachable) {
		t.Fatalf("连接失败应 ok=false 且为 ErrUnreachable: ok=%v err=%v", ok, err)
	}
}
