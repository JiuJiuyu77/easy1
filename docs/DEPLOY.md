# 部署指南

本仓库镜像：`ghcr.io/jiujiuyu77/easy1`（由 `.github/workflows/docker-publish.yml` 构建，`linux/amd64` + `linux/arm64`）。

> 宝塔面板（Baota/aaPanel）用户请看 **[宝塔部署教程](./DEPLOY-BAOTA.md)**，里面有反代、HTTPS、目录属主和 GHCR 拉取的处理。

## 1. 选择镜像 tag

| tag | 说明 |
| --- | --- |
| `latest` | 跟随 `main` 的最新构建，适合自用/跟进 |
| `main` | 同上，分支名 tag |
| `sha-<7位>` | 固定到某次提交，适合可复现部署 |
| `1.2.3` / `1.2` | 仅当推送 `v*` 标签时生成 |

```bash
# 私有包先登录（公开包可跳过）
echo "$GITHUB_TOKEN" | docker login ghcr.io -u <你的GitHub用户名> --password-stdin
docker pull ghcr.io/jiujiuyu77/easy1:latest
```

固定版本部署时用 `docker-compose.yml` 里的环境变量覆盖：

```bash
EASY_PROXIES_IMAGE=ghcr.io/jiujiuyu77/easy1:sha-7d8fa1f docker compose up -d
```

## 2. 方式 A：docker compose + host 网络（推荐）

节点池会给每个节点分配独立端口（从 `24000` 起），host 网络省掉逐个映射。

```bash
git clone https://github.com/JiuJiuyu77/easy1.git && cd easy1
mkdir -p data logs && sudo chown -R 10001:10001 data logs   # 与 compose 里的 user 一致
docker compose up -d
docker compose logs -f easy_proxies
```

- `docker-compose.yml` 默认 `image: ${EASY_PROXIES_IMAGE:-ghcr.io/jiujiuyu77/easy1:latest}`、`network_mode: host`、挂载 `./data:/etc/easy_proxies` 与 `./logs:/app/logs`。
- 用当前用户跑（避免 chown 10001）：`UID=$(id -u) GID=$(id -g) docker compose up -d`。

## 3. 方式 B：端口映射（macOS 等不能用 host 网络）

注释掉 `network_mode: host`，启用 `ports`：

```yaml
    ports:
      - "2323:2323"                    # pool / hybrid 共享入口
      - "9091:9091"                    # WebUI / API
      - "24000-24200:24000-24200"      # multi-port / hybrid 每节点端口
      # optional: 旧版来源 IP 粘性入口（sticky.enabled=true 时，默认 listener.port+1）
      - "2324:2324"
```

端口范围按节点数量调整；超出范围时端口分配会失败或落到未映射端口上。

## 4. 方式 C：docker run

```bash
mkdir -p data logs && chmod -R u+w data logs
docker run -d --name easy_proxies --restart unless-stopped \
  --user $(id -u):$(id -g) \
  -v $(pwd)/data:/etc/easy_proxies \
  -v $(pwd)/logs:/app/logs \
  --network host \
  ghcr.io/jiujiuyu77/easy1:latest
```

## 5. 方式 D：源码运行

```bash
go run -tags "with_utls with_quic with_grpc with_wireguard with_gvisor with_clash_api" \
  ./cmd/easy_proxies -config config.yaml
```

需要 Go `1.26.10`（见 `go.mod`）；要流量统计（Clash API）就必须带上述 tags。

## 6. 首次启动

1. 打开 `http://<服务器>:9091`（`management.listen` 决定监听地址；默认无密码）。
2. **空配置也能启动**：没有节点时只会起管理端，日志提示 `WebUI is available for importing nodes.`。
3. 进「导入节点」粘贴订阅链接或 URI，测速后把可用节点加入池 —— 节点池是监听端口的唯一来源，池里有节点才会真正开监听。
4. 想让配置直接生效也可以编辑 `data/config.yaml`（`nodes` / `nodes_file` / `subscriptions`）。

## 7. data 目录里有什么（全部要备份）

