package proto

import (
	"fmt"
	"strings"
	"unicode"
)

// SeatSource 是卡上协调者席位的占用来源。
type SeatSource string

const (
	SeatSourceBind       SeatSource = "bind"
	SeatSourceCoordinate SeatSource = "coordinate"
)

// ValidateSeat 检查席位身份与来源是否构成可占用的规范席位。
// identity 为空时仅允许 source 为空；非空 identity 必须能被
// ParseSeatIdentity 解码，来源只能是 bind 或 coordinate。
func ValidateSeat(identity string, source SeatSource) error {
	if identity == "" {
		if source != "" {
			return fmt.Errorf("空席位不能带来源 %q", source)
		}
		return nil
	}
	if _, _, err := ParseSeatIdentity(identity); err != nil {
		return fmt.Errorf("席位身份无效: %w", err)
	}
	switch source {
	case SeatSourceBind, SeatSourceCoordinate:
		return nil
	default:
		return fmt.Errorf("席位来源非法: %q", source)
	}
}

// EncodeSeatIdentity 把当前 CLI 物种名和会话 id 编成账本/房间共用的出示身份。
// 语法固定为 cli:<cli>#<session_id>；# 是唯一分隔符，旧的 cli:user@host
// 因而不会被误认成合法席位。
func EncodeSeatIdentity(cli, sessionID string) (string, error) {
	if err := validateSeatPart("cli", cli, true); err != nil {
		return "", err
	}
	if err := validateSeatPart("session_id", sessionID, false); err != nil {
		return "", err
	}
	return "cli:" + cli + "#" + sessionID, nil
}

// ParseSeatIdentity 解出 EncodeSeatIdentity 的规范值。空串由调用方表示空座，
// 传入空串本身视为非法输入，避免把缺失身份悄悄降级成空座。
func ParseSeatIdentity(raw string) (cli, sessionID string, err error) {
	if raw == "" {
		return "", "", fmt.Errorf("席位身份为空")
	}
	if !strings.HasPrefix(raw, "cli:") {
		return "", "", fmt.Errorf("席位身份格式非法: %q", raw)
	}
	parts := strings.Split(raw[len("cli:"):], "#")
	if len(parts) != 2 {
		return "", "", fmt.Errorf("席位身份格式非法: %q", raw)
	}
	if err := validateSeatPart("cli", parts[0], true); err != nil {
		return "", "", err
	}
	if err := validateSeatPart("session_id", parts[1], false); err != nil {
		return "", "", err
	}
	return parts[0], parts[1], nil
}

func validateSeatPart(name, value string, cli bool) error {
	if value == "" || strings.TrimSpace(value) != value {
		return fmt.Errorf("%s 不能为空或带首尾空白", name)
	}
	if strings.IndexFunc(value, unicode.IsSpace) >= 0 || strings.ContainsRune(value, '#') {
		return fmt.Errorf("%s 含非法字符", name)
	}
	if cli && strings.ContainsRune(value, ':') {
		return fmt.Errorf("cli 含非法字符")
	}
	return nil
}
