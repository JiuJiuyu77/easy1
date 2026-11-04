# 宝塔面板部署教程

以宝塔 Linux 面板 9.x 为例，在**已有宝塔**的服务器上部署 easy1（Docker 方式）。
全程两条路：**A. 终端 + compose（推荐）**，**B. 面板 Docker 界面点选**。反代与 HTTPS 两种都通用。

---

## 0. 准备

1. 面板 → 软件商店 → 安装 **Docker**（宝塔会同时装好 compose 插件）。
2. 终端确认：`docker version && docker compose version`（老面板可能是 `docker-compose version`，下文相应替换）。
3. 规划端口（先确认没被占用：`ss -lntp | grep -E '2323|9091|2400'`）：

| 端口 | 用途 | 是否必须对外开放 |
| --- | --- | --- |
| 9091 | WebUI / API | 反代方案**不必**（只给 127.0.0.1） |
| 2323 | pool / hybrid 共享代理入口 | 是（给客户端用） |
| 24000-24200 | 每节点独立入口（multi-port/hybrid） | 是（按节点数扩范围） |
| 2324 | 旧版来源 IP 粘性入口（`sticky.enabled` 时） | 需要时放行 |

> 宝塔面板「安全」和云厂商**安全组**是两套，都要放行。

---

## 1. 方式 A：终端 + docker compose（推荐）

### 1.1 建目录

```bash
mkdir -p /www/wwwroot/easy1/data /www/wwwroot/easy1/logs
cd /www/wwwroot/easy1
```

### 1.2 写 `docker-compose.yml`

宝塔面板 → 文件 → 进入 `/www/wwwroot/easy1` → 新建文件 `docker-compose.yml`，粘贴：

```yaml
services:
  easy_proxies:
    image: ${EASY_PROXIES_IMAGE:-ghcr.io/jiujiuyu77/easy1:latest}
    container_name: easy_proxies
    restart: unless-stopped
    network_mode: host          # 省去逐个映射节点端口
    user: "10001:10001"         # 与下面 chown 的 UID/GID 保持一致
    volumes:
      - ./data:/etc/easy_proxies
      - ./logs:/app/logs
```

### 1.3 解决目录属主

容器以非 root 运行，`data`/`logs` 必须可写，否则启动即报 `Cannot create config.yaml`：

```bash
chown -R 10001:10001 /www/wwwroot/easy1/data /www/wwwroot/easy1/logs
```

不想改属主？把 compose 里的 `user` 换成宝塔的 www 用户：
`uid=$(id -u www)`（通常是 1000），写成 `user: "1000:1000"`，再 `chown -R www:www data logs`。

### 1.4 登录 GHCR 并启动

私有包需要登录（公开包可跳过）。**令牌用 GitHub PAT，勾 `read:packages`**：

```bash
echo "<你的PAT>" | docker login ghcr.io -u <你的GitHub用户名> --password-stdin
cd /www/wwwroot/easy1 && docker compose up -d
docker compose logs -f easy_proxies      # 看到 management listen 即成功
```

**国内拉取慢/超时**（ghcr.io 不走宝塔的 Docker Hub 加速）：
- 直接重试几次，或用 `docker pull ghcr.io/jiujiuyu77/easy1:latest` 单独拉；
- 或者在能联网的机器上 `docker pull` 后 `docker save ghcr.io/jiujiuyu77/easy1:latest | gzip > easy1.tar.gz`，上传到宝塔 `docker load -i easy1.tar.gz`，再 `docker compose up -d`（镜像已在本地就不会再拉）。

### 1.5 验证

```bash
curl -s http://127.0.0.1:9091/api/auth      # 返回 JSON 即 WebUI 已起
ss -lntp | grep -E '2323|9091'              # 端口在听
```

首次启动**没有节点也能跑**，此时只有管理端在听，日志会提示 `WebUI is available for importing nodes.`。

---

## 2. 方式 B：面板 Docker 界面创建

