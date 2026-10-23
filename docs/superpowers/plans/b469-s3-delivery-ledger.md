# B469 S3 delivery implementation ledger

## 偏离与自主决定

- Desktop notices reuse the existing local desktop mailbox and continue opener. A failed mailbox write remains on the existing bounded retry path; a letter already written but not opened is recorded as `needs_human` with `last_error=desktop_open_failed` and is not rewritten automatically, preserving the existing one-letter/open-failure behavior. Coordinator confirmed explicit `RetryTaskTicketNotice` transitions same-version notices back to pending and emits a fresh addressed event; the consumer therefore retries the opener without rewriting the existing letter.
- Failed resident/card rounds persist `needs_human` against the current ownership version; admission-stalled rounds retain the existing in-flight claim and retry behavior. A notice is marked `delivered` only after the existing short round returns successfully (resident admission release is deferred inside that call), or after the target desktop opener returns successfully.
- No additional wire, Runner, hostapi, or ownership API was introduced. These choices use the approved S3 contract's local-only delivery boundary.

## Scope and behavior

- `EvTaskTicketNotice` is decoded separately from `room_message`; required source identity, ticket kind, source sequence and ownership version are checked.
- Every wake re-reads the current notice and matches the exact target and ownership version before resolving current local resident/seat state. Only a current push seat or push resident queues a short wake. Pull seats remain on their existing wait path.
- All event and active-notice intake reuses `proto.ValidateTaskTicketNotice`; stale events must also match the current persisted source sequence and ticket kind before waking or acknowledging.
- `resolveWakeCard` preserves a valid origin pull seat on its wait path, but skips an empty/invalid origin pull seat while walking toward a valid push ancestor; ancestor pull seats continue to be skipped.
- Desktop notices remain content-free bells. Current pending/needs-human notices are re-read from the ledger each cycle so a mailbox-write failure can retry even after the event cursor advances. A successful opener marks the current target/version delivered. An unopened letter is left human-visible and not duplicated.
- Delivery status updates re-read and compare the target/version before applying the ledger CAS; failures log identifiers/status without logging ticket content.
- Resident and card wake prompts include the originating machine, full task ID, full ticket ID, ticket kind, and the executable `handoff show <task> --target <machine>` instruction. They do not include the permission/question payload.
- Once any notice exists for the exact source machine/task/ticket, its later legacy `task_mirrored` permission/question event is suppressed at the automation consumer. The lookup is exact; a missing notice (`sql.ErrNoRows`) preserves the legacy wake path for older mirrors. Other mirrored task types are unchanged.

## Verification

- Red: `TestTaskTicketNoticeWakeRequiresCurrentLocalAddress` initially failed because `EvTaskTicketNotice` was discarded as an unaddressed no-card event.
- Green: `go test ./internal/agentd -run '^Test(TaskTicketNoticeWakeRequiresCurrentLocalAddress|DesktopNoticeOpenFailureRemainsHumanVisible)$' -count=1` passed.
- Green after coordinator feedback: focused B435 valid-pull/ancestor-pull coverage and `TestB435ResidentBridgeSourceGuard` passed.
- Green: `TestTaskTicketNoticeCardPushConsumesAndAcknowledges` and `TestTaskTicketNoticeResidentPushConsumesAndAcknowledges` exercise the real automation consumer through the existing keystone short-round path and verify `delivered` acknowledgment.
- Red→green: `TestTaskTicketNoticeSuppressesLaterLegacyMirrorWake` first reproduced two resumes (notice delivery followed by the same ticket's mirror), then passed after exact-key notice lookup suppressed the duplicate. `TestLegacyMirrorWithoutNoticeStillWakes` confirms older mirror behavior remains.
- Green: the card and resident consumer tests assert that the captured Runner prompt contains source machine, full task/ticket IDs, kind, and the `handoff show ... --target ...` command.
- `go build -mod=readonly ./...` passed.
- Final focused rerun: ten notice/resolver/source-guard tests passed; `go build -mod=readonly ./...` passed after shared fail-closed validation was added. No mutation test or commit was performed.

## Unverified / coordination

- Real two-machine delivery, database disconnect/replay, ownership replacement while an external round is running, and physical desktop opener behavior remain outside this local test run and are still acceptance gates.
- Full `go test ./internal/agentd` ran but failed only at the unrelated `TestHostWakeResolvesLoginProxyOnHostMachine` assertion in `b453_carrier_account_test.go:244` (expected NO_PROXY to include `10.0.0.0/8`; actual proxy probe output did not). Its isolated rerun failed the same assertion. This slice did not modify that test or its host-proxy code.
- Same-version retry event behavior is delegated to the ledger retry method. Manual desktop retry after a written-but-unopened letter was not exercised end-to-end here.
