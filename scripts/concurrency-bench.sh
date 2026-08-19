#!/usr/bin/env bash
# 职责：在执行机上对 handoff 后端（go build/test）和前端（vitest + tsc + vite build）
#       做「1 路 vs 3 路同时跑」对照，并按秒采样 load / 进程数 / CPU / 内存压力。
# 边界：不改仓库代码、不起 agentd、不碰 ~/.handoff/tasks。工作副本落在 /tmp。

set -euo pipefail

# 非交互 ssh 只有系统 PATH，login shell 里的 go/node 不会自动出现。
export PATH="/usr/local/go/bin:/usr/local/bin:/opt/homebrew/bin:${PATH}"

REPO="${REPO:-/Users/sycm/.handoff/repos/handoff}"
BRANCH="${BRANCH:-origin/handoff/web-console}"
BASE="${BASE:-/tmp/handoff-concurrency-bench}"
INTERVAL="${INTERVAL:-1}"
# 1=只采样、不把 worker 自己的 stdout 混进指标文件
LOG_FILE="${BASE}/bench.log"
SAMPLES="${BASE}/samples.jsonl"
SUMMARY="${BASE}/summary.md"
WARMUP="${WARMUP:-1}"

log() {
	# 结构化落盘：级别 + 事件 + 键值。stdout 只给人看进度，不算日志。
	local level="$1" event="$2"
	shift 2
	local ts kv=""
	ts="$(date -u +%Y-%m-%dT%H:%M:%S%z)"
	printf -v kv ' %s' "$@"
	printf '%s level=%s event=%s%s\n' "$ts" "$level" "$event" "$kv" >>"$LOG_FILE"
}

die() {
	log error "$1" "${@:2}"
	echo "ERROR: $1" >&2
	exit 1
}

ensure_tools() {
	command -v go >/dev/null || die "go_missing" "path=$PATH"
	command -v node >/dev/null || die "node_missing" "path=$PATH"
	command -v npm >/dev/null || die "npm_missing" "path=$PATH"
	command -v git >/dev/null || die "git_missing"
	log info tools_ok "go=$(command -v go)" "node=$(command -v node)" "npm=$(command -v npm)"
}

sample_once() {
	# 采样命令允许失败：pgrep 无匹配返回 1，配 set -e 会把整个采样器打死
	set +e
	local phase="$1" load nproc cpu_sum pages_free compile_n
	load="$(sysctl -n vm.loadavg 2>/dev/null | tr -d '{}')"
	nproc="$(ps -ax 2>/dev/null | wc -l | tr -d ' ')"
	# 各进程 %cpu 之和：10 核满载约 1000，比 top 在非 tty 下更稳
	cpu_sum="$(ps -Ao %cpu= 2>/dev/null | awk '{s+=$1} END {printf "%.1f", s+0}')"
	pages_free="$(sysctl -n vm.page_free_count 2>/dev/null)"
	compile_n="$(ps -ax -o command= 2>/dev/null | grep -E 'go-build|go test|vite|vitest|tsc |esbuild' | grep -v grep | wc -l | tr -d ' ')"
	set -e
	printf '{"ts":%s,"phase":"%s","load":"%s","nproc":%s,"cpu_sum":"%s","compile_n":%s,"pages_free":"%s"}\n' \
		"$(date +%s)" "$phase" "$(echo "$load" | awk '{print $1" "$2" "$3}')" \
		"${nproc:-0}" "${cpu_sum:-}" "${compile_n:-0}" "${pages_free:-}"
}

sampler_loop() {
	local phase="$1" pidfile="$2"
	# 停靠 pid 文件在不在，不靠 BASHPID：macOS 自带 bash 3.2 没有这个变量，
	# set -u 下写它会让采样器秒退，samples.jsonl 就会是空的。
	while [[ -f "$pidfile" ]]; do
		sample_once "$phase" >>"$SAMPLES" || log error sample_failed "phase=$phase"
		sleep "$INTERVAL"
	done
}

start_sampler() {
	local phase="$1"
	local pidfile="${BASE}/sampler.${phase}.pid"
	# 先落哨兵文件再后台：循环条件是「文件还在」，得保证启动瞬间文件已存在
	: >"$pidfile"
	sampler_loop "$phase" "$pidfile" &
	echo $! >"$pidfile"
	local i=0
	while [[ ! -s "$SAMPLES" && $i -lt 25 ]]; do
		sleep 0.2
		i=$((i + 1))
	done
	if [[ ! -s "$SAMPLES" ]]; then
		log error sampler_empty "phase=$phase" "pid=$(cat "$pidfile")"
	fi
	log info sampler_started "phase=$phase" "pid=$(cat "$pidfile")" "bytes=$(wc -c <"$SAMPLES" | tr -d ' ')"
}

