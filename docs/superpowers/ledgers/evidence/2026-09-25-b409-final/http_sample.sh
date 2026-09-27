#!/usr/bin/env bash
# http_sample.sh — r2 冻结采样规则：单列首请求 + 5 次预热（不计入）+ 100 次
# 计入样本。输出 TSV：kind\tstatus\ttime_total_s\tsize_download_bytes。
# 用法：http_sample.sh <name> <url>（TOKEN 环境变量提供 Bearer 令牌）
set -euo pipefail
name=$1 url=$2
auth=(-H "Authorization: Bearer ${TOKEN:?TOKEN 未设置}")
req() {
  curl -sS -o /dev/null -w '%{http_code}\t%{time_total}\t%{size_download}\n' \
    "${auth[@]}" "$url" || printf 'CURL_ERR\t0\t0\n'
}
echo -e "kind\tstatus\tseconds\tbytes"
echo -e "first\t$(req | tr '\n' '\t' | sed 's/\t$//')"
for _ in 1 2 3 4 5; do req > /dev/null; done
for _ in $(seq 100); do
  echo -e "sample\t$(req | tr '\n' '\t' | sed 's/\t$//')"
done
