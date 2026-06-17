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

### 1) 中心机:起 Postgres、ClickHouse、coordinatord、apisrv、web
可直接用 `deploy/compose/docker-compose.yml`(单机起全套),或把 coordinatord/apisrv 单独部署。确保 **coordinatord 的 7070 对各 worker 机器可达**(compose 已 `ports: 7070:7070`)。

### 2) 构建 worker 镜像(或二进制)
镜像:
```bash
docker build -f deploy/docker/Dockerfile --build-arg SERVICE=workerd -t loadify-workerd:latest .
```
或二进制:
```bash
go build -o workerd ./cmd/workerd
```

### 3) 每台压力机上启动一个(或多个)workerd
```bash
docker run -d --name loadify-workerd \
  -e LOADIFY_COORDINATOR_GRPC="<协调器主机或IP>:7070" \
  -e LOADIFY_WORKER_REGION="bj-1" \
  -e LOADIFY_WORKER_HTTP_ADDR=":8090" \
  loadify-workerd:latest
```
在每台服务器重复这一步(`LOADIFY_WORKER_REGION` 可按机房/可用区区分)。一台机器上想多开就多跑几个容器。

> ⚠️ **`docker compose ... --scale workerd=N` 只在同一台机器上起 N 个**,不是多机。多机务必在每台机器分别启动 workerd 并指向同一个协调器。

### 4) 验证
打开 web 的「工作节点」页,应看到所有机器的 worker 上线(含各自 CPU/内存/区域);或用 CLI 发一次压测确认分片到了多台。

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
