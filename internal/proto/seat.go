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
