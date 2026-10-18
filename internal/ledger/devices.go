// devices.go —— 全队共享设备表领域（B439 WU2）。
//
// 职责：
//   - 权威状态：全队机器行（名字唯一、连接方式、凭据、登记者实例 id、更新时间、
//     可追加 attrs）
//   - 写入口两个：自登记 upsert（RegisterDevice）与控制台/CLI 增删（PutDevice /
//     DeleteDevice）
//   - 读面两个：列表读（ListDevices，剥凭据，供控制台/CLI）；池专用读
//     （DeviceTarget / DeviceTargets，含凭据，供 target 客户端池选路）
//
// 边界：
//   - 不依赖 internal/config、internal/agentd：连接块用本包显式投影
//     DeviceTarget 表达，不与任何上层类型耦合（architecture：设备表领域独立）
//   - 凭据只在池专用读方法出现；ListDevices 恒剥 Token/Credential（F8）
//   - 加字段走 attrs JSONB，不改主键、不迁移既有行（F4）
package ledger

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// 设备表错误哨兵。调用方用 errors.Is 判类。
var (
	// ErrDeviceExists 表示控制台/CLI 新增时同名机器已存在（应答 409）。
	ErrDeviceExists = errors.New("设备表中同名机器已存在")
	// ErrDeviceNotFound 表示要删的机器不存在（应答 404）。
	ErrDeviceNotFound = errors.New("设备表中无此机器")
	// ErrDeviceConflict 表示自登记撞名：名字已被另一个机器实例占用。
	ErrDeviceConflict = errors.New("设备表机器名已被另一实例占用")
)

// DeviceTarget 是设备表连接块的显式 JSON 投影。
//
// 为什么用指针而不是纯 string：存储结构必须区分「字段缺失」与「零值」——
// relay 五元组里缺 node 与 node 为空串是两种不同的事实，序列化边界不能把它们
// 抹平成一种（plan §7.2 序列化边界断言）。omitempty 对指针只在 nil 时省略，
// 指向空串的指针照常落盘，因此两态可往返。
//
// 为什么不用 config.Target：config.Target 只有 yaml tag，json.Marshal 会产出
// 大写字段名（plan §2.2 的陷阱）；且账本领域不应依赖 config。
type DeviceTarget struct {
	Addr       *string `json:"addr,omitempty"`
	Token      *string `json:"token,omitempty"`
	User       *string `json:"user,omitempty"`
	Relay      *string `json:"relay,omitempty"`
	Credential *string `json:"credential,omitempty"`
	Node       *string `json:"node,omitempty"`
}

// Device 是一行设备记录。Target 是否含凭据取决于读它的方法
// （ListDevices 剥，DeviceTarget/DeviceTargets 留）。
type Device struct {
	Name         string
	Target       DeviceTarget
	InstanceID   string
	RegisteredAt time.Time
	UpdatedAt    time.Time
	Attrs        json.RawMessage
}

