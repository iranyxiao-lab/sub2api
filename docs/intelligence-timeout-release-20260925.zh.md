# 智力测试执行时限改造与发布

## 改造

- `595401887`：新入队测试默认上限 300 秒，支持配置 30–1800 秒。每条运行保存时限快照，执行租约为时限加 30 秒，计划锁覆盖排队、执行和收尾。迁移 243 为旧记录及旧应用写入保留 120 秒默认值。
- 结果卡片显示实际时限，失败保留部分回答；入队幂等、单执行名额和禁止自动重试保持不变。合入工作区已有单题选择修复，立即测试先保存当前设置。
- 增加首个/最后一个 SSE data 事件耗时与完成状态日志。`c4c50767c` 将运行完成日志改为结构化 INFO，修复首次生产验收发现的 legacy 日志适配器把 `error_code=` 成功日志误判为 ERROR 的问题。

## 本地验证

- `go test -tags unit ./internal/config ./internal/service -run 'TestIntelligence|TestScheduled|TestParseTestSSE' -count=1 -timeout 6m` 通过，包含真实等待 121 秒后成功的流式测试，以及浏览器请求取消不影响后台任务的测试。
- `go test -tags integration ./internal/repository -run 'TestIntelligence|TestScheduled' -count=1 -timeout 6m` 通过，使用本地一次性 PostgreSQL/Redis 容器，验证 330 秒租约、930 秒计划锁、超时快照幂等、旧写入默认 120 秒及结果完成后释放锁。
- `go test -tags unit ./internal/config ./internal/service ./migrations -skip 'TestIntelligenceRunSurvives' -count=1 -timeout 10m`：config、migrations 通过；service 全包仅 `TestOllamaProbeCallback_StaleLongDoesNotOverrideNewShort` 失败。跳过的两个长耗时用例已在前一命令独立通过。
- 在改造前 `cf518f3b6` 的干净 Git archive 中，对该 Ollama 用例执行 `-count=5` 同样失败。本次未修改该用例或相关冷却逻辑，也未把 service 全包标为通过。
- 最终日志修复后，`go test -tags unit ./internal/service -run 'TestIntelligence' -skip 'TestIntelligenceRunSurvives' -count=1 -timeout 2m` 通过。
- 前端 `pnpm run typecheck`、`pnpm run lint:check` 通过；智力测试面板及 i18n 共 10 文件、69 项测试通过。英文文案插值调整后再跑相关 3 文件、25 项通过。
- 两次生产 Docker 构建均从已提交的独立 Git archive 构建，前端语言检查、类型检查、Vite 构建与 Go 嵌入式构建通过。没有打包本地环境文件、未跟踪脚本或数据目录。

## Chrome 验收

使用用户现有 Chrome 扩展与登录会话，在独立标签页操作真实生产页面。

- 首次运行 #50：账号 #44、原计划与原模型，点击“立即测试”后显示执行中与上限 300 秒。数据库运行快照为 300、租约为 330 秒。
- 执行期间关闭并重新打开面板，记录继续存在；最终成功，耗时 79.318 秒，答案正常展示。旧记录 #49 仍显示上限 120 秒。
- 新增诊断日志记录首次 SSE data 事件 46.289 秒、末事件 79.318 秒及 `completed=true`。
- 121 秒边界验证来自本地受控上游测试，不能把这次 79.318 秒的真实模型调用当作线上跨 120 秒证明。

最终镜像 `c4c50767c` 再通过 Chrome 点击“立即测试”，运行 #51 成功，耗时 65.608 秒，页面展示上限 300 秒和完整回答，控制台未捕获 error 日志。诊断首个 SSE data 事件为 19.363 秒、末事件 65.607 秒，`completed=true`；运行完成日志为 INFO。

两次验收均复用账号 #44 的已保存计划，没有修改模型、题目、定时设置或账号调度开关，没有删除历史，没有自动重试。截图与数据库/诊断记录保存在最终发布目录。

## 发布与回滚

只操作 homestay 的 `/opt/sub2api` 应用。每次切换前保存数据库 custom dump、原 Compose、原代理配置及镜像标识，校验 `pg_restore --list` 可读取；未执行数据库恢复演练。

