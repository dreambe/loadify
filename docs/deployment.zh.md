# 部署指南(多机 / 多节点)

loadify 的工作节点(workerd)是**无状态压力机**:它主动拨号连接协调器(coordinatord)的 gRPC 端口,由协调器把压测的 ramp 分片下发给所有在线、健康的 worker。**因此原生支持把 worker 部署到任意多台服务器**——只要每台都能连到协调器的 gRPC 端口即可。

## 组件与端口

| 组件 | 角色 | 端口 | 副本 |
|---|---|---|---|
| `apisrv` | REST/WS API + 前端的后端 | 8080(HTTP) | 可多副本(无状态) |
| `coordinatord` | 调度/准入,分片下发给 worker | **7070(gRPC)**、7071(健康/指标) | **单副本**(run 状态在内存) |
| `workerd` | 压力机,执行压测 | 8090(健康) | **多副本 / 多机** |
| `web` | Next.js 前端 | 3000 | 可多副本 |
| Postgres | 元数据(用例/运行/用户) | 5432 | 外部托管 |
| ClickHouse | 指标(滚动聚合/采样) | 9000 | 外部托管 |

> loadify **不自带**有状态数据库:Postgres 与 ClickHouse 需你自行提供(托管服务或集群内 operator)。

## 数据流(谁连谁)

```
浏览器 ─→ web(3000) ─→ apisrv(8080) ─┬─→ Postgres(5432)
                                      ├─→ ClickHouse(9000)
                                      └─→ coordinatord(7070, gRPC)
workerd(多台) ─→ coordinatord(7070, gRPC)   # worker 主动外连协调器
workerd(多台) ─→ 被压测的目标系统            # 每台 worker 都要能连到目标
```

关键点:**worker 主动连协调器**(不是协调器连 worker),所以 worker 放在哪台机器都行,只要它能访问 `coordinatord:7070`,且能访问被压测目标。

---

## 方式 A:多台服务器(裸 Docker / 二进制)

适合没有 Kubernetes、手动把 worker 铺到多台机器的场景。

### 1) 中心机:起全套(Postgres、ClickHouse、coordinatord、apisrv、web,含一个本机 worker)
```bash
cd deploy/compose
cp .env.example .env        # 按需配置(见 .env.example)
docker compose up -d        # 不带服务名 = 起全套
```
或把 coordinatord/apisrv 单独部署。确保 **coordinatord 的 7070 对各 worker 机器可达**(compose 已 `ports: 7070:7070`)。

> 中心机用 `docker compose up -d`(主栈,全套);压力机用 `docker compose -f docker-compose.worker.yml up -d`(只起 worker)。**两者不要混用**,详见第 3 步的警告。

### 2) 构建 worker 镜像(或二进制)
镜像:
```bash
docker build -f deploy/docker/Dockerfile --build-arg SERVICE=workerd -t loadify-workerd:latest .
```
或二进制:
```bash
go build -o workerd ./cmd/workerd
```

### 3) 每台压力机上启动 workerd —— **用 worker 专用 compose**

> 🚨 **最容易踩的坑:worker 机上不要用主栈 `docker-compose.yml`。**
>
> | 命令 | 实际行为 |
> |---|---|
> | `docker compose up -d workerd` | 用**默认** `docker-compose.yml`(主栈),`workerd` 只是其中一个**服务名**;它会连带起**本地的** coordinatord + clickhouse,且 worker 写死连**本地** coordinator(`coordinatord:7070`)——**于是 worker 注册到了本机、而不是中心机,中心平台的「工作节点」页看不到它**。 |
> | `docker compose -f docker-compose.worker.yml up -d` | 用 **worker 专用文件**:只起 `workerd` 一个容器,连 **`.env` 里指定的中心 coordinator**。**worker 机就该用这条。** |
>
> 记法:**`workerd` 是服务名;`docker-compose.worker.yml` 是文件名(必须 `-f` 指定)**,两者不是一回事。

worker 是**无状态**压力机,机器上**只需要 workerd 这一个容器**,不需要 Postgres/ClickHouse/coordinatord/apisrv/web。

```bash
cd deploy/compose
cp .env.example .env
# 编辑 .env,关键是指向中心机:
#   LOADIFY_COORDINATOR_GRPC=<中心机IP>:7070   # 必填,缺了会直接报错
#   LOADIFY_WORKER_ID=worker2                  # 节点名,全局唯一(别和其它机器重名)
#   LOADIFY_WORKER_REGION=bj-1                 # 区域标签,可选
#   受限网络(国内)再打开 .env 末尾的镜像源那几行
docker compose -f docker-compose.worker.yml up -d --build
docker compose -f docker-compose.worker.yml ps     # 应当只有 workerd 一个容器
```

在每台压力机重复这一步,每台给一个**不同的** `LOADIFY_WORKER_ID`。

> ⚠️ 同一台机器**一般无需多开 worker**:单个 workerd 进程会用 Go 协程吃满整机 CPU,横向扩容请**加机器**,不是在一台机器上加进程。真要在同机多开,用 `--scale workerd=N`(仅本机生效,不是多机)。

