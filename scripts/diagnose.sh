#!/bin/sh
# easy1 部署自检：容器状态、端口、读写端点，以及反代是否破坏写操作。
#
# 用法：
#   sh scripts/diagnose.sh                                  # 只测本机 127.0.0.1:9091
#   sh scripts/diagnose.sh http://127.0.0.1:9091 https://proxy.example.com
#
# 说明：PUT /api/settings 传空对象 {} 是安全的——服务端按“未提供的字段保持原值”
# 处理，只会把当前值原样写回，不会清空配置。

set -u

LOCAL_BASE=${1:-http://127.0.0.1:9091}
PUBLIC_BASE=${2:-}

code() {
    out=$(curl -s -o /dev/null -w '%{http_code}' "$@" 2>/dev/null)
    case "${out:-000}" in
        000) echo "无响应(000)" ;;
        *) echo "$out" ;;
    esac
}
body() { curl -s "$@" 2>/dev/null | head -c 200; }

echo "== 容器"
if command -v docker >/dev/null 2>&1; then
    docker ps --filter name=easy_proxies --format '  {{.Names}}  {{.Status}}' 2>/dev/null || echo "  (docker ps 失败)"
else
    echo "  (没有 docker 命令，跳过)"
fi

echo
echo "== 读接口（期望 200）"
for path in /api/auth /api/ui/summary /api/settings /api/subscription/config /api/chain-profiles; do
    printf '  GET  %-28s %s\n' "$path" "$(code "$LOCAL_BASE$path")"
done

echo
echo "== 写接口（直连，等价非浏览器客户端）"
printf '  PUT  %-28s %s\n' '/api/settings' "$(code -X PUT "$LOCAL_BASE/api/settings" -H 'Content-Type: application/json' --data '{}')"
printf '  POST %-28s %s\n' '/api/reload' "$(code -X POST "$LOCAL_BASE/api/reload")"

if [ -n "$PUBLIC_BASE" ]; then
    echo
    echo "== 写接口（经反代 $PUBLIC_BASE，带浏览器 Origin）"
    printf '  PUT  %-28s %s\n' '/api/settings' "$(code -X PUT "$PUBLIC_BASE/api/settings" -H "Origin: $PUBLIC_BASE" -H 'Content-Type: application/json' --data '{}')"
    printf '  POST %-28s %s\n' '/api/reload' "$(code -X POST "$PUBLIC_BASE/api/reload" -H "Origin: $PUBLIC_BASE")"
    echo
    echo "  反代响应体（判断是否后端在回答）："
    body -X PUT "$PUBLIC_BASE/api/settings" -H "Origin: $PUBLIC_BASE" -H 'Content-Type: application/json' --data '{}'
    echo
fi

cat <<'TIPS'

== 判读
  读接口全 200、直连写接口 200            → 后端正常，问题在反代或前端
  直连写 200，反代写 403 且含「跨站请求已拒绝」 → 反代没传原始 Host：
      在站点配置里加 proxy_set_header Host $host; 然后 nginx -s reload
  直连写 200，反代写 405 或返回 HTML        → 该层代理/CDN 不放行 PUT/POST，改用 Nginx 直连反代 127.0.0.1:9091
  直连写也非 200                          → 后端问题：docker compose logs --tail=60 easy_proxies
  读接口有非 200                          → 先看容器是否在跑、端口是否被占用
TIPS
