# Loadify 能力差距与路线图(对标 JMeter / 阿里云 PTS)

> 诚实的现状盘点:已具备的能力、与成熟压测产品的差距、以及建议的演进顺序。
> 更新于 2026-06。

## 已具备(且部分超出 JMeter 默认体验)

- 多协议驱动:HTTP/HTTPS、gRPC(动态调用)、WebSocket、SSE
- 闭环(VU)与开环(到达率/QPS)两种负载模型——JMeter 原生只有线程组闭环模型,
  开环要装 Throughput Shaping Timer 插件
- 多步骤场景:顺序串联(JSON 提取 → {{变量}} 传参)与按权重流量配比
- 逐请求校验点(状态码/响应体/JSON 路径)+ 