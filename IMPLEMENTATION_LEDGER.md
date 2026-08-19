# Ledger core implementation ledger

- Baseline: `77d6254925546dccee9903cb722823855165aa37`; `go build ./...` passed; `go test ./...` had pre-existing environment/platform failures recorded in the execution log.
- Task 1 repair round 1: fixed config fixture's required `StallTimeout`, placed `Ledger` after `Targets`, updated strict-key help, and exercised loading without a ledger section; commit scope: `go.mod go.sum internal/config/ IMPLEMENTATION_LEDGER.md`.
- Task 1 completed: pgx dependency and `LedgerConfig` with `omitempty` round-trip coverage; commit scope: Task 1 files above.
- Task 2 completed: added `internal/ledger` declarations, controlled vocabulary, domain types, and sentinel errors; commit scope: `internal/ledger/`.
- Task 3 repair round 1: retained dialect/target Open logging while redacting credentials from PostgreSQL DSNs; commit scope: `internal/ledger/store.go`.
- Task 3 completed: added SQLite/PG Open, idempotent schema, q/time conversion, mutate locking, and event listener base; commit scope: `internal/ledger/store.go internal/ledger/store_test.go internal/ledger/store_pg_test.go`.
- Task 4 completed: added immutable versioned workflows and idempotent feature/bug defaults with gates; commit scope: `internal/ledger/workflows.go internal/ledger/workflows_test.go`.
