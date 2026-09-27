#!/usr/bin/env bash
# run_u7_harness.sh — B409.7（U7）HTTP API 与 CLI 故事功能验收主 harness。
#
# 职责：在隔离可丢弃 PG 上分三阶段 seed 增长矩阵，用真实 agentd（embedweb
# 构建）+ 真实 handoff CLI 按 r2 冻结规则采样（每场景首请求 + 5 预热 + 100
# 样本，nearest-rank p50/p95/max、错误数、response bytes，p95 ≤2s 每场景独立
# 判定），并验证 CLI 故事功能与 session wait 30 次空等时限。
#
# 红线：DSN/凭据只经环境变量传入，本脚本与全部证据文件不得包含 DSN、密码、
# token、真实用户消息正文；证据目录落盘前有脱敏扫描。
#
# 用法：LEDGER_TEST_PG_DSN='postgres://…' bash run_u7_harness.sh
set -euo pipefail
REPO_ROOT=$(git rev-parse --show-toplevel)
EV_DIR="$REPO_ROOT/docs/superpowers/ledgers/evidence/2026-09-25-b409-final"
WORK=${WORK:-/tmp/b409u7-harness}
PORT=${U7_AGENTD_PORT:-17877}
BASE="http://127.0.0.1:${PORT}"
: "${LEDGER_TEST_PG_DSN:?必须导出 LEDGER_TEST_PG_DSN（专用可丢弃库，不写入文件）}"
export GOCACHE=${GOCACHE:-/private/tmp/handoff-b409-u7-gocache}

mkdir -p "$WORK" "$EV_DIR/golden"

echo "==[0] 环境与隔离库核验"
DB_NAME=$(printf '%s' "$LEDGER_TEST_PG_DSN" | sed -E 's|.*/([^/?]+)\?.*|\1|')
if [ "$DB_NAME" != "handoff_b409_test" ]; then
  echo "拒绝非专用库: current db=$DB_NAME" >&2; exit 1
fi
echo "db_name=$DB_NAME (专用可丢弃库校验通过；DSN 本体不入档)"
SHA=$(git -C "$REPO_ROOT" rev-parse HEAD)
echo "sha=$SHA"
docker exec handoff-b409-pg psql -U handoff -d "$DB_NAME" -tAc 'SHOW server_version;' | sed 's/ .*//' > "$EV_DIR/env.txt" || true
cat >> "$EV_DIR/env.txt" <<ENV
sha=$SHA
db=$DB_NAME (isolated disposable, container handoff-b409-pg)
agentd_port=$PORT (isolated; production 7777 untouched)
build=CGO_ENABLED=0 go build -trimpath -tags embedweb
sample_rule=first + 5 warmup + 100 samples, nearest-rank p50/p95/max, p95<=2s per scenario
web_first_screen=留交协调者（真实浏览器三组读数）
ENV

echo "==[1] 构建 agentd/CLI 二进制（embedweb）"
CGO_ENABLED=0 go build -trimpath -tags embedweb -o "$WORK/handoff" "$REPO_ROOT"

echo "==[2] 重置专用库并生成隔离配置（DSN/token 只进 /tmp 配置）"
docker exec handoff-b409-pg psql -U handoff -d "$DB_NAME" -q -c "TRUNCATE card_events, cards, card_relations, card_tasks, card_dispatch_rounds, open_ticket_projection, open_ticket_projection_state, workflows, dispatch_templates, disciplines, decisions, mirror_lease, mirror_cursors, session_delivery_cursors, card_run_locks, ledger_meta, driver_leases, seat_bearings, wake_claims, sessions, session_cards, card_prefixes, registry RESTART IDENTITY CASCADE;"
TOKEN=$(openssl rand -hex 16)
cat > "$WORK/config.yaml" <<CFG
listen: 127.0.0.1:${PORT}
datadir: ${WORK}/data
token: ${TOKEN}
console_user: sy
ledger:
  dsn: ${LEDGER_TEST_PG_DSN}
CFG
mkdir -p "$WORK/data"