| 文件 | 作用 |
| --- | --- |
| `config.yaml` | 主配置（自动生成于首次启动） |
| `nodes.txt` | 订阅/文件来源的节点缓存 |
| `node_ports.json` | 节点 → 本地端口映射，删掉会导致端口重排 |
| `managed_nodes.db` | WebUI 节点池（sqlite，可能伴随 `-wal`/`-shm`） |
| `managed_nodes.json` | 旧版节点池，仅迁移用 |

> 目录必须以容器用户身份可写；只读挂载会让节点池初始化失败。

## 8. 端口一览

| 端口 | 用途 | 开关 |
| --- | --- | --- |
| `2323` | pool / hybrid 共享入口（HTTP+SOCKS5） | `listener.port` |
| `24000+` | 每节点独立入口 | `multi_port.base_port`（multi-port/hybrid） |
| `9091` | WebUI + REST API | `management.listen` |
| `listener.port+1` | 旧版来源 IP 粘性入口 | `sticky.enabled` |
| `geoip.port` | GeoIP 路由入口（可选） | `geoip.enabled` |

## 9. 反向代理 / HTTPS

管理端支持会话 Cookie 与 `Authorization: Bearer <密码>` 两种鉴权，放在 Nginx/Caddy 后面即可：

```nginx
location / {
    proxy_pass http://127.0.0.1:9091;
    proxy_http_version 1.1;
    proxy_set_header Upgrade $http_upgrade;   # /api/traffic、/api/job-events 用 SSE
    proxy_set_header Connection "upgrade";
    proxy_buffering off;                       # 否则 SSE 会被缓冲
    proxy_read_timeout 3600s;
}
```

`management.password` 为空表示**任何人可访问**，公网暴露前务必设置。

## 10. 新功能怎么用

**前置代理（链式路由）**：设置页「前置代理」→ 添加，填跳板 URI（最多 8 跳）→ 保存后先测速再应用；导入节点时可选「前置代理」，该节点就会经过这条链出网。等价配置：

```yaml
chain_profiles:
  - id: jp-relay
    name: 日本中转
    enabled: true
    hops:
      - uri: socks5://user:pass@1.2.3.4:1080
nodes:
  - name: target
    uri: vless://uuid@example.com:443?security=tls#target
    chain_profile_id: jp-relay
```

**填错最常见的样子**（报 `vless uri missing uuid in userinfo` 就是这个）：

| 症状 | 原因 | 正确写法 |
| --- | --- | --- |
| 只粘了 `remarks=…&tls=1&pbk=…` 这段 | 分享文本里 `vless://uuid@host:port?` 前缀丢了 | 从客户端/订阅里复制**整行** URI |
| `vless://host:port?…` | `@` 前没有 UUID | `vless://<UUID>@host:port?…` |
| `peer=`、`remarks=`、`tls=1`、`xtls=2`、`tfo`、`udp` | 这些是客户端私有参数，不是标准 VLESS 参数 | 名称用 `#备注`；TLS 用 `security=tls|reality`；服务器名用 `sni=`；其余可删 |
| reality 节点报 `requires public_key (pbk)` | 少了 `pbk` | 补 `security=reality&pbk=<base64 32字节>&sid=<短ID>` |

> 「测试连接」是**经这条链访问 GitHub**（`https://github.com/`），需要 2xx 且页面正常，所以它验证的是真实可用性；如果服务器访问不了 GitHub，请先确认出口网络。
>
> 粘贴的 URI 会自动修：URL 编码（`%2B`/`%2F`/`%3D`）、换行/零宽字符、客户端追加的 `?remarks=…` 都会被还原成可用的 base64。

一个可用的 reality 例子：

```
vless://<UUID>@<地址>:443?security=reality&pbk=<公钥>&sid=<短ID>&fp=chrome&type=tcp&sni=<伪装域名>#韩国住宅IP
```

**会话路由**：需要 `pool` 或 `hybrid` 模式 + 共享入口用户名。设置页「会话路由与粘性」可开关、调 TTL（默认 24h）与上限（默认 10000）；「会话路由」页生成 `用户名.sessionID` 的 HTTP/SOCKS5 链接与 curl。停用后按普通用户名调度。

## 11. 目标域名被 RST 时怎么定位

App 走代理访问某个域名时被 `RST`，先用脚本判断是哪一跳断的：

**首选：面板自带的探测接口**（零依赖，容器里没有 openssl/curl 也能用）：

