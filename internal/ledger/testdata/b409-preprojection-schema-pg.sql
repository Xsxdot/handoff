-- b409-preprojection-schema-pg.sql — B409.7 (U7) 旧库升级验收夹具（PostgreSQL 方言）。
-- 冻结来源 revision：a7983e9c（1f28372b "feat: add rebuildable open ticket projection"
-- 的父提交，即 B409.1 引入 open_ticket_projection 之前的最后 schema 状态）。
-- 内容 = 该 revision internal/ledger/store.go ddlStatements(true) 的逐字提取，
-- 仅做语句分号收尾；未从任何生产/共享数据库复制 DDL。仅供
-- b409_final_acceptance_test.go 在隔离 SQLite/专用可丢弃 PG 上重建旧库形态，
-- 验证真实 ledger.Open/ensureSchema 升级路径；禁止用于其它目的。
CREATE TABLE IF NOT EXISTS cards (
				id TEXT PRIMARY KEY, title TEXT NOT NULL, status TEXT NOT NULL,
				terminate_reason TEXT, priority TEXT NOT NULL DEFAULT '中',
				project TEXT NOT NULL, parent_id TEXT REFERENCES cards(id),
				workflow_name TEXT NOT NULL, workflow_version INT NOT NULL,
				attachments JSONB NOT NULL DEFAULT '[]', acceptance_criteria TEXT,
				base_branch TEXT, driver_session TEXT, driver_source TEXT, driver_heartbeat_at TIMESTAMPTZ,
				created_at TIMESTAMPTZ NOT NULL, updated_at TIMESTAMPTZ NOT NULL);
CREATE INDEX IF NOT EXISTS idx_cards_board ON cards(project, status);
CREATE INDEX IF NOT EXISTS idx_cards_parent ON cards(parent_id);
CREATE TABLE IF NOT EXISTS card_relations (
				from_id TEXT NOT NULL REFERENCES cards(id),
				to_id TEXT NOT NULL REFERENCES cards(id),
				type TEXT NOT NULL, created_at TIMESTAMPTZ NOT NULL,
				PRIMARY KEY (from_id, to_id, type));
CREATE UNIQUE INDEX IF NOT EXISTS uq_rel_merged_into
				ON card_relations(from_id) WHERE type = 'merged_into';
CREATE INDEX IF NOT EXISTS idx_rel_to ON card_relations(to_id, type);
CREATE TABLE IF NOT EXISTS card_tasks (
				card_id TEXT NOT NULL REFERENCES cards(id),
				target TEXT NOT NULL, task_id TEXT NOT NULL, purpose TEXT NOT NULL,
				created_at TIMESTAMPTZ NOT NULL, PRIMARY KEY (target, task_id));
CREATE INDEX IF NOT EXISTS idx_card_tasks_card ON card_tasks(card_id);
CREATE TABLE IF NOT EXISTS card_dispatch_rounds (
				card_id TEXT NOT NULL REFERENCES cards(id),
				purpose TEXT NOT NULL,
				created_at TIMESTAMPTZ NOT NULL);
CREATE INDEX IF NOT EXISTS idx_card_dispatch_rounds_card_purpose
				ON card_dispatch_rounds(card_id, purpose);
CREATE TABLE IF NOT EXISTS card_events (
				seq BIGSERIAL PRIMARY KEY, card_id TEXT REFERENCES cards(id),
				type TEXT NOT NULL, actor TEXT NOT NULL, payload JSONB NOT NULL,
				source_target TEXT, source_task TEXT, source_seq BIGINT,
				created_at TIMESTAMPTZ NOT NULL);
CREATE UNIQUE INDEX IF NOT EXISTS uq_events_mirror
				ON card_events(source_target, source_task, source_seq)
				WHERE source_target IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_events_card ON card_events(card_id, seq);
CREATE INDEX IF NOT EXISTS idx_room_messages_room_seq
				ON card_events((payload->>'room'), seq DESC)
				WHERE type = 'room_message' AND card_id IS NULL;
CREATE TABLE IF NOT EXISTS workflows (
				name TEXT NOT NULL, version INT NOT NULL, definition JSONB NOT NULL,
				created_at TIMESTAMPTZ NOT NULL, PRIMARY KEY (name, version));
CREATE TABLE IF NOT EXISTS dispatch_templates (
				name TEXT NOT NULL, version INT NOT NULL, definition JSONB NOT NULL,
				created_at TIMESTAMPTZ NOT NULL, PRIMARY KEY (name, version));
