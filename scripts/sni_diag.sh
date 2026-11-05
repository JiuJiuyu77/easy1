#!/bin/sh
# 定位「目标域名被 RST」发生在哪一跳。
#
# 用法：
#   sh scripts/sni_diag.sh bldimg.example.com                 # 只测本机直连
#   sh scripts/sni_diag.sh bldimg.example.com 2323            # 再经面板池口(HTTP代理)测
#   sh scripts/sni_diag.sh target.com 2323 1.1.1.1            # 指定 CONNECT 目的 IP
#
# 判读：
#   本机直连就已 RST（耗时几毫秒~几十毫秒）→ 出口链路/运营商/GFW 按 SNI 阻断，
#      与面板无关；换线路、或在面板入口套一层自有 TLS 承载（App 侧不再直连目标 SNI）。
#   本机直连正常、经面板 RST 且耗时极短 → 面板进程本地拒绝，需要看面板侧拦截规则。
#   经面板正常 → 面板没问题，问题在客户端到面板这一段（同样看 SNI 是否被中间层阻断）。

set -u

TARGET=${1:-}
PROXY_PORT=${2:-}
CONNECT_IP=${3:-}

if [ -z "$TARGET" ]; then
    echo "用法: sh scripts/sni_diag.sh <目标域名> [面板池口端口] [CONNECT 目的IP]" >&2
    exit 2
fi

timeout_cmd=""
if command -v timeout >/dev/null 2>&1; then timeout_cmd="timeout 12"; fi

now_ms() { date +%s%3N 2>/dev/null || echo 0; }

# 用 openssl 发起一次带 SNI 的 TLS 握手，输出耗时与结果
handshake() {
    host=$1
    sni=$2
    if ! command -v openssl >/dev/null 2>&1; then
        echo "openssl-missing"
        return
    fi
    start=$(now_ms)
    out=$( { $timeout_cmd sh -c "echo | openssl s_client -connect ${host}:443 -servername ${sni} -brief 2>&1" ; } 2>/dev/null )
    end=$(now_ms)
    cost=$((end - start))
    out=$(printf '%s' "$out" | tr -d '\r')
    case "$out" in
        *"Verification: OK"*|*"CONNECTION ESTABLISHED"*|*"Server certificate"*|*"depth="*)
            echo "握手成功 ${cost}ms" ;;
        *"reset"*|*"RST"*|*"Connection reset"*)
            echo "RST ${cost}ms" ;;
        *"wrong version number"*|*"alert"*)
            echo "TLS告警 ${cost}ms" ;;
        "")
            echo "无响应 ${cost}ms" ;;
        *)
            echo "$(echo "$out" | tail -1 | cut -c1-60) ${cost}ms" ;;
    esac
}

echo "== 1) 本机直连 $TARGET（TLS SNI 矩阵）"
for sni in "$TARGET" "example.com" "www.bing.com"; do
    printf '   SNI=%-28s %s\n' "$sni" "$(handshake "$TARGET" "$sni")"
done
if [ -n "$CONNECT_IP" ]; then
    echo
    echo "== 1b) 直连指定 IP $CONNECT_IP，SNI 矩阵"
    for sni in "$TARGET" "example.com"; do
        printf '   SNI=%-28s %s\n' "$sni" "$(handshake "$CONNECT_IP" "$sni")"
    done
fi

echo
echo "== 2) DNS 解析"
if command -v getent >/dev/null 2>&1; then
    getent hosts "$TARGET" | head -3 || echo "   解析失败"
else
    echo "   (无 getent)"
fi

if [ -z "$PROXY_PORT" ]; then
    echo
    echo "（未给面板池口端口，跳过经面板的测试）"
    exit 0
fi

echo
echo "== 3) 经面板池口 127.0.0.1:$PROXY_PORT（CONNECT 隧道）"
if ! command -v curl >/dev/null 2>&1; then
    echo "   需要 curl"
    exit 0
fi
for ip in "$TARGET" "1.1.1.1"; do
    target_url="https://${ip}/"
    extra=""
    if [ "$ip" != "$TARGET" ]; then
        # 保留真实 SNI：用 --resolve 让 URL 域名解析到指定 IP
        target_url="https://${TARGET}/"
        extra="--resolve ${TARGET}:443:${ip}"
    fi
    printf '   CONNECT=%-16s ' "$ip"
    # shellcheck disable=SC2086
    result=$($timeout_cmd curl -sS -o /dev/null -X GET -x "http://127.0.0.1:${PROXY_PORT}" \
        $extra -w '%{http_code} %{time_total}s' "$target_url" 2>&1)
    echo "$result"
done

cat <<'TIPS'

== 判读
   本机直连 SNI=目标 就 RST，SNI=其他 正常  → 链路/运营商/GFW 按 SNI 阻断（面板无关）
   本机直连正常，经面板 RST 且耗时 <50ms   → 面板进程本地拦截，检查面板侧规则
   经面板正常                              → 面板放行；若 App 仍失败，问题在 App→面板 之间
TIPS
