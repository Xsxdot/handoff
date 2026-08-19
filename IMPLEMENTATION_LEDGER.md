# Ledger core implementation ledger

- Baseline: `77d6254925546dccee9903cb722823855165aa37`; `go build ./...` passed; `go test ./...` had pre-existing environment/platform failures recorded in the execution log.
- Task 1 repair round 1: fixed config fixture's required `StallTimeout`, placed `Ledger` after `Targets`, updated strict-key help, and exercised loading without a ledger section; commit scope: `go.mod go.sum internal/config/ IMPLEMENTATION_LEDGER.md`.
- Task 1 completed: pgx dependency and `LedgerConfig` with `omitempty` round-trip coverage; commit scope: Task 1 files above.
- Task 2 completed: added `internal/ledger` declarations, controlled vocabulary, domain types, and sentinel errors; commit scope: `internal/ledger/`.
- Task 3 repair round 1: retained dialect/target Open logging while redacting credentials from PostgreSQL DSNs; commit scope: `internal/ledger/store.go`.
- Task 3 completed: added SQLite/PG Open, idempotent schema, q/time conversion, mutate locking, and event listener base; commit scope: `internal/ledger/store.go internal/ledger/store_test.go internal/ledger/store_pg_test.go`.
- Task 4 completed: added immutable versioned workflows and idempotent feature/bug defaults with gates; commit scope: `internal/ledger/workflows.go internal/ledger/workflows_test.go`.
- Task 5 completed: added card creation/read/update/termination/revival, B-number allocation, event append, and ascending event reads; commit scope: `internal/ledger/cards.go internal/ledger/events.go internal/ledger/cards_test.go`.
- Task 6 repair round 1: preserved `ErrNotFound` only for missing workflow rows and wrapped other workflow read errors without misclassification; commit scope: `internal/ledger/move.go`.
- Task 6 completed: added workflow-version state validation, gate checks, explicit/database CAS, and refusal logging; commit scope: `internal/ledger/move.go internal/ledger/move_test.go`.
- Task 7 repair round 1: added Warn logging for self-block cycle rejection with the card id; commit scope: `internal/ledger/relations.go`.
- Task 7 completed: added typed relations, blocks cycle/parent-child checks, bidirectional relation reads, and effective base inheritance; commit scope: `internal/ledger/relations.go internal/ledger/relations_test.go`.
- Task 8+9 completed: added merge/unmerge/split, comment references, acceptance/needs/subtree events, and derived ListCards views/filters; commit scope: `internal/ledger/merge.go internal/ledger/events.go internal/ledger/derived.go` plus paired tests.
