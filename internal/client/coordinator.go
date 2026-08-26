// coordinator.go —— 协调者拉起拨号方法（B156.3 K4）。
//
// 与 K3 的 CoordinatorLaunch 同底座（do/httpError/decodeWire）；差别只在带 source：
// K3 版 POST {}（服务端缺省 manual，供看板/控制台按钮），本版显式带 source，供 CLI
// 开卡即绑（card add --coordinate → source=card_create）。两条拨号并存的原因见 plan
// §D5.4 / §6 待拍板 ④。
package client

import (
	"context"
	"net/http"
	"net/url"

	"github.com/Xsxdot/handoff/internal/proto"
)

// CoordinatorLaunchAs 以指定 source 拉起并绑定协调者（POST /api/cards/{id}/
// coordinator/launch）。source 只进审计：manual=看板按钮、card_create=开卡即绑。
func (c *Client) CoordinatorLaunchAs(ctx context.Context, cardID, source string) (*proto.CoordinatorLaunchResp, error) {
	resp, err := c.do(ctx, http.MethodPost,
		"/api/cards/"+url.PathEscape(cardID)+"/coordinator/launch",
		map[string]string{"source": source})
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