**改了 `.env` 怎么生效?** 重新跑同一条 `up -d` 命令即可(compose 会重建容器);**别用 `restart`**(它复用旧容器、不读新环境变量)。运行时变量(如 `LOADIFY_WORKER_ID`/`LOADIFY_WORKER_REGION`)不用 `--build`。

或直接 `docker run`(已有镜像时):
```bash
docker run -d --name loadify-workerd --restart unless-stopped \
  -e LOADIFY_COORDINATOR_GRPC="<中心机IP>:7070" \
  -e LOADIFY_WORKER_ID="worker2" \
  -e LOADIFY_WORKER_REGION="bj-1" \
  -e LOADIFY_WORKER_HTTP_ADDR=":8090" \
  loadify-workerd:latest
```

### 4) 验证
打开 web 的「工作节点」页,应看到所有机器的 worker 上线(含各自名字/CPU/内存/区域);或用 CLI 发一次压测确认分片到了多台。

---

## 方式 B:Kubernetes(多节点集群)+ Helm

集群本身是多节点时,workerd 作为 Deployment 多副本,K8s 会把 pod 调度到不同物理节点。

```bash
helm install loadify deploy/helm/loadify \
  --set secret.postgresDSN="postgres://user:pass@pg:5432/loadify?sslmode=disable" \
  --set database.clickhouse.addr="clickhouse:9000" \
  --set secret.jwtSecret="$(openssl rand -hex 32)" \
  --set workerd.replicas=6 \
  --set ingress.enabled=true --set ingress.host=loadify.example.com
```

- **扩 worker**:`--set workerd.replicas=N`,或开自动扩缩 `--set workerd.autoscaling.enabled=true`(`minReplicas`/`maxReplicas`/`targetCPUUtilizationPercentage`)。
- **强制把 worker 铺散到不同物理节点**:用 `values.yaml` 的 `affinity` 设 podAntiAffinity(按 `app.kubernetes.io/component: workerd` 反亲和),或用 `nodeSelector`/`tolerations` 把 worker 钉到压测专用节点池。
- 镜像默认 `ghcr.io/dreambe/loadify-<组件>:<tag>`(见 `image.registry/repository/tag`);如未发布到该 registry,请自建镜像并覆盖 `image.*`。
- coordinatord 在 chart 里固定 `replicas: 1`(见下)。

---

## 网络与安全(务必读)

- ⚠️ **协调器 ↔ worker 的 gRPC 当前是明文(insecure),没有 TLS/鉴权**(`cmd/workerd` 用 `insecure.NewCredentials()`)。**不要把 7070 暴露到公网**。多机部署请把这条链路放在**内网 / VPC / VPN / WireGuard** 之类的可信网络里;跨公网必须自己加 TLS 或用加密隧道。
- worker 机器需要能出站访问:① 协调器 7070;② 被压测目标系统。
- apisrv 的 `LOADIFY_JWT_SECRET` 生产环境务必改成随机值;永久 API 令牌是明文可逆存储(便于在设置页查看),数据库静态加密很重要(见 `docs/testing-and-ops.zh.md`)。
- bootstrap 管理员 `LOADIFY_ADMIN_EMAIL/PASSWORD` 上线后请改。

## 关于"单协调器"与容量

- **coordinatord 是单副本**,所有 worker 连这一个。它的 run/队列状态在内存,但每个待派发/运行中的压测的下发负载会持久化到 Postgres(`runs.dispatch_payload`),协调器重启后由 apisrv 的 reaper **重放**,排队任务不会丢、也不会被谎报成"已完成"。目前**还不是 leader 选举的多活 HA**——协调器是控制面的单点,请保证其可用性与网络可达。
- **每节点 CPU 保护阈值**:`LOADIFY_WORKER_CPU_MAX_PCT`(协调器侧,默认 85,按占总容量百分比、已对多核归一化)。某节点 CPU 超阈值就不再接新任务;集群整体满载时新压测进入**排队**,前端会显示「排队中 · 预计…」。设为 0 关闭该保护。

## 相关环境变量(节选)

| 变量 | 用于 | 说明 |
|---|---|---|
| `LOADIFY_COORDINATOR_GRPC` | apisrv, workerd | 协调器 gRPC 地址,如 `coord-host:7070` |
| `LOADIFY_WORKER_REGION` | workerd | 该 worker 的区域标签(机房/AZ) |
| `LOADIFY_WORKER_HTTP_ADDR` | workerd | 健康端口,默认 `:8090` |
| `LOADIFY_MAX_CONCURRENT_RUNS` | coordinatord | 同时并发运行的压测上限(默认 8) |
| `LOADIFY_WORKER_CPU_MAX_PCT` | coordinatord | 节点 CPU 保护阈值(默认 85,0=关) |
| `LOADIFY_POSTGRES_DSN` / `LOADIFY_CLICKHOUSE_ADDR` | apisrv / 两者 | 外部数据库 |
| `LOADIFY_JWT_SECRET` | apisrv | 生产必须改随机值 |

完整变量见 README 的 Configuration 表。

---

## 镜像发布与拉取(传到哪里)

