# 更新记录

## 2026-09-17 上游同步

- 同步范围：`07bf8b92f` 至 `efe9aab1e`（上游版本 `0.2.0` 至 `0.2.5`，共 476 个提交，其中 204 个合并提交）。
- 同步方式：执行 `git fetch origin --prune --tags` 后，用默认三方合并（`ort`，未使用 `-X ours`）将 `origin/main` 纳入本地 `main`，真实冲突逐个人工解决并统一以本地业务实现为准；同步前保留基线标签 `sync-baseline-20260917` 与备份分支 `backup/pre-upstream-sync-20260917`，本地领先的 9 个业务提交（链上 USDT、Cloudflare 边界加固等）全部保留。
- 冲突共 4 个文件：`backend/go.mod`、`backend/go.sum` 与两个 `GroupsView` 前端测试。依赖冲突保留上游升级后的版本（`go-redis v9.22.0`、`x/crypto v0.55.0`、`grpc v1.83.2`、`otel v1.44.0` 等），再重新引入本地链上依赖（`go-ethereum`、`btcsuite/btcd`、`go-tpm`），最后由 `go mod tidy` 统一整理模块图；前端测试冲突按上游重命名（`getModelsListCandidates` → `getModelAllowlistCandidates`）采用上游版本，同时保留本地新增的 `getLiveCapability` 模拟。
- 上游变更规模：857 个文件，新增 51,179 行、删除 4,906 行；纳入生成代码后相对本地业务基线共 894 个文件，新增 54,200 行、删除 5,189 行。
- 生成代码按仓库既有流程重建：Ent 用隔离生成器重建到 `backend/entgen-output` 并同步到 `backend/ent`（334 个生成文件与 schema 逐一哈希比对一致），Wire 重新生成 `backend/cmd/server/wire_gen.go`（重生成结果与合并结果无差异）。

### 新增与增强

- 新增分组级模型白名单强制（`model_allowlist`）与 Codex 模型清单配置，覆盖网关模型列表、WebSocket 与调度校验，并补齐管理员界面和测试。
- 新增 MiniMax 平台与 OpenCode 平台（Zen、GO 账号类型），包含账号类型、模型映射、前端选择项和导入探测。
- 新增订阅批量操作、API Key 批量编辑和用户批量删除，完善对应权限、事务处理与前端交互。
- 增强 OpenAI/Codex 能力：OAuth 生图改走原生 Codex Images、WebSocket 连接池常驻读循环应答上游 ping、按 Codex 线程派生 WS 执行作用域、GPT-6 Astra 与 ultrafast service tier、Anthropic reasoning effort 计费控制。
- 完善 Antigravity（Gemini 3.7/3.8 Flash、OAuth token 缓存按账号隔离）、Ollama Cloud 用量窗口异步限流重置和 Grok 媒体资格控制。
- 增强运营与前端能力：站点类型三态开关、自定义页面按钮隐藏与拖拽、支付帮助文本 Markdown 渲染、Channel Monitor V2 用户排行、简单模式基础账号分组、账户到期预设、OpenAI 周成本估算和兑换码分页。
- 部署侧新增 Apple 网络子网可选配置，并为长时间 LLM 生成流补充上游 HTTP/2 PING 保活。

### 修复与安全

- 修复 DeepSeek Responses 并行工具输出、Antigravity 归因元数据与 SSE 空行、OpenAI 清单校验与响应亲和恢复、Gemini finishReason 计费口径等问题。
- 修复认证与会话：TOTP 设码同步、注册密码确认、临时故障下会话保留、OAuth 促销码透传，以及暂停账号继续刷新令牌。
- 修复支付链路：退款余额提示按请求金额计算、支付配置请求竞态、切换订单状态时重置页面、非法金额输入后的文本恢复。
- 修复权限与配额：拒绝负数平台限额、API Key 重置配额后状态同步、代理凭据可显式清空、批量生图访问缓存按用户隔离。
- 依赖安全：将 `google.golang.org/grpc` 升级到 `v1.83.2`，修复 govulncheck 报告的两条安全公告。

### 数据库、配置与部署

- 新增迁移 `234`（渠道最大推理强度倍率）、`235`/`236`（分组模型白名单及修复）、`237`（MiniMax 平台）、`238`（OpenCode GO 平台、清理未配置平台限额），并补充对应迁移回归测试；与本地链上业务迁移 `192_add_onchain_usdt_foundation.sql` 并存。
- 后端版本同步至 `0.2.5`；`deploy/` 下的配置样例、Compose 文件与文档随上游更新，本地 Cloudflare 边界加固（origin guard、cloudflared HTTP/2 传输）保持不变。

### 同步后校验