宝塔 → Docker → 容器 → 创建容器：

| 项 | 填法 |
| --- | --- |
| 镜像 | `ghcr.io/jiujiuyu77/easy1:latest`（镜像管理先拉取） |
| 网络 | `host`（或在「端口映射」填 2323、9091、24000-24200） |
| 目录映射 | `/www/wwwroot/easy1/data` → `/etc/easy_proxies`；`/www/wwwroot/easy1/logs` → `/app/logs` |
| 重启策略 | 始终重启 / unless-stopped |
| 用户 | 留空即 root（省事但权限较大）；填 `10001` 则需先 chown 目录 |

命令行等价：

```bash
docker run -d --name easy_proxies --restart unless-stopped \
  --network host \
  -v /www/wwwroot/easy1/data:/etc/easy_proxies \
  -v /www/wwwroot/easy1/logs:/app/logs \
  ghcr.io/jiujiuyu77/easy1:latest
```

---

## 3. 绑定域名 + 反向代理（强烈建议）

直接 IP:9091 裸奔没有 HTTPS，且面板公网暴露风险高。用宝塔反代 + 证书：

1. 网站 → 添加站点，域名填 `proxy.example.com`，PHP 选「纯静态」，根目录随意。
2. 该站点 → 设置 → **反向代理** → 添加：
   - 代理名称：`easy1`
   - 目标 URL：`http://127.0.0.1:9091`
   - 发送域名：`$host`
3. 打开该站点 → 配置文件，在 `#PROXY-START` … `#PROXY-END` 之间补齐（**这三行是必须的**）：

```nginx
    proxy_http_version 1.1;
    proxy_set_header Host $host;                 # 必须：管理端写操作有同源校验
    proxy_set_header X-Real-IP $remote_addr;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_set_header X-Forwarded-Proto $scheme;
    proxy_set_header Upgrade $http_upgrade;      # /api/traffic、/api/job-events 是 SSE/WS
    proxy_set_header Connection "";
    proxy_buffering off;                          # 不关会把实时流量/日志卡住
    proxy_cache off;
    proxy_read_timeout 3600s;
    proxy_send_timeout 3600s;
```

4. 站点 → SSL → Let's Encrypt 申请证书 → 开启「强制 HTTPS」。

要点说明：

- **`proxy_set_header Host $host;` 不能省**：管理端的写操作会校验 Origin/Referer 与 Host 是否同源，丢了 Host 会出现「保存失败/403」。
- **`proxy_buffering off` 不能省**：否则仪表盘实时流量、日志控制台、任务进度（SSE）不刷新。
- **宝塔默认的静态资源规则会截胡 `/assets/`**。宝塔建站默认带这两段（正则 location 优先级高于 `location /`）：

```nginx
location ~ .*\.(gif|jpg|jpeg|png|bmp|swf)$ { expires 30d; }
location ~ .*\.(js|css)?$                  { expires 12h; }
```

  它们没有 `proxy_pass`，于是 `/assets/js/core.js`、`/assets/css/base.css` 会去**站点根目录**找文件并 404，
  表现就是「页面只有裸 HTML：没有样式、图标是黑块、弹窗和表单全铺开」。把它们改成反代，或直接删掉：

```nginx
location ~ .*\.(js|css|png|svg|gif|jpg|jpeg|ico)$ {
    proxy_pass http://127.0.0.1:9091;
    proxy_set_header Host $host;
    proxy_buffering off;
    expires -1;
}
```

- 反代生效后，WebUI 只监听 127.0.0.1:9091，安全组里**不用**再开放 9091。

改完执行 `nginx -t && nginx -s reload`（宝塔里点「保存」会自动重载），然后**强刷浏览器**（iOS Safari 长按刷新 → 无痕窗口最稳）。

---

## 4. 第一次进去要做的三件事