镜像不用手动传。仓库在 GitHub,最省事的方案是 **GHCR(GitHub Container Registry)**:

- 打一个版本 tag 即自动构建并推送(见 `.github/workflows/release.yml`):
  ```bash
  git tag v0.1.0 && git push origin v0.1.0
  ```
  产物:`ghcr.io/dreambe/loadify-<组件>:v0.1.0`(+ `:latest`),**多架构(amd64 + arm64)**,
  用 `GITHUB_TOKEN` 推送、**无需额外密钥**;公开仓库免费。
- 首次推送后到 GitHub → Packages 把这几个 package 设为 **public**(否则拉取需登录)。
- 组件:`loadify-apisrv` / `loadify-coordinatord` / `loadify-workerd` / `loadify-web`。

部署时指向它(Helm 默认就是 `ghcr.io/dreambe/loadify`):
```bash
helm upgrade --install loadify deploy/helm/loadify \
  --set image.registry=ghcr.io --set image.repository=dreambe/loadify --set image.tag=v0.1.0
```

### 国内部署(GHCR 拉取慢/不稳)
两种办法,任选其一:
1. **重打 tag 推到国内 registry**(阿里云 ACR 个人版免费、腾讯 TCR、华为 SWR,或自建 Harbor):
   ```bash
   for c in apisrv coordinatord workerd web; do
     docker pull  ghcr.io/dreambe/loadify-$c:v0.1.0
     docker tag   ghcr.io/dreambe/loadify-$c:v0.1.0 registry.cn-hangzhou.aliyuncs.com/你的命名空间/loadify-$c:v0.1.0
     docker push  registry.cn-hangzhou.aliyuncs.com/你的命名空间/loadify-$c:v0.1.0
   done
   ```
   然后 `--set image.registry=registry.cn-hangzhou.aliyuncs.com --set image.repository=你的命名空间/loadify`。
2. 给 Docker / containerd 配 registry 镜像加速 / 代理。

> ⚠️ **web 镜像的坑**:`NEXT_PUBLIC_API_BASE` 在**构建时**烘焙进前端包。要么用下面的 nginx
> **同源**方案(浏览器同域访问 `/api`,把 API base 设成你的对外域名),要么用你的 API 公网地址
> **重新构建 web 镜像**(`--build-arg NEXT_PUBLIC_API_BASE=https://loadify.example.com`,
> 或在仓库 Variables 里设 `LOADIFY_API_BASE` 后再打 tag)。

---

## 用 nginx 做对外入口(反向代理)

典型拓扑:**边缘机**跑 nginx + `web`(3000)+ `apisrv`(8080);**其它服务器**只跑 `workerd`。
**worker 不经过 nginx**——它直连协调器的 gRPC(7070,走内网),见下方说明。

把下面这段放到 `/etc/nginx/conf.d/loadify.conf`(你的 `nginx.conf` 已 `include conf.d/*.conf`,
**主文件不用改**)。`web`/`apisrv` 在本机就用 `127.0.0.1`,在别的机器就填其私网 IP。

```nginx
upstream loadify_web { server 127.0.0.1:3000; }
upstream loadify_api { server 127.0.0.1:8080; }

# WebSocket 升级所需
map $http_upgrade $connection_upgrade { default upgrade; '' close; }

server {
    listen 80;
    server_name loadify.example.com;     # ← 改成你的域名

    client_max_body_size 32m;            # 导入/脚本可能较大

    # API + 实时 WebSocket(/api/v1/runs/<id>/live)
    location /api/ {
        proxy_pass http://loadify_api;
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection $connection_upgrade;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_read_timeout 3600s;        # 实时长连接
        proxy_buffering off;             # 实时推送不缓冲
    }

    location = /healthz     { proxy_pass http://loadify_api; }
    location = /openapi.yaml { proxy_pass http://loadify_api; }

    # 前端(Next.js)
    location / {
        proxy_pass http://loadify_web;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }
}
```

要点:
- **同源**:浏览器访问 `https://loadify.example.com`,API 走同域 `/api`,**无跨域问题**。
  对应地,web 构建时的 `NEXT_PUBLIC_API_BASE` 设为 `https://loadify.example.com`(见上文 web 镜像坑)。
- **WebSocket**:实时图表用 WS(`/api/v1/runs/<id>/live`),所以 `/api/` 必须带 `Upgrade`/
  `Connection` 头并放大 `proxy_read_timeout`,上面已包含。
- **X-Forwarded-For**:apisrv 的登录限流按它取客户端 IP,nginx 已正确透传。
- **TLS**:生产建议加 443(用你 `nginx.conf` 注释里的 TLS 模板,把 `location /` 换成上面的三段
  `location`),并 80→443 跳转。

### worker 所在的其它服务器(不经 nginx)
- worker **主动外连**协调器 gRPC `7070`,这是 **TCP/gRPC,不要用 nginx 的 http 反代**。
- 让各 worker 机器通过**内网 / VPC / VPN** 直连协调器的 `7070`。
- ⚠️ 该 gRPC 当前**明文无鉴权**,**切勿把 7070 暴露公网**(详见上文「网络与安全」)。
- worker 还需能直连**被压测目标**。