CREATE TABLE IF NOT EXISTS disciplines (
				name TEXT NOT NULL, version INT NOT NULL, body TEXT NOT NULL,
				created_at TIMESTAMPTZ NOT NULL, PRIMARY KEY (name, version));
CREATE TABLE IF NOT EXISTS decisions (
				id BIGSERIAL PRIMARY KEY, card_id TEXT REFERENCES cards(id),
				body TEXT NOT NULL, options JSONB,
				status TEXT NOT NULL DEFAULT 'open', created_by TEXT NOT NULL,
				answer TEXT, answered_by TEXT,
				created_at TIMESTAMPTZ NOT NULL, answered_at TIMESTAMPTZ);
CREATE INDEX IF NOT EXISTS idx_decisions_open ON decisions(status) WHERE status = 'open';
CREATE TABLE IF NOT EXISTS mirror_lease (
				id INT PRIMARY KEY CHECK (id = 1),
				holder TEXT NOT NULL, lease_until TIMESTAMPTZ NOT NULL);
CREATE TABLE IF NOT EXISTS mirror_cursors (
				target TEXT PRIMARY KEY, last_seq BIGINT NOT NULL,
				updated_at TIMESTAMPTZ NOT NULL);
CREATE TABLE IF NOT EXISTS session_delivery_cursors (
				member TEXT PRIMARY KEY, last_seq BIGINT NOT NULL,
				updated_at TIMESTAMPTZ NOT NULL);
CREATE TABLE IF NOT EXISTS card_run_locks (
				card_id TEXT PRIMARY KEY REFERENCES cards(id),
				node TEXT NOT NULL, holder TEXT NOT NULL,
				acquired_at TIMESTAMPTZ NOT NULL, expires_at TIMESTAMPTZ NOT NULL);
CREATE TABLE IF NOT EXISTS ledger_meta (
				key TEXT PRIMARY KEY, value TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS driver_leases (
				session TEXT PRIMARY KEY,
				expires_at TIMESTAMPTZ NOT NULL);
CREATE TABLE IF NOT EXISTS seat_bearings (
				card_id TEXT PRIMARY KEY,
				identity TEXT NOT NULL,
				carrier TEXT NOT NULL,
				machine TEXT NOT NULL,
				home_dir TEXT NOT NULL,
				workdir TEXT NOT NULL,
				model TEXT NOT NULL,
				bound_at TIMESTAMPTZ NOT NULL);
CREATE TABLE IF NOT EXISTS wake_claims (
				card TEXT NOT NULL,
				seq BIGINT NOT NULL,
				holder TEXT NOT NULL,
				lease_until TIMESTAMPTZ NOT NULL,
				done_at TIMESTAMPTZ,
				PRIMARY KEY (card, seq));
CREATE INDEX IF NOT EXISTS idx_wake_claims_seq ON wake_claims(seq);
CREATE TABLE IF NOT EXISTS sessions (
				id TEXT PRIMARY KEY, title TEXT NOT NULL, owner TEXT NOT NULL,
				archived BOOLEAN NOT NULL DEFAULT false, members JSONB NOT NULL DEFAULT '[]',
				created_at TIMESTAMPTZ NOT NULL, updated_at TIMESTAMPTZ NOT NULL);
CREATE TABLE IF NOT EXISTS session_cards (
				session_id TEXT NOT NULL REFERENCES sessions(id),
				card_id TEXT NOT NULL REFERENCES cards(id),
				created_at TIMESTAMPTZ NOT NULL,
				PRIMARY KEY (session_id, card_id));
CREATE UNIQUE INDEX IF NOT EXISTS uq_session_cards_card
				ON session_cards(card_id);
CREATE TABLE IF NOT EXISTS card_prefixes (
				project TEXT PRIMARY KEY, prefix TEXT NOT NULL UNIQUE);
CREATE TABLE IF NOT EXISTS registry (
				kind TEXT NOT NULL, id TEXT NOT NULL, version INTEGER NOT NULL,
				seq BIGINT NOT NULL, body JSONB NOT NULL,
				actor TEXT NOT NULL, updated_at TIMESTAMPTZ NOT NULL,
				PRIMARY KEY (kind, id));
CREATE INDEX IF NOT EXISTS idx_registry_kind_seq ON registry(kind, seq);
INSERT INTO card_prefixes (project, prefix) VALUES ('handoff', 'B')
				ON CONFLICT (project) DO NOTHING;