1. **设置访问密码**：设置 → 基础运行 → 管理密码（`management.password`）。为空表示任何拿到地址的人都能操作，反代到公网前务必设置。
2. **导入节点**：导入节点 → 粘贴订阅/URI → 测速 → 勾选「测速成功后自动加入节点池」→ 导入并测试。
   > 节点池是监听端口的唯一来源，**只进候选不会开监听**。
3. **可选**：设置页「前置代理」加中转链；「会话路由与粘性」开启命名会话，会话路由页生成 `用户名.sessionID` 链接。

---

## 5. 端口与防火墙

- host 网络模式下无需端口映射；如果改成 `ports` 映射，记得把用到的端口段一起映射（`24000-24200:24000-24200`）。
- 宝塔「安全」+ 云安全组都放行 `2323`、`24000-24200`（以及客户端要用的粘性端口）；**9091 不对外**。
- 节点超过 200 个时，扩大 `multi_port.base_port` 起点范围或放行更宽的端口段。

---

## 6. 日常运维

```bash
cd /www/wwwroot/easy1
docker compose logs -f --tail=200 easy_proxies     # 日志
docker compose restart easy_proxies                # 重启
docker compose pull && docker compose up -d        # 升级
EASY_PROXIES_IMAGE=ghcr.io/jiujiuyu77/easy1:sha-ada7392 docker compose up -d   # 回滚到指定提交
```

**备份**：整个 `/www/wwwroot/easy1/data` 目录（含 `config.yaml`、`node_ports.json`、`managed_nodes.db`）——宝塔「计划任务 → 备份网站/目录」可加一条定时打包。也可用面板内「设置 → 备份」导到本地或 WebDAV。

---

## 7. 宝塔环境常见报错对照

| 报错/现象 | 原因 | 处理 |
| --- | --- | --- |
| `Cannot create config.yaml (/etc/easy_proxies not writable)` | data 目录属主不对（root 拥有，容器以 10001 跑） | `chown -R 10001:10001 data logs`，或对齐 compose 里的 `user` |
| `nodes.txt is a directory` | 文件级挂载但宿主机文件不存在，Docker 建了目录 | 删掉该目录后 `touch data/nodes.txt`，或改用目录挂载 |
| 拉镜像 `denied` / `unauthorized` | 未登录或 PAT 缺 `read:packages` | 重新 `docker login ghcr.io` |
| 拉镜像超时 | ghcr.io 直连不稳 | 多试几次 / 用其它机器 `docker save` 上传后 `docker load` |
| **页面只有裸 HTML**：无样式、图标黑块、登录框和弹窗全铺在页面上 | `/assets/css/*`、`/assets/js/*` 404（宝塔默认静态正则抢走，见第 3 节） | 把 `location ~ .*\.(js|css|png|svg...)$` 改成反代到 9091 |
| 页面能开，保存设置报错/403 | 反代丢了 Host | 补 `proxy_set_header Host $host;` |
| 仪表盘流量、日志不更新 | 反代缓冲了 SSE | 补 `proxy_buffering off;`、`proxy_read_timeout 3600s;` |
| 导入成功但 WebUI 端口列表为空 | 节点没进池 | 导入时勾选自动加入池，或在候选列表手动提升 |
| 前置代理保存报 `vless uri missing uuid in userinfo` | 节点 URI 缺 `vless://<UUID>@` 前缀（只粘了 `?` 后面的参数） | 复制整行 URI，见 [部署指南](./DEPLOY.md#10-新功能怎么用) |
| `sticky.port conflicts with listener.port` | 粘性端口与共享入口冲突 | 设置页改粘性端口（默认 listener.port+1） |
| 端口被占用启动失败 | 2323/9091/24000 段与面板或其它服务冲突 | 改 `listener.port` / `multi_port.base_port`，并同步防火墙 |
| 面板里容器反复重启 | 目录权限或 sqlite 打不开 | `docker compose logs` 看首行错误，按上面两条处理 |

---

更细的镜像 tag 策略、数据文件清单、非宝塔环境的部署方式见 [部署指南](./DEPLOY.md)。
