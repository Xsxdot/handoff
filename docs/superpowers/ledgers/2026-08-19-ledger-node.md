# Ledger Node B156.1 implementation ledger

- Baseline: `069ef0bc`; existing `dispatch_templates` schema and discipline block files were present before this plan.
- Task 1 completed: added immutable versioned DispatchTemplate storage, default feature/review templates with embedded handoff-verdict contract, per-target model overrides, and coverage for idempotent seeding; commit scope: `internal/ledger/templates.go internal/ledger/templates_test.go docs/superpowers/ledgers/2026-08-19-ledger-node.md`.