stop_sampler() {
	local phase="$1"
	local pidfile="${BASE}/sampler.${phase}.pid"
	if [[ -f "$pidfile" ]]; then
		local pid
		pid="$(cat "$pidfile")"
		rm -f "$pidfile"
		if [[ -n "$pid" && "$pid" != "$$" ]]; then
			wait "$pid" 2>/dev/null || kill "$pid" 2>/dev/null || true
		fi
		log info sampler_stopped "phase=$phase" "pid=$pid"
	fi
}

worker_backend() {
	local root="$1" label="$2"
	log info worker_backend_enter "label=$label" "root=$root"
	(
		cd "$root"
		go build ./...
		go test ./... -count=1
	)
	log info worker_backend_ok "label=$label"
}

worker_frontend() {
	local root="$1" label="$2"
	log info worker_frontend_enter "label=$label" "root=$root"
	(
		cd "$root/web"
		# 三路并行时不能共用 dist/；typecheck 仍写到各自 worktree 的 tsbuildinfo
		npx vitest run
		npx tsc -b
		npx vite build --outDir "dist-bench-${label}"
	)
	log info worker_frontend_ok "label=$label"
}

worker_full() {
	local root="$1" label="$2" t0 t1
	t0="$(date +%s)"
	log info worker_enter "label=$label" "root=$root"
	worker_backend "$root" "$label"
	worker_frontend "$root" "$label"
	t1="$(date +%s)"
	local elapsed=$((t1 - t0))
	echo "$label $elapsed" >>"${BASE}/timings.${PHASE}.txt"
	log info worker_exit "label=$label" "elapsed_s=$elapsed"
}

run_phase() {
	local phase="$1" n="$2"
	PHASE="$phase"
	: >"${BASE}/timings.${phase}.txt"
	log info phase_enter "phase=$phase" "workers=$n"
	echo "=== phase ${phase}  workers=${n}  $(date) ==="

	start_sampler "$phase"
	local t0 t1
	t0="$(date +%s)"
	local pids=()
	local i
	for i in $(seq 1 "$n"); do
		worker_full "${BASE}/w${i}" "w${i}" >"${BASE}/worker.${phase}.w${i}.out" 2>&1 &
		pids+=("$!")
	done

	local fail=0
	local pid
	for pid in "${pids[@]}"; do
		if ! wait "$pid"; then
			fail=1
			log error worker_failed "phase=$phase" "pid=$pid"
		fi
	done
	t1="$(date +%s)"
	stop_sampler "$phase"

	echo "$phase wall=$((t1 - t0)) fail=$fail" >>"${BASE}/walls.txt"
	log info phase_exit "phase=$phase" "wall_s=$((t1 - t0))" "fail=$fail"
	if [[ "$fail" -ne 0 ]]; then
		echo "phase ${phase} 有 worker 失败，见 ${BASE}/worker.${phase}.*.out" >&2
	fi
}

setup() {
	mkdir -p "$BASE"
	: >"$LOG_FILE"
	: >"$SAMPLES"
	: >"${BASE}/walls.txt"
	log info setup_enter "repo=$REPO" "branch=$BRANCH" "base=$BASE"

	ensure_tools
	[[ -d "$REPO/.git" ]] || die "repo_missing" "repo=$REPO"

	log info git_fetch_begin "repo=$REPO"
	git -C "$REPO" fetch origin handoff/web-console
	log info git_fetch_ok "rev=$(git -C "$REPO" rev-parse "$BRANCH")"

	local i
	for i in 1 2 3; do
		local dest="${BASE}/w${i}"
		if [[ ! -d "$dest/.git" && ! -f "$dest/.git" ]]; then
			log info worktree_add "dest=$dest"
			git -C "$REPO" worktree add --force "$dest" "$BRANCH"
		else
			log info worktree_reuse "dest=$dest"
		fi
	done

	# 只在一份树里 npm ci，再同步 node_modules，避免三份各下一次依赖
	log info npm_ci_begin "root=${BASE}/w1/web"
	if [[ ! -d "${BASE}/w1/web/node_modules" ]]; then
		(cd "${BASE}/w1/web" && npm ci)
	fi
	log info npm_ci_ok
	local j
	for j in 2 3; do
		log info node_modules_sync "from=w1" "to=w${j}"
		rsync -a --delete "${BASE}/w1/web/node_modules/" "${BASE}/w${j}/web/node_modules/"
	done
	log info setup_ok
}

