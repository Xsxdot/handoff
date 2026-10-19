// selfregister.go —— 机器自登记与本机 targets 一次性导入（B439 WU2 / S3 / S8）。
//
// 职责：
//   - 机器实例 id：<DataDir>/machine-id，首次需要时生成随机 hex（0600）
//   - 自登记：把本机（自名 + 连接方式 + 凭据）upsert 进共享设备表；撞名（异实例）
//     返回可行动错误，调用方据此不挂载账本子系统
//   - S8 导入：本地 targets: 里设备表中没有的条目一次性导入（同名不覆盖，
//     异连接 WARN 列差异），之后本地 targets: 被忽略
//
// 边界：
//   - 不打开账本、不构造 client、不做网络：账本由 cmd 组装点打开后传入
//   - 日志与 WARN 不打印凭据值（只列字段名，B409.6 脱敏纪律）
package agentd

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/ledger"
)

// machineIDFile 是本机机器实例 id 的文件名（落 <DataDir>）。
const machineIDFile = "machine-id"

// machineIDPath 返回本机实例 id 文件路径。
func machineIDPath(dataDir string) string { return filepath.Join(dataDir, machineIDFile) }

// LoadOrCreateMachineID 读取本机机器实例 id；不存在时生成并落盘（0600）。
//
// 实例 id 用来区分「同一台机器重启」（id 不变 → 自登记更新）与「另一台撞名」
// （id 不同 → 冲突拒挂载）。
func LoadOrCreateMachineID(dataDir string, log *slog.Logger) (string, error) {
	if log == nil {
		log = slog.Default()
	}
	p := machineIDPath(dataDir)
	b, err := os.ReadFile(p)
	if err == nil {
		id := strings.TrimSpace(string(b))
		if id == "" {
			return "", fmt.Errorf("机器实例 id 文件 %s 为空", p)
		}
		return id, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("读机器实例 id %s: %w", p, err)
	}
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("生成机器实例 id: %w", err)
	}
	id := hex.EncodeToString(raw)
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return "", fmt.Errorf("创建数据目录 %s: %w", dataDir, err)
	}
	if err := os.WriteFile(p, []byte(id+"\n"), 0o600); err != nil {
		return "", fmt.Errorf("写机器实例 id %s: %w", p, err)
	}
	log.Info("已生成机器实例 id", "path", p)
	return id, nil
}

// SelfTarget 依据配置构造本机的设备表连接块。
//
// 直连优先：配了 advertise: 即登记直连形态（advertise 地址 + token），即使同时配了
// relay: 段——relay: 是本机向 relay 的出站注册，不代表对端应改走 relay（B438：直连
// 可达的照旧直连）。只有没有 advertise: 时才登记 relay 五元组。两者都没有时返回错误
// ——调用方应已在挂载门（cfgHasLedgerIdentity）拦下。
//
// 注意：relay 形态登记的 Credential 是本机 relay: 段的注册凭据，relay 服务端的
// CONNECT 要求连接角色凭据，对端拿它拨号会 AUTH_FAILED；relay 形态该发布什么凭据
// 另行设计（见 roadmap），在那之前直连可达的机器务必配 advertise:。
func SelfTarget(cfg *config.Config) (config.Target, error) {
	if cfg == nil {
		return config.Target{}, errors.New("自登记：配置为空")
	}
	if cfg.Advertise != "" {
		return config.Target{Addr: cfg.Advertise, Token: cfg.Token}, nil
	}
	if cfg.Relay != nil {
		return config.Target{
			Relay: cfg.Relay.URL, Credential: cfg.Relay.Credential,
			Node: cfg.Relay.Node, Token: cfg.Token,
		}, nil
	}
	return config.Target{}, errors.New("自登记：直连形态缺 advertise:（在 config.yaml 设置对外可达地址）")
}