- `go build ./...`、`go vet ./...`、`go mod verify`、`go mod tidy -diff` 均通过，Ent 与 Wire 生成代码已确认与当前 schema/装配一致。
- `go test ./...`：除 `internal/repository` 的 3 个 `TestPgDumper*` 用例外全部通过。这三个用例来自上游，依赖 `sh` 命令，在 Windows 上缺少 `sh` 可执行文件而失败；文件与 `origin/main` 完全一致，属本机环境限制。
- `go test -tags=unit ./...`：仓库包同样受上述 `sh` 限制；另外 `internal/service` 的 `TestOllamaProbeCallback_StaleLongDoesNotOverrideNewShort` 在纯上游 `origin/main` 检出上同样失败，为上游既有问题，与本次合并无关。
- `go test -tags=integration ./migrations/... ./internal/repository/...`：迁移包通过，链上相关集成用例（`TestOnchain*`）全部通过，其余仅上述 `sh` 用例失败。
- 前端通过 `vue-tsc --noEmit`、`eslint`、i18n 语言包完整性检查；`vitest run` 298 个测试文件、2249 个用例全部通过。
- 本地修复：本地链上测试的 `requireApplicationErrorReason` 与上游 unit 测试助手同名，导致 `go test -tags=unit` 无法编译（该问题在合并前基线即已存在），已将本地链上测试统一改名为 `requireOnchainErrorReason`。

## 2026-08-11 上游同步

- 同步范围：`a19c9f8d8` 至 `0f73203e3`（上游版本 `0.1.171` 至 `0.1.173`，共 198 个提交）。
- 同步方式：执行 `git fetch origin --prune --tags` 后，以三方合并将 `origin/main` 纳入本地 `main`；同步前保留基线标签 `sync-baseline-20260811`，未覆盖同步前的本地未提交改动。
- 上游变更规模：518 个文件，新增 43,229 行、删除 3,214 行。
- 共处理 6 个文件冲突：依赖注入和清理逻辑按语义合并，同时保留本地链上 USDT 业务与上游 Channel Monitor V2/Grok 服务；生成文件通过 Wire 和隔离 Ent 生成器重建，其余配置、依赖校验和前端测试冲突按双方有效变更合并。

### 新增与增强

- 新增 Channel Monitor V2 被动聚合、隐私默认值、历史回填、健康阈值、缓存与模式路由，并补齐管理员和用户端监控界面。
- 完善 Grok 平台接入，覆盖视频、图片、语音、搜索、可配置模型映射、OAuth/SSO/密码认证、配额调度和分项计费。
- 新增上游响应模型观测与审计，将实际响应模型纳入安全计费和不一致诊断。
- 增强大体积备份的分段上传与恢复，并新增按邮箱域名控制注册额度的配置能力。

### 修复与安全

- 修复 OpenAI Responses 400 错误透传、流式响应输出前恢复、OAuth 路由与过期处理，以及工具 schema 和内容块兼容问题。
- 修复 Gemini 原生图片数量计费、非流式图片请求上下文解绑和连接池 429 处理问题。
- 防止待完成 OAuth 流程被接管，强化 API Key 配额与过期校验、客户端指纹 User-Agent 校验，并为上游 TCP 连接增加显式超时。
- 修复订阅每日额度重置、最近轮次审计去重、调度诊断和系统日志退避处理。

### 数据库、配置与依赖

- 新增迁移 `194` 至 `206` 和 `217` 至 `220`，涵盖上游响应模型、Channel Monitor V2 以及 Grok 视频、语音和搜索计费；与本地 `192_add_onchain_usdt_foundation.sql` 链上业务迁移并存。
- 后端版本更新至 `0.1.173`，并将 `nanoid` 更新至 `3.3.17` 以修复安全问题。
- Wire 注入和清理流程同时保留链上扫描、结算、对账、归集运行时，以及 Channel Monitor V2 聚合器和原有监控运行器。

### 同步后校验

- 通过 `go mod verify`、后端 `go test ./...`、前端 `vue-tsc --noEmit`、`eslint` 和全量 `vitest run`。
- 使用仓库要求的 pnpm `9.15.9` 完成冻结锁文件安装；本机默认 pnpm 11 不兼容旧式 `pnpm.overrides` 字段，未据此改写项目锁文件。

## 2026-08-06 上游同步

- 同步范围：`5a6143097` 至 `00b859617`（上游版本 `0.1.168` 至 `0.1.171`，共 150 个提交）。
- 同步方式：先执行 `git fetch origin --prune --tags`，再将 `origin/main` 通过三方合并纳入本地 `main`；使用 `ort -X ours` 处理重叠变更，冲突片段以本地业务实现为准，最终工作区无未解决冲突。
- 同步前已保留本地基线标签 `sync-baseline-20260806`，便于回溯本次合并前的业务版本。
- 合并后重新整理 Go 模块图并生成 Wire 依赖代码，保留本地链上 Ent 生成代码和业务注入关系。
- 上游变更规模：442 个文件，新增 28,227 行、删除 2,089 行；合并并完成依赖、生成代码及回归测试补齐后，相对本地业务基线纳入 442 个文件，新增 28,348 行、删除 2,069 行。

