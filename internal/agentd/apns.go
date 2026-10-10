// apns.go —— APNs 出站发送器（B432 spec §契约面 2 的执行侧）。
//
// 职责：把一条 proto.PushNotification 以 alert 推给一个 APNs device token，
// 并把 APNs 的响应翻译成 fanout 认得的错误（410 = 设备失效）。
//
// 边界：
//   - **只发不收**：不轮询、不重试；重试与去重是 fanout/事件层的事
//   - **不新增依赖**：APNs provider 证书用 .p8 + ES256 JWT，全部 stdlib 手搓
//     （crypto/ecdsa + encoding/json + net/http 自动 HTTP/2）
//   - **日志不落 token**：token 明文只在请求 URL 里出现，日志只落 host 与长度
//   - 错误带 status 与 body 摘要（Apple 的 reason 码在那里），**不回显 token**
package agentd

import (
	"context"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/proto"
)

const (
	// defaultAPNsHost 是 APNs 生产环境（token-based authentication 的 https 端点）。
	defaultAPNsHost = "https://api.push.apple.com"
	// apnsPathPrefix 是 APNs 投递路径前缀：<host>/3/device/<token>。
	apnsPathPrefix = "/3/device/"
	// apnsJWTSkew 是 JWT 提前过期量：Apple 要求 iat 在 1 小时内，留 10 分钟余量。
	apnsJWTSkew = 10 * time.Minute
	// apnsErrorBodyLimit 是错误里回显的 body 摘要长度（够看清 reason 码即可）。
	apnsErrorBodyLimit = 200
)

// apsAlert 是 APNs 的 alert 通知体（标题走 title，正文由壳侧展示 title）。
type apsAlert struct {
	Title string `json:"title"`
}

// apsPayload 是 APNs 载荷的 aps 节。
type apsPayload struct {
	Alert  apsAlert `json:"alert"`
	Badge  int      `json:"badge"`
	Sound  string   `json:"sound"`
	Thread string   `json:"thread-id"`
}

// apnsPayload 是 APNs 载荷整体：aps + handoff（plan 决策 D5 的 custom key）。
// 字段顺序即 JSON 键顺序，aps 在前便于日志/抓包先读到系统节。
type apnsPayload struct {
	Aps     apsPayload             `json:"aps"`
	Handoff proto.PushNotification `json:"handoff"`
}

// APNsSender 是 PushSender 的生产实现。
//
// 并发安全：jwt/tokenExp 由 mu 保护（多设备并发投递共享同一把 provider key）；
// 其余字段构造后只读。
type APNsSender struct {
	host   string // 含 scheme 的基址
	topic  string // apns-topic = bundle id
	keyID  string
	teamID string
	key    *ecdsa.PrivateKey
	hc     *http.Client
	log    *slog.Logger
	mu     sync.Mutex
	jwt    string
	jwtExp time.Time
}

// NewAPNsSender 构造发送器。
//
// 参数：cfg 五个键必须配齐（keyid/team/bundle/keyfile 缺一即错，不半开）；
// hc 为 nil 时用 http.DefaultClient；log 为 nil 时用 slog.Default()。
// 返回：sender 或错误（配置缺失 / 私钥读不出）。
//
// 注意：host 空取生产端点；含 "://" 的 host 原样使用（测试指 httptest），
// 不含 scheme 时补 https://。
func NewAPNsSender(cfg config.PushConfig, hc *http.Client, log *slog.Logger) (*APNsSender, error) {
	if log == nil {
		log = slog.Default()
	}
	missing := make([]string, 0, 4)
	if cfg.APNsKeyID == "" {
		missing = append(missing, "apns_key_id")
	}
	if cfg.APNsTeamID == "" {
		missing = append(missing, "apns_team_id")
	}
	if cfg.APNsBundleID == "" {
		missing = append(missing, "apns_bundle_id")
	}
	if cfg.APNsKeyFile == "" {
		missing = append(missing, "apns_key_file")
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("APNs 配置不完整，缺 %s（推送停用，静默降级站内）", strings.Join(missing, ","))
	}
	key, err := loadAPNsKey(cfg.APNsKeyFile)
	if err != nil {
		return nil, fmt.Errorf("加载 APNs 私钥: %w", err)
	}
	if hc == nil {
		hc = http.DefaultClient
	}
	host := defaultAPNsHost
	if cfg.APNsHost != "" {
		host = cfg.APNsHost
		if !strings.Contains(host, "://") {
			host = "https://" + host
		}
	}
	return &APNsSender{
		host: host, topic: cfg.APNsBundleID,
		keyID: cfg.APNsKeyID, teamID: cfg.APNsTeamID, key: key,
		hc: hc, log: log,
	}, nil
}

