# internal: 权威云端子系统

[中文](README.md) | [English](README.en.md)

`internal` 存放 Ora Cloud 的私有内部实现包。遵循 `AGENTS.md` 中记录的架构边界规范，所有核心业务状态、策略逻辑、协议转换与基础设施适配器均保持在 `internal/` 作用域内私有化。

## 模块概览

- [core](core/README.md)：权威领域核心，拥有业务状态机、事务边界、数据库级咨询锁、密码学身份认证，以及 Cloud Skill 摄取 saga（`Store.IngestSkill` / `Store.IngestSkills`）与 source intake 接缝（`Store.IngestSource`：whole-request 授权 + `target_skill_id` 作用域校验 + 幂等上抛）。
  - [migrations](core/migrations/README.md)：包含按顺序执行、仅向前的 PostgreSQL Schema 迁移脚本和校验和验证。
- [api](api/README.md)：HTTP 表现层与协议接入层。
  - [router](api/router/README.md)：绑定 HTTP 路由、验证两层 JWT 凭据、在 64 KiB 大小限制下解析 JSON 请求体（`POST /api/v1/tenants/:tid/spaces/:spaceId/skills/imports` multipart 上传路由例外，专用 handler + route-local 256 MiB 预算），并将领域错误转换为稳定契约。
- [gateway](gateway/README.md)：公开认证边界：Login Attempt 与 Browser Session 的 PostgreSQL 存储、provider-neutral 登录编排、内部 JWT 签发、Cookie/CSRF/redirect 防护与 `/api/v1` 代理，并对 Skills 上传路由做 route-aware 的 64 KiB body 上限豁免（其余路由保持 64 KiB）。
  - [idaas](gateway/idaas/README.md)：华为 IDaaS 2.0（`client_secret_post`）适配器，产生 `source=huawei-corp` 的 `VerifiedIdentity`。
  - [github](gateway/github/README.md)：GitHub OAuth App 适配器，产生 `VerifiedIdentity`。
  - [devlogin](gateway/devlogin/README.md)：仅限本地开发的 provider：一个输入任意身份即可登录的本地表单，只在 loopback 开发 origin 上注册。
- [contract](contract/README.md)：定义 OpenAPI 3.0 Schema 模型（含 Skills 上传的 `SourceUploadResult`/`SourcePreparationFailure`/`SourceIngestionItem` 与 multipart request body）、DTO 结构体与契约覆盖率测试。
- [skillpkg](skillpkg/README.md)：Cloud Skill canonical package v1 的纯内容层——canonical path 校验、ManifestV1、tree digest 与 `ora-skill-package` v1 的 encode/decode/verify，不依赖数据库或存储。
- [skillmeta](skillmeta/README.md)：Cloud Skill 的 `SKILL.md` 元数据业务层——解析 YAML frontmatter、校验 `name`/`description` 并派生 `canonical_name = ASCII lowercase(name)`；纯 CPU、确定性、无 I/O，与 `internal/skillpkg`（身份层，永不解析 `name`）严格分离。
- [skillstore](skillstore/README.md)：Cloud Skill canonical package 的 provider-neutral Object Storage 语义层——物理 `package_digest`、稳定逻辑 object key、create-only `ObjectStore` port、五值结果分类与按 `object_locator` probe 的 reconciliation；不含具体 provider / 上传 HTTP API，由 `internal/core` 的摄取 saga 通过 `Store.SkillsObjectStore` 驱动 `PutImmutable`/`Reconcile`。
  - [fakestore](skillstore/fakestore/README.md)：`skillstore.ObjectStore` 的确定性内存测试替身（test double），可注入全部失败模式。
- [skillsource](skillsource/README.md)：Cloud Skill 的 source intake 层——把 directory / ZIP / uncompressed TAR 摄取为规范化 `sourceEntry`，执行 source 级安全校验与 `SKILL.md` candidate 发现，产出喂给 `IngestSkills` saga 的 `PreparedCandidate`；纯内存、不落库，复用 `skillpkg.ValidatePath` 与 `skillmeta.Parse`。
- [repository](repository/README.md)：基于 GORM 管理 PostgreSQL 连接池与启动时快速探活。
- [config](config/README.md)：加载并校验应用程序配置文件及环境变量覆盖。
- [logger](logger/README.md)：基于 Zap 和 Lumberjack 提供结构化、非阻塞的 JSON 日志记录。
- [simulator](simulator/README.md)：实现 Substrate 执行引擎、Controller 与 Workspace Node 的进程内替身。
- [controlpb](controlpb/README.md)：由 [`proto/`](../proto/README.md) 生成的 Controller 内部控制契约 gRPC Go 代码（服务端桩与消息），只读。
- [controlgrpc](controlgrpc/README.md)：该契约的 gRPC 服务端：调用方身份拦截器、`Fault` → 状态码映射、租约服务；只翻译，不含业务。

## 分层与架构规则

1. **严格单向依赖**：
   - `cmd/*` $\rightarrow$ `internal/api/router`, `internal/gateway`, `internal/core`, `internal/config`, `internal/logger`, `internal/repository`。
   - `internal/gateway` $\rightarrow$ `internal/core`（仅 `Claims` 类型）、`internal/config`、`internal/logger`；`internal/gateway/idaas`、`internal/gateway/github` 与 `internal/gateway/devlogin` $\rightarrow$ `internal/gateway`。Gateway 不查询 Cloud 业务表。
   - `internal/api/router` $\rightarrow$ `internal/core`, `internal/contract`；`internal/controlgrpc` $\rightarrow$ `internal/core`, `internal/controlpb`。
   - `internal/core` $\rightarrow$ 标准库、`gorm.io/gorm`、`internal/core/migrations`、`internal/skillpkg`、`internal/skillmeta`、`internal/skillstore`、`internal/skillsource`（仅 `IngestSource` 消费 `PreparedSourceResult`）。
   - `internal/repository` $\rightarrow$ `internal/config`, `gorm.io/gorm`。
- `internal/skillpkg` $\rightarrow$ 标准库（无内部依赖）。
   - `internal/skillmeta` $\rightarrow$ 标准库、`gopkg.in/yaml.v3`（无内部依赖）。
   - `internal/skillstore` $\rightarrow$ 标准库、`internal/skillpkg`（`internal/skillstore/fakestore` $\rightarrow$ `internal/skillstore`）。
   - `internal/skillsource` $\rightarrow$ 标准库、`internal/skillpkg`、`internal/skillmeta`（不 import `core` / `skillstore`）。
   - 底层包（`core`、`repository`）严禁反向导入上层表现层包（`api`、`router`）。
2. **PostgreSQL 为唯一权威持久化**：
   - 所有共享业务状态必须持久化在 PostgreSQL 中。严禁跨请求在内存中缓存权威领域状态。
3. **事务边界约束**：
   - 数据库事务与咨询锁严格限制在 `core.Store.transact` 内的 PostgreSQL 操作中。**绝对禁止**跨外部 HTTP 请求、Git CLI 命令或文件系统 I/O 持有事务。
4. **错误处理**：
   - 面向客户端的公开错误统一使用稳定的 `Fault` 结构体（包含 `Code`、`Params`、`Status`）。内部 SQL 细节、错误堆栈与数据库异常仅记录于内部日志中，绝不对外暴露。

参见 [AGENTS.md](../AGENTS.md)、[核心不变量与契约](../docs/core-contract.md) 与 [认证配置与凭据](../docs/authentication.md)。
