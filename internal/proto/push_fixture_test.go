// B432 推送 wire 形状的 Go 侧金样本：键集与 roundtrip 恒等。
//
// 锁三类编码（序列化边界缺陷族）：
//   - PushDeviceRegisterReq 的键集恰为 {device_id,platform,apns_token,auth_session_id}，
//     且 auth_session_id 空值省键（omitempty 语义）——改名/删键/去 omitempty 当场变红；
//   - PushNotification roundtrip 恒等，用 map 区分「键缺失」与「零值」；
//   - 空 CardID / DeepLink marshal 后键必须缺失（深链为空=无深链，语义不同）。
//
// 壳侧不直接解析这些类型（壳经 Go 核走 bind 字符串面）；APNs 侧由
// internal/agentd/apns.go 的 `handoff` 键消费。本测试是附加锁，不顶替
// T2/T6 的缝级断言（plan §6.4）。
package proto

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

// pushReqFixture 返回一份所有键都非空的登记请求（键集断言的正例）。
func pushReqFixture() PushDeviceRegisterReq {
	return PushDeviceRegisterReq{
		DeviceID:      "iphone-15",
		Platform:      PushPlatformIOS,
		APNSToken:     "aabbccdd",
		AuthSessionID: "sess-1",
	}
}

func TestPushDeviceRegisterReqGoldenKeys(t *testing.T) {
	raw, err := json.Marshal(pushReqFixture())
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]json.RawMessage
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	want := []string{"device_id", "platform", "apns_token", "auth_session_id"}
	if len(got) != len(want) {
		t.Fatalf("键集应为 %v，实为 %d 键: %s", want, len(got), raw)
	}
	for _, k := range want {
		if _, ok := got[k]; !ok {
			t.Fatalf("缺键 %q: %s", k, raw)
		}
	}

	// auth_session_id 空 → 省键（壳未持会话时的常态）。
	empty := pushReqFixture()
	empty.AuthSessionID = ""
	raw2, err := json.Marshal(empty)
	if err != nil {
		t.Fatal(err)
	}
	var got2 map[string]json.RawMessage
	if err := json.Unmarshal(raw2, &got2); err != nil {
		t.Fatal(err)
	}
	if _, ok := got2["auth_session_id"]; ok {
		t.Fatalf("auth_session_id 为空时必须省键: %s", raw2)
	}
	if len(got2) != 3 {
		t.Fatalf("空 auth_session_id 应只剩 3 键，实为 %d: %s", len(got2), raw2)
	}
}

func TestPushNotificationRoundTrip(t *testing.T) {
	exp := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	dev := PushDevice{
		Member: "user:sy", DeviceID: "iphone-15", Platform: PushPlatformIOS,
		APNSToken: "aabbccdd", AuthSessionID: "sess-1", UpdatedAt: exp,
	}
	raw, err := json.Marshal(dev)
	if err != nil {
		t.Fatal(err)
	}
	var back PushDevice
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(dev, back) {
		t.Fatalf("PushDevice roundtrip 不恒等:\nwant %#v\ngot  %#v", dev, back)
	}

	n := PushNotification{
		EventType: "decision", Member: "user:sy", Title: "裁决",
		CardID: "B432", RefID: "42", DeepLink: "/cards?card=B432",
	}
	raw2, err := json.Marshal(n)
	if err != nil {
		t.Fatal(err)
	}
	var back2 PushNotification
	if err := json.Unmarshal(raw2, &back2); err != nil {
		t.Fatal(err)
	}
	if n != back2 {
		t.Fatalf("PushNotification roundtrip 不恒等:\nwant %#v\ngot  %#v", n, back2)
	}

	// 空 CardID / DeepLink：marshal 后键必须缺失（缺深链=落工作台，不是空串深链）。
	slack := PushNotification{EventType: "mention", Member: "user:sy", Title: "@你", RefID: "7"}
	raw3, err := json.Marshal(slack)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]json.RawMessage
	if err := json.Unmarshal(raw3, &got); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"event_type", "member", "title", "ref_id"} {
		if _, ok := got[k]; !ok {
			t.Fatalf("必出键 %q 缺失: %s", k, raw3)
		}
	}
	for _, k := range []string{"card_id", "deep_link"} {
		if _, ok := got[k]; ok {
			t.Fatalf("空值键 %q 必须省略: %s", k, raw3)
		}
	}
	var back3 PushNotification
	if err := json.Unmarshal(raw3, &back3); err != nil {
		t.Fatal(err)
	}
	if back3.CardID != "" || back3.DeepLink != "" {
		t.Fatalf("省键解回应为空串: %#v", back3)
	}
}
