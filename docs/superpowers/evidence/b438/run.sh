#!/bin/bash
# run.sh start <a|b> <binary> | stop <a|b> ：只管 /root/b438-real 下的隔离实例（按 /proc/<pid>/exe 与 cwd 精确匹配）
cmd=$1; s=$2; bin=$3
stop() {
  for p in $(pgrep -f -- "--config /root/b438-real/$1/config.yaml"); do
    case "$(readlink /proc/$p/exe)" in /root/b438-real/bin/*) kill -TERM $p; echo "stopped $1 pid=$p";; esac
  done
}
case $cmd in
start)
  : > /root/b438-real/$s/data/agentd.log
  (ip netns exec b438-host-$s env -i PATH=/usr/bin:/bin HOME=/root/b438-real/$s/home HANDOFF_LOG_LEVEL=debug setsid /root/b438-real/bin/$bin --config /root/b438-real/$s/config.yaml agentd > /root/b438-real/$s/agentd.out 2>&1 < /dev/null &) ;;
stop) stop $s ;;
esac
