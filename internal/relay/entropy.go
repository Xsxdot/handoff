package relay

import "errors"

// CheckTokenEntropy rejects relay tokens that do not provide at least 128 bits
// of hex-encoded entropy. Relay mode is fail-closed because the token is the
// offline-guessing-resistant source for the E2E PSK.
func CheckTokenEntropy(token string) error {
	if len(token) < 32 {
		return errors.New("relay 模式要求高熵 token，请用 handoff init 生成的 token")
	}
	for _, r := range token {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')) {
			return errors.New("relay 模式要求高熵 token，请用 handoff init 生成的 token")
		}
	}
	return nil
}
