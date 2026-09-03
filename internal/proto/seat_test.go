package proto

import "testing"

func TestSeatIdentityGolden(t *testing.T) {
	got, err := EncodeSeatIdentity("codex", "thread-01")
	if err != nil {
		t.Fatalf("EncodeSeatIdentity: %v", err)
	}
	const want = "cli:codex#thread-01"
	if got != want {
		t.Fatalf("encoded identity = %q, want %q", got, want)
	}
	cli, sessionID, err := ParseSeatIdentity(got)
	if err != nil {
		t.Fatalf("ParseSeatIdentity: %v", err)
	}
	if cli != "codex" || sessionID != "thread-01" {
		t.Fatalf("parsed identity = (%q, %q), want (codex, thread-01)", cli, sessionID)
	}
}

func TestSeatIdentityRejectsLegacyAndAmbiguousValues(t *testing.T) {
	for _, raw := range []string{
		"cli:user@host",
		"cli:codex",
		"cli:#thread-01",
		"cli:codex#",
		"cli:codex#thread#01",
	} {
		if _, _, err := ParseSeatIdentity(raw); err == nil {
			t.Errorf("ParseSeatIdentity(%q) unexpectedly succeeded", raw)
		}
	}
}

func TestValidateSeatRequiresKnownSourceForIdentity(t *testing.T) {
	identity, err := EncodeSeatIdentity("codex", "thread-01")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, raw string
		source    SeatSource
		wantErr   bool
	}{
		{name: "bind", raw: identity, source: SeatSourceBind},
		{name: "coordinate", raw: identity, source: SeatSourceCoordinate},
		{name: "empty seat", raw: "", source: ""},
		{name: "unknown source", raw: identity, source: "manual", wantErr: true},
		{name: "legacy identity", raw: "cli:old@host", source: SeatSourceBind, wantErr: true},
		{name: "empty identity with source", raw: "", source: SeatSourceBind, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if gotErr := ValidateSeat(tc.raw, tc.source); (gotErr != nil) != tc.wantErr {
				t.Fatalf("ValidateSeat(%q, %q) error=%v wantErr=%v", tc.raw, tc.source, gotErr, tc.wantErr)
			}
		})
	}
}