warmup() {
	log info warmup_enter
	# 预热 Go 编译缓存和 vite 依赖解析，避免第一轮单并发被冷启动惩罚
	(cd "${BASE}/w1" && go test ./... -count=0 >/dev/null)
	(cd "${BASE}/w1/web" && npx vitest run >/dev/null && npx tsc -b >/dev/null)
	log info warmup_ok
}

summarize() {
	log info summarize_enter
	{
		echo "# handoff 前后端 1 路 vs 3 路对照"
		echo
		echo "- 机器：$(sysctl -n machdep.cpu.brand_string)  ncpu=$(sysctl -n hw.ncpu)  mem_gb=$(($(sysctl -n hw.memsize) / 1024 / 1024 / 1024))"
		echo "- 分支：$(git -C "${BASE}/w1" rev-parse --short HEAD) $(git -C "${BASE}/w1" rev-parse --abbrev-ref HEAD)"
		echo "- 后端：\`go build ./...\` + \`go test ./... -count=1\`"
		echo "- 前端：\`vitest run\` + \`tsc -b\` + \`vite build\`"
		echo "- 每个 worker 顺序跑完后端再跑前端；三路 = 三个独立 worktree 同时开跑"
		echo
		echo "## 墙钟"
		echo
		echo '```'
		cat "${BASE}/walls.txt"
		echo
		echo "--- per-worker seconds ---"
		echo "single:"
		cat "${BASE}/timings.single.txt" 2>/dev/null || true
		echo "triple:"
		cat "${BASE}/timings.triple.txt" 2>/dev/null || true
		echo '```'
		echo
		echo "## 采样摘要（每秒一行）"
		echo
		python3 - "$SAMPLES" <<'PY'
import json, sys, collections
path = sys.argv[1]
by = collections.defaultdict(list)
try:
    lines = open(path).read().splitlines()
except FileNotFoundError:
    print("无采样文件")
    raise SystemExit(0)
for line in lines:
    line=line.strip()
    if not line:
        continue
    try:
        o=json.loads(line)
    except json.JSONDecodeError:
        continue
    by[o.get("phase","?")].append(o)

def fnum(xs, key):
    out=[]
    for x in xs:
        v=x.get(key)
        try:
            if v is None or v=="":
                continue
            # load 取 1 分钟值
            if key=="load":
                out.append(float(str(v).split()[0]))
            else:
                out.append(float(v))
        except ValueError:
            pass
    return out

def stat(xs):
    if not xs:
        return "n/a"
    xs=sorted(xs)
    n=len(xs)
    avg=sum(xs)/n
    p95=xs[min(n-1, int(n*0.95))]
    return f"n={n} min={xs[0]:.1f} avg={avg:.1f} p95={p95:.1f} max={xs[-1]:.1f}"

print("| phase | load1 | cpu_sum (10核满载约1000) | nproc | compile_n |")
print("|-------|-------|-------------------------|-------|-----------|")
for phase in ("single","triple"):
    xs=by.get(phase,[])
    print(f"| {phase} | {stat(fnum(xs,'load'))} | {stat(fnum(xs,'cpu_sum'))} | {stat(fnum(xs,'nproc'))} | {stat(fnum(xs,'compile_n'))} |")
PY
		echo
		echo "原始样本：\`${SAMPLES}\`"
		echo "完整日志：\`${LOG_FILE}\`"
	} >"$SUMMARY"
	log info summarize_ok "summary=$SUMMARY"
	cat "$SUMMARY"
}

cleanup() {
	log info cleanup_enter
	local i
	for i in 1 2 3; do
		if [[ -e "${BASE}/w${i}" ]]; then
			git -C "$REPO" worktree remove --force "${BASE}/w${i}" 2>/dev/null || rm -rf "${BASE}/w${i}"
		fi
	done
	log info cleanup_ok
}

cmd="${1:-run}"
case "$cmd" in
setup)
	mkdir -p "$BASE"
	: >"$LOG_FILE"
	setup
	;;
run)
	mkdir -p "$BASE"
	: >"$LOG_FILE"
	: >"$SAMPLES"
	: >"${BASE}/walls.txt"
	setup
	if [[ "$WARMUP" == "1" ]]; then
		warmup
	fi
	# 两轮之间歇 5 秒，让 loadavg 回落，避免单路被三路尾巴污染
	run_phase single 1
	sleep 5
	run_phase triple 3
	summarize
	log info run_ok
	;;
cleanup)
	mkdir -p "$BASE"
	touch "$LOG_FILE"
	cleanup
	;;
*)
	echo "用法: $0 setup|run|cleanup" >&2
	exit 2
	;;
esac
