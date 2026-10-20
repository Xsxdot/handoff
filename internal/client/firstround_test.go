package client_test

// firstround_test.go —— B448 首轮转交出站方法的边界：404 归版本过旧哨兵、错误体解码出 kind / pointed /
// session_id、超长中文错误体不因 4096 截断丢 kind、请求体 squad/carrier 键恒在（F6/F7/F21）。

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/client"
	"github.com/Xsxdot/handoff/internal/proto"
)

func TestFirstRoundUnsupported404(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) }))
	defer srv.Close()
	_, err := client.New(srv.URL, "tok").CoordinatorFirstRound(context.Background(), "B1",
		proto.FirstRoundTransferReq{Squad: "q", Carrier: "c"})
	if !errors.Is(err, client.ErrFirstRoundUnsupported) {
		t.Fatalf("卡转交 404 应归 ErrFirstRoundUnsupported，得 %v", err)
	}
	_, err = client.New(srv.URL, "tok").ResidentFirstRound(context.Background(), "session:1",
		proto.FirstRoundTransferReq{Squad: "q", Carrier: "c"})
	if !errors.Is(err, client.ErrFirstRoundUnsupported) {
		t.Fatalf("驻场转交 404 应归 ErrFirstRoundUnsupported，得 %v", err)
	}
}

func TestFirstRoundErrorBodyDecodesKindPastTruncation(t *testing.T) {
	long := strings.Repeat("中文原因", 600) // ~7200 字节，超过 httpError 的 4096
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(proto.FirstRoundTransferError{
			Error: long, Kind: proto.FirstRoundKindResidentOccupied, Pointed: true, SessionID: "ses-x"})
	}))
	defer srv.Close()
	_, err := client.New(srv.URL, "tok").ResidentFirstRound(context.Background(), "session:1",
		proto.FirstRoundTransferReq{Squad: "q", Carrier: "c"})
	var fe *client.FirstRoundError
	if !errors.As(err, &fe) {
		t.Fatalf("非 2xx 应为 *FirstRoundError，得 %T %v", err, err)
	}
	if fe.Status != http.StatusConflict || fe.Kind != proto.FirstRoundKindResidentOccupied || !fe.Pointed ||
		fe.SessionID != "ses-x" || fe.Message != long {
		t.Fatalf("错误体解码不符: status=%d kind=%q pointed=%v sid=%q msglen=%d",
			fe.Status, fe.Kind, fe.Pointed, fe.SessionID, len(fe.Message))
	}
}

func TestFirstRoundErrorWithoutKindKeepsRawText(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":"自动化层未装配"}`))
	}))
	defer srv.Close()
	_, err := client.New(srv.URL, "tok").CoordinatorFirstRound(context.Background(), "B1",
		proto.FirstRoundTransferReq{Squad: "q", Carrier: "c"})
	var fe *client.FirstRoundError
	if !errors.As(err, &fe) || fe.Kind != "" || fe.Message != "自动化层未装配" || fe.Status != 503 {
		t.Fatalf("无 kind 错误应保留原文与状态，得 %#v", err)
	}
}

func TestFirstRoundRequestShapeAndSuccess(t *testing.T) {
	var gotPath string
	var gotKeys map[string]json.RawMessage
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &gotKeys)
		_ = json.NewEncoder(w).Encode(proto.CoordinatorFirstRoundResp{
			CoordinatorLaunchResp: proto.CoordinatorLaunchResp{Woke: true, SessionID: "ses-1"}, HandledBy: "m"})
	}))
	defer srv.Close()
	out, err := client.New(srv.URL, "tok").CoordinatorFirstRound(context.Background(), "B 7",
		proto.FirstRoundTransferReq{})
	if err != nil {
		t.Fatal(err)
	}
	if !out.Woke || out.SessionID != "ses-1" || out.HandledBy != "m" {
		t.Fatalf("回执解码不符: %+v", out)
	}
	if gotPath != "/api/cards/B 7/coordinator/first-round" {
		t.Fatalf("路径不符: %q", gotPath)
	}
	for _, k := range []string{"squad", "carrier"} {
		if _, ok := gotKeys[k]; !ok {
			t.Fatalf("零值请求也须带 %s 键（F6/F7），得 %v", k, gotKeys)
		}
	}
	if _, ok := gotKeys["expect"]; ok {
		t.Fatalf("首轮请求不得带 expect 键（F13），得 %v", gotKeys)
	}
}