// RegisterDevice 自登记 upsert 一台机器。
//
// 语义（F4）：
//   - 行不存在 → 插入（registered_at = updated_at = now）
//   - 行已存在且 instance_id 相同 → 更新连接块与 updated_at（换 IP/relay 不换身份）
//   - 行已存在但 instance_id 为空（控制台先加的行）→ 接管：写入 instance_id 与连接块
//   - 行已存在且 instance_id 非空且不同 → ErrDeviceConflict，错误点名名字
//
// 参数：
//   - name: 全队机器名（身份键）；空串拒绝
//   - instanceID: 本机数据目录里的机器实例 id；空串拒绝（自登记必须带实例身份）
//   - target: 连接块（含凭据）
//   - attrs: 可追加属性 JSON；nil 视为 {}
func (s *Store) RegisterDevice(name, instanceID string, target DeviceTarget, attrs []byte) error {
	if name == "" {
		return fmt.Errorf("设备自登记: 机器名不能为空")
	}
	if instanceID == "" {
		return fmt.Errorf("设备自登记 %s: 机器实例 id 不能为空", name)
	}
	body, err := encodeDeviceTarget(target)
	if err != nil {
		return fmt.Errorf("设备自登记 %s: %w", name, err)
	}
	attrJSON, err := normalizeAttrs(attrs)
	if err != nil {
		return fmt.Errorf("设备自登记 %s: %w", name, err)
	}
	return s.mutate(func(tx *sql.Tx, _ *eventSink) error {
		var existing string
		scanErr := tx.QueryRow(s.q(`SELECT instance_id FROM devices WHERE name = ?`), name).Scan(&existing)
		switch {
		case errors.Is(scanErr, sql.ErrNoRows):
			now := s.tval(s.timeNow())
			_, err := tx.Exec(s.q(`INSERT INTO devices (name, target, instance_id, registered_at, updated_at, attrs)
				VALUES (?, ?, ?, ?, ?, ?)`), name, body, instanceID, now, now, attrJSON)
			if err != nil {
				return fmt.Errorf("设备自登记插入 %s: %w", name, err)
			}
			log().Info("设备自登记完成", "machine", name, "instance_id", instanceID)
			return nil
		case scanErr != nil:
			return fmt.Errorf("设备自登记读现状 %s: %w", name, scanErr)
		}
		if existing != "" && existing != instanceID {
			err := fmt.Errorf("设备自登记被拒：机器名 %s 已被另一实例占用（本机实例 %s，占用者实例 %s）；"+
				"请改 config.yaml 的 name: 或清理设备表该行: %w", name, instanceID, existing, ErrDeviceConflict)
			log().Warn("设备自登记撞名被拒", "machine", name, "instance_id", instanceID, "occupant", existing)
			return err
		}
		// 同实例更新，或接管控制台先加的空实例行。
		_, err := tx.Exec(s.q(`UPDATE devices SET target = ?, instance_id = ?, updated_at = ?, attrs = ? WHERE name = ?`),
			body, instanceID, s.tval(s.timeNow()), attrJSON, name)
		if err != nil {
			return fmt.Errorf("设备自登记更新 %s: %w", name, err)
		}
		log().Info("设备自登记更新", "machine", name, "instance_id", instanceID, "adopted", existing == "")
		return nil
	})
}

// PutDevice 控制台/CLI 新增一台机器；同名已存在返回 ErrDeviceExists（应答 409）。
//
// 新增行不带机器实例 id（控制台不知道对端实例）：instance_id 留空，之后该机
// 自登记时按 RegisterDevice 的接管分支写入真实实例 id。
func (s *Store) PutDevice(name string, target DeviceTarget, attrs []byte) error {
	if name == "" {
		return fmt.Errorf("新增设备: 机器名不能为空")
	}
	body, err := encodeDeviceTarget(target)
	if err != nil {
		return fmt.Errorf("新增设备 %s: %w", name, err)
	}
	attrJSON, err := normalizeAttrs(attrs)
	if err != nil {
		return fmt.Errorf("新增设备 %s: %w", name, err)
	}
	return s.mutate(func(tx *sql.Tx, _ *eventSink) error {
		var existing string
		scanErr := tx.QueryRow(s.q(`SELECT name FROM devices WHERE name = ?`), name).Scan(&existing)
		switch {
		case scanErr == nil:
			return fmt.Errorf("%w: %s", ErrDeviceExists, name)
		case !errors.Is(scanErr, sql.ErrNoRows):
			return fmt.Errorf("新增设备读现状 %s: %w", name, scanErr)
		}
		now := s.tval(s.timeNow())
		_, err := tx.Exec(s.q(`INSERT INTO devices (name, target, instance_id, registered_at, updated_at, attrs)
			VALUES (?, ?, '', ?, ?, ?)`), name, body, now, now, attrJSON)
		if err != nil {
			return fmt.Errorf("新增设备插入 %s: %w", name, err)
		}
		log().Info("设备已新增", "machine", name)
		return nil
	})
}

// DeleteDevice 删除一台机器；不存在返回 ErrDeviceNotFound（应答 404）。
func (s *Store) DeleteDevice(name string) error {
	if name == "" {
		return fmt.Errorf("%w: 名字为空", ErrDeviceNotFound)
	}
	return s.mutate(func(tx *sql.Tx, _ *eventSink) error {
		result, err := tx.Exec(s.q(`DELETE FROM devices WHERE name = ?`), name)
		if err != nil {
			return fmt.Errorf("删除设备 %s: %w", name, err)
		}
		if count, _ := result.RowsAffected(); count == 0 {
			return fmt.Errorf("%w: %s", ErrDeviceNotFound, name)
		}
		log().Info("设备已删除", "machine", name)
		return nil
	})
}

