# 智力测试 HTTP 提交修复与 Chrome 验收

## 修复与本地验证

- 修复提交 `83f1c97c5` 已推送 fork/main；镜像从该提交的独立 Git archive 构建，没有打包工作区未跟踪文件。
- HTTP 页面没有 `crypto.randomUUID`，旧实现又在提交异常捕获范围外调用它，导致立即测试无反应；保存并开始则把后续提交异常误报为保存失败。
- 新实现优先使用原生 UUID，缺失时用 `crypto.getRandomValues` 生成 UUID v4；不使用弱随机数。保存和提交错误分离，保留不确定提交的幂等键。
- 前端定向 Vitest 18 项、`pnpm run typecheck`、修改文件 ESLint、`pnpm run build` 均通过。Docker 内生产构建通过；有资源分块大小、Browserslist 数据陈旧和 Node 弃用警告。
- 相比上一镜像，无后端源码或数据库迁移变化；本次未重跑完整后端测试。

## 真实 Chrome 验收

使用用户现有 Chrome 扩展连接、登录会话和生产 HTTP 页面，不是模拟 API。

| 操作 | 发布前 | 发布后 |
| --- | --- | --- |
| 立即测试 | 页面不变化；控制台 `TypeError: crypto.randomUUID is not a function` | 真实 POST 返回 202，新增历史记录并显示执行中；运行 #8 成功，约 15.5 秒，回答直接展示 |
| 保存并开始 | 同时提示“计划更新成功”和“保存失败” | 只提示更新成功，自动转到历史页并显示排队记录；真实 POST 返回 202 |
| 重新打开历史 | 原有记录保留 | 新记录仍存在，成功回答内联显示，失败原因独立展示 |

验收复用账号 #39 的现有计划 #1，没有更改题目、模型、定时设置或网关账号调度。新增两条真实测试记录，不删除历史、不自动重试。

保存并开始对应运行 #9 的模型调用约 22.5 秒后失败，错误是 `upstream_error / Upstream request failed`，不是保存失败或 `context canceled`。这次验收证明两个按钮提交正常、记录持久化及成功答案展示，不代表上游每次调用均成功，也不新增对 HTML/移动端的生产验收声明。

## 发布信息

- 目标：homestay 的 `/opt/sub2api`；当前应用 `sub2api-httpfix`，监听 `127.0.0.1:18084`。
- 镜像 `sub2api:intelligence-83f1c97c5`；镜像 ID `sha256:099dea6ed4f8b37f6b8318188d2bb2307aad035e99b81f06618a19ac8b39b444`。
- 上一版镜像 `sub2api:intelligence-e671b4dd5`，容器 `sub2api-next` 停止保留，自动重启策略关闭。
- Compose SHA-256：`92108f037b045cb3ae165adf14c942dda76b6db41b7cd651830bbaf314471d4b`。
- 代理 SHA-256：`2379c2a66562fd680511ee572a8ee9b55fb95e6e6e8851038fceb37b28ae4d74`。
- 发布目录 `/opt/sub2api/releases/intelligence-83f1c97c5/` 保留配置备份、自定义格式数据库备份、备份目录清单和观察证据。
- 数据库备份 SHA-256：`913d2633c346666980e37d6ff8b9f26a7f0be48e3542dd40b6ac9d8cff32805a`，已通过 `pg_restore --list` 验证可读；未执行恢复演练。

候选使用独立数据目录，确认 25 个周期任务 deferred、零 activated，健康、真实登录和管理员接口通过后，仅修改目标 upstream 并平滑 reload。旧目标 worker 消失、旧端口连接数为零、无排队或运行中的测试后，停止上一实例，再激活新实例。未重建 PostgreSQL/Redis，也未重启其他业务。

交接时激活目录由 `umask 077` 创建，应用用户无法遍历，造成后台周期任务激活短暂延迟。发现后仅调整无秘密的控制目录/标记权限，确认全部 25 个任务 activated；没有两个已激活应用并行。不能据此声称整个交接过程完全没有副作用。

## 发布观察

- 连续观察 606 秒、31 个样本，补充观察至后台激活确认后 604 秒。代理与应用 `/health` 全部 200；最大响应耗时分别为 3.5ms、1.0ms。PostgreSQL/Redis 就绪检查全部通过；样本中所有运行容器的启动时间、重启次数及健康状态保持一致。
- 新版本 Chrome 操作期间未捕获新增脚本错误。两个按钮各产生一次真实 POST/202；未以历史错误日志作为修复后新错误。
- 最终应用日志统计快照：200×256、304×4、202×2、401×1、404×1、502×1、429×1。401 为 `/api/v1/auth/me`，404 为不存在的单计划 GET 路径；不能宣称窗口零错误。
- 502 对应 `gpt-5.6-luna` 的普通网关请求：上游返回 404，排除账号后无法再选择候选；随后同模型请求返回 429。已按发布门禁暂停收尾并核对请求链路，没有修改上游模型/账号配置，没有自动重试；这不是智力测试提交接口失败。本次后端未改变，保留新前端修复，不能把应用健康等同于所有上游可用。
- 总响应耗时对比存在负载差异：切换前 10 分钟同账号 `gpt-6-astra` 请求平均输出 260 token，切换后检查时约 407 token；同期首 token 平均耗时 5483ms → 6875ms、P95 8649ms → 9613ms。最终管理接口 P95 为 11ms（基线 12ms）；网关总耗时 P95 为 31388ms（基线 26636ms）。这不是等负载压测，不能证明性能无劣化或零业务影响。

证据位于发布目录：`observation.json`、`observation-activation-tail.json`、`final-summary.json`、`latency-summary.json`、`gateway-latency-groups.json`、`usage-latency-check.txt`、`usage-volume-check.txt`、`acceptance-api-evidence.json`。上述统计是记录时快照，不代表之后持续状态。
