// 本文件是 push_devices 表（B432 APNs MVP 设备登记）的持久化实现。
//
// 职责：
//   - UpsertPushDevice 按 (member, device_id) 幂等登记/覆盖
//   - DeletePushDevice 按 (member, device_id) 删除，不存在返回 ErrNotFound
//   - ListPushDevices 列出某成员全部设备，按 updated_at 倒序
//
// 边界：
//   - **叶子层**：方法错误 return 前不打日志（store.go 文件头纪律），由调用方
//     （agentd handler / fanout）带上下文记录
//   - **不判平台合法性**：platform 白名单（仅 ios）属接口层参数校验
//   - 不解释 apns_token 的格式与真伪：登记只表示「壳上报过」，不代表可达；
//     真伪与投递结果由 fanout/APNs 侧反馈（410 → 删设备）
//   - token 明文只在本表与投递路径出现，禁止进日志
package store

import (
	"context"
	"fmt"
	"time"

	"github.com/Xsxdot/handoff/internal/proto"
)

// pushDeviceColumns 是 push_devices 的读列清单，与写入处保持同一顺序。
const pushDeviceColumns = "member, device_id, platform, apns_token, auth_session_id, updated_at"

// scanPushDeviceRow 把一行扫进 proto.PushDevice；时间列按 fmtTime 的 RFC3339Nano
// 文本解析（解析失败回落零值，与 parseTime 既有语义一致）。
func scanPushDeviceRow(sc rowScanner) (proto.PushDevice, error) {
	var d proto.PushDevice
	var updatedAt string
	if err := sc.Scan(&d.Member, &d.DeviceID, &d.Platform, &d.APNSToken, &d.AuthSessionID, &updatedAt); err != nil {
		return proto.PushDevice{}, err
	}
	d.UpdatedAt = parseTime(updatedAt)
	return d, nil
}

// UpsertPushDevice 按 (member, device_id) 幂等登记/覆盖。
//
// 参数：d.Member 由服务端注入（决策 D3）；d.UpdatedAt 为零值时取 now。
// 返回：写库故障（带 member/device 上下文）。
//
// 注意：覆盖语义是「换 token / 换会话后仍只有一行」——fanout 按行投递，
// 追加会让失效设备永远留在表里轮不到 410 清理。
func (s *Store) UpsertPushDevice(d *proto.PushDevice) error {
	if d.UpdatedAt.IsZero() {
		d.UpdatedAt = time.Now()
	}
	if _, err := s.db.ExecContext(context.Background(), `
INSERT INTO push_devices (member, device_id, platform, apns_token, auth_session_id, updated_at)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT(member, device_id) DO UPDATE SET
  platform = excluded.platform, apns_token = excluded.apns_token,
  auth_session_id = excluded.auth_session_id, updated_at = excluded.updated_at`,
		d.Member, d.DeviceID, d.Platform, d.APNSToken, d.AuthSessionID, fmtTime(d.UpdatedAt)); err != nil {
		return fmt.Errorf("登记推送设备 %s/%s: %w", d.Member, d.DeviceID, err)
	}
	return nil
}

// DeletePushDevice 删除一台已登记设备；不存在时返回 ErrNotFound。
//
// 返回：写库故障、影响行数读取故障，或 ErrNotFound（调用方据此回 404 /
// fanout 在 410 后忽略第二次删除）。
func (s *Store) DeletePushDevice(member, deviceID string) error {
	res, err := s.db.ExecContext(context.Background(),
		"DELETE FROM push_devices WHERE member = ? AND device_id = ?", member, deviceID)
	if err != nil {
		return fmt.Errorf("删除推送设备 %s/%s: %w", member, deviceID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("读取删除推送设备影响行数: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("推送设备 %s/%s: %w", member, deviceID, ErrNotFound)
	}
	return nil
}

// ListPushDevices 列出某成员全部设备，按 updated_at 倒序（最近登记的先投）。
//
// 返回：行集（无设备时为 nil，非错误）、查询故障。
// 注意：返回整段 apns_token——调用方（fanout）只把它交给 PushSender，
// 不得写进日志或响应。
func (s *Store) ListPushDevices(member string) ([]proto.PushDevice, error) {
	rows, err := s.db.QueryContext(context.Background(),
		"SELECT "+pushDeviceColumns+" FROM push_devices WHERE member = ? ORDER BY updated_at DESC", member)
	if err != nil {
		return nil, fmt.Errorf("查询成员 %s 的推送设备: %w", member, err)
	}
	defer rows.Close()
	var out []proto.PushDevice
	for rows.Next() {
		d, err := scanPushDeviceRow(rows)
		if err != nil {
			return nil, fmt.Errorf("读取推送设备行: %w", err)
		}
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历推送设备: %w", err)
	}
	return out, nil
}
