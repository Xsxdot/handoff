#!/usr/bin/env bash
# b393-resume-hang.sh —— 复现 opencode resume 挂死：回合已跑完（exiting loop）
# 但 CLI 进程永不 dispose，stdout 零字节、不退出。
#
# 用法：b393-resume-hang.sh [会话id]   （缺省：现场已知挂死会话）
# 退出码：0=在界内退出（未复现）；124=挂死被看门狗杀掉（复现）。
#
# 职责：只驱动 CLI 子进程，把「挂死」这一外部现实压缩成一次可判读的 rc/字节读数。
# 边界：只驱动 CLI，不碰账本/agentd/线上状态；临时输出落 $TMPDIR。
set -u
SID="${1:-ses_f45bc164affebNHHlbcxaGAxC5}"
LIMIT="${B393_LIMIT:-45}"   # 观察窗秒数；健康会话数秒内返回
OUTDIR="${TMPDIR:-/tmp}"
start=$(date +%s)
timeout -k 3 "$LIMIT" opencode run --format json -s "$SID" -- "say b393-hang-probe" </dev/null \
  >"$OUTDIR/b393-hang.out" 2>"$OUTDIR/b393-hang.err"
rc=$?
dur=$(( $(date +%s) - start ))
printf 'sid=%s rc=%s dur=%ss out_bytes=%s err_bytes=%s\n' \
  "$SID" "$rc" "$dur" "$(wc -c <"$OUTDIR/b393-hang.out")" "$(wc -c <"$OUTDIR/b393-hang.err")"
exit "$rc"
