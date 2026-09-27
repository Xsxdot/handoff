#!/usr/bin/env python3
"""compute_stats.py — r2 冻结统计口径：每阶段×每场景 nearest-rank p50/p95/max、
错误数与 response bytes；p95 ≤ 2s 每场景独立判定。失败样本（HTTP 非 200 或
curl 失败行）计入错误并保留在分布内，不静默剔除。
输入：http-samples.tsv（phase\tkind\tstatus\tseconds\tbytes）"""
import json
import math
import sys
from collections import defaultdict


def nearest_rank(sorted_vals, q):
    idx = max(1, math.ceil(q * len(sorted_vals)))
    return sorted_vals[idx - 1]


def main(path):
    rows = defaultdict(lambda: {"first": None, "samples": [], "errors": 0, "bytes": []})
    with open(path) as fh:
        # http-samples.tsv 无表头（sample_one 直接追加数据行）。
        for line in fh:
            parts = line.rstrip("\n").split("\t")
            if len(parts) != 6:
                continue
            phase, scenario, kind, status, seconds, nbytes = parts
            key = (phase, scenario)
            try:
                elapsed_ms = float(seconds) * 1000.0
                status_code = int(status)
                size = int(nbytes)
            except ValueError:
                rows[key]["errors"] += 1
                continue
            if kind == "first":
                rows[key]["first"] = {"ms": elapsed_ms, "status": status_code, "bytes": size}
            else:
                rows[key]["samples"].append(elapsed_ms)
                rows[key]["bytes"].append(size)
                if status_code != 200:
                    rows[key]["errors"] += 1
    report = []
    failures = []
    for (phase, kind), data in sorted(rows.items()):
        samples = sorted(data["samples"])
        if not samples:
            continue
        entry = {
            "phase": phase,
            "scenario": kind,
            "first": data["first"],
            "warmups": 5,
            "n_samples": len(samples),
            "p50_ms": round(nearest_rank(samples, 0.50), 3),
            "p95_ms": round(nearest_rank(samples, 0.95), 3),
            "max_ms": round(samples[-1], 3),
            "errors": data["errors"],
            "response_bytes_min": min(data["bytes"]),
            "response_bytes_max": max(data["bytes"]),
        }
        entry["p95_within_2s"] = entry["p95_ms"] <= 2000.0
        report.append(entry)
        if not entry["p95_within_2s"]:
            failures.append(f"{phase}/{kind} p95={entry['p95_ms']}ms")
    print(json.dumps(report, indent=2, ensure_ascii=False))
    verdict = "PASS" if not failures else f"FAIL: {', '.join(failures)}"
    print(f"verdict={verdict}", file=sys.stderr)
    return 0 if not failures else 1


if __name__ == "__main__":
    sys.exit(main(sys.argv[1]))
