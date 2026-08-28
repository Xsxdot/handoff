// squads.go —— 编制域登记面/队列/协调者拉起的拨号方法（B156.3 K3）。
//
// 职责：与 client.go 同一底座（do/httpError）；线格式在 internal/proto，
// 与 web/src/api/scheduling.ts 镜像一字不差。无业务判断：登记规则在服务端
// scheduling.Service，本包只保证传输与错误正文透明（httpStatusError 原样携带
// 服务端 {"error"} 正文，CLI 直接呈现给操作者）。
package client

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"github.com/Xsxdot/handoff/internal/proto"
)

// decodeWire 整段读入再解码（Decoder.Decode 只吃第一个 JSON 值、尾随内容被
// 静默丢弃，截断或串包会被当成有效响应——理由同 CardStep 的注释）。
func decodeWire(resp *http.Response, into any) error {
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("读取响应: %w", err)
	}
	if err := json.Unmarshal(body, into); err != nil {
		return fmt.Errorf("解析响应 %s: %w", string(body), err)
	}
	return nil
}

// Squads 拉取载体+小队登记面（GET /api/squads，各行带 registry 版本）。
func (c *Client) Squads(ctx context.Context) (*proto.SquadsResp, error) {
	resp, err := c.do(ctx, http.MethodGet, "/api/squads", nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, c.httpError("squads", resp)
	}
	var out proto.SquadsResp
	if err := decodeWire(resp, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// putRegistration 是 PUT 登记的公共尾段：路径带 ?expect=，成功解码 SquadPutResp。
func (c *Client) putRegistration(ctx context.Context, op, path string, in any) (*proto.SquadPutResp, error) {
	resp, err := c.do(ctx, http.MethodPut, path, in)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, c.httpError(op, resp)
	}
	var out proto.SquadPutResp
	if err := decodeWire(resp, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// PutSquad 以 CAS 写小队（成员必须指向已登记载体；角色 executor|coordinator；
// 空成员合法——岔口四：先立队再补成员）。
// 注（B156.3 K3 收尾自审）：Client.PutCarrier 不预造——本卡 CLI 面没有 carrier
// 命令（契约 §11 原文如此，引导缝见 plan D5.1），零调用方的拨号方法按配对判据
// 不落地；载体 PUT 的 wire 契约由网关 TC2 与 TS putCarrier 两侧锁住。
func (c *Client) PutSquad(ctx context.Context, name string, expect int, in proto.SquadInput) (*proto.SquadPutResp, error) {
	in.Name = name
	return c.putRegistration(ctx, "put squad",
		"/api/squads/squads/"+url.PathEscape(name)+"?expect="+fmt.Sprint(expect), in)
}

// CoordinatorLaunch 一键拉起绑定协调者（POST coordinator/launch，端点本体归
// K4；未登记协调者小队时服务端 400 报文含 handoff squad create 指路——岔口四）。
func (c *Client) CoordinatorLaunch(ctx context.Context, cardID string) (*proto.CoordinatorLaunchResp, error) {
	resp, err := c.do(ctx, http.MethodPost,
		"/api/cards/"+url.PathEscape(cardID)+"/coordinator/launch", struct{}{})
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, c.httpError("coordinator launch", resp)
	}
	var out proto.CoordinatorLaunchResp
	if err := decodeWire(resp, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