候选使用独立服务器数据副本与控制目录，绑定 loopback 端口，通过健康及未登录管理员接口返回 401 的检查，25 个周期任务保持 deferred。平滑切换目标代理 upstream 后，确认旧代理 worker 消失、旧端口连接为零且无活动智力测试，再停止旧应用、关闭其自动重启、激活新实例。未重建 PostgreSQL/Redis，未操作其他业务。

回滚必须先排空并停止新实例，再恢复上一实例、代理和持久化 Compose 及重启策略，避免两套周期任务同时运行。迁移 243 只新增兼容列；应用回退时保留该列，不执行反向删除迁移。配置与备份始终留在服务器，权限 600。

### 最终部署标识

- 镜像：`sub2api:intelligence-timeout-c4c50767c`。
- 镜像 ID：`sha256:5f6c2d54b25ac17663b1064cfd30fd84bbd94e847f1826e080a40e3586fa018a`。
- 服务：`sub2api-timeout-final`，loopback `18086`；持久化入口 `/opt/sub2api/docker-compose.yml`。
- Compose SHA-256：`63161e154e19b56abd72fcafaf50b88e766f16c9e6f1f346bc5ba223d0422fdc`。
- nginx 配置 SHA-256：`9f77ab3ddd79711a1285b7860bc57608942c33086e541f46b4d7cc5477d721c4`。
- 最终发布目录 `/opt/sub2api/releases/intelligence-timeout-c4c50767c/`，包含独立 data/control、数据库备份、原 Compose/代理配置与运行证据。
- 最终切换前备份 SHA-256：`9591a44c3e522515ca1845c9fe144d8218ee688f9f1a1fbb7af4195f69f5ecaf`。
- 迁移 243 已于 2026-09-25 22:57:20（Asia/Shanghai）应用，数据库迁移记录校验和为 `b50953500a93b4a3859dea8574cecfcf9f32702aa575c37a0a5c87903cf0ee34`，默认值 120 与 30–1800 范围约束均已核实。
- 上一候选 `sub2api-timeout` 和改造前的 `sub2api-httpfix` 均停止保留，重启策略为 no。回到改造前版本的备份位于 `/opt/sub2api/releases/intelligence-timeout-595401887/`。

## 最终发布观察

最终激活后连续观察 609.56 秒，31 轮采样；代理与应用健康检查共 62 次全部通过，PostgreSQL/Redis 就绪检查全部通过。应用保持 healthy、重启次数为 0；其他原有业务容器的启动时间、重启次数及健康状态均未变化。

| 范围 | 切换前统计 | 最终观察窗口 | P95 耗时（前 → 后） |
| --- | --- | --- | --- |
| 管理接口 | 200×157、304×3、202×1、401×1 | 200×72、202×1、401×3 | 11ms → 9ms |
| 普通网关 | 200×46、429×4 | 200×40、429×2 | 53297ms → 50030ms |
| 其他接口 | 200×101、304×6 | 200×24、404×1、401×2 | 12ms → 7ms |

窗口内没有 5xx，不能据此声称所有请求均成功。429 来自普通 `/v1/responses`；404 涉及 `/api/v1/usage/dashboard/api-keys-usage`，401 涉及其他管理/用户接口。这些响应并非两次智力测试的执行结果，根因未在本次任务中完整定位。新旧实例的 JWT 环境配置和应用配置文件已核实一致；本次没有修改鉴权或普通网关逻辑。统计窗口负载与请求数量不同，不能作为等负载性能基准。

最终 Compose 与 nginx 校验和复查一致，应用、PostgreSQL、Redis 均 healthy。所有历史应用停止保留，仅最终实例启用自动重启和周期任务。

服务器发布目录保留 `observation.json`、`observation-summary.json`、`baseline-metrics.json`、`migration-and-run.txt`、`chrome-acceptance-records.txt`、`intelligence-diagnostics.txt`、`chrome-run-50.png` 和 `chrome-run-51.png`。首次镜像的观察窗口因日志分级补丁更新而结束，最终镜像重新完成了完整 10 分钟观察。
