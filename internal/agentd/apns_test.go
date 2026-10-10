// apns_test.go —— APNs 出站边界（缝 S3 的边界侧）：请求形状、错误映射、JWT。
//
// 职责：用 httptest 假 APNs 钉 Send 的**请求形状**（路径/头/载荷）与 410→
// ErrPushUnregistered 映射；钉 ES256 JWT 的算法与 claims。
// 边界：真实 HTTP/2、Apple 是否接受该 JWT —— 边界型接缝，归真机清单（plan §10）；
// 本文件只验机内可判的契约形状。
package agentd

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/testhttp"
)

// writeAPNsKey 生成一把 P-256 私钥并写成 PKCS8 PEM（.p8 形态），返回路径。
func writeAPNsKey(t *testing.T) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("生成测试密钥: %v", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("PKCS8 编码: %v", err)
	}
	path := filepath.Join(t.TempDir(), "apns.p8")
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0o600); err != nil {
		t.Fatalf("写测试密钥: %v", err)
	}
	return path
}

// newTestAPNsSender 造一把带真实密钥的 sender，host 指向假 APNs。
func newTestAPNsSender(t *testing.T, host string) *APNsSender {
	t.Helper()
	cfg := config.PushConfig{
		APNsKeyID: "KEYID12345", APNsTeamID: "TEAMID1234", APNsBundleID: "dev.gosuper.handoff.mobile",
		APNsKeyFile: writeAPNsKey(t), APNsHost: host,
	}
	s, err := NewAPNsSender(cfg, &http.Client{}, slog.New(&roomsLogCapture{}))
	if err != nil {
		t.Fatalf("NewAPNsSender: %v", err)
	}
	return s
}

// TestAPNsSenderRequestShape 是缝 S3 边界的主断言：Send 打出的请求必须是
// POST /3/device/<token> + apns-* 头 + badge/deep_link 载荷。
func TestAPNsSenderRequestShape(t *testing.T) {
	type seen struct {
		path, topic, pushType, priority, auth string
		body                                  []byte
	}
	got := seen{}
	ts := testhttp.NewServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		got = seen{
			path: r.URL.Path, topic: r.Header.Get("apns-topic"),
			pushType: r.Header.Get("apns-push-type"),
			priority: r.Header.Get("apns-priority"),
			auth:     r.Header.Get("authorization"),
			body:     body,
		}
		w.WriteHeader(http.StatusOK)
	}))

	s := newTestAPNsSender(t, ts.URL)
	n := proto.PushNotification{EventType: "decision", Member: "user:sy",
		Title: "裁决", CardID: "B1", RefID: "42", DeepLink: "/cards?card=B1"}
	if err := s.Send(context.Background(), "device-token-1", n, 5); err != nil {
		t.Fatalf("Send: %v", err)
	}

	if got.path != "/3/device/device-token-1" {
		t.Errorf("路径 = %q，期望 /3/device/device-token-1", got.path)
	}
	if !strings.HasPrefix(got.auth, "bearer ") || len(got.auth) < 20 {
		t.Errorf("authorization = %q，期望 bearer <jwt>", got.auth)
	}
	if got.topic != "dev.gosuper.handoff.mobile" {
		t.Errorf("apns-topic = %q", got.topic)
	}
	if got.pushType != "alert" {
		t.Errorf("apns-push-type = %q，期望 alert", got.pushType)
	}
	if got.priority != "10" {
		t.Errorf("apns-priority = %q，期望 10", got.priority)
	}

	var payload struct {
		Aps struct {
			Alert struct {
				Title string `json:"title"`
			} `json:"alert"`
			Badge  int    `json:"badge"`
			Sound  string `json:"sound"`
			Thread string `json:"thread-id"`
		} `json:"aps"`
		Handoff proto.PushNotification `json:"handoff"`
	}
	if err := json.Unmarshal(got.body, &payload); err != nil {
		t.Fatalf("载荷解析失败: %v（原文 %s）", err, got.body)
	}
	if payload.Aps.Badge != 5 {
		t.Errorf("aps.badge = %d，期望 5", payload.Aps.Badge)
	}
	if payload.Aps.Alert.Title != "裁决" {
		t.Errorf("aps.alert.title = %q", payload.Aps.Alert.Title)
	}
	if payload.Aps.Sound != "default" || payload.Aps.Thread != "handoff-needs-you" {
		t.Errorf("aps.sound/thread = %q/%q", payload.Aps.Sound, payload.Aps.Thread)
	}
	if payload.Handoff != n {
		t.Errorf("handoff 键载荷 = %+v，期望 %+v（序列化边界回归）", payload.Handoff, n)
	}
}

