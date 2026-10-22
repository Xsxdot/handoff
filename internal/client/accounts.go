// accounts.go —— 账号与额度端点的拨号方法（B453 S11）。
//
// 职责：与 squads.go 同一底座（do/httpError/decodeWire）；线格式在 internal/proto
// （AccountsResp/AccountView，spec F1/F4/F5）。无业务判断：状态推导与探测都在服务端，
// 本包只保证传输与错误正文透明（服务端 {"error"} 原样带给 CLI）。
package client

import (
	"context"
	"net/http"
	"net/url"

	"github.com/Xsxdot/handoff/internal/proto"
)

// Accounts 拉取全部账号读面（GET /api/accounts）。
func (c *Client) Accounts(ctx context.Context) (*proto.AccountsResp, error) {
	resp, err := c.do(ctx, http.MethodGet, "/api/accounts", nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, c.httpError("accounts", resp)
	}
	var out proto.AccountsResp
	if err := decodeWire(resp, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ProbeAccount 让账号所在机立即探测一次并返回新读面（POST /api/accounts/{name}/probe；
// 所在机非本机时由服务端转发）。探测失败的结论在返回的 View 里，不是 error。
func (c *Client) ProbeAccount(ctx context.Context, name string) (*proto.AccountView, error) {
	resp, err := c.do(ctx, http.MethodPost, "/api/accounts/"+url.PathEscape(name)+"/probe", nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, c.httpError("probe account", resp)
	}
	var out proto.AccountView
	if err := decodeWire(resp, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DiscoverAccounts 让入口机立即发现：machine 空 = 全部已登记机器（含本机）各扫一次，非空 =
// 只跑指定机（POST /api/accounts/discover，B464 A10/C2）。单机失败在 Machines 行里，不是 error；
// 只有请求本身失败（连不上入口机、入口机 5xx）才是 error。
func (c *Client) DiscoverAccounts(ctx context.Context, machine string) (*proto.AccountDiscoverResp, error) {
	path := "/api/accounts/discover"
	if machine != "" {
		path += "?machine=" + url.QueryEscape(machine)
	}
	resp, err := c.do(ctx, http.MethodPost, path, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, c.httpError("discover accounts", resp)
	}
	var out proto.AccountDiscoverResp
	if err := decodeWire(resp, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