### 新增与增强

- 新增分组级利润控制、调度与计费利润门禁、收益预览命令及相关权限和缓存处理。
- 新增阿里云与腾讯验证码认证门禁，完善 OAuth 待定流程校验、管理配置、前端组件和测试覆盖。
- 增强 OpenAI/Codex 兼容能力，涵盖版本同步、统一出站身份、Responses/Chat 工具与媒体转换、WebSocket/SSE 生命周期、额度重置及容量故障转移。
- 增强上游计费探测和多平台费率回写，完善组合模型、推理策略、模型广场及账户批量操作能力。
- 完善内容审核代理、SMTP STARTTLS、Grok/Anthropic/Gemini 兼容、提示审计和运维报表等功能。

### 修复与安全

- 修复订阅续费与配额窗口并发、支付退款幂等及余额保护、支付方式展示、账户刷新竞态和未结算用量保留问题。
- 修复 OpenAI/Codex、Grok、Anthropic 和 Gemini 的流式响应、工具调用、计费、故障转移及模型映射问题。
- 修复 OAuth 验证码提交门禁、邮箱和 SMTP 消息格式、上游 URL 路径校验、代理流熔断及安全审计配置问题。
- 加固安全响应头、容器最小权限、提示审计辅助字段和请求取消后的资源清理，并补充相应回归测试。

### 数据库、配置与部署

- 新增迁移 `192` 和 `193`：分组利润控制及其鉴权缓存失效处理。
- 配置示例新增利润控制、验证码、代理和上游计费探测相关选项；后端版本同步至 `0.1.171`。
- 更新 Docker/Compose、镜像和品牌资源，补充容器安全与运行时资源检查脚本，并完善前端国际化、移动端布局和管理设置页面。

### 同步后校验

- 重新整理 `go.sum`，移除未被当前依赖图使用的旧校验项，并通过 `go mod verify` 与 `go mod tidy -diff` 校验。
- 修复 Caddy 配置检查脚本对 Windows CRLF 工作区的解析兼容性，确保部署配置回归检查稳定执行。

## 2026-07-30 上游同步

- 同步范围：`37ed639d1` 至 `5a6143097`（上游版本 `0.1.164` 至 `0.1.168`，共 150 个提交）。
- 同步方式：先执行 `git fetch origin --prune --tags`，再将本地 `main` 以 `--ff-only` 快进到 `origin/main`；本次没有文件冲突，所有本地未跟踪业务文件均已保留。
- 变更规模：390 个文件，新增 21,709 行、删除 1,207 行。

### 新增与增强

- 新增 OpenAI Live 网关、macOS Live attestation、会话生命周期与存储容错，并支持按分组控制 Live 请求及持久化客户端会话标识。
- 新增 Passkey/WebAuthn 登录、注册、撤销、后台开关和个人资料管理界面；注册与撤销操作要求账户密码确认。
- 新增模型广场及分组级模型定价展示，支持筛选、模型 ID 复制和分组展示控制。
- 新增 Kimi K3 与 1M 后缀兼容，补充 Claude Sonnet 5 状态别名，并保留 GPT-5.6 `max` reasoning effort。
- 新增面板 API 限流配置，降低高频管理请求对数据库的压力。
- 增强 Antigravity、OpenAI Responses、Anthropic 和 Gemini 之间的协议转换、工具调用配对及流式响应兼容性。
- 增强公告预览与富文本展示、移动端可用渠道/推广码操作、用量请求类型筛选和多币种支付统计。

### 修复与安全

- 修复邮箱别名注册查重的点号绕过、误拒、无界扫描和并发竞态，并增加数据库唯一索引保障。
- 将用户、API Key 等仓储更新限制到明确声明的字段，避免部分更新覆盖未提交字段或并发写入。
- 修复 OpenAI API Key/Codex 请求中的 item ID、输入命名空间、模型映射、Web Search、跨模式 reasoning 故障转移及同账户重试问题。
- 修复 OpenAI Live 在租约丢失、存储故障和 finalize/observer 并发场景下的会话终止、用量记录与资源清理问题。
- 修复安全审计配置解密失败后配置消失或无法保存的问题，并恢复不可用配置的拒绝策略。
- 修复 Ollama Cloud 用量刷新在 PostgreSQL 16 及更早版本上的到期判断、请求去抖和刷新候选饥饿问题。
- 修复 Caddy 压缩导致 SSE 缓冲、配置文件显式路径未生效、Gemini 图片输出丢失及多个计费/统计口径问题。
- 更新后端镜像、遥测等依赖，并继续升级前端 PostCSS 以修复安全公告。

