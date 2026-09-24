# AGENTS.md

## 适用范围与优先级

- 本说明适用于仓库根目录及其所有文件。
- 如果更深层目录中存在另一份 `AGENTS.md`，则该子树以更深层的文件为准。
- 系统、开发者与用户指令的优先级高于本文件。
- 修改前先阅读相关源码、测试与文档。保留用户已有的改动，不要重置或覆盖无关的工作。
- 保持改动聚焦。不要格式化或重组无关代码；除非用户明确要求，不要创建提交。

## 项目概览

Sub2API 是一个 AI API 网关，后端使用 Go，前端使用 Vue。

- 后端：Go 1.26.5、Gin、Ent、PostgreSQL 和 Redis。
- 前端：Vue 3、TypeScript、Vite 和 pnpm。
- 部署：Docker Compose 是全栈部署的主要方式；部署脚本与 Caddy 配置位于 `deploy/` 下。
- 前端构建产物输出到 `backend/internal/web/dist/`，用于可选地嵌入后端二进制文件。

## 仓库结构

| 路径 | 职责 |
| --- | --- |
| `backend/cmd/server/` | 服务入口、Wire 装配与构建元信息。 |
| `backend/internal/handler/` | HTTP 与协议处理器。 |
| `backend/internal/service/` | 应用与业务逻辑。 |
| `backend/internal/repository/` | 数据库与持久化访问。 |
| `backend/internal/config/` | 配置加载与校验。 |
| `backend/ent/schema/` | Ent schema 源文件。 |
| `backend/ent/` | 生成的 Ent 客户端代码；禁止手工修改。 |
| `backend/migrations/` | 内嵌的有序 PostgreSQL 迁移及其回归测试。 |
| `backend/internal/web/` | 前端嵌入与静态资源托管。 |
| `frontend/src/api/` | 前端 API 客户端与请求类型。 |
| `frontend/src/components/` | 可复用的 Vue 组件。 |
| `frontend/src/features/` | 按功能划分的前端模块。 |
| `frontend/src/stores/` | Pinia 应用状态。 |
| `frontend/src/views/` | 路由级页面。 |
| `frontend/src/router/` | 路由与导航守卫。 |
| `frontend/src/i18n/` | 本地化文案与语言包测试。 |
| `deploy/` | Compose 文件、部署脚本、环境变量模板与部署测试。 |
| `.github/workflows/` | CI、安全扫描与发布自动化。 |
| `docs/` | 功能、支付、合规与运维文档。 |

## 工具链与本地服务

- 使用 Go `1.26.5`，以 `backend/go.mod` 声明并已在 CI 中校验的版本为准。
- 本地前端开发使用 Node.js `20` 与 pnpm `9`，以与 CI 保持一致。修改前端依赖时使用 pnpm，不要使用 npm 或 yarn。
- 需要完整本地环境时，使用 Docker Compose v2 与 `deploy/docker-compose.dev.yml`。集成测试也会通过仓库的测试环境使用 PostgreSQL 和 Redis。
- 凭据保存在本地 `.env` 或配置文件中。以 `deploy/.env.example` 和 `deploy/config.example.yaml` 作为模板；切勿提交真实密钥。
- 在没有 `make` 的系统上，直接运行 Makefile 中对应的底层命令。

## 常用命令

在指定目录下执行命令。

```text
# 仓库级构建与检查
make build
make test

# 后端
cd backend
go run ./cmd/server
make build
make generate
make test-unit
make test-integration
make test
golangci-lint run ./...

# 前端
cd frontend
pnpm install --frozen-lockfile
pnpm run dev
pnpm run build
pnpm run lint:check
pnpm run typecheck
pnpm run test:run
pnpm run test:coverage

# CI 使用的部署脚本检查
bash -n deploy/apple-container.sh
bash deploy/tests/apple-container-test.sh
sh deploy/test-caddyfile-cache.sh
```

根目录的 `make test` 会运行后端检查，以及前端 lint、类型检查与关键 Vitest 测试集。CI 还会按各自的 build tag 运行后端单元测试与集成测试，因此后端改动需要运行这两个带 tag 的测试套件。

## 架构与依赖规则

- 后端常规依赖方向为 `handler -> service -> repository`。Handler 负责转换传输层关注点；Service 负责业务规则；Repository 负责持久化细节。
- `backend/.golangci.yml` 要求 handler 与 service 不得直接依赖 repository、GORM 或 Redis 包，除非有明确记录的例外。改动包边界后请运行配置好的 lint。
- 优先复用已有的配置加载器、API 客户端、存储、服务与共享组件，而不是引入并行的抽象。
- 新增接口时，按需同步更新 handler、service、持久化或上游集成、带类型的前端 API 以及测试。保持鉴权、授权、限流、错误映射以及请求/响应兼容性不变。
- 对于用户可见的前端文案，使用现有的 `frontend/src/i18n/` 语言包并补充或更新语言覆盖，不要在视图中硬编码字符串。

## 生成代码、数据库迁移与构建产物

- 只在 `backend/ent/schema/` 下修改 Ent schema。schema 变更后运行 `cd backend && make generate`，审阅生成的 diff，并在需要时一并提交受版本控制的生成文件。
- `backend/cmd/server/wire_gen.go` 由 Wire 生成，禁止手工修改；使用 `cd backend && go generate ./cmd/server` 重新生成。
- `frontend` 的 `pnpm run build` 会把生成的资源写入 `backend/internal/web/dist/`。不要手工修改该目录，也不要提交普通构建产物。
- 数据库变更以新迁移的形式添加到 `backend/migrations/`，使用下一个可用的本地编号和简短的小写描述。不要改写已经应用过的迁移，也不要重新编号现有文件。
- 迁移会被内嵌进二进制文件。在合适的场景优先使用幂等 SQL（`IF EXISTS` / `IF NOT EXISTS`），并对兼容性敏感的改动补充或更新迁移回归测试。
- 如果 `package.json` 发生变化，请使用 pnpm 更新 `frontend/pnpm-lock.yaml`，并确认 `pnpm install --frozen-lockfile` 可以通过。

