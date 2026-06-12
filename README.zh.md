# Loadify — 分布式压测平台

[English](README.md) · **中文**

`loadify` 是一个分布式压测平台,支持 **HTTP/HTTPS、gRPC、WebSocket 和 SSE**,
提供声明式用例构建器、无代码的**多步骤场景编排**(按比例混合流量 + 接口串联传参)、
结构化的**响应校验**(状态码 / 响应体 / JSON 路径)、内嵌 **JavaScript(goja)脚本**、
实时与历史看板、明暗主题、**JWT/RBAC + 飞书 OAuth 登录**,
以及 Docker Compose / Kubernetes(Helm)部署。

## 组成

| 二进制         | 职责                                                      |
|----------------|-----------------------------------------------------------|
| `apisrv`       | 对外 REST + WebSocket API、鉴权、元数据、指标查询          |
| `coordinatord` | 任务调度:工作节点注册、VU 分片、指标聚合                   |
| `workerd`      | 无状态压力生成器(goroutine VU 池、各协议驱动)              |
| `loadifyctl`   | 命令行工具,在终端 / CI 中发起压测                         |
| `web/`         | Next.js 看板(实时图表、用例构建器、用户管理)              |

## 数据存储

- **PostgreSQL** — 元数据与 RBAC(用户、用例定义、压测任务)。
- **ClickHouse** — 时序指标(每秒汇总、采样原始行)。

## 能力

- **协议** — HTTP/HTTPS(httptrace 各阶段耗时)、gRPC(基于描述符或全局注册表的
  动态调用)、WebSocket(每 VU 持久连接)、SSE(事件流)。
- **场景编排(无代码)** — **顺序模式**把多个 HTTP 步骤串起来,从某一步的 JSON
  响应里提取字段存为 `{{变量}}` 供后续步骤使用(如登录→拿 token 调下一个接口);
  **比例模式**按权重混合多个接口,模拟真实流量配比。启动时编译为脚本,复用脚本引擎。
- **校验点** — 对每个请求按 状态码 / 响应体 / JSON 路径 做断言,支持
  `等于/不等于/大于/小于/≥/≤/包含/存在`;未通过计为错误,并在实时日志里显示
  具体是哪条断言、实际值是什么。
- **脚本** — 用 goja 写 `iteration()` 函数,调用注入的 `http` API,按负载场景运行
  并产出逐次迭代的指标。
- **分布式** — coordinator 把压力曲线分片到各 worker,精确合并每秒 HdrHistogram,
  并推送实时 tick;apisrv 经 WebSocket 转发给浏览器。历史曲线从 ClickHouse 查询。
- **鉴权** — 本地邮箱/密码(bcrypt)与飞书 OAuth 登录、HS256 JWT,
  以及 `viewer < operator < admin` 三级权限。
- **SLA 阈值** — k6 风格的达标判定(p50/p90/p95/p99、错误率、QPS),压测结束时评估,
  任一不达标则任务判失败。
- **调度** — 容量感知准入:集群达到并发上限或 worker CPU 饱和时任务排队,
  有空槽时排空。支持周期性**定时任务**(多副本安全抢占)与每秒序列的 **CSV 导出**。
- **前端(Next.js)** — 中英文界面切换(默认中文)、明暗主题;结构化 HTTP 请求与
  阶梯压力曲线构建器(带曲线预览);实时图表带十字线联动;可折叠的响应日志(支持
  只看错误);并排的结果对比(差值色标)。

## 快速开始(Docker Compose)

```bash
docker compose -f deploy/compose/docker-compose.yml up --build --scale workerd=2
# 界面: http://localhost:3000  (admin@loadify.local / admin12345)
# API:  http://localhost:8080
```

用命令行发起一次压测:

```bash
go run ./cmd/loadifyctl \
  --api http://localhost:8080 \
  --email admin@loadify.local --password admin12345 \
  --url http://echo-target:8088/ --vus 50 --duration 15s
```

### 飞书登录配置

在 `deploy/compose/` 下新建 `.env`(已被 gitignore,**切勿提交真实密钥**,
可参考 `.env.example`):

```env
LOADIFY_FEISHU_APP_ID=cli_xxx
LOADIFY_FEISHU_APP_SECRET=xxx
LOADIFY_FEISHU_REDIRECT_URL=http://localhost:8080/api/v1/auth/feishu/callback
LOADIFY_FRONTEND_URL=http://localhost:3000
LOADIFY_API_BASE=http://localhost:8080
```

回调地址要与飞书开放平台「安全设置 → 重定向 URL」里登记的**完全一致**,
注意是 API 端口 `8080`(不是前端的 `3000`)。配好后重启 apisrv,启动日志会打印
`feishu login enabled`;访问 `GET /api/v1/auth/config` 应返回 `{"feishu_enabled":true}`。

## 面向智能体 / 自动化

loadify 既能被人使用,也能被自动化智能体驱动,三种等价入口:

- **MCP 服务**(`loadify-mcp`)— 基于 Model Context Protocol(stdio)暴露工具
  (`loadify_quick_run`、`loadify_run_status`、`loadify_list_workers`)。
- **REST API** — 由 `GET /openapi.yaml` 提供机器可读的 OpenAPI 规范。
- **命令行**(`loadifyctl`)— 一条命令完成 创建 → 运行 → 等待 → 汇总,适合 CI。

## 开发

```bash
make build        # 编译全部 Go 二进制到 ./bin
make test         # go test -race ./...
make vet          # go vet ./...
make web-install  # 安装前端依赖
make web-build    # 构建 Next.js 前端
make proto        # 重新生成 gRPC stub(需要 buf + protoc 插件)
```

`api/gen/` 下的生成代码已 gitignore,CI 用 `buf generate` 重新生成。

## 配置(环境变量,`LOADIFY_` 前缀)

| 变量 | 默认值 | 使用方 |
|------|--------|--------|
| `LOADIFY_API_HTTP_ADDR` | `:8080` | apisrv |
| `LOADIFY_COORDINATOR_GRPC` | `coordinatord:7070` | apisrv, workerd |
| `LOADIFY_POSTGRES_DSN` | `postgres://loadify:loadify@postgres:5432/loadify?sslmode=disable` | apisrv |
| `LOADIFY_CLICKHOUSE_ADDR` | `clickhouse:9000` | apisrv, coordinatord |
| `LOADIFY_JWT_SECRET` | `dev-insecure-secret-change-me` | apisrv |
| `LOADIFY_JWT_TTL_HOURS` | `24` | apisrv |
| `LOADIFY_FEISHU_APP_ID` / `_APP_SECRET` / `_REDIRECT_URL` | — | apisrv |
| `LOADIFY_FRONTEND_URL` | `http://localhost:3000` | apisrv(OAuth 跳回) |
| `LOADIFY_ADMIN_EMAIL` / `_ADMIN_PASSWORD` | — | apisrv(引导管理员) |
| `LOADIFY_WORKER_REGION` | `default` | workerd |

详细的 Kubernetes(Helm)部署、目录结构等见 [英文 README](README.md)。
