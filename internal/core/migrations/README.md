# 数据库迁移模块

[中文](README.md) | [English](README.en.md)

本模块包含 Ora Cloud 线性、仅向前的 PostgreSQL Schema 迁移目录。迁移通过 `embed.FS` 直接嵌入 Go 应用程序二进制，并由 `cloudctl migrate` 以确定的方式应用。

## 迁移目录

迁移脚本严格按照数字序号递增顺序执行：

- **`0001_core.sql`**：基础领域 Schema：
  - 身份与访问管理：`users`、`user_identities`、`tenants`、`tenant_memberships`、`credential_refs`。
  - 项目与工作区：`projects`、`project_storage`、`workspaces`、`workspace_worktrees`、`tasks`。
  - 执行运行时：`sandbox_instances`、`workspace_nodes`、`sessions`。
  - 控制平面：`effects`、`operations`、`tickets`、`controller_leases`、`idempotency_keys`。
  - 不变量：部分唯一索引 `one_main` 保证每个项目最多只有一个活动的 `main` 工作区。外键在所有层级中严格强制租户和所有者的包含关系。
- **`0002_aggregate_guards.sql`**：并发与互斥守卫：
  - 防止在同一个项目聚合根上发生并发的生命周期变更。
  - 确保祖先实体软删除后，其子实体无法进行活动状态转换。
- **`0003_resource_versions.sql`**：乐观并发版本控制：
  - 在可变实体（`projects`、`workspaces`、`tasks`、`nodes`、`operations`）上强制执行 `version` 递增规则。
  - 杜绝并发 API 操作中的更新丢失（lost updates）问题。
- **`0004_effect_intent_and_ticket_scope.sql`**：执行意图与 Ticket 作用域约束：
  - 严格将执行 Ticket 限制到活动的 Workspace Node 和有效的准入 epoch。
  - 将持久化的 Effect 声明绑定至特定的操作阶段。
- **`0005_gateway_auth.sql`**：Gateway 认证表（由 `cmd/gateway` 独占运行时访问）：
  - `gateway_login_attempts`：一次性登录尝试；只保存 attempt secret 与 `state` 的 SHA-256 digest，`return_to` 在数据库层拒绝绝对、`//`、`/\` 形式，有效期不超过 1 小时，`consumed_at` 保证最多创建一个 session。
  - `gateway_sessions`：浏览器会话；只保存 token digest，`expires_at` 非空且不超过创建后 90 天，吊销时间与有限的 `revoked_reason` 同时存在，并为 identity 吊销与有界清理建立索引。
- **`0006_collab_spaces.sql`**：协作空间（产品术语 Workspace）Schema：
  - `collab_workspaces`：租户内的协作与可见性边界（名称、不可变 slug、归档时间、乐观版本）。
  - `collab_workspace_members`：成员与角色（owner/admin/member）、状态（active/disabled）、乐观版本。
  - 与运行时 `workspaces` 表（执行环境）严格分离。
- **`0007_project_space_scope.sql`**：Project 空间作用域与数据回填：
  - 为每个既有租户（含仍有 Project 的已删除租户）创建默认 Space（slug=`default`）。
  - 既有 active tenant members 加入默认 Space（admin→owner，member→member）。
  - 既有 Project 回填 `space_id` 并转为 NOT NULL，复合外键 `(space_id, tenant_id)` 在 SQL 级杜绝跨租户归属。
  - `SET CONSTRAINTS ALL IMMEDIATE` 在 ALTER 前触发回填 UPDATE 排队的 deferred 约束触发器。
- **`0008_issues.sql`**：Issues 看板基线表 `issues`。
- **`0009_issue_extensions.sql`**：`issue_statuses`、`issue_comments`、`labels`、`issue_labels`、`issue_subscribers`、`issue_views` + `issues` ALTER（`number`、`properties`、状态格式检查）。
- **`0010_issue_collaboration.sql`**：`issues` ALTER（`assignee_type`/`assignee_id`/`project_ref` + 回填）、`issue_comments` ALTER（`parent_id`/`author_type`/`author_id`/`seq` + 回填 + `UNIQUE(issue_id,seq)`）、新表 `issue_runs`、`issue_activities`、`issue_context_refs`。
- **`0011_issue_interactions.sql`**：新表 `issue_interactions`（`@` 交互脊）——每个选中的协作目标一行：`id, tenant_id, issue_id, comment_id, target_type, target_id, mode, task, run_id, created_at`。
- **`0012_issue_interaction_input.sql`**：一个通用增量列：`ALTER TABLE issue_interactions ADD COLUMN input jsonb NOT NULL DEFAULT '{}' CHECK (jsonb_typeof(input)='object')` —— 已确认的表单值。

> 迁移编号说明（upstream 合并对齐）：本分支的协作空间迁移 `0011_collab_spaces.sql` /
> `0012_project_space_scope.sql` 与 upstream `0006_collab_spaces.sql` / `0007_project_space_scope.sql`
> 功能重叠；**upstream 编号与语义胜出**，本分支的重复迁移已删除，仅保留 upstream 的 0006/0007。
> 原有 Issue 迁移重新编号以保持线性、仅向前的顺序（Schema 语义不变）：
> `0006_issues.sql` → `0008_issues.sql`、`0007_issue_extensions.sql` → `0009_issue_extensions.sql`、
> `0008_issue_collaboration.sql` → `0010_issue_collaboration.sql`、
> `0009_issue_interactions.sql` → `0011_issue_interactions.sql`、
> `0010_issue_interaction_input.sql` → `0012_issue_interaction_input.sql`。

## 校验和完整性与不可变性

- **`schema_migrations` 表**：记录已应用的迁移版本号、SHA256 校验和以及执行时间戳（`version`、`checksum`、`applied_at`）。
- **服务端启动自检**：启动时，`cmd/server` 执行 `store.CheckSchema`，严格校验：
  1. 所有内嵌的 `.sql` 迁移脚本均已存在于 `schema_migrations` 表中。
  2. 每个内嵌文件的 SHA256 校验和与数据库中记录的校验和完全吻合。
  3. 数据库中不存在任何未知或多余的迁移版本。
  如果发现任何校验和不匹配或未应用的迁移，服务器会立即终止。
- **不使用 AutoMigrate**：生产服务器守护进程启动时**从不**执行 DDL 或修改表结构。迁移必须使用专用数据库管理员凭据通过 `cloudctl migrate` 应用。

参见 [core 总览](../README.md)、[cloudctl CLI 工具](../../../cmd/cloudctl/README.md) 与 [核心不变量与契约](../../../docs/core-contract.md)。