// TestAPNsSenderMaps410 锁 410 → ErrPushUnregistered（fanout 据此删设备）。
func TestAPNsSenderMaps410(t *testing.T) {
	ts := testhttp.NewServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusGone)
		_, _ = w.Write([]byte(`{"reason":"Unregistered"}`))
	}))

	s := newTestAPNsSender(t, ts.URL)
	err := s.Send(context.Background(), "dead-token", proto.PushNotification{RefID: "1"}, 0)
	if !errors.Is(err, ErrPushUnregistered) {
		t.Fatalf("410 应映射为 ErrPushUnregistered，实为 %v", err)
	}
}

// TestAPNsSenderNon200IsError 锁非 200（非 410）为带 status 的错误，且不回显 token。
func TestAPNsSenderNon200IsError(t *testing.T) {
	ts := testhttp.NewServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"reason":"BadDeviceToken"}`))
	}))

	s := newTestAPNsSender(t, ts.URL)
	err := s.Send(context.Background(), "device-token", proto.PushNotification{RefID: "1"}, 0)
	if err == nil {
		t.Fatal("400 应报错")
	}
	if !strings.Contains(err.Error(), "400") {
		t.Errorf("错误应含 status： %v", err)
	}
	if strings.Contains(err.Error(), "device-token") {
		t.Errorf("错误不得回显 token： %v", err)
	}
}

// TestAPNsSenderConfigIncomplete 未配齐凭据必须构造失败（fanout 据此停用，不半开）。
func TestAPNsSenderConfigIncomplete(t *testing.T) {
	for name, cfg := range map[string]config.PushConfig{
		"缺 key id":  {APNsTeamID: "T", APNsBundleID: "b", APNsKeyFile: writeAPNsKey(t)},
		"缺 team":    {APNsKeyID: "K", APNsBundleID: "b", APNsKeyFile: writeAPNsKey(t)},
		"缺 bundle":  {APNsKeyID: "K", APNsTeamID: "T", APNsKeyFile: writeAPNsKey(t)},
		"缺 keyfile": {APNsKeyID: "K", APNsTeamID: "T", APNsBundleID: "b"},
	} {
		if _, err := NewAPNsSender(cfg, nil, slog.New(&roomsLogCapture{})); err == nil {
			t.Errorf("%s: 应构造失败", name)
		}
	}
}

// TestLoadAPNsKeyRejectsBadPEM 锁坏 PEM / 非 PKCS8 / 非 EC 密钥的拒收。
func TestLoadAPNsKeyRejectsBadPEM(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.p8")
	if err := os.WriteFile(bad, []byte("not a pem"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadAPNsKey(bad); err == nil {
		t.Fatal("畸形 PEM 应报错")
	}
	if _, err := loadAPNsKey(filepath.Join(dir, "missing.p8")); err == nil {
		t.Fatal("不存在的文件应报错")
	}

	// 合法 PEM 但内容不是 PKCS8 私钥。
	notKey := filepath.Join(dir, "notkey.p8")
	if err := os.WriteFile(notKey, pem.EncodeToMemory(&pem.Block{
		Type: "PRIVATE KEY", Bytes: []byte("garbage"),
	}), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadAPNsKey(notKey); err == nil {
		t.Fatal("非 PKCS8 内容应报错")
	}
}

// TestAPNsSenderBearerToken 钉 JWT 形状：三段、header alg=ES256+kid、claims iss=team。
func TestAPNsSenderBearerToken(t *testing.T) {
	s := newTestAPNsSender(t, "https://api.push.apple.com")
	now := time.Date(2026, 10, 10, 8, 0, 0, 0, time.UTC)
	tok, err := s.bearerToken(now)
	if err != nil {
		t.Fatalf("bearerToken: %v", err)
	}
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		t.Fatalf("JWT 应三段，实为 %d 段", len(parts))
	}
	decode := func(seg string) map[string]any {
		raw, err := base64.RawURLEncoding.DecodeString(seg)
		if err != nil {
			t.Fatalf("b64url 解码失败: %v", err)
		}
		var m map[string]any
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatalf("JSON 解码失败: %v（%s）", err, raw)
		}
		return m
	}
	header := decode(parts[0])
	if header["alg"] != "ES256" || header["kid"] != "KEYID12345" {
		t.Fatalf("header = %+v，期望 alg=ES256 kid=KEYID12345", header)
	}
	claims := decode(parts[1])
	if claims["iss"] != "TEAMID1234" {
		t.Fatalf("claims.iss = %+v", claims["iss"])
	}
	if _, ok := claims["iat"].(float64); !ok {
		t.Fatalf("claims.iat 应为数字: %+v", claims["iat"])
	}
	// 签名段必须是 P-256 的 raw r||s（64 字节）。
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || len(sig) != 64 {
		t.Fatalf("签名应为 64 字节 raw r||s，实为 %d 字节（err=%v）", len(sig), err)
	}
}
