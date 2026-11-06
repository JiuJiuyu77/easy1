#!/bin/sh
# 定位「某个域名被切断」到底发生在哪一层（在面板主机上执行）。
#
# 用法（宝塔终端，root）：
#   sh find_sni_filter.sh bldimg.com
#   sh find_sni_filter.sh bldimg.com 8.163.17.195   # 第二个参数：本机公网 IP
#
# 它做四件事：
#   1) 查本机是否有按域名的包过滤（iptables/nft string 匹配、hosts、dnsmasq）
#   2) 查是否有第二个代理/DPI 进程在抢流量（sing-box/clash/xray/v2ray/tproxy…）
#   3) 直接对目标做 TLS 握手，记录「切断耗时」——小于一个到节点的 RTT 即为本地切断
#   4) 明文 80 端口取证：若返回云厂商备案页，说明是**云厂商网络层**域名拦截，
#      与本面板、与机场都无关
#
# 判读见脚本末尾。

set -u

DOMAIN=${1:-}
PUBLIC_IP=${2:-}

if [ -z "$DOMAIN" ]; then
    echo "用法: sh find_sni_filter.sh <域名> [本机公网IP]" >&2
    exit 2
fi

have() { command -v "$1" >/dev/null 2>&1; }
now_ms() { date +%s%3N 2>/dev/null || echo 0; }
section() { printf '\n== %s\n' "$1"; }

section "1) 按域名的包过滤（字符串匹配）"
if have iptables-save; then
    hits=$(iptables-save 2>/dev/null | grep -iE "string|$DOMAIN")
    [ -n "$hits" ] && echo "$hits" | head -20 || echo "   iptables: 无匹配"
else
    echo "   iptables-save 不存在"
fi
if have nft; then
    hits=$(nft list ruleset 2>/dev/null | grep -iE "string|$DOMAIN")
    [ -n "$hits" ] && echo "$hits" | head -20 || echo "   nftables: 无匹配"
else
    echo "   nft 不存在"
fi
if have ip6tables-save; then
    hits=$(ip6tables-save 2>/dev/null | grep -iE "string|$DOMAIN")
    [ -n "$hits" ] && echo "$hits" | head -10 || true
fi

section "2) 本机 DNS / hosts 覆盖"
grep -nE "[[:space:]]$DOMAIN([[:space:]]|$)" /etc/hosts 2>/dev/null || echo "   /etc/hosts: 无该域名"
if [ -d /etc/dnsmasq.d ]; then
    grep -rn "$DOMAIN" /etc/dnsmasq.d 2>/dev/null | head -5 || echo "   dnsmasq.d: 无该域名"
fi
if have resolvectl; then
    resolvectl query "$DOMAIN" 2>/dev/null | head -5 || true
fi

section "3) 本机可能抢流量的代理/DPI 进程"
ps -eo pid,comm,args 2>/dev/null | grep -iE "sing-box|clash|xray|v2ray|trojan|naive|tproxy|sniff|dpi|redsocks|gost" | grep -v grep | head -15 || echo "   无相关进程"

section "4) 本机监听端口（看是否有透明代理/额外入口）"
if have ss; then
    ss -lntp 2>/dev/null | head -25
elif have netstat; then
    netstat -lntp 2>/dev/null | head -25
fi

section "5) TLS 切断耗时取证（同一 IP，两个 SNI 对照）"
handshake() {
    host=$1; sni=$2
    if ! have openssl; then echo "   (无 openssl，跳过)"; return; fi
    start=$(now_ms)
    out=$( { timeout 12 sh -c "echo | openssl s_client -connect ${host}:443 -servername ${sni} -brief 2>&1"; } 2>/dev/null )
    end=$(now_ms)
    cost=$((end - start))
    case "$out" in
        *"Verification: OK"*|*"CONNECTION ESTABLISHED"*|*"Server certificate"*|*"depth="*) echo "   握手成功 ${cost}ms" ;;
        *"reset"*|*"Connection reset"*) echo "   RST ${cost}ms" ;;
        *) echo "   被切断/失败 ${cost}ms" ;;
    esac
}
printf '   SNI=%-24s ' "$DOMAIN"; handshake "$DOMAIN" "$DOMAIN"
printf '   SNI=%-24s ' "example.com"; handshake "$DOMAIN" "example.com"

section "6) 明文 80 端口取证（云厂商拦截会返回自己的页面）"
if have curl; then
    curl -s -m 8 -o /dev/null -D - "http://$DOMAIN/" 2>/dev/null | head -8 || echo "   80 端口无响应"
    body=$(curl -s -m 8 "http://$DOMAIN/" 2>/dev/null | head -c 400)
    case "$body" in
        *备案*|*beian*|*"Aliyun"*|*"aliyun"*) echo "   >>> 响应里出现备案/阿里云字样：这是云厂商网络层的域名拦截" ;;
    esac
else
    echo "   (无 curl，跳过)"
fi

cat <<'TIPS'

== 判读
   A. 第 6 步返回云厂商备案页（403 + 备案字样）
      → 云厂商网络层按域名拦截。面板与机场都无法绕过：
        · 换一台非大陆/非该厂商的机器跑面板；或
        · 完成域名备案；或
        · 客户端不要直连该 SNI（由境外出口发起 TLS）。
   B. 第 1 步命中 string/域名规则
      → 本机防火墙拦的：删掉该规则（iptables -D … / nft delete rule …）。
   C. 第 3 步有第二个代理/DPI 进程在跑
      → 它抢了流量：停掉它（systemctl stop <名字>），再复测。
   D. 以上都没有，但第 5 步「SNI=域名」被切、换 SNI 正常
      → 拦截在更外层（运营商/机房侧），同样只能换出口机器。
   E. 第 5 步两个 SNI 都正常
      → 本机没问题，问题在客户端到面板这一段。
TIPS
