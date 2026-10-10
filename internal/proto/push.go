// B432 iOS APNs MVP 的推送 wire 类型（唯一定义处）。
//
// 边界：设备登记（spec §契约面 1）走 HTTP JSON，由 internal/client 编、
// internal/agentd 解；内部 Fanout 载荷（spec §契约面 2）不对外，由
// internal/agentd 编、APNs `handoff` 键承载、iOS 壳从 userInfo 取深链。
// 壳不直接解析本文件的 Go 类型（壳经 Go 核与 bind 字符串面通信）。
// 设备登记的 member 由服务端注入，不由请求体携带（plan 决策 D3）。
package proto

import "time"

// PushPlatformIOS 是本 MVP 唯一合法平台；platform 白名单强制为它。
const PushPlatformIOS = "ios"

// PushDevice 是一台已登记推送的设备（spec DeviceRegistration 实体）。
// 存储与扇出查表用；member 在服务端注入后随实体落库。
type PushDevice struct {
	Member        string    `json:"member"`
	DeviceID      string    `json:"device_id"`
	Platform      string    `json:"platform"`
	APNSToken     string    `json:"apns_token"`
	AuthSessionID string    `json:"auth_session_id,omitempty"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// PushDeviceRegisterReq 是 POST /api/push/devices 的请求体（壳/核编、agentd 解）。
// member 不在请求体——服务端按控制台身份注入（决策 D3）。
type PushDeviceRegisterReq struct {
	DeviceID      string `json:"device_id"`
	Platform      string `json:"platform"`
	APNSToken     string `json:"apns_token"`
	AuthSessionID string `json:"auth_session_id,omitempty"`
}

// PushDeviceDeleteReq 是 DELETE /api/push/devices 的请求体（壳/核编、agentd 解）。
type PushDeviceDeleteReq struct {
	DeviceID string `json:"device_id"`
}

// PushNotification 是内部 Fanout 的通知载荷（agentd 编、APNs/壳消费，不对外 REST）。
// DeepLink 空即「无深链」：壳落工作台「需要你处理」入口（plan 决策 D4/D5）。
type PushNotification struct {
	EventType string `json:"event_type"`
	Member    string `json:"member"`
	Title     string `json:"title"`
	CardID    string `json:"card_id,omitempty"`
	RefID     string `json:"ref_id"`
	DeepLink  string `json:"deep_link,omitempty"`
}
