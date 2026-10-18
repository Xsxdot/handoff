// B439 WU2 设备表领域：行往返、实例冲突判定、凭据剥离、增删语义。
//
// 断言对应 plan §7.2 与冻结 F4/F8。SQLite 全量跑；PG 真实往返由
// TestB439DevicesPG 在 LEDGER_TEST_PG_DSN 门控下跑（同一断言函数双后端）。
package ledger

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
)

func sp(s string) *string { return &s }

// deviceTarget 是测试用的连接块构造：非空字段才落指针，与 agentd 侧从
// config.Target 的投影一致。
func deviceTarget(addr, token, user, relay, cred, node string) DeviceTarget {
	dt := DeviceTarget{}
	if addr != "" {
		dt.Addr = sp(addr)
	}
	if token != "" {
		dt.Token = sp(token)
	}
	if user != "" {
		dt.User = sp(user)
	}
	if relay != "" {
		dt.Relay = sp(relay)
	}
	if cred != "" {
		dt.Credential = sp(cred)
	}
	if node != "" {
		dt.Node = sp(node)
	}
	return dt
}

// assertDeviceRoundTrip 是双后端共用的往返断言：注册 → 读回含凭据 → 列表剥凭据。
func assertDeviceRoundTrip(t *testing.T, s *Store) {
	t.Helper()
	direct := deviceTarget("100.81.114.119:7777", "tok-direct", "deploy", "", "", "")
	if err := s.RegisterDevice("linux-01", "inst-linux", direct, []byte(`{"k":"v"}`)); err != nil {
		t.Fatalf("RegisterDevice direct: %v", err)
	}
	relay := deviceTarget("", "tok-relay", "", "wss://relay.example", "cred-relay", "macbook-pro")
	if err := s.RegisterDevice("mac-02", "inst-mac", relay, nil); err != nil {
		t.Fatalf("RegisterDevice relay: %v", err)
	}

	got, err := s.DeviceTarget("linux-01")
	if err != nil {
		t.Fatalf("DeviceTarget direct: %v", err)
	}
	if got.Addr == nil || *got.Addr != "100.81.114.119:7777" || got.Token == nil || *got.Token != "tok-direct" ||
		got.User == nil || *got.User != "deploy" {
		t.Fatalf("直连连接块往返不一致: %+v", got)
	}
	gotRelay, err := s.DeviceTarget("mac-02")
	if err != nil {
		t.Fatalf("DeviceTarget relay: %v", err)
	}
	if gotRelay.Relay == nil || *gotRelay.Relay != "wss://relay.example" ||
		gotRelay.Credential == nil || *gotRelay.Credential != "cred-relay" ||
		gotRelay.Node == nil || *gotRelay.Node != "macbook-pro" || gotRelay.Token == nil || *gotRelay.Token != "tok-relay" {
		t.Fatalf("relay 五元组往返不一致: %+v", gotRelay)
	}

	attrs, err := s.DeviceAttrs("linux-01")
	if err != nil {
		t.Fatalf("DeviceAttrs: %v", err)
	}
	if string(attrs) != `{"k":"v"}` {
		t.Fatalf("attrs 往返不一致: %s", attrs)
	}

	// F8：列表读面必须剥凭据。
	list, err := s.ListDevices()
	if err != nil {
		t.Fatalf("ListDevices: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("列表应有 2 台: %+v", list)
	}
	for _, d := range list {
		if d.Target.Token != nil || d.Target.Credential != nil {
			t.Fatalf("ListDevices 泄漏凭据: %+v", d)
		}
	}
}

// TestB439DeviceRoundTripSQLite 设备表行往返（SQLite 真实写读）。
func TestB439DeviceRoundTripSQLite(t *testing.T) {
	s := newTestStore(t)
	assertDeviceRoundTrip(t, s)
}

// TestB439DevicesPG 同一往返断言在真 PG 上跑（需 LEDGER_TEST_PG_DSN）。
func TestB439DevicesPG(t *testing.T) {
	dsn := os.Getenv("LEDGER_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("未设 LEDGER_TEST_PG_DSN，跳过设备表 PG 往返")
	}
	s, err := OpenShared(dsn)
	if err != nil {
		t.Fatalf("open pg: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	// 清掉本次两个名字，避免历史残留影响断言。
	for _, n := range []string{"linux-01", "mac-02"} {
		_ = s.DeleteDevice(n)
	}
	assertDeviceRoundTrip(t, s)
}

// TestB439DevicePresenceDistinct 区分字段缺失与零值：缺 node 与空 node
// 往返后仍是两种形态（relay 五元组的序列化边界）。
func TestB439DevicePresenceDistinct(t *testing.T) {
	s := newTestStore(t)
	// missing：整个 node 键缺席（nil 指针）。
	if err := s.RegisterDevice("missing-node", "i1", DeviceTarget{Relay: sp("wss://r"), Token: sp("t")}, nil); err != nil {
		t.Fatalf("RegisterDevice missing: %v", err)
	}
	// empty：node 键存在但为空串（非 nil 指针）。
	if err := s.RegisterDevice("empty-node", "i2", DeviceTarget{Relay: sp("wss://r"), Token: sp("t"), Node: sp("")}, nil); err != nil {
		t.Fatalf("RegisterDevice empty: %v", err)
	}
	missing, err := s.DeviceTarget("missing-node")
	if err != nil {
		t.Fatal(err)
	}
	empty, err := s.DeviceTarget("empty-node")
	if err != nil {
		t.Fatal(err)
	}
	if missing.Node != nil {
		t.Fatalf("缺 node 读回应为 nil 指针: %+v", missing.Node)
	}
	if empty.Node == nil || *empty.Node != "" {
		t.Fatalf("空 node 读回应为指向空串的指针: %+v", empty.Node)
	}
}

// TestB439DeviceRegisterConflict 同实例重登更新连接、异实例撞名拒绝点名。
func TestB439DeviceRegisterConflict(t *testing.T) {
	s := newTestStore(t)
	if err := s.RegisterDevice("linux-01", "inst-a", deviceTarget("10.0.0.1:7777", "t1", "", "", "", ""), nil); err != nil {
		t.Fatalf("首次登记: %v", err)
	}
	// 同实例换连接：更新成功，身份不变。
	if err := s.RegisterDevice("linux-01", "inst-a", deviceTarget("10.0.0.2:7777", "t2", "", "", "", ""), nil); err != nil {
		t.Fatalf("同实例重登应更新: %v", err)
	}
	got, err := s.DeviceTarget("linux-01")
	if err != nil {
		t.Fatal(err)
	}
	if got.Addr == nil || *got.Addr != "10.0.0.2:7777" {
		t.Fatalf("同实例重登未更新连接: %+v", got)
	}
	// 异实例撞名：拒绝，错误点名名字。
	err = s.RegisterDevice("linux-01", "inst-b", deviceTarget("10.0.0.3:7777", "t3", "", "", "", ""), nil)
	if err == nil {
		t.Fatal("异实例撞名必须被拒")
	}
	if !errors.Is(err, ErrDeviceConflict) {
		t.Fatalf("撞名错误应包装 ErrDeviceConflict: %v", err)
	}
	if !strings.Contains(err.Error(), "linux-01") {
		t.Fatalf("撞名错误应点名名字: %v", err)
	}
	// 被拒后连接未被改写。
	after, _ := s.DeviceTarget("linux-01")
	if after.Addr == nil || *after.Addr != "10.0.0.2:7777" {
		t.Fatalf("撞名被拒后连接被改写: %+v", after)
	}
}

// TestB439DevicePutDelete 控制台增删语义：重名 409 语义、不存在 404 语义。
func TestB439DevicePutDelete(t *testing.T) {
	s := newTestStore(t)
	if err := s.PutDevice("mac-02", deviceTarget("", "tok", "", "wss://r", "cred", "mac-02"), nil); err != nil {
		t.Fatalf("PutDevice 新增: %v", err)
	}
	if err := s.PutDevice("mac-02", deviceTarget("1.2.3.4:7777", "t", "", "", "", ""), nil); !errors.Is(err, ErrDeviceExists) {
		t.Fatalf("重名新增应 ErrDeviceExists: %v", err)
	}
	if err := s.DeleteDevice("nope"); !errors.Is(err, ErrDeviceNotFound) {
		t.Fatalf("删除不存在应 ErrDeviceNotFound: %v", err)
	}
	if err := s.DeleteDevice("mac-02"); err != nil {
		t.Fatalf("删除存在: %v", err)
	}
	if _, err := s.DeviceTarget("mac-02"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("删除后读取应 ErrNotFound: %v", err)
	}
}

// TestB439DeviceConsoleRowAdopted 控制台先加的行（无实例 id）被自登记接管：
// 否则「控制台加机 + 该机自登记」永远撞名。
func TestB439DeviceConsoleRowAdopted(t *testing.T) {
	s := newTestStore(t)
	if err := s.PutDevice("linux-01", deviceTarget("10.0.0.1:7777", "tok", "", "", "", ""), nil); err != nil {
		t.Fatalf("PutDevice: %v", err)
	}
	if err := s.RegisterDevice("linux-01", "inst-a", deviceTarget("10.0.0.9:7777", "tok2", "", "", "", ""), nil); err != nil {
		t.Fatalf("自登记应接管控制台行: %v", err)
	}
	got, err := s.DeviceTarget("linux-01")
	if err != nil {
		t.Fatal(err)
	}
	if got.Addr == nil || *got.Addr != "10.0.0.9:7777" {
		t.Fatalf("接管后连接应更新: %+v", got)
	}
}

// TestB439DeviceAttrsDefault 未写 attrs 的行读回空 JSON 对象（B437/B438 追加字段的落点）。
func TestB439DeviceAttrsDefault(t *testing.T) {
	s := newTestStore(t)
	if err := s.RegisterDevice("linux-01", "i", deviceTarget("1.2.3.4:7777", "t", "", "", "", ""), nil); err != nil {
		t.Fatal(err)
	}
	attrs, err := s.DeviceAttrs("linux-01")
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(attrs, &m); err != nil {
		t.Fatalf("默认 attrs 应是合法 JSON 对象: %v (%s)", err, attrs)
	}
}

// TestB439DevicesDDLParity 双后端都建了 devices 表（对等检已由 DDLParity 覆盖，
// 这里显式钉死表名，防它被误删）。
func TestB439DevicesDDLParity(t *testing.T) {
	for _, pg := range []bool{true, false} {
		found := false
		for _, stmt := range ddlStatements(pg) {
			if createTableName.FindStringSubmatch(stmt) != nil && strings.Contains(stmt, "devices") {
				found = true
			}
		}
		if !found {
			t.Fatalf("pg=%v 方言缺 devices 表 DDL", pg)
		}
	}
}