```bash
# 同一个 SNI 打两跳：面板进程直连 + 经面板池口 CONNECT
curl -s "http://127.0.0.1:9091/api/probes/sni?target=目标域名&sni=目标域名" | python3 -m json.tool
# 指定目的 IP，观察"IP 可达但该 SNI 被重置"
curl -s "http://127.0.0.1:9091/api/probes/sni?target=1.1.1.1&sni=目标域名"
```

返回 `direct` / `via_panel` 两跳的 `ok`、`ms`、`error` 和一句 `verdict`。有管理密码时加
`-H "Authorization: Bearer <密码>"`（或先登录拿 Cookie）。

**在主机上定位拦截层**（宝塔终端，root；脚本可单独复制到任意目录运行）：

```bash
sh find_sni_filter.sh bldimg.com
```

依次检查：本机按域名的包过滤（iptables/nft 字符串规则）、hosts/dnsmasq 覆盖、
第二个抢流量的代理/DPI 进程、监听端口、同 IP 双 SNI 的切断耗时、以及**明文 80 端口取证**。

> **关键判据**：若明文 80 端口返回 **403 且页面含「备案」字样**，这是**云厂商网络层**的域名拦截
> （阿里云等对未备案域名会这样返回），不是本面板、也不是机场返回的 —— 本仓库代码里没有任何
> 返回 403/备案页的逻辑（`grep -rn "备案\|403" internal` 为空）。这种情况下：
> 换一台非大陆/非该厂商的机器跑面板，或完成域名备案，或让客户端不直连该 SNI。

**备选：脚本方式**（需要在 git 仓库目录里执行；部署目录若只有 compose 文件则没有该脚本）：

```bash
sh scripts/sni_diag.sh <目标域名>            # 本机直连的 SNI 矩阵
sh scripts/sni_diag.sh <目标域名> 2323       # 再经面板池口（CONNECT 隧道）
```

- **本机直连就 RST、换个 SNI 就正常** → 链路/运营商按 SNI 阻断，与本面板无关。
  这类情况只有两条路：换出口线路，或让客户端**不要直连目标 SNI**——即客户端以
  HTTP 代理的绝对 URL 形式请求（`GET https://目标/路径`），由面板侧发起 TLS。
  本项目的 mixed 监听（2323）原生支持这种非隧道转发。
- **本机直连正常、经面板 RST 且耗时极短（<50ms）** → 面板进程本地拒绝，需要排查
  服务器上的其它组件（反向代理、面板插件、云厂商网络策略）。
  本仓库自身**不含**按域名 reject 的规则（`Reject`/`geosite`/`rule_set`/
  `domain_suffix` 全仓无命中），可用 `grep -rn "Reject\|geosite\|rule_set" internal` 复核。

## 12. 升级与回滚

```bash
docker compose pull && docker compose up -d --force-recreate
```

- 升级前备份整个 `data` 目录（含 `node_ports.json`、`managed_nodes.db`）。
- 回滚：`EASY_PROXIES_IMAGE=ghcr.io/jiujiuyu77/easy1:sha-<旧提交> docker compose up -d`。
- WebUI「设置 → 备份」支持本地 zip 与 WebDAV 目标，可在另一台实例恢复。

## 13. 排障

| 现象 | 原因与处理 |
| --- | --- |
| 启动即退出，`Cannot create config.yaml` | 挂载目录不可写：`chown -R <容器UID> data` |
| `nodes.txt is a directory` | 文件级挂载但宿主机文件不存在，Docker 建了目录：删掉后 `touch` 或改为目录挂载 |
| 端口占用导致启动失败 | 会自动重分配并重试；仍冲突时改 `multi_port.base_port` / `listener.port` |
| WebUI 导入成功但没监听 | 节点只在候选列表，需加入池（导入时勾选自动加入或手动提升） |
| 节点端口与 WebUI 不一致 | 删了 `node_ports.json` 或手工改过 `config.yaml` 端口；重启后会按池重排 |
| `sticky.port conflicts with listener.port` | 粘性端口不能复用共享入口端口，改 `sticky.port` |
| 日志为空 | `log.output=stdout` 时只看容器日志；写文件时确认 `./logs` 已挂载 |
