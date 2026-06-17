# 测试与运维约定

本文记录 loadify 的端到端测试约定,以及几项需要运维知晓的安全/数据策略决策。

## 端到端冒烟测试(Playwright)

### 为什么存在
单元测试和 `tsc` 抓不到"一处产出、另一处消费"这类缝隙 bug:在 A 页复制一个 ID/令牌,到 B 页用不了;生成的令牌读不回来。历史上这类问题反复出现(短/长 ID 不匹配等),所以专门用一组真实浏览器流程把闭环兜住。

### 怎么跑
```bash
make e2e-up        # docker compose 起全栈(postgres/clickhouse/coordinator/apisrv/worker/web/echo-target)
make e2e           # 安装依赖 + 浏览器,跑 web/e2e 下的冒烟用例
make e2e-down      # 拆栈(含数据卷)
```
CI 在 `.github/workflows/e2e.yml` 自动做同样的事(PR 与 main)。

### 覆盖的闭环(`web/e2e/smoke.spec.ts`)
- 每个主页面能渲染(`.nav` 挂载,不是白屏/崩溃)。
- **永久 API 令牌**:设置页能显示、能完整复制(`lfy_` 开头的完整值)、能重置出新值。
- **运行 ID 闭环**:详情页能看到 ID chip → 把**完整 UUID** 粘进结果对比的选择器 → 对比表能渲染(即历史上短/长 ID 不匹配的那个 bug)。

`web/e2e/global-setup.ts` 用 bootstrap 管理员登录、通过公开 API 播种两次已完成的压测(对比页需要 A/B),并把会话写进 Playwright 的 storageState(应用把 JWT 存在 localStorage)。

### 加新流程的判断标准
只要一个值在某处产出、在另一处消费(复制/导出/分享/ID/令牌/下载),就值得加一条 e2e:断言"闭环成立",而不只是"页面打开"。

## 安全策略(运维须知)

- **会话 JWT 存在浏览器 localStorage**:便于无 cookie 的跨域 API 调用,但 XSS 可读。CSP 已收紧;迁移到 httpOnly cookie 是后续项(需同步调整 WS 握手与分享令牌路径)。
- **永久 API 令牌按明文可逆存储**:产品要求"随时可在设置查看"(飞书应用密钥式),这就要求可逆存储,无法只存哈希。**因此该令牌与密码同等敏感**:数据库静态加密(at-rest encryption)很重要;泄露后用"重置"即可吊销旧令牌。列表接口与日志不会输出该令牌。
- **登录限流**:按客户端 IP 固定窗口限流(默认 10 次/分钟),在做 bcrypt 之前拦截,减缓撞库/暴力破解;键取 `X-Forwarded-For` 首跳或 `RemoteAddr`(部署应置于已知代理/ingress 之后)。
- **节点 CPU 保护阈值**:`LOADIFY_WORKER_CPU_MAX_PCT` 默认 85,按"占总容量的百分比"判定(已对多核归一化);超阈值的节点不接新任务,任务进入排队。设为 0 关闭。

## 数据保留策略

指标存储(ClickHouse)已配置 TTL:
- `rollup_1s`(每秒滚动聚合):保留 **90 天**。
- `samples`(逐请求采样,含响应体片段):保留 **7 天**。

含义:超过保留期的压测在图表/对比里会显示空数据(并非 bug,是已过期)。如需更长留存,可在 ClickHouse 上调大 TTL;长期留存建议另建更粗粒度(如 1 分钟)的下采样表,避免每秒粒度长期膨胀(尚未实现,作为后续项)。

## 准入与队列的可恢复性

准入队列以 Postgres 为准:每个待派发/运行中的压测会把其 StartRun 负载(`runs.dispatch_payload`)持久化。协调器重启丢失内存队列后,apisrv 的 reaper 会用该负载重放(`StartRun` 对 run id 幂等),而不会把没跑过的排队任务谎报成"已完成"。无法重放且无任何指标的任务会被诚实地标为 `aborted`。