# 跨运行防混拼：每次运行清空样本流与旧统计。
: > "$EV_DIR/http-samples.tsv"
rm -f "$EV_DIR/http-stats.json"

echo "==[3] 三阶段 seed（Go harness）"
RUN_ID="b409-u7-$(date +%s)"
export LEDGER_TEST_PG_PERF=1 LEDGER_TEST_PG_PERF_RUN_ID="$RUN_ID"
seed_phase() {
  LEDGER_TEST_PG_SEED_PHASE=$1 go test -C "$REPO_ROOT" ./internal/ledger/ \
    -run 'TestB409U7PGSeedPhase' -count=1 -v -timeout 20m 2>&1 | \
    { grep -E 'B409_U7_STAGE|B409_U7_FIXTURE|B409_U7_ENVELOPE|^(ok|FAIL|---)' || true; }
}
seed_phase 1 | tee "$EV_DIR/seed-phase1.log"
SESSION_MAIN=$(grep -o 'session_main=[^ ]*' "$EV_DIR/seed-phase1.log" | head -1 | cut -d= -f2)
WAIT_MEMBER=$(grep -o 'wait_member=[^ ]*' "$EV_DIR/seed-phase1.log" | head -1 | cut -d= -f2)
IDLE_MEMBER=$(grep -o 'idle_member=[^ ]*' "$EV_DIR/seed-phase1.log" | head -1 | cut -d= -f2)
PROJECT=$(grep -o 'project=[^ ]*' "$EV_DIR/seed-phase1.log" | head -1 | cut -d= -f2)
CARD_A=$(grep -o 'card_a=[^ ]*' "$EV_DIR/seed-phase1.log" | head -1 | cut -d= -f2)
echo "session_main=$SESSION_MAIN wait_member=$WAIT_MEMBER project=$PROJECT card_a=$CARD_A"

echo "==[4] 启动隔离 agentd（等健康）"
# HANDOFF_LOG_LEVEL=info：U6 的 rows_returned/payload_bytes 诊断是 INFO 级，
# logx 缺省 warn 会整级吞掉（review Critical-1 根因），harness 必须显式打开。
# datadir 的 agentd.log 跨运行保留（logx 追加写不截断），启动前清空，防止
# 上次运行的诊断行混进本次 stage-lines 证据（与步骤[2]样本防混拼同一理由）。
: > "$WORK/data/agentd.log"
HANDOFF_LOG_LEVEL=info "$WORK/handoff" --config "$WORK/config.yaml" agentd > "$WORK/agentd.log" 2>&1 &
AGENTD_PID=$!
trap 'kill $AGENTD_PID 2>/dev/null || true' EXIT
for _ in $(seq 120); do
  code=$(curl -s -o /dev/null -w '%{http_code}' -H "Authorization: Bearer $TOKEN" "$BASE/api/cards" || true)
  [ "$code" = "200" ] && break
  sleep 0.5
done
[ "$code" = "200" ] || { echo "agentd 未就绪" >&2; exit 1; }
echo "agentd ready pid=$AGENTD_PID"

HCLI="$WORK/handoff"
CLIARGS=(--config "$WORK/config.yaml")
ENC_SESSION=$(python3 -c "import urllib.parse,sys;print(urllib.parse.quote(sys.argv[1],safe=''))" "$SESSION_MAIN")
ENC_SESSION_EMPTY=$(python3 -c "import urllib.parse,sys;print(urllib.parse.quote(sys.argv[1],safe=''))" "$(grep -o 'session_empty=[^ ]*' "$EV_DIR/seed-phase1.log" | head -1 | cut -d= -f2)")
export TOKEN

sample_one() {
  # 每场景独立一行流：phase\tscenario\tkind\tstatus\tseconds\tbytes
  local ph=$1 sc=$2 url=$3
  bash "$EV_DIR/http_sample.sh" "$sc" "$url" | tail -n +2 |
    awk -F'\t' -v ph="$ph" -v sc="$sc" '{print ph"\t"sc"\t"$0}' >> "$EV_DIR/http-samples.tsv"
}
measure_phase() {
  local phase=$1
  echo "-- 阶段 $phase HTTP 采样"
  sample_one "$phase" cards "$BASE/api/cards"
  sample_one "$phase" sessions "$BASE/api/sessions"
  sample_one "$phase" rooms "$BASE/api/rooms?limit=50"
  sample_one "$phase" messages "$BASE/api/rooms/${ENC_SESSION}/messages?limit=200"
}

