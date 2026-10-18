#!/bin/bash
# B438 真机拓扑：ns wan（公网 198.51.100.0/24，relay+STUN）+ 两个 NAT（nat-a/nat-b）+ 其后的 host-a/host-b。
# 全部状态在 netns 内，down 即删净，不碰宿主网络配置。
set -e
X="ip netns exec"
case "$1" in
up)
  for n in wan nat-a nat-b host-a host-b; do ip netns add b438-$n; $X b438-$n ip link set lo up; done
  $X b438-wan ip link add br-wan type bridge; $X b438-wan ip addr add 198.51.100.1/24 dev br-wan; $X b438-wan ip link set br-wan up
  i=0
  for s in a b; do
    i=$((i+1))
    ip link add b438w$s type veth peer name b438n$s
    ip link set b438w$s netns b438-wan; ip link set b438n$s netns b438-nat-$s
    $X b438-wan ip link set b438w$s master br-wan; $X b438-wan ip link set b438w$s up
    $X b438-nat-$s ip link set b438n$s name wan0; $X b438-nat-$s ip addr add 198.51.100.1$i/24 dev wan0; $X b438-nat-$s ip link set wan0 up
    ip link add b438l$s type veth peer name b438h$s
    ip link set b438l$s netns b438-nat-$s; ip link set b438h$s netns b438-host-$s
    $X b438-nat-$s ip link set b438l$s name lan0; $X b438-nat-$s ip addr add 10.10.$i.1/24 dev lan0; $X b438-nat-$s ip link set lan0 up
    $X b438-host-$s ip link set b438h$s name eth0; $X b438-host-$s ip addr add 10.10.$i.2/24 dev eth0; $X b438-host-$s ip link set eth0 up
    $X b438-host-$s ip route add default via 10.10.$i.1
    $X b438-nat-$s sysctl -qw net.ipv4.ip_forward=1
    $X b438-nat-$s iptables -t nat -A POSTROUTING -o wan0 -j MASQUERADE
    # 家用路由器的常规 WAN 入站防火墙：未经请求的新入站在 filter INPUT 丢弃，conntrack
    # 条目因此不被确认（不会占住 NAT 映射元组）。nofw 去掉它复现「无防火墙 Linux NAT」。
    $X b438-nat-$s iptables -A INPUT -i wan0 -m conntrack --ctstate NEW -j DROP
  done
  ;;
nofw)  for s in a b; do $X b438-nat-$s iptables -D INPUT -i wan0 -m conntrack --ctstate NEW -j DROP; done ;;
fw)    for s in a b; do $X b438-nat-$s iptables -A INPUT -i wan0 -m conntrack --ctstate NEW -j DROP; done ;;
sym)   # nat-b 改对称：端口随机化（映射随目的端点变化）
  $X b438-nat-b iptables -t nat -F POSTROUTING
  $X b438-nat-b iptables -t nat -A POSTROUTING -o wan0 -j MASQUERADE --random-fully ;;
cone)
  $X b438-nat-b iptables -t nat -F POSTROUTING
  $X b438-nat-b iptables -t nat -A POSTROUTING -o wan0 -j MASQUERADE ;;
cut)   $X b438-nat-a iptables -I FORWARD -p udp -j DROP ;;
uncut) $X b438-nat-a iptables -D FORWARD -p udp -j DROP ;;
down)  for n in wan nat-a nat-b host-a host-b; do ip netns del b438-$n 2>/dev/null || true; done ;;
*) echo "usage: $0 up|fw|nofw|sym|cone|cut|uncut|down"; exit 2 ;;
esac