// ListDevices 列出全部设备行，**剥掉凭据**（F8）。
//
// 控制台机器页与 handoff machines 读这条；池选路读 DeviceTarget/DeviceTargets。
func (s *Store) ListDevices() ([]Device, error) {
	rows, err := s.db.Query(s.q(`SELECT name, target, instance_id, registered_at, updated_at, attrs FROM devices ORDER BY name ASC`))
	if err != nil {
		return nil, fmt.Errorf("设备列表读: %w", err)
	}
	defer rows.Close()
	var out []Device
	for rows.Next() {
		d, err := scanDevice(rows)
		if err != nil {
			return nil, err
		}
		d.Target.Token = nil
		d.Target.Credential = nil
		out = append(out, d)
	}
	return out, rows.Err()
}

// DeviceTargets 返回全部设备行，**含凭据**（池专用批量读，F8）。
func (s *Store) DeviceTargets() ([]Device, error) {
	rows, err := s.db.Query(s.q(`SELECT name, target, instance_id, registered_at, updated_at, attrs FROM devices ORDER BY name ASC`))
	if err != nil {
		return nil, fmt.Errorf("设备表批量读: %w", err)
	}
	defer rows.Close()
	var out []Device
	for rows.Next() {
		d, err := scanDevice(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// DeviceTarget 读一台机器的连接块（含凭据，池专用读，F8）；不存在返回 ErrNotFound。
func (s *Store) DeviceTarget(name string) (DeviceTarget, error) {
	row := s.db.QueryRow(s.q(`SELECT name, target, instance_id, registered_at, updated_at, attrs FROM devices WHERE name = ?`), name)
	d, err := scanDevice(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return DeviceTarget{}, fmt.Errorf("设备 %s: %w", name, ErrNotFound)
		}
		return DeviceTarget{}, fmt.Errorf("设备读 %s: %w", name, err)
	}
	return d.Target, nil
}

// DeviceAttrs 读一台机器的可追加属性（预留读面，本卡零消费）；不存在返回 ErrNotFound。
func (s *Store) DeviceAttrs(name string) ([]byte, error) {
	var attrs string
	err := s.db.QueryRow(s.q(`SELECT attrs FROM devices WHERE name = ?`), name).Scan(&attrs)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("设备 %s: %w", name, ErrNotFound)
		}
		return nil, fmt.Errorf("设备 attrs 读 %s: %w", name, err)
	}
	return []byte(attrs), nil
}

// deviceScanner 抽象 *sql.Row 与 *sql.Rows 的 Scan。
type deviceScanner interface{ Scan(dest ...any) error }

// scanDevice 从一行读 Device。attrs 读出后做一次 JSON 合法性归一（默认 '{}'）。
func scanDevice(sc deviceScanner) (Device, error) {
	var (
		d         Device
		targetRaw string
		attrsRaw  string
		regRaw    any
		updRaw    any
	)
	if err := sc.Scan(&d.Name, &targetRaw, &d.InstanceID, &regRaw, &updRaw, &attrsRaw); err != nil {
		return Device{}, err
	}
	if err := json.Unmarshal([]byte(targetRaw), &d.Target); err != nil {
		return Device{}, fmt.Errorf("设备 %s 连接块解析: %w", d.Name, err)
	}
	d.RegisteredAt = toTime(regRaw)
	d.UpdatedAt = toTime(updRaw)
	if attrsRaw == "" {
		attrsRaw = "{}"
	}
	d.Attrs = json.RawMessage(attrsRaw)
	return d, nil
}

// encodeDeviceTarget 把连接块序列化成落库 JSON。
func encodeDeviceTarget(t DeviceTarget) (string, error) {
	b, err := json.Marshal(t)
	if err != nil {
		return "", fmt.Errorf("连接块序列化: %w", err)
	}
	return string(b), nil
}

// normalizeAttrs 归一 attrs：nil → '{}'；非对象 JSON 拒绝（B437/B438 按机器追加字段的前提）。
func normalizeAttrs(attrs []byte) (string, error) {
	if len(attrs) == 0 {
		return "{}", nil
	}
	var probe map[string]any
	if err := json.Unmarshal(attrs, &probe); err != nil {
		return "", fmt.Errorf("attrs 必须是 JSON 对象: %w", err)
	}
	return string(attrs), nil
}