# 冻结 HTTP 场景金样（阶段①首次响应原文，作为 S1/S2 用户可见 JSON 基准）
golden_capture() {
  curl -sS -H "Authorization: Bearer $TOKEN" "$BASE/api/cards" > "$EV_DIR/golden/api-cards-phase1.json"
  curl -sS -H "Authorization: Bearer $TOKEN" "$BASE/api/sessions" > "$EV_DIR/golden/api-sessions-phase1.json"
  curl -sS -H "Authorization: Bearer $TOKEN" "$BASE/api/rooms/${ENC_SESSION}/messages?limit=200" > "$EV_DIR/golden/api-messages-phase1.json"
}

cli_story() {
  echo "-- CLI 故事功能"
  {
    echo "### handoff card list --json"
    "$HCLI" "${CLIARGS[@]}" card list --json
    echo "### handoff card wait $CARD_A --timeout 4s（首行 card_snapshot，随后空等到 124）"
    rc=0
    "$HCLI" "${CLIARGS[@]}" card wait "$CARD_A" --timeout 4s > /tmp/u7-card-wait.txt 2>&1 || rc=$?
    echo "card_wait_exit=${rc} 124=timeout-close; first line should be a full card_snapshot"
    head -1 /tmp/u7-card-wait.txt
    echo "### handoff session list --json"
    "$HCLI" "${CLIARGS[@]}" session list --json
    echo "### handoff session detail $SESSION_MAIN --json"
    "$HCLI" "${CLIARGS[@]}" session detail "$SESSION_MAIN" --json
    echo "### handoff room list"
    "$HCLI" "${CLIARGS[@]}" room list
    echo "### handoff room read ${SESSION_MAIN}（行数统计）"
    "$HCLI" "${CLIARGS[@]}" room read "$SESSION_MAIN" | tee /tmp/u7-room-read.txt | wc -l
    echo "room_read_first3:"
    head -3 /tmp/u7-room-read.txt
    echo "### handoff room inbox（经 agentd HTTP）"
    "$HCLI" "${CLIARGS[@]}" room inbox
    echo "### handoff session wait ${WAIT_MEMBER}（首个完整 backlog 行）"
    rc=0
    "$HCLI" "${CLIARGS[@]}" session wait "$WAIT_MEMBER" --timeout 5s || rc=$?
    echo "wait_hit_exit=${rc} expect 0"
    echo "### handoff session wait $WAIT_MEMBER --timeout 400ms（已交付不回放）"
    rc=0
    "$HCLI" "${CLIARGS[@]}" session wait "$WAIT_MEMBER" --timeout 400ms || rc=$?
    echo "wait_replay_exit=${rc} expect 124 and no output above"
    echo "### handoff session wait $IDLE_MEMBER --timeout 400ms（空积压不输出）"
    rc=0
    "$HCLI" "${CLIARGS[@]}" session wait "$IDLE_MEMBER" --timeout 400ms || rc=$?
    echo "wait_idle_exit=${rc} expect 124 and no output above"
  } > "$EV_DIR/cli-story.txt" 2>&1
}

