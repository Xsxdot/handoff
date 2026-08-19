# Relay implementation ledger

| Entry | Scope | Commit range | Verification |
|---|---|---|---|
| Task 1 complete | RelayConfig, relay Target form, validation, Load logging | `HEAD^..HEAD` (Task 1 commit) | `go test ./internal/config/ -run 'Relay|IsRelay' -v` passed; full package has pre-existing root-permission failure in `TestLoadStripUpdateDoesNotBlockOnSaveFailure` |
| Task 2 complete | Relay control frame JSON codec, WSS text helpers, ControlError | `HEAD^..HEAD` (Task 2 commit) | `go test ./internal/relay/ -run Frame -v` passed |
| Task 3 complete | HKDF-SHA256 PSK derivation, Noise NNpsk0, framed secure net.Conn | `HEAD^..HEAD` (Task 3 commit) | `go test ./internal/relay/ -v` passed |
| Task 4 complete | Coordinator lazy relay Dialer, HTTP Transport, fake-relay E2E | `HEAD^..HEAD` (Task 4 commit) | `go test ./internal/relay/ -v` passed; yamux emitted its raw context-canceled close log during cleanup |
| Task 5 complete | Relay client/factory, token entropy gate, command migration, Bundle pull | `HEAD^..HEAD` (Task 5 commit) | `go test ./cmd/` passed; targeted cmd/client/relay tests passed; full internal/client has root-permission failures in cursor fallback/error tests |
| Task 6 complete | Executor relay Listener, per-session E2E/app yamux, reconnect, appListener | `HEAD^..HEAD` (Task 6 commit) | `go test ./internal/relay/ -v -count=1` passed; mux ordering synchronized with Dialer and yamux stderr logging suppressed |
| Task 7 complete | agentd relay token fail-closed gate and concurrent RelayListener startup | `HEAD^..HEAD` (Task 7 commit) | `go test ./cmd/ -run '^TestAgentdRelayTokenGate$' -count=1 -v` passed; `go test ./internal/relay/ -count=1` passed; `go build ./...` passed; broader cmd regression raw failure: `登记到本机: project add: 状态码 404: 404 page not found` |
| Task 8 complete | Integration build-tag skeleton, relay run instructions, English/Chinese configuration docs | `HEAD^..HEAD` (Task 8 commit) | `git diff --check` passed; integration skeleton is excluded unless `-tags integration` is supplied; reviewer-only cross-repo run intentionally not executed per plan |