### 数据库、配置与部署

- 新增迁移 `187` 至 `191`：用量日志会话 ID、Live 请求类型、分组 Live 开关、邮箱别名去重索引及 Passkey 凭据表。
- 配置示例新增 WebAuthn/Passkey、面板 API 限流等配置项；部署前应核对域名、反向代理及可信来源设置。
- Caddy 配置明确关闭可能缓冲 SSE 的压缩行为，并补充边缘安全文档和可移植性检查。

## 2026-07-25 上游同步

- 同步范围：`d4b9797ff` 至 `37ed639d1`（上游版本 `0.1.161` 至 `0.1.164`）。
- 同步方式：本地 `main` 快进到已获取的 `origin/main`，保留所有本地未跟踪业务文件；本次没有文件冲突。

### 新增与增强

- 新增异步生图对象存储的后台配置与管理界面，配置保存后即时生效，并补充环境变量映射和测试。
- 新增组合分组和模型路由，可在同一分组内编排多个平台及模型别名，并正确处理路由归属、状态查询与实际转发模型计费。
- 新增 Ollama Cloud 官方用量自动刷新、解析、展示和管理配置，并避免在审计日志中记录会话明文。
- 新增 Anthropic `claude-opus-5` 模型适配和 OpenAI 分组级 reasoning effort 策略。
- 新增可配置的客户端 IP 解析模式、可信代理和自定义请求头，覆盖服务端持久化、管理接口、审计、前端设置及部署文档。
- 增强 OpenAI/Codex 兼容能力，包括模型发现、token 计数、Agent Identity 团队隔离、WebSocket/HTTP 桥接、Responses 转换和调用 ID 规范化。
- 增强 Grok 兼容能力，包括 Codex compact、跨轮次工具缓存、Trae/Claude Desktop 工具桥接、视频内容代理、配额获取与本地 token 估算。
- 改进 Anthropic 与 OpenAI Responses/Chat Completions 互转，兼容紧凑 SSE 事件格式及更多响应内容类型。
- GitHub 更新检查支持令牌配置，降低匿名 API 限流对版本检查的影响。
- Redis 增加 ACL 用户名配置，并完善代理质量检测、调度快照和运维定时报表。
- 支付宝预创建支付新增移动端深链接唤起，并完善支付结果状态处理。

### 修复与安全

- 修复 hosted image generation token 未计入 Responses 账单、故障转移后重复缓存计费和同步缓存计费不一致的问题。
- 修复部分更新 API Key 时意外清空 IP 白名单或黑名单的问题。
- 修复 S3 备份配置可能持久化自动生成临时密钥的问题。
- 修复安全审计开关无法关闭的问题，仅在阻断意图下采用 fail-closed 行为。
- 修复优雅关停超时后跳过清理流程导致用量或计费记录丢失的问题。
- 修复组合模型请求按别名而非实际转发模型计费、渠道价格模型名未归一化的问题。
- 修复 OpenAI 流中断后未隔离异常代理、Grok 账户收到 402 后未进入冷却的问题。
- 修复调度器配额元数据和 `LastUsedAt` 缓存写入隔离问题，以及 API Key 解密失败后仍继续调度的问题。
- 修复套餐有效期单位、剩余天数取整和到期时间精度显示问题。
- 将 Axios 升级至 `1.18.1`，修复 `GHSA-gcfj-64vw-6mp9` 安全问题。
- 将 PostCSS 升级至 `8.5.18` 或更高版本，并升级 `golang.org/x/text`，修复前后端依赖安全告警。

### 前端与品牌

- 启用新版 SVG 标识并同步更新 README、站点图标和品牌资源。
- 完成批量生图页面国际化，补齐中英文文案并清理遗留翻译键。
- 修复移动端和 iOS 输入体验、窄屏布局、滚动区域及暗色模式可读性问题。
- 完善备份、系统设置、账号、订阅、支付和运维监控页面的交互与测试。
- 新增组合分组、推理策略和 Ollama Cloud 用量管理界面，修复账号操作菜单、运维图表和筛选器在移动端的溢出问题。

### 部署与文档

- 修复开发及本地 Docker Compose 中 Redis 参数未生效的问题，并传递 PostgreSQL 调优参数。
- 修正 Compose 示例镜像地址，补充 GitHub token、客户端 IP、可信代理和异步生图配置文档。
- 更新 Kyren Topup 支付提供商说明及相关中英文支付文档。
- 新增组合模型路由、分组推理策略、移动支付宝和分组鉴权缓存相关数据库迁移及组合分组使用文档。