functional_api() {
  echo "-- 功能级 API（成功/空/错误/权限/取消，描述性）"
  {
    echo "### GET /api/sessions/{fixture}（成功）"
    curl -sS -w '\nstatus=%{http_code} bytes=%{size_download} time=%{time_total}\n' -H "Authorization: Bearer $TOKEN" "$BASE/api/sessions/${ENC_SESSION}"
    echo "### GET /api/sessions/{不存在}（错误 404）"
    curl -sS -o /dev/null -w 'status=%{http_code}\n' -H "Authorization: Bearer $TOKEN" "$BASE/api/sessions/session%3Anope-u7"
    echo "### GET /api/rooms/{空会话}/messages?limit=200（真实空）"
    curl -sS -w '\nstatus=%{http_code} bytes=%{size_download}\n' -H "Authorization: Bearer $TOKEN" "$BASE/api/rooms/${ENC_SESSION_EMPTY}/messages?limit=200"
    echo "### GET /api/cards 无 token（权限 401）"
    curl -sS -o /dev/null -w 'status=%{http_code}\n' "$BASE/api/cards"
    echo "### 客户端中途断开（取消语义，agentd 日志取证）"
    rc=0
    curl -sS -o /dev/null --max-time 0.05 -H "Authorization: Bearer $TOKEN" "$BASE/api/rooms/${ENC_SESSION}/messages?limit=200" || rc=$?
    echo "client_abort_exit=${rc} (28=超时中断)"
    echo "### GET /api/inbox（成功）"
    curl -sS -w '\nstatus=%{http_code} bytes=%{size_download} time=%{time_total}\n' -H "Authorization: Bearer $TOKEN" "$BASE/api/inbox"
  } > "$EV_DIR/api-functional.txt" 2>&1
}

echo "==[5] 阶段①测量"
measure_phase 1
golden_capture
cli_story
functional_api
echo "step5-phase1 done"

echo "==[6] 阶段②测量（+20,000 无关非镜像）"
seed_phase 2 | tee "$EV_DIR/seed-phase2.log"
measure_phase 2

echo "==[7] 阶段③测量（镜像行/字节各自翻倍）"
seed_phase 3 | tee "$EV_DIR/seed-phase3.log"
measure_phase 3

echo "==[8] session wait --timeout 5s 空等 30 次（子进程启动至退出 ≤6s）"
{
  echo "member=$IDLE_MEMBER (无积压无新消息)"
  for i in $(seq 30); do
    t0=$(date +%s%N)
    rc=0
    "$HCLI" "${CLIARGS[@]}" session wait "$IDLE_MEMBER" --timeout 5s > /dev/null 2>&1 || rc=$?
    t1=$(date +%s%N)
    echo -e "$i\t$rc\t$(( (t1 - t0) / 1000000 ))ms"
  done
} > "$EV_DIR/session-wait-30x.tsv"

echo "==[9] 收口：agentd 阶段日志、脱敏扫描"
# agentd 的 slog 经 logx.Setup 落 datadir/agentd.log（stdout 重定向收不到）。
# 不设行数上限（review Critical-1：head -400 使超过 400 行的断言必假）；
# logx 旋转上限 100MB，本 harness 采样规模远达不到。
grep -E 'rows_returned|payload_bytes' "$WORK/data/agentd.log" > "$EV_DIR/agentd-stage-lines.log" || true
cp "$WORK/data/agentd.log" "$EV_DIR/agentd-full.log"
kill $AGENTD_PID 2>/dev/null || true
wait $AGENTD_PID 2>/dev/null || true
# 脱敏扫描只针对生成的数据文件（脚本自身的用法示例注释经 code review 入档）。
# 泛化模式（review Minor-1）：不写死任何口令字面量——连接串（含凭据/端口）、
# password 赋值、Bearer token 都拦。
if grep -RInE 'postgres(ql)?://[^[:space:]]*[:@]|[Pp]assword[[:space:]]*[=:]|Bearer [A-Za-z0-9]' \
     "$EV_DIR"/*.log "$EV_DIR"/*.tsv "$EV_DIR"/*.txt "$EV_DIR"/*.json "$EV_DIR"/golden 2>/dev/null; then
  echo "脱敏扫描失败：证据数据文件含敏感串" >&2
  exit 1
fi
echo "脱敏扫描通过（无 DSN 密码/连接串/Bearer token）"
echo "==[10] 统计（nearest-rank）"
python3 "$EV_DIR/compute_stats.py" "$EV_DIR/http-samples.tsv" > "$EV_DIR/http-stats.json"
echo "harness 完成（统计已写入 http-stats.json）"
