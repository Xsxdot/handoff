# Relay implementation ledger

| Entry | Scope | Commit range | Verification |
|---|---|---|---|
| Task 1 complete | RelayConfig, relay Target form, validation, Load logging | `HEAD^..HEAD` (Task 1 commit) | `go test ./internal/config/ -run 'Relay|IsRelay' -v` passed; full package has pre-existing root-permission failure in `TestLoadStripUpdateDoesNotBlockOnSaveFailure` |
| Task 2 complete | Relay control frame JSON codec, WSS text helpers, ControlError | `HEAD^..HEAD` (Task 2 commit) | `go test ./internal/relay/ -run Frame -v` passed |
| Task 3 complete | HKDF-SHA256 PSK derivation, Noise NNpsk0, framed secure net.Conn | `HEAD^..HEAD` (Task 3 commit) | `go test ./internal/relay/ -v` passed |
| Task 4 complete | Coordinator lazy relay Dialer, HTTP Transport, fake-relay E2E | `HEAD^..HEAD` (Task 4 commit) | `go test ./internal/relay/ -v` passed; yamux emitted its raw context-canceled close log during cleanup |
