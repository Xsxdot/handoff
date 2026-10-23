package proto

import "testing"

func TestValidateOriginSessionID(t *testing.T) {
	for _, id := range []string{"ses_live_1", "local-abc123", "session_01", "session:abc-123"} {
		if err := ValidateOriginSessionID(id); err != nil {
			t.Errorf("%q rejected: %v", id, err)
		}
	}
	for _, id := range []string{"", " ses_1", "ses/1", "ses one", "ses#1"} {
		if err := ValidateOriginSessionID(id); err == nil {
			t.Errorf("%q accepted", id)
		}
	}
}
