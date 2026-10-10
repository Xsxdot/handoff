// push_test.go —— push_devices 表的 CRUD 与边界。
//
// 职责：验证 upsert 覆盖、删除不存在返回 ErrNotFound、member 隔离。
// 边界：这三支是 store 方法入口（不在 REST 缝上）的**附加**锁（plan §6.4）——
// 端到端行为由 internal/agentd 的 pushapi_test 覆盖；member 隔离从单条 HTTP
// 请求构造不出（一个 agentd 只有一个 console_user），故在此补断言。
package store

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/proto"
)

// newPushStore 开一个临时库，供本文件的用例共用。
func newPushStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func pushFixture(member, device, token string) *proto.PushDevice {
	return &proto.PushDevice{
		Member: member, DeviceID: device, Platform: proto.PushPlatformIOS,
		APNSToken: token, UpdatedAt: time.Now(),
	}
}

// TestUpsertPushDeviceOverwrites 锁同键覆盖语义：换 token 后只有一行且为新值。
func TestUpsertPushDeviceOverwrites(t *testing.T) {
	st := newPushStore(t)

	if err := st.UpsertPushDevice(pushFixture("user:sy", "d1", "old-token")); err != nil {
		t.Fatalf("UpsertPushDevice(首次): %v", err)
	}
	first, err := st.ListPushDevices("user:sy")
	if err != nil {
		t.Fatalf("ListPushDevices: %v", err)
	}
	if len(first) != 1 {
		t.Fatalf("首次登记后设备数 = %d，期望 1", len(first))
	}

	next := pushFixture("user:sy", "d1", "new-token")
	if err := st.UpsertPushDevice(next); err != nil {
		t.Fatalf("UpsertPushDevice(覆盖): %v", err)
	}
	got, err := st.ListPushDevices("user:sy")
	if err != nil {
		t.Fatalf("ListPushDevices: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("覆盖后设备数 = %d，期望 1（按 (member,device_id) 覆盖而非追加）", len(got))
	}
	if got[0].APNSToken != "new-token" {
		t.Fatalf("覆盖后 token = %q，期望 new-token", got[0].APNSToken)
	}
	if got[0].UpdatedAt.Before(first[0].UpdatedAt) {
		t.Fatalf("覆盖后 UpdatedAt 应不早于首次：%v < %v", got[0].UpdatedAt, first[0].UpdatedAt)
	}
	if got[0].Member != "user:sy" || got[0].Platform != proto.PushPlatformIOS {
		t.Fatalf("登记行字段错: %+v", got[0])
	}
}

// TestDeletePushDeviceNotFound 锁删除不存在返回 ErrNotFound（REST 据此回 404）。
func TestDeletePushDeviceNotFound(t *testing.T) {
	st := newPushStore(t)
	if err := st.DeletePushDevice("user:sy", "ghost"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("删除不存在设备 err = %v，期望 ErrNotFound", err)
	}
}

// TestListPushDevicesScopedByMember 锁 member 隔离：不同成员的设备不串。
func TestListPushDevicesScopedByMember(t *testing.T) {
	st := newPushStore(t)
	if err := st.UpsertPushDevice(pushFixture("user:sy", "d1", "tok-a")); err != nil {
		t.Fatalf("UpsertPushDevice(a): %v", err)
	}
	if err := st.UpsertPushDevice(pushFixture("user:other", "d2", "tok-b")); err != nil {
		t.Fatalf("UpsertPushDevice(b): %v", err)
	}
	got, err := st.ListPushDevices("user:sy")
	if err != nil {
		t.Fatalf("ListPushDevices: %v", err)
	}
	if len(got) != 1 || got[0].DeviceID != "d1" || got[0].APNSToken != "tok-a" {
		t.Fatalf("member 隔离失效: %+v", got)
	}
	if err := st.DeletePushDevice("user:other", "d1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("删除他人的设备 err = %v，期望 ErrNotFound", err)
	}
}
