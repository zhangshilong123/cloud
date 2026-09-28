# 数据库迁移模块

[中文](README.md) | [English](README.en.md)

本模块包含 Ora Cloud 线性、仅向前的 PostgreSQL Schema 迁移目录。迁移通过 `embed.FS` 直接嵌入 Go 应用程序二进制，并由 `cloudctl migrate` 以确定的方式应用。

## 迁移目录

迁移脚本严格按照数字序号递增顺序执行。序列是 **append-only**：`0001–0007` 是 upstream 基线（与 `upstream/main` 逐字节一致、不可修改），`0008–0012` 是本地 Issue 迁移，`0013` 起为后续兼容迁移。

- **`0001_core.sql`**（upstream）：基础领域 Schema：
  - 身份与访问管理：`users`、`user_identities`、`tenants`、`tenant_memberships`、`credential_refs`。
  - 项目与工作区：`projects`、`project_storage`、`workspaces`、`workspace_worktrees`、`tasks`。
  - 执行运行时：`sandbox_instances`、`workspace_nodes`、`sessions`。
  - 控制平面：`effects`、`operations`、`tickets`、`controller_leases`、`idempotency_keys`。
  - 不变量：部分唯一索引 `one_main` 保证每个项目最多只有一个活动的 `main` 工作区。外键在所有层级中严格强制租户和所有者的包含关系。
- **`0002_aggregate_guards.sql`**（upstream）：并发与互斥守卫：
  - 防止在同一个项目聚合根上发生并发的生命周期变更。
  - 确保祖先实体软删除后，其子实体无法进行活动状态转换。
- **`0003_resource_versions.sql`**（upstream）：乐观并发版本控制：
  - 在可变实体（`projects`、`workspaces`、`tasks`、`nodes`、`operations`）上强制执行 `version` 递增规则。
  - 杜绝并发 API 操作中的更新丢失（lost updates）问题。
- **`0004_effect_intent_and_ticket_scope.sql`**（upstream）：执行意图与 Ticket 作用域约束：
  - 严格将执行 Ticket 限制到活动的 Workspace Node 和有效的准入 epoch。
  - 将持久化的 Effect 声明绑定至特定的操作阶段。