// SelfRegister 把本机登记进共享设备表。撞名（异实例）返回包装 ledger.ErrDeviceConflict
// 的错误，错误文本点名冲突的名字。
func SelfRegister(st *ledger.Store, cfg *config.Config, log *slog.Logger) error {
	if log == nil {
		log = slog.Default()
	}
	if st == nil {
		return errors.New("自登记：账本未打开")
	}
	if cfg.Name == "" {
		return errors.New("自登记：缺少自名（config.yaml 的 name:）")
	}
	target, err := SelfTarget(cfg)
	if err != nil {
		return err
	}
	id, err := LoadOrCreateMachineID(cfg.DataDir, log)
	if err != nil {
		return err
	}
	if err := st.RegisterDevice(cfg.Name, id, configToDeviceTarget(target), nil); err != nil {
		return fmt.Errorf("自登记机器 %s: %w", cfg.Name, err)
	}
	log.Info("机器自登记完成", "machine", cfg.Name, "relay", target.IsRelay())
	return nil
}

// ImportLocalTargets 把本地 targets: 中设备表还没有的条目一次性导入设备表（S8）。
//
// 语义：
//   - 同名已存在且连接方式相同 → 跳过
//   - 同名已存在但连接方式不同 → 不覆盖，WARN 列出差异字段（不打印值）
//   - 保留字/非法名条目（本机 等）→ 不导入，WARN 点名
//   - 其余 → 新增
//
// 之后池只读设备表，本地 targets: 被忽略；只要有本地条目就 WARN 一次提示删除。
func ImportLocalTargets(st *ledger.Store, cfg *config.Config, log *slog.Logger) error {
	if log == nil {
		log = slog.Default()
	}
	if st == nil {
		return errors.New("导入本地 targets：账本未打开")
	}
	names := make([]string, 0, len(cfg.Targets))
	for name := range cfg.Targets {
		names = append(names, name)
	}
	sort.Strings(names)
	imported, skipped, conflicted := 0, 0, 0
	for _, name := range names {
		if cfg.Name != "" && name == cfg.Name {
			skipped++ // 自名那一行由自登记负责，本地同名条目忽略
			continue
		}
		if verr := config.ValidateSelfName(name); verr != nil {
			log.Warn("本地 targets 条目名是保留字/非法名，不导入设备表", "target", name)
			continue
		}
		want := cfg.Targets[name]
		existing, err := st.DeviceTarget(name)
		switch {
		case err == nil:
			if diff := diffTargetFields(deviceTargetToConfig(existing), want); len(diff) > 0 {
				conflicted++
				log.Warn("本地 targets 条目与设备表同名但连接方式不同，不覆盖",
					"target", name, "diff_fields", strings.Join(diff, ","))
			} else {
				skipped++
			}
			continue
		case !errors.Is(err, ledger.ErrNotFound):
			return fmt.Errorf("导入本地 targets %s：读设备表: %w", name, err)
		}
		if err := st.PutDevice(name, configToDeviceTarget(want), nil); err != nil {
			if errors.Is(err, ledger.ErrDeviceExists) {
				// 并发：读时不在、写时已在，视为同名存在，不覆盖。
				skipped++
				continue
			}
			return fmt.Errorf("导入本地 targets %s: %w", name, err)
		}
		imported++
		log.Info("本地 targets 条目已导入设备表", "target", name, "relay", want.IsRelay())
	}
	if len(names) > 0 {
		log.Warn("本地 targets: 已被设备表取代，池不再读它；请从 config.yaml 删除这些条目",
			"entries", len(names), "imported", imported, "skipped", skipped, "conflicted", conflicted)
	}
	return nil
}

// diffTargetFields 返回两个连接方式不同的字段名（**只列字段名，不列值**，防凭据进日志）。
func diffTargetFields(a, b config.Target) []string {
	var out []string
	if a.Addr != b.Addr {
		out = append(out, "addr")
	}
	if a.Token != b.Token {
		out = append(out, "token")
	}
	if a.User != b.User {
		out = append(out, "user")
	}
	if a.Relay != b.Relay {
		out = append(out, "relay")
	}
	if a.Credential != b.Credential {
		out = append(out, "credential")
	}
	if a.Node != b.Node {
		out = append(out, "node")
	}
	return out
}