## 测试要求

- 行为变更需补充或更新测试；测试应贴近其所覆盖的包或功能。
- 后端单元测试使用 `unit` build tag，集成测试使用 `integration` tag。涉及 PostgreSQL、Redis、迁移、并发、计费、鉴权、支付或上游路由的改动，应包含相应的集成或回归覆盖。
- 前端测试使用 Vitest 与 `*.spec.ts` 文件。当相关区域受影响时，覆盖路由守卫、API 错误状态、加载状态、权限校验与语言包行为。
- 迭代过程中运行最小可用的检查，交接前运行受影响范围内完整的命令集。报告任何无法运行的检查及其原因。
- 不要为了让改动通过而弱化、删除或跳过测试。如果某个测试暴露了有意的契约变更，请一并更新实现、测试与文档。

## 安全与数据处理

- 切勿提交 API key、密码、JWT 密钥、支付凭据、私钥、生产 URL 或复制而来的 `.env` / `config.yaml` 文件。在日志、测试夹具、截图与测试失败输出中对密钥进行脱敏。
- 将鉴权、授权、代理、URL 白名单、可信代理头、CORS/CSP、限流、计费、支付、链上代码与凭据处理的改动视为安全敏感改动。保持 fail-closed 行为，并补充回归测试。
- 使用现有的辅助函数与配置策略，对外部传入的 URL、请求头、载荷大小、模型名与文件路径进行校验和边界限制。
- 不要在开发或测试过程中对真实数据执行破坏性的数据库或部署命令。使用一次性本地服务或测试容器。

## 远程生产部署：homestay

homestay 生产环境通过本地 `~/.ssh/config` 中的 SSH 别名 `homestay` 访问。请将该主机视为线上生产环境。

- 仅通过 `ssh homestay` 连接。由 SSH 别名决定用户、端口、主机与密钥；不要将真实主机名、私钥路径或凭据复制到本仓库。
- 该主机是多租户的：明确在本任务范围内的是 `/opt/sub2api` Sub2API Compose 项目；另有 `/opt/sub2api-e2e` 检出和其他业务，绝不停止、重建或重新配置无关服务。
- homestay 的目标部署目录是 `/opt/sub2api`，其中包含 Compose 文件、权限为 600 的 `.env`、发布镜像和备份；部署前必须重新核实目录内容、服务名、端口和近期备份，不得打印含密钥的 Compose 展开配置。
- 2026-09-25 分阶段发布后，应用服务为 `sub2api-next`，loopback 端口为 `18083`；PostgreSQL/Redis 仍是原服务。`/opt/sub2api/docker-compose.yml` 为持久化入口，旧 `sub2api` 容器保留为停止状态用于回滚。部署前仍必须重新核实，不得启动旧应用与新应用并行处理周期任务。
- 当前应用的数据目录及激活标记位于 `/opt/sub2api/releases/intelligence-e671b4dd5/`。回滚先按发布文档排空流量并停止新实例，再恢复旧实例和代理配置；不要只恢复代理而留下两套周期任务运行。
- 主机级 nginx 一直在监听 `80` 和 `443` 用于 TLS 终止；`systemctl is-active nginx` 不能可靠反映其状态，因此请检查运行中的进程或管理面板，不要据此认定服务已停止。
- 切勿将本地的 `.env`、`deploy/.env`、`backend/config.yaml`、`.ssh` 内容或数据库数据目录复制到该主机。切勿打印 `docker compose config` 的输出，因为环境变量插值可能泄露密钥。
- homestay 的部署、重启、镜像拉取或迁移都属于变更操作，需要用户明确授权。仅通过测试不构成授权。
- 采用低影响的发布方式：先做只读预检（`docker compose -f compose.yml ps`、健康检查端点、MySQL/Redis 就绪状态），确认存在近期且可恢复的备份，记录当前镜像 tag 或 digest 以及 Compose 文件校验和，单独拉取目标不可变镜像，并仅用 `--no-deps` 重建应用服务。不要把 `docker compose down`、`down -v`、全服务 `restart` 或 `--force-recreate` 作为常规发布步骤。
- 数据库迁移是单向的（forward-only）。保持其与当前运行中的镜像向后兼容，避免在同一时间窗口内进行长时间表锁与破坏性 schema 变更；回滚时应使用经过验证的备份恢复或经过评审的补偿性 SQL 迁移，而不是仅回退应用镜像。
- 如果健康检查、错误率、延迟或依赖就绪状态出现劣化，请停止发布，保留上一个镜像引用，对密钥脱敏后保存日志与健康证据，并报告确切的镜像、Compose 校验和、迁移状态以及失败的检查项。

## 工作与交接流程

1. 检查相关的实现、测试、配置与文档。如果存在 `.codegraph/`，在广泛文本搜索之前先使用其代码导航；否则使用 `rg` 与现有的项目结构。
2. 做出最小且自洽的源码与测试改动。除非用户明确要求破坏性变更，否则保持公开 API 兼容。
3. 仅当代码生成的输入发生变化时才重新生成代码并更新 lockfile；审阅生成的 diff，而不是盲目接受。
4. 运行格式化与针对性检查，然后运行上文列出的受影响的完整检查。
5. 审阅 `git diff` 与 `git status`，确认没有引入密钥或构建产物，并在交接时报告确切的命令与结果。

对于库或框架的当前行为，优先采用仓库锁定的版本；当本地代码或测试无法确定契约时，查阅上游最新文档。