- **`0005_gateway_auth.sql`**（upstream）：Gateway 认证表（由 `cmd/gateway` 独占运行时访问）：
  - `gateway_login_attempts`：一次性登录尝试；只保存 attempt secret 与 `state` 的 SHA-256 digest，`return_to` 在数据库层拒绝绝对、`//`、`/\` 形式，有效期不超过 1 小时，`consumed_at` 保证最多创建一个 session。
  - `gateway_sessions`：浏览器会话；只保存 token digest，`expires_at` 非空且不超过创建后 90 天，吊销时间与有限的 `revoked_reason` 同时存在，并为 identity 吊销与有界清理建立索引。
- **`0006_collab_spaces.sql`**（upstream，与 `upstream/main` 逐字节一致）：协作空间（Collaboration Space，简称 Space）Schema：
  - `collab_workspaces`：租户内的协作与可见性边界（名称、不可变 slug、归档时间、乐观版本）。归档是软删除，slug 不随之释放：`UNIQUE(tenant_id, slug)` 覆盖活动与已归档行。
  - `collab_workspace_members`：成员与角色（owner/admin/member）、状态（active/disabled）、乐观版本。
  - 与运行时 `workspaces` 表（Runtime Workspace，执行环境）严格分离。
- **`0007_project_space_scope.sql`**（upstream，与 `upstream/main` 逐字节一致）：Project 的 Space 关联（**upstream 原语义：强制**）：
  - 为每个既有租户（含仍有 Project 的已删除租户）创建默认 Space（slug=`default`）。
  - 既有 active tenant members 加入默认 Space（admin→owner，member→member）。
  - 新增 `projects.space_id uuid NOT NULL`，并把每个既有 Project 绑定到其租户的默认 Space。
  - 复合外键 `(space_id, tenant_id) REFERENCES collab_workspaces(id, tenant_id)` 在 SQL 级杜绝跨租户归属；`project_space_list(space_id, id)` 索引支持按 Space 列举 Project。
- **`0008_issues.sql`**：Issues 看板基线表 `issues`（原 `0006_issues.sql`；迁移对账时前移重编号，令 upstream `0001–0007` 保持逐字节一致）。
- **`0009_issue_extensions.sql`**：`issue_statuses`、`issue_comments`、`labels`、`issue_labels`、`issue_subscribers`、`issue_views` + `issues` ALTER（`number`、`properties`、状态格式检查）。（原 `0007_issue_extensions.sql`）
- **`0010_issue_collaboration.sql`**：`issues` ALTER（`assignee_type`/`assignee_id`/`project_ref` + 回填）、`issue_comments` ALTER（`parent_id`/`author_type`/`author_id`/`seq` + 回填 + `UNIQUE(issue_id,seq)`）、新表 `issue_runs`、`issue_activities`、`issue_context_refs`。（原 `0008_issue_collaboration.sql`）
- **`0011_issue_interactions.sql`**：新表 `issue_interactions`（`@` 交互脊）——每个选中的协作目标一行：`id, tenant_id, issue_id, comment_id, target_type, target_id, mode, task, run_id, created_at`。（原 `0009_issue_interactions.sql`）
- **`0012_issue_interaction_input.sql`**：一个通用增量列：`ALTER TABLE issue_interactions ADD COLUMN input jsonb NOT NULL DEFAULT '{}' CHECK (jsonb_typeof(input)='object')` —— 已确认的表单值。刻意排除 `version`、`status` 枚举、`confirmed_at` 与独立 inputs 表；`0011` 不被修改。（原 `0010_issue_interaction_input.sql`）
- **`0013_project_space_optional.sql`**（append-only 兼容迁移）：`projects.space_id` 恢复为**可空**——upstream `0007` 施加了 `NOT NULL` 并对既有项目做了全量绑定；产品决策（PS3 / D2=C）要求 Space 保持**可选**分组。`0013` 仅放开约束；刻意**不解绑** `0007` 已分配给默认 Space 的项目（无数据改动、无作用域收缩）。
- **`0014_clone_coordination.sql`**（append-only，排在 upstream `0008–0013` 之后）：经内部控制契约的 clone 协调（与 Effect 级 `operations` 模型独立）：
  - `clone_requests`：Cloud 在业务事务中接受的工作项，`(tenant, user, request_id)` 幂等，状态 `queued→dispatched→succeeded/failed`。
  - `clone_executions`：Controller 派发前登记的执行（每个请求恰一个执行，身份为 Controller 选择的 opaque 字符串）、输入与终态结果、登记时的租约 epoch。
  - `clone_event_receipts`：Node 原事件的精确收据 `(execution, sequence, event)`，是确认 Node 的唯一依据。
  - `control_submissions`：每个状态变更提交的身份、请求摘要与记录的响应；同身份同内容回放响应，不重新应用。
- **`0015_skills.sql`**（append-only，排在 `0013` 之后）：Cloud Skills 的 Workspace 归属领域与持久化地基，对应 `specs/decisions/cloud/skills/0-cloud-skills.md`：
  - `skills`：归属恰好一个 Collaboration Workspace（`collab_workspaces`，而非运行时 `workspaces` 表）的可变业务资源；active Skill 的 `(workspace_id, canonical_name)` 由部分唯一索引约束，软删除后释放该名称。
  - `skill_revisions`：某 Skill 的不可变 canonical content 快照（刻意不含 `version`/`updated_at`/`deleted_at`）；`UNIQUE(skill_id, digest_algorithm, content_digest)` 令同一 Skill 同一 digest 只存在一个 revision，不同 Skill 相同内容仍为独立业务 revision。
  - `skill_ingestions`：journal-first 的上传/导入 saga evidence，状态 `planned→storing→verified→committed/failed`，与 SkillRevision 严格分离。
  - `skills.current_revision_id` 复合外键 `(current_revision_id, id) → skill_revisions(id, skill_id)` 保证指针只能指向本 Skill 的 revision；`skills.workspace_id` 由 `skill_immutable` 触发器禁止原地变更（跨 Workspace 移动只能 copy/export/import 新 Skill）。
  - 刻意不创建 AgentSkillBinding / ExecutionSkillBinding / Object Storage 表；signed RetrievalCapability 属于 bearer credential，不持久化。
- **`0016_skill_ingestion_idempotency.sql`**（append-only，排在 `0015` 之后；对应 `specs/decisions/cloud/skills/20260927-canonical-ingestion-saga.md` Schema 一节）：对 `skill_ingestions` 的四个 additive 变更，实现 canonical ingestion saga（Phase 2）的幂等与恢复：
  - `canonical_name`（nullable，`IS NULL OR length BETWEEN 1 AND 200`）：candidate 的稳定身份分量（D6/D11），是 per-candidate 幂等 namespace `(workspace_id, idempotency_key, canonical_name)` 的一部分；此处先 nullable 以兼容 Step 1A 的历史/测试行（唯一索引对 NULL 视作互异），ingestion 管线总是写入非空值，后续 forward migration 可收紧为 `NOT NULL`。
  - `request_fingerprint`（`IS NULL OR length BETWEEN 1 AND 128`）：语义请求指纹（D10），`(target_skill_id, display_name, summary, content_digest, package_digest)` 的 domain-separated、length-prefixed SHA-256 hex，是 replay-vs-conflict（D22）的 durable 判定依据。
  - `activation_outcome`（`IS NULL OR IN ('activated','activation_conflict')`）：activation CAS 结果的 durable 表示（D20）；非 `committed` 态恒为 NULL。
  - 唯一索引 `skill_ingestion_candidate_uniq(workspace_id, idempotency_key, canonical_name)`：确定、并发安全的 per-candidate 幂等身份，是恢复路径「确定性命中既有 ingestion」的前提。
  - 本文件不修改 `0015_skills.sql`。
- **`0017_agents_and_skill_bindings.sql`**（append-only，排在 `0016` 之后；对应 `specs/decisions/cloud/agent/0-agent-skill-binding.md`）：Cloud Skills 的 Agent 权威与 AgentSkillBinding：
  - `agents`：归属恰好一个 Collaboration Workspace 的可变业务资源（软删除 `deleted_at`、`status active|disabled`、active name per-workspace 唯一、workspace 归属不可变）。
  - `agent_skill_bindings`：可变的 future-execution 配置（`enabled` boolean、`version`、composite PK `(agent_id, skill_id)`、remove=DELETE），只引用 Skill 业务身份，绝不引用 SkillRevision / digest / locator。
  - 历史正确性由 Phase 5 的 `execution_skill_bindings` 承载，不由本表承载。
- **`0018_execution_snapshot.sql`**（append-only，排在 `0017` 之后；对应 `specs/decisions/cloud/skills/20260927-execution-skill-snapshot.md`）：Cloud Skills 的逻辑 Execution / 物理 Attempt / 不可变 ExecutionSkillBinding 快照：
  - `executions`：逻辑执行身份（`execution_id`、`tenant_id`、`workspace_id`、`agent_id`、`actor_user_id`、`input jsonb`），无 version/updated_at/deleted_at。
  - `attempts`：物理尝试（`ordinal` 递增、状态机 `eligible→…→succeeded/failed/canceled/superseded`、`UNIQUE(execution_id, ordinal)`）；首个 Attempt 与 Execution 同一事务原子创建。
  - `execution_skill_bindings`：不可变执行 fact（`skill_revision_id` + 冗余 `content_digest`/`size_bytes`/`package_format(_version)`，复合外键 `(skill_revision_id, skill_id)`），trigger 阻止 UPDATE，无 bearer-credential 字段。
  - 刻意不创建 RetrievalCapability / signed-URL / object-locator 字段，也不引入 dispatch/claim/lease 管线（Phase 6+）。
- **`0019_skill_delivery_metadata.sql`**（append-only，排在 `0018` 之后；对应 plan Phase 6A.1）：冻结交付 integrity 元数据，使 Node 的字节级校验链（`SHA256(package bytes) == package_digest` **且** decoded tree digest == `content_digest`）能由 durable 不可变 revision 元数据驱动，而非解析 object key / signed URL / frontend / mutable current-revision：
  - `skill_revisions` 增加 `package_digest`（exact ora-skill-package container 的字节级 SHA-256，object-storage ADR D4）与 `package_digest_algorithm`（默认 `sha256`）。`digest_algorithm` 仍是 **content/tree** digest 算法（与 `0015` 的 `UNIQUE(skill_id,digest_algorithm,content_digest)` 一致）；package digest 用独立显式列，两层永不混同。
  - `execution_skill_bindings` 增加 `digest_algorithm`（content）、`package_digest`、`package_digest_algorithm`，使冻结快照携带完整 delivery digest（admission 时一次写入；表的 immutability trigger 继续禁止 UPDATE）。
  - **Legacy-row 兼容（§4）**：全部新列可空——0019 之前的 revision/binding 行没有 durable package digest，且迁移 SQL 绝不允许靠解析 `object_locator` key 或访问 Object Storage 来恢复（违反 §7 权威规则）。新写入要求该值，core 的 admission 路径在可信 `package_digest` 缺失时 fail closed。pre-0019 行的回填（如需）属独立的 runtime 机制（re-ingest / verified recompute），不是迁移 SQL 编造 digest。
  - 本文件不修改 `0015` / `0018`。

## 校验和完整性与不可变性

- **`schema_migrations` 表**：记录已应用的迁移版本号、SHA256 校验和以及执行时间戳（`version`、`checksum`、`applied_at`）。**`version` 是完整文件名**（如 `0010_issue_collaboration.sql`），因此重命名已应用的迁移会改变其身份并破坏既有数据库——迁移一经应用不可修改，只能追加新文件。
- **服务端启动自检**：启动时，`cmd/server` 执行 `store.CheckSchema`，严格校验：
  1. 所有内嵌的 `.sql` 迁移脚本均已存在于 `schema_migrations` 表中。
  2. 每个内嵌文件的 SHA256 校验和与数据库中记录的校验和完全吻合。
  3. 数据库中不存在任何未知或多余的迁移版本。
  如果发现任何校验和不匹配或未应用的迁移，服务器会立即终止。
- **不使用 AutoMigrate**：生产服务器守护进程启动时**从不**执行 DDL 或修改表结构。迁移必须使用专用数据库管理员凭据通过 `cloudctl migrate` 应用。

参见 [core 总览](../README.md)、[cloudctl CLI 工具](../../../cmd/cloudctl/README.md) 与 [核心不变量与契约](../../../docs/core-contract.md)。