// loadAPNsKey 读 .p8（PKCS8 PEM）并取出 ECDSA 私钥。
//
// 返回：*ecdsa.PrivateKey 或错误（文件不存在 / 畸形 PEM / 非 PKCS8 / 非 EC 密钥）。
// 注意：只认 PKCS8——Apple 下载的 .p8 就是这个格式，不猜其它变体。
func loadAPNsKey(path string) (*ecdsa.PrivateKey, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读私钥文件: %w", err)
	}
	block, _ := pem.Decode(raw)
	if block == nil {
		return nil, errors.New("私钥文件不是合法 PEM")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("解析 PKCS8: %w", err)
	}
	key, ok := parsed.(*ecdsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("私钥类型 %T 不是 ECDSA", parsed)
	}
	return key, nil
}

// bearerToken 返回 provider JWT（ES256，缓存至 iat 后 50 分钟）。
//
// 参数：now 由调用方给定（测试可钉时间）。返回：三段式 JWT 或签名/编码错误。
// 注意：Apple 要求 iat 在 1 小时内——缓存窗口刻意小于 1 小时。
func (s *APNsSender) bearerToken(now time.Time) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.jwt != "" && now.Before(s.jwtExp) {
		return s.jwt, nil
	}
	header, err := json.Marshal(map[string]string{"alg": "ES256", "kid": s.keyID})
	if err != nil {
		return "", fmt.Errorf("编码 JWT header: %w", err)
	}
	claims, err := json.Marshal(map[string]any{"iss": s.teamID, "iat": now.Unix()})
	if err != nil {
		return "", fmt.Errorf("编码 JWT claims: %w", err)
	}
	signingInput := base64.RawURLEncoding.EncodeToString(header) + "." +
		base64.RawURLEncoding.EncodeToString(claims)
	digest := sha256.Sum256([]byte(signingInput))
	r, ss, err := ecdsa.Sign(rand.Reader, s.key, digest[:])
	if err != nil {
		return "", fmt.Errorf("ECDSA 签名: %w", err)
	}
	// P-256 的 raw 签名是 r||s 各 32 字节定长（不是 ASN.1 DER——APNs 只认这个）。
	sig := make([]byte, 64)
	r.FillBytes(sig[:32])
	ss.FillBytes(sig[32:])
	s.jwt = signingInput + "." + base64.RawURLEncoding.EncodeToString(sig)
	s.jwtExp = now.Add(50 * time.Minute)
	return s.jwt, nil
}

// Send 把一条通知投给一个 APNs token（PushSender 实现）。
//
// 参数：token 是 APNs device token（十六进制串，只进 URL，不进日志）；
// n 是通知载荷；badge 是该成员当前未处理数。
// 返回：nil（APNs 200）/ ErrPushUnregistered（410，token 失效）/
//
//	带 status 与 body 摘要的错误（其余非 200）/ 编码或网络错误。
//
// 注意：不重试——410 由 fanout 删设备，其余失败只 Warn（不报假送达）。
func (s *APNsSender) Send(ctx context.Context, token string, n proto.PushNotification, badge int) error {
	s.log.Debug("APNs 投递开始", "host", s.host, "token_len", len(token),
		"event_type", n.EventType, "badge", badge)
	jwt, err := s.bearerToken(time.Now())
	if err != nil {
		s.log.Error("APNs JWT 签名失败", "host", s.host, "cause", err)
		return fmt.Errorf("APNs JWT: %w", err)
	}
	body, err := json.Marshal(apnsPayload{
		Aps: apsPayload{
			Alert:  apsAlert{Title: n.Title},
			Badge:  badge,
			Sound:  "default",
			Thread: "handoff-needs-you",
		},
		Handoff: n,
	})
	if err != nil {
		return fmt.Errorf("编码 APNs 载荷: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		s.host+apnsPathPrefix+token, strings.NewReader(string(body)))
	if err != nil {
		return fmt.Errorf("构造 APNs 请求: %w", err)
	}
	req.Header.Set("authorization", "bearer "+jwt)
	req.Header.Set("apns-topic", s.topic)
	req.Header.Set("apns-push-type", "alert")
	req.Header.Set("apns-priority", "10")
	req.Header.Set("content-type", "application/json")

	resp, err := s.hc.Do(req)
	if err != nil {
		s.log.Warn("APNs 请求失败", "host", s.host, "cause", err)
		return fmt.Errorf("APNs 请求失败: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	switch {
	case resp.StatusCode == http.StatusOK:
		return nil
	case resp.StatusCode == http.StatusGone:
		// 410 = token 已失效（用户卸载/重装 App）。fanout 据此删设备，不重试。
		return ErrPushUnregistered
	default:
		summary := truncateRunes(string(respBody), apnsErrorBodyLimit)
		s.log.Warn("APNs 投递被拒", "host", s.host, "status", resp.StatusCode,
			"event_type", n.EventType, "body", summary)
		return fmt.Errorf("APNs 投递失败 status=%d body=%s", resp.StatusCode, summary)
	}
}
