// peer client：本机 agentd 对远端 agentd 的 HTTP 客户端。
//
// 职责：
//   - Hello / MachineSnapshot / EventsAfter 三个 peer 操作
//   - 错误映射：401/403 → AUTH_FAILED，协议不兼容 → incompatible，网络错误 → unavailable
//   - 所有请求设 connect/header/read deadline
//
// 边界：
//   - 只能由本机 agentd 构造；credential resolver 通过 Machine.SecretRef 取 token
//   - 不记录 token 到日志
package peer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// 错误哨兵（Supervisor 据此映射 Machine 状态）。
var (
	ErrAuthFailed   = errors.New("peer 认证失败")
	ErrIncompatible = errors.New("peer 协议不兼容")
	ErrUnavailable  = errors.New("peer 不可达")
)

// ClientConfig 是 peer client 的构造参数。
type ClientConfig struct {
	Endpoint string // 远端 agentd 地址（含 scheme）
	Token    string // 远端 token（来自 Machine.SecretRef 解析）
}

// Client 是 peer v1 HTTP 客户端。
type Client struct {
	endpoint string
	token    string
	hc       *http.Client
}

// NewClient 创建 peer client。
//
// 为什么 token 由调用方注入而非 client 自取：client 不持有 config，
// credential resolver 在 supervisor 层按 Machine.SecretRef 解析后传入。
func NewClient(cfg ClientConfig) *Client {
	return &Client{
		endpoint: strings.TrimSuffix(cfg.Endpoint, "/"),
		token:    cfg.Token,
		hc: &http.Client{
			// peer 是机器到机器的同步，超时严格有界防止半死连接挂起
			Timeout: 30 * time.Second,
		},
	}
}

// Hello 获取远端协议版本与 capability。
func (c *Client) Hello(ctx context.Context) (Hello, error) {
	var out Hello
	if err := c.do(ctx, http.MethodGet, "/v1/peer/hello", nil, &out); err != nil {
		return Hello{}, err
	}
	return out, nil
}

// MachineSnapshot 获取远端全量快照。
func (c *Client) MachineSnapshot(ctx context.Context) (MachineSnapshot, error) {
	var out MachineSnapshot
	if err := c.do(ctx, http.MethodGet, "/v1/machine/snapshot", nil, &out); err != nil {
		return MachineSnapshot{}, err
	}
	return out, nil
}

// EventsAfter 拉取 machine 在 afterSeq 之后的事件，最多 limit 条。
func (c *Client) EventsAfter(ctx context.Context, machineID string, afterSeq int64, limit int) ([]MachineEvent, error) {
	path := fmt.Sprintf("/v1/machine/events?machine_id=%s&after=%d&limit=%d", machineID, afterSeq, limit)
	var out []MachineEvent
	if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// Close 释放资源（当前无持久连接，保留接口便于生命周期统一）。
func (c *Client) Close() {}

// do 执行带鉴权与错误映射的 HTTP 请求。
func (c *Client) do(ctx context.Context, method, path string, body io.Reader, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, c.endpoint+path, body)
	if err != nil {
		return fmt.Errorf("构造请求 %s: %w", path, err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.hc.Do(req)
	if err != nil {
		// 网络错误（连接拒绝/超时/断线）→ unavailable
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return ErrAuthFailed
	case resp.StatusCode == http.StatusConflict: // 协议不兼容由远端显式标记
		return ErrIncompatible
	case resp.StatusCode < 200 || resp.StatusCode >= 300:
		return fmt.Errorf("peer 返回非 2xx: %d", resp.StatusCode)
	}
	if out != nil {
		if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(out); err != nil {
			return fmt.Errorf("解码 peer 响应 %s: %w", path, err)
		}
	}
	return nil
}
