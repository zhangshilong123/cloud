# 开发进度（新员工向）

一眼看清 Ora Cloud 目前**做到哪了、在进行什么、刻意没做什么**。更新于 **Wave 3B-2 Workflow
Interaction Shell 实现并验证 + User Registration 落地 + Project Workspace Sharing（Step 3）+
Workspace Member Management & Onboarding（Step 3A）**之后：`@` 协作链路
（Mention / Task / **Workflow Form Mode**）已端到端
打通——`@Workflow → FormDescriptor → 动态表单 → 可选 AI Assist → Review → Confirm → IssueRun → mock
执行 → Timeline`；**Project 已按 Workspace 共享**（同 Workspace 成员互见/互访共享项目，删除需
creator 或 owner/admin）；**Workspace foundation 已收口**（0-Workspace onboarding + 可选创建；成员
角色 owner-only、owner immutable；移除 owner-only 且仅移除成员资格）。契约与实现记录见
[12-collab §38 / §38.37](../../migrations/multica-issue-board/12-collaboration-architecture.md#38-wave-3b-2--workflow-interaction-design-frozen)，
下一步是 3C（Issue Detail & Collaboration UI）与 Issue Workspace Scoping。另：Cloud Skills 公共上传 API
已落地（Step 3A 契约冻结 + Step 3B 实现，见下文 Cloud Skills 一节）。

> **2026-09-21 — Workspace Integration Stage A**：`origin/main`（含 `cmd/gateway`、`0005_gateway_auth`）已受控融合进
> `workspace合并`（main 只读、SHA 不变）；Issues 迁移前移重编号 **0005→0006 … 0009→0010** 以保持上游编号稳定；
> 前端认证/会话基座 = ora-web cookie 会话（决策 D-Auth=A）。记录见
> [workspace-integration-stage-a.md](../../migrations/workspace-integration-stage-a.md)。
>
> **2026-09-23 — 922GithubAuth ← main 会话架构合入**：feature 分支 `922GithubAuth` 合并 main 的
> **gateway-会话架构**（已合并提交 `9e887d4`，待人工测试验收）。前端会话/路由采用 main：`SessionProvider`/`useSession`/
> `RequireSession`、`/onboarding` + `/w/:workspaceSlug` 路由、onboarding 独立页（`useCreateTenant` /
> `useCreateSpace`）；删除 feature 的 zustand 商店（`auth-store`/`demo-auth-store`）；保留 Step 3A 能力
> （members owner-immutable UI、`canDeleteProject = creator OR owner/admin`、issues 接真实后端）。
> 开发拓扑 = **双入口**（ora-web :8080 DEV-only + gateway :8081 生产规范）；项目模型 = **混合/optional
> space**（`space_id` 可空，默认空间；reconciliation 后由 upstream `0007` + `0013_project_space_optional`
> 承载：`0007` 绑定既有项目，`0013` 恢复可空、不解绑）。注册 UI 移除（见
> [user-registration.md](../../migrations/user-registration.md) 合并注记）。
>
> **2026-09-21 — User Registration**：登录页「注册」入口落地 —— `POST /auth/register` 创建 User Identity
> （姓名 + 邮箱），邮箱 trim + lowercase 大小写不敏感唯一，重复 `409 user_already_exists`，注册成功直接进入
> 既有 current-user 流程；**AUTHENTICATION NOT FULLY IMPLEMENTED · PROJECT SHARING NOT
> IMPLEMENTED**。ADR：`specs/decisions/cloud/identity-access/0-user-registration.md`；记录见
> [user-registration.md](../../migrations/user-registration.md)。
>
> **2026-09-21 — Workspace Add Member**：Workspace 管理界面「添加成员」落地 —— owner/admin 用 email 把
> **已注册用户**添加为普通成员（`POST /spaces/:sid/members`），原子双写（同一事务 ensure 租户成员 +
> 空间成员），新成员固定 `member`、重复添加幂等返回 existing、未知邮箱 `404 user_not_registered`；
> 新成员 refresh/re-enter 后能在 selector 看到并切换该 Workspace，但 **Project 访问仍 owner-only**
> （当前实现，migration pending；旧 SD6「Workspace Membership ≠ Project Access」已被取代）。
> **PROJECT SHARING / EMAIL INVITATION NOT IMPLEMENTED**。ADR：
> `specs/decisions/cloud/collaboration-workspace/20260921-workspace-add-member.md`；记录见
> [workspace-membership.md](../../migrations/workspace-membership.md)。
>
> **2026-09-21 — Workspace Sharing Model（Step 2B）**：模型对齐（非资源迁移）—— **Workspace 冻结为
> 资源共享边界**（member 最终可访问 Workspace 共享资源：Projects/Issues/Agents/Teams/Workflows/MCPs/
> Skills），**禁止逐资源 membership**（`project_members` 等不设计）；统一删除规则
> `CanDeleteWorkspaceResource = creator OR owner/admin`；落地最小谓词 `IsWorkspaceMember` /
> `IsWorkspaceAdmin` / `CanDeleteWorkspaceResource`（无通用 RBAC engine）。旧 D1 owner-isolation
> **产品规则已取代**：owner-only Project 访问降级为**当前实现**，project workspace-sharing migration
> pending（下一步）。ADR：`specs/decisions/cloud/collaboration-workspace/20260921-workspace-sharing-model.md`；
> 记录见 [workspace-sharing-model.md](../../migrations/workspace-sharing-model.md)。
>
> **2026-09-21 — Project Workspace Sharing（Step 3）**：实施真正的项目共享迁移 —— `project()` /
> `workspace()` / 空间项目列表从 owner-only 切换为 **workspace-shared**（`CanAccessProject =
> space_id 归属 + Workspace 成员资格`；unscoped `space_id=NULL` 保持 owner-only）；运行时 Workspace
> 继承父 Project 访问；删除规则 `creator OR Workspace owner/admin` 接入真实资源（member 删他人 →
> `403 space_role_required`，非成员 → 404 隐藏）；tenant 级 `/projects` 列表保持 owner 过滤；
> **前端零改动**。Issue / Agent / Team / Workflow / MCP / Skill 作用域仍未实施。ADR：
> `specs/decisions/cloud/collaboration-workspace/20260921-project-workspace-sharing.md`；记录见
> [project-workspace-sharing.md](../../migrations/project-workspace-sharing.md)。
>
> **2026-09-22 — Workspace Member Management & Onboarding（Step 3A）**：Workspace foundation 收口 ——
> 注册用户 0 Workspace 是**合法状态**：前端 onboarding 空态（「创建工作区」复用 `POST /spaces`，creator
> 自动成为 owner，或「请工作区所有者通过邮箱添加你」），不再只是退出登录死胡同；成员角色管理 **owner-only**
> （`PUT /members/:uid` 收紧，admin↔member；**owner 角色 immutable** —— 任何所有权转移 →
> `409 ownership_transfer_not_supported`）；新增 **owner-only** `DELETE /members/:uid` 硬删成员（owner
> 行不可移除 → `409 cannot_remove_workspace_owner`；移除 = 仅移除工作区成员资格，账号/租户成员/资源保留，
> 访问自然撤销）；Project delete UI 对齐后端 `creator OR owner/admin`。**PROJECT WORKSPACE SHARING:
> UNCHANGED**；Issue / Agent / Team / Workflow / MCP / Skill 作用域仍未实施。ADR：
> `specs/decisions/cloud/collaboration-workspace/20260922-workspace-member-management.md`；记录见
> [workspace-member-management.md](../../migrations/workspace-member-management.md)。

图例：✅ 已完成 · 🚧 进行中 · 🧭 规划中（仅架构方案，未编码） · ⏸️ 刻意暂缓 · ❌ 未开始

## 平台核心

| 能力 | 状态 | 说明 |
| --- | --- | --- |
| 租户 / 用户 / 成员 | ✅ | 含角色（admin/member）、最后管理员保护 |
| 身份认证（双 JWT） | ✅ | service + user 两层凭证 |
| 项目（project） | ✅ | 绑定 Git 仓库，生命周期管理 |
| 工作区（workspace） | ✅ | main / isolated，Git worktree |
| 异步操作（operation） | ✅ | 持久化、可重试、分步推进 |
| 幂等 + 乐观并发 | ✅ | Idempotency-Key + version |
| 真实节点 / 沙箱 / 存储 | ⏸️ | 目前用内存模拟器（`internal/simulator`）；生产 Substrate 是后续阶段 |

## 身份与会话 — User Registration ✅ IMPLEMENTED

注册 = **创建 User Identity**（姓名 + 邮箱 → 会话 → 进入既有 current-user 流程），不是密码认证系统。
ADR：`specs/decisions/cloud/identity-access/0-user-registration.md`（SD1–SD5）；记录见
[user-registration.md](../../migrations/user-registration.md)。

| 能力 | 状态 | 说明 |
| --- | --- | --- |
| `POST /auth/register`（ora-web 边界） | ✅ | `{name, email}` → 建 user + identity + 租户普通成员 → 设置 `ora_subject` cookie → 返回登录同构响应 |
| 邮箱规范化 / 大小写不敏感唯一 | ✅ | trim + lowercase；`Alice@Example.com` == `alice@example.com`；`user_identities(PK(source,subject))` 兜底 |
| 重复邮箱 | ✅ | 稳定 `409 user_already_exists`（精确 + 大小写变体），不静默复用 |
| 非法邮箱 / 空姓名 | ✅ | `400 invalid_email` / `400 name_required`（轻量门：非 @ 结构 / 空名） |
| 前端注册模式 | ✅/⚠️ | 原 Step 描述：既有 Login 页加「注册」切换，成功进 `/default/projects`。**合并 main 后已变**：前端 Login 页为 provider-button 形态（gateway 会话），**无 register 模式**；`POST /auth/register` 仅保留为 ora-web 开发边界 API（浏览器经 ora-web :8080 注册仍可用）；合并后注册成功经 `/onboarding` + `/w/:slug` 进入（见 [[user-registration]] 合并注记） |
| 测试 | ✅ | 后端集成 6 用例 + 前端 5 用例全通过；HTTP smoke 通过 |
| **AUTHENTICATION** | ❌ **NOT FULLY IMPLEMENTED** | `/auth/login` 仍是「任意 email 即登录」，无密码/凭据/OAuth/SSO |
| **PROJECT SHARING** | ❌ **NOT IMPLEMENTED** | 无项目分享 |

## Workspace Add Member — 用 email 添加已注册用户 ✅ IMPLEMENTED

owner/admin 在 Workspace 管理界面用 email 把**已注册用户**添加为普通成员；成员列表立即出现该用户，
该用户 refresh/re-enter 后能在 Workspace selector 看到并切换。**当前：Project 按 Workspace 共享**
（Step 3 已实施项目共享 —— 新成员可见/可切换 Workspace，并能访问该 Workspace 内的共享 Project 及其
运行时 Workspace；旧「Workspace Membership ≠ Project Access」产品语义已被
[workspace-sharing-model.md](../../migrations/workspace-sharing-model.md) 取代）。
ADR：`specs/decisions/cloud/collaboration-workspace/20260921-workspace-add-member.md`（SD1–SD6）；
记录见 [workspace-membership.md](../../migrations/workspace-membership.md)。

| 能力 | 状态 | 说明 |
| --- | --- | --- |
| `POST /spaces/:sid/members`（email 添加） | ✅ | `{email}` → 严格 `users JOIN user_identities`（source=调用者身份源、subject=normalizeEmail）解析；未知/非 active → `404 user_not_registered`（不建号、不邀请、无 pending） |
| 原子双写 | ✅ | 同一事务先 ensure 租户成员（`ON CONFLICT DO NOTHING` 保留原角色）再建空间成员，无半状态 |
| 角色门 | ✅ | 业务级强制（后端检查，非仅隐藏按钮）：owner/admin 可添加；member → `403 space_role_required` |
| 新成员固定 `member` | ✅ | request 不接受 owner/admin；角色调整仍走既有 `PUT /members/:uid` |
| 重复添加幂等 | ✅ | 返回 existing membership（200，不改 role/status/version，count 仍 1） |
| Idempotency-Key | ✅ | POST 自动继承；前端 mutation 生成/复用 key 防重放 |
| 前端添加成员 Dialog | ✅ | 邮箱输入 + 本地校验（空/非法提示）+ 服务端 `user_not_registered` →「该邮箱尚未注册，请先完成注册。」；成功关 dialog + 列表刷新；触发器仅 owner/admin 可见 |
| Selector 反射新 Workspace | ✅ | refresh/re-enter = 新页面加载 → 内存缓存重取 `/spaces`，selector 出现并可切换；未重写 Current Workspace provider |
| **CURRENT PROJECT ACCESS** | ✅ **WORKSPACE MEMBERSHIP** | B 加入 W 后可访问 W 内共享 Project：`/spaces/:sid/projects` 含全部活跃项目、`/projects/:pid` 200、`/workspaces/:wid` 200；unscoped `space_id=NULL` 项目保持 owner-only（无 scope widening） |
| 测试 | ✅ | 后端集成 8 用例 + 前端 7 用例全通过；HTTP smoke 通过（A/B 双用户）；Step 2B 的两个 owner-only 回归已改写为 Step 3 共享断言（`TestScopedProjectSharedWithWorkspaceMembers` / `TestRuntimeWorkspaceInheritsProjectAccess`） |
| **PROJECT WORKSPACE SHARING** | ✅ **IMPLEMENTED** | 项目共享已实施（Step 3）：访问 = `space_id` 归属 + Workspace 成员资格；删除 = creator 或 owner/admin（member 删他人 `403 space_role_required`，非成员 404 隐藏）；无 `project_members` |
| **EMAIL INVITATION** | ❌ **NOT IMPLEMENTED** | 只接受已注册用户，无邀请邮件/pending/自动建号 |
| **WORKSPACE MEMBER AUTO-PROJECT ACCESS** | ✅ **IMPLEMENTED** | 空间成员身份派生共享 Project 的可见/访问/运行时继承（Step 3） |

## Workspace Resource Sharing Model — Workspace 资源共享边界 ✅ IMPLEMENTED（Step 2B 对齐）

**Workspace = resource sharing boundary**（资源共享模型对齐，非资源迁移）：Workspace 成员最终可访问
该 Workspace 内共享的资源（Projects / Issues / Agents / Teams / Workflows / MCPs / Skills）；
**禁止**逐资源 membership（`project_members` / `issue_members` / `agent_members` / `team_members`
不设计）。统一删除规则：**`CanDeleteWorkspaceResource = creator OR workspace owner/admin`**。旧 D1
owner-isolation 产品规则已**取代**（记录为历史，不删除）；owner-only Project 访问是**当前实现**，
migration pending。ADR：`specs/decisions/cloud/collaboration-workspace/20260921-workspace-sharing-model.md`
（SS1–SS5）；记录见 [workspace-sharing-model.md](../../migrations/workspace-sharing-model.md)。

| 能力 | 状态 | 说明 |
| --- | --- | --- |
| `IsWorkspaceMember(uid, sid)` | ✅ | Store 方法 `(bool, error)`；活跃成员（复用 `spaceMember` join，非 panic） |
| `IsWorkspaceAdmin(uid, sid)` | ✅ | **owner OR admin** |
| `CanDeleteWorkspaceResource(uid, sid, creator)` | ✅ | `uid == creator` 或 owner/admin；普通 member 不能删他人，非成员无权限 |
| 事务内 `workspaceRole(t, sid, uid)` | ✅ | 非 panic 谓词，供未来 delete-authorization 在同一 `transact` 内复用 |
| 无通用 RBAC engine | ✅ | 只落地最小谓词 foundation，不建 RBAC 框架 |
| 测试 | ✅ | `integration/space_permission_test.go` 8+1 用例全通过（真实 PG） |
| **Workspace Membership** | ✅ **IMPLEMENTED** | Step 2 能力原样保留（添加/幂等/角色门/可见性） |
| **Project Workspace Scoping** | ✅ **IMPLEMENTED（Step 3）** | Project list/detail/runtime 访问已切换为 workspace-shared；删除 = creator 或 owner/admin；unscoped 保持 owner-only。见 [project-workspace-sharing.md](../../migrations/project-workspace-sharing.md) |
| **Issue Workspace Scoping** | ❌ **NOT IMPLEMENTED** | `issues.space_id` 未引入 |
| **Agent / Team / Workflow / MCP / Skill Workspace Scoping** | ❌ **NOT IMPLEMENTED** | 各资源仍无 Workspace 共享 |

## Workspace Member Management & Onboarding — Step 3A ✅ IMPLEMENTED

Workspace foundation 收口：**0-Workspace 是合法状态**（注册用户可选创建自己的 Workspace，或等待被添加）；
成员角色管理 **owner-only**（owner 角色经 member API **immutable**，禁止所有权转移）；移除成员
**owner-only 硬删**（仅移除工作区成员资格，不删账号/租户成员/资源，访问自然撤销）；Project delete UI
对齐后端 `creator OR owner/admin`。ADR：`specs/decisions/cloud/collaboration-workspace/20260922-workspace-member-management.md`
（MM1–MM9）；记录见 [workspace-member-management.md](../../migrations/workspace-member-management.md)。

| 能力 | 状态 | 说明 |
| --- | --- | --- |
| 0-Workspace onboarding | ✅ | 注册后无 W：前端空态「你还没有加入任何工作区」+「创建工作区」（复用 `POST /spaces`，creator 自动成为 owner）或「请工作区所有者通过邮箱添加你」；不 crash / 白屏 / redirect / fake workspace；新注册用户不自动加入 default space |
| 角色变更 = owner-only | ✅ | `PUT /members/:uid` actor 收紧为 owner；admin 保留 Add Member，不扩大为改角色/删成员 |
| owner 角色 immutable | ✅ | 任何所有权转移（member→owner、admin→owner、owner 自降）→ `409 ownership_transfer_not_supported`；last-owner 死分支删除 |
| Remove member | ✅ | 新增 `DELETE /members/:uid`（owner-only、Idempotency-Key + version、硬删）；owner 行不可移除 → `409 cannot_remove_workspace_owner`（含 owner 自移除） |
| 移除语义 = membership-only | ✅ | 只删 `collab_workspace_members` 行；用户账号、租户成员关系、`projects.owner_user_id`（creator 身份）与 W 内资源均保留；其余成员仍可访问 |
| 访问自然撤销 | ✅ | 移除后 `listSpaces`/`spaceMember`/`workspaceRole` 无活跃行 → W / P / Runtime 自动隐藏；被移除 creator 不能绕过成员资格（scoped 授权先满足活跃成员） |
| 能力矩阵 | ✅ | Owner {Add ✓, Change role ✓, Remove ✓}；Admin {Add ✓, Change role ✕, Remove ✕}；Member {全 ✕}；前端 UI 与后端独立强制一致 |
| Project delete UI 对齐 | ✅ | `canDeleteProject = owner/admin OR currentUser == project.ownerUserId`（member+creator 可见；后端 Project delete 授权 UNCHANGED） |
| 前端成员管理 UI | ✅ | 角色 Select 仅 owner 显示且无 owner 项；Remove AlertDialog（「从工作区移除「{name}」？」+ membership-only 文案）；owner 行只读 |
| 测试 | ✅ | 后端集成 8 用例（注册三态 / owner 增升降删 / 移除撤销+资源保留 / 授权矩阵 / owner 保护 / 跨 tenant 无泄漏）+ 3 个既有测试改写；前端 18/18（onboarding / members owner-only / project delete UI）；HTTP smoke 通过 |
| **PROJECT WORKSPACE SHARING** | ✅ **UNCHANGED** | 后端共享模型（Step 3）未动，仅前端删除按钮门对齐 |
| **ISSUE WORKSPACE SCOPING** | ❌ **NOT IMPLEMENTED — NEXT STEP** | `issues.space_id` 未引入 |
| **AGENT/TEAM/WORKFLOW/MCP/SKILL WORKSPACE SCOPING** | ❌ **NOT IMPLEMENTED** | 各资源仍无 Workspace 共享 |

## Cloud Skills — 持久化地基 + Canonical Package v1 + Object Storage 抽象 + Ingestion Saga 实现（Phase 2 / Step 1A + Step 2A；Phase 3 / Step 2B + Step 2C）✅ PERSISTENCE + CANONICAL PACKAGE + OBJECT STORAGE ABSTRACTION + INGESTION SAGA IMPLEMENTED

Cloud Skills 域的第一步：**持久化地基**已落户（migration `0015_skills.sql`），**仅建表 + 约束 +
持久化测试**，不涉及任何 HTTP API、Object Storage 或执行链路。Skill 归属**恰好一个** Collaboration
Workspace（`collab_workspaces`，非运行时 `workspaces` 表），与「Workspace 资源共享边界」模型方向一致，
但**尚未**进入共享/授权暴露（前文各「Workspace Scoping」行仍为 NOT IMPLEMENTED——那是指把 Skill 作为
Workspace 共享资源暴露的下一步，持久化地基只先把表和归属约束立起来）。Step 2A 在其上新增
**canonical package v1 纯内容层**（`internal/skillpkg`：canonical path 校验、ManifestV1、per-file SHA-256、
tree digest、`ora-skill-package` v1 encode/decode/verify、bounds 与 golden vectors），**无** Object Storage /
上传 API / 摄取 saga / 执行 / 前端。Step 2B.0 冻结了**Object Storage 设计**（ADR
`20260927-object-storage-abstraction.md`：immutable object identity、object key 推导、create-only
`PutImmutable`、五值外部结果分类（`CONFIRMED_PRESENT_MATCHING` / `CONFIRMED_ABSENT` / `MISMATCH` /
`DEFINITE_FAILURE` / `INDETERMINATE`）、按 `object_locator` probe 的 reconciliation、`MISMATCH` fail
closed、crash adopt 语义、`skill_ingestions` 状态映射）。Step 2B 把其中**已经冻结的 port / 语义类型 /
reconciliation core / 测试替身 / 测试**落地为代码（`internal/skillstore`：`ObjectStore` port、
`PutImmutable`/`Stat`/`Get`、typed outcome 分类、`Locator`、`Reconcile` 三项校验；`internal/skillstore/fakestore`
内存测试替身）——但**仍然没有**任何生产 Object Storage provider / 配置 / SDK 依赖 / 上传 HTTP API /
摄取管线 / `SKILL.md` 发现 / `RetrievalCapability` / Node 交付，`0015_skills.sql` 不需要修改。ADR：
`specs/decisions/cloud/skills/0-cloud-skills.md`（根，状态 `proposed`）＋
`specs/decisions/cloud/skills/20260924-canonical-skill-package-v1.md`（dated follow-up，冻结
manifest/digest/container 字节契约，状态 `proposed`）＋
`specs/decisions/cloud/skills/20260927-object-storage-abstraction.md`（dated follow-up，冻结 Object
Storage 抽象与 write/reconcile/recovery 语义，状态 `proposed`）。Step 2C.0 又冻结了**摄取 saga 设计**（ADR
`specs/decisions/cloud/skills/20260927-canonical-ingestion-saga.md`，状态 `proposed`），并经 **Step 2C.0a
design correction** 修正 3 个 blocking 语义缺口：candidate 模型、batch partial-success、**语义 request 指纹
`(target_skill_id, display_name, summary, content_digest, package_digest)`**、per-candidate 幂等身份
`(workspace_id, idempotency_key, canonical_name)`、TX #1/TX #2 内容、**CAS 冲突的 durable 表示
`activation_outcome`（`activated`/`activation_conflict`）**、以及**崩溃恢复 = CLIENT-DRIVEN CONTINUATION**——
V1 无服务端后台 recovery worker、无 canonical Object Storage 持久化之前的 autonomous package-byte recovery，
bytes 由客户端幂等重提交确定性重建、对照 durable identity（`object_locator` + `expected_digest` +
`request_fingerprint`）验证后 probe/adopt；`planned`/`storing` 会无限期 stranded 直至同一 `Idempotency-Key`
重提交。并明确需要一个新的 forward migration（`0016_*`，四个 additive 变更：`canonical_name` +
`request_fingerprint` + `activation_outcome` 列 + 唯一索引，`0015` 不改）。**摄取实现尚未开始**。
**Step 2C.0b** 又关闭了阻塞 Step 2C 的 `canonical_name` 派生缺口（ADR
`specs/decisions/cloud/skills/20260927-skill-md-metadata-contract.md`，状态 `proposed`）：冻结 `SKILL.md`
metadata contract —— `canonical_name = ASCII lowercase(TrimSpace(name))` 为唯一变换、`name` 必填且必须是
YAML string（ASCII `[A-Za-z0-9._-]+`、不以 `.` 开头、≤ 200 bytes）、`description` 可选（string、≤ 4096
bytes）、重复 key 与非法 YAML fail closed、未知字段不透明；解析器归业务 metadata 层（未来
`internal/skillmeta`），`internal/skillpkg` 保持冻结不解析 name；与 Desktop `0-static-skill-package.md` D3
及全部审计到的 multica 内容兼容。仅设计，无代码、无迁移。

**Step 2C 摄取实现已落地**：`internal/skillmeta` 解析器（strict YAML frontmatter + `canonical_name`
派生，纯 CPU、无 I/O，与 `internal/skillpkg` 严格分离）→ forward migration `0016_skill_ingestion_idempotency.sql`
（`canonical_name` + `request_fingerprint` + `activation_outcome` + `UNIQUE(workspace_id, idempotency_key,
canonical_name)`，`0015_skills.sql` 不改）→ `internal/core` 的 journal-first 摄取 saga
（`Store.IngestSkill` / `Store.IngestSkills`：TX #1 authorize + `planned` 行、Object Storage
`PutImmutable`/`Reconcile` 完全在事务外、TX #2 create/reuse Skill + SkillRevision + activation CAS →
`committed`/`activation_outcome`；batch partial-success；client-driven continuation 崩溃恢复）。11 个
`integration/skill_ingestion_test.go` 集成用例（真实 PostgreSQL + 内存 fakestore）覆盖
commit/activate、幂等 replay、409 conflict、revision 去重、batch partial-success、reconcile mismatch、
adopt、strand/resume、authorization、explicit update、object-store unavailable，全部通过。
**仍未实现**：上传 HTTP API（**契约已由 Step 3A 冻结，实现 NOT started**）/ 生产 Object Storage provider /
`RetrievalCapability` / Node 交付 / 前端看板。

**Step 2C.1 source intake & candidate discovery 已实现**：ADR
`specs/decisions/cloud/skills/20260927-source-intake-candidate-discovery.md`（状态 `proposed`，已批准实施）
冻结的四个语义缺口已落地为 `internal/skillsource` + `core.Store.IngestSource` 接缝，未加 migration：
archive 支持集 = 仅 ZIP + 未压缩 TAR（`.tar.gz`/`.tgz`/gzip 不支持，编码由 caller 显式 `SourceKind` 决定，
无 sniffing / parser fallback）；candidate 根之外的游离文件在 source-level 安全校验后忽略（unsafe 则拒绝
source）；nested candidate root = source-level structural error（整 source 在 saga 前失败）；同一 source 内
duplicate canonical_name = source-level structural conflict（整 source 在 TX #1 前失败，绝不靠 DB `UNIQUE`
决定）；invalid `SKILL.md` metadata = candidate-local preparation failure（无 ingestion 行、sibling 继续），
由 `PreparedSourceResult{Candidates[], PreparationFailures[]}` 承载；只有确定的 `PreparedCandidate[]` 才经
`core.Store.IngestSource` 进入现有 `IngestSkills` saga（partial-success 不变）；directory / ZIP / TAR 同一
逻辑树在 candidate path、bytes、`canonical_name`、manifest、digest、package 上完全收敛；schema 影响 NONE
（`0015`/`0016` 不变）。source adapter 单测（directory/zip/tar/convergence）+ 6 个集成用例（真实
PostgreSQL + 内存 fakestore）全部通过。

**Step 3A public upload API contract 已冻结（仅设计，实现 NOT started）**：ADR
`specs/decisions/cloud/skills/20260927-public-upload-api-contract.md`（状态 `proposed`）冻结了公共上传边界
——endpoint `POST /api/v1/tenants/:tid/spaces/:spaceId/skills/imports`（tenant-prefixed、space-scoped，
`spaceId` 即 saga 的 `workspace_id`）；transport 为 `multipart/form-data` 单 archive part、`source_kind ∈
{zip, tar}` 显式（无 sniffing/fallback，directory 由客户端打包为 zip/tar 再传，`KindDirectory` 仍是服务器
端摄取面）；request 字段 = `source_kind`/`source`/可选 `target_skill_id`/`display_name`/`summary`（禁 client
传 canonical_name/digest/locator/revision_id）；`Idempotency-Key` 必填且语义 = saga 幂等 namespace（不写通用
`idempotency_records`）；response = 200 envelope `{sourceKind, preparationFailures[], ingestions[]}`，
source structural failure → `400 source_*`、candidate 失败 → per-item、partial success overall 200、
`activation_conflict` 是 `activation` 非 error_code；replay/continuation 沿用 saga D14/D22；上传预算 ≤ 2 GiB
（provisional 产品上限 256 MiB，超限 `413`），buffering ephemeral-only；schema 影响 NONE（`0015`/`0016`
不变）。**实现未开始**（multipart transport + router/gateway body 预算协调 + contract/OpenAPI/frontend
同步，留待 Step 3B）。

**Step 3B public upload API 已实现**：Step 3A 冻结的契约落地为代码 —— `POST /api/v1/tenants/:tid/spaces/:spaceId/skills/imports`
路由（`router.Routes()` 白名单 + 专用 `uploadSkillSource` handler，`internal/api/router/skill_upload.go`）；multipart
`source_kind∈{zip,tar}` + 单 `source` part，`target_skill_id`/`display_name`/`summary` 可选，字段白名单严格（禁
`canonical_name`/`content_digest`/`package_digest`/`object_locator`/`revision_id`，未知/重复 → `unknown_field`/
`duplicate_field`）；route-local 256 MiB 预算（`skillsUploadBudget`，超限 `413 upload_too_large`，ephemeral-only 不落
staging），**其余所有路由保持 64 KiB**——gateway 路由感知豁免（`requestBodyLimit`/`isSkillsUploadPath` 仅匹配 8 段
`skills/imports` 路径，`internal/gateway/handler.go`）+ router 通用 JSON 循环跳过该路由
（`internal/api/router/router.go`）；双凭证（gateway service role + `X-Ora-User-Token`，`user.Caller ==
service.Subject`）→ `store.ResolveIdentity` → `core.Store.IngestSource`（whole-request 授权：非成员 `404 not_found`、
成员非 owner/admin `403 workspace_admin_required`、`target_skill_id` 跨 Workspace/不存在 `404 not_found`、多 candidate
+ target `400 single_candidate_required`——这些是 HTTP 级拒绝，绝不进 200 envelope 的 per-item errorCode）；`Idempotency-Key`
必填、语义 = saga 幂等 namespace（same key + same fingerprint → replay、same key + different fingerprint → `409
idempotency_conflict`，由 `IngestSkills` 上抛 `*Fault` 不再吞入 per-item）；200 envelope
`{sourceKind, preparationFailures[], ingestions[]}`，source structural failure → `400 source_*`（零副作用），partial
success overall 200，`activation_conflict` 仍是 `activation` 字段而非 errorCode；schema 影响 NONE（`0015`/`0016` 不变、
无 `0017`）；contract/OpenAPI 同步（`internal/contract/openapi.go` 新增 `SourceUploadResult`/`SourcePreparationFailure`/
`SourceIngestionItem` schema + multipart requestBody + `413` 描述，`api/openapi.json` 经 `go run ./cmd/openapi` 重生成
并通过 `TestPublishedOpenAPIIsValidAndCurrent`；frontend 经 orval 重生成 `sourceKind`/`preparationFailures`/`ingestions`
类型，**无看板 UI**）；测试：router 单测（multipart 严格性/预算/缺省）、gateway body-limit 单测、
`integration/skill_upload_test.go` 11 用例（zip/tar happy、replay、409、member 403 / non-member 404 / cross-workspace
404、single_candidate、partial-success 映射、fatal source 零副作用、>64 KiB 接受）全部通过。**仍未实现**：生产 Object
Storage provider / `RetrievalCapability` / Node 交付 / Skills 看板 UI / AgentSkillBinding / ExecutionSkillBinding。

**Step 4A 生产 Object Storage provider 契约已冻结（仅设计，实现 NOT started）**：ADR
`specs/decisions/cloud/skills/20260927-production-object-storage-provider.md`（状态 `proposed`）在设计层关闭了生产
Object Storage 缺口。审计结论：仓库**没有**任何既有权威对象存储 provider / 部署约定（`go.mod` 无 S3/AWS/MinIO/GCS/Azure
SDK；`compose.yaml`/CI 只有 PostgreSQL；无 Helm/K8s；`config` 无 storage 段；`scripts/`/`Learn/`/`configs/`/`Taskfile.yml`
无对象存储引用），因此新选 V1 provider 契约 = **单一 S3-compatible provider（不默认 AWS）**，唯一生产实现方向
（`aws-sdk-go-v2` + `service/s3`，依赖仅在实现切片引入）。冻结的决策：`PutImmutable` create-only = 原生条件
`PutObject(IfNoneMatch="*")` → 412 = `PutAlreadyExists`（禁止 HEAD-then-PUT 的 TOCTOU，无条件写入则 fail closed）；V1
单次原子 PUT、**不启用 multipart**（单 PUT 5 GiB 上限 ≫ `MaxPackageBytes` ≈1 GiB 与 256 MiB 产品上限）；当前 port 保持
`[]byte` 全缓冲、单次 PUT 无放大（256 MiB 不构成操作不安全）；错误映射 200→created / 412→already-exists / 400·403·size
→ permanent / DNS·连接被拒·429 → transient / 5xx·发送后超时 → ambiguous，冻结 taxonomy（not_found / already_exists /
temporary / throttled / unauthorized / forbidden / invalid_configuration / integrity_mismatch / ambiguous /
permanent_failure，原始 SDK 错误绝不泄漏）；超时 connect 5s / request 30s、全部携带 caller context、无后台 retry
worker；配置新增 `storage` 段（provider/bucket/region/endpoint/path_style/credential_mode/credentials_file/tls.verify/
ca_file/allow_insecure_http/timeouts），bucket 单一配置、桶名不进 durable state；凭证仅部署平台机制（环境 / workload
identity / 共享凭证文件），禁止进 DB/`SkillRevision`/API 响应/日志；TLS 生产默认 HTTPS + 校验、`allow_insecure_http`
默认 false；integrity 权威 = `SHA256 + skillpkg.Decode + TreeDigestHex`（ETag 仅 evidence）；启动行为 = `storage` 存在
但非法 → fail fast、缺失 → 进程启动 + `SkillsObjectStore` nil + saga 返回 `object_store_unavailable`（已实现）、不新增
远程 bucket 启动探活；schema 影响 NONE（`0015`/`0016` 不变）。**生产 provider 实现未开始**（adapter + SDK 依赖 +
`cmd/server` 装配 + `CLOUD_STORAGE_*` 绑定 + 启动校验，留待实现切片）。

**Step 4B 生产 Object Storage provider 已实现（改动保持 uncommitted）**：Step 4A 契约落地为生产 adapter——新包
`internal/skillstore/s3store`、`internal/config` 的 `storage` 段、`cmd/server` 装配，**不改变** `ObjectStore` port、schema
（`0015`/`0016` 不变）、摄取 saga 语义。要点：`PutImmutable` = 单次 `PutObject(IfNoneMatch="*")`（无 HEAD-then-PUT、无
multipart），`Stat` = HEAD，`Get` = 有界读 + 1 字节 oversize 探测（绝不无界 `io.ReadAll`）；错误分类经
`*smithyhttp.ResponseError` 取 HTTP 状态（每个非 2xx 都能经 `*smithy.OperationError` 到达），404→absent、412→
already-exists、429→transient、5xx→ambiguous、其余 4xx→permanent、DNS/`dial` 被拒→transient、建立后中断/deadline/
cancel→ambiguous、未知→ambiguous，原始 SDK 错误绝不跨 port；超时 connect 5s/request 30s、每操作派生 caller deadline、
transport `http.DefaultTransport.Clone()`（不改全局）、`RetryMaxAttempts:3`；凭证三模式 `environment`/`shared_credentials_file`/
`workload_identity`，adapter 内解析、永不持久化/进日志；`storage` 段指针 `StorageConfig`（缺失→nil、存在但非法→fail fast），
`applyDefaults`（credential_mode→environment、tls.verify→true、timeouts→5s/30s）后 `Validate`（provider=="s3"、bucket/region
必填、endpoint scheme 与 http-vs-allow_insecure_http、credential_mode 白名单、credentials_file 必填、verify=false ⊥ ca_file、
timeouts≥0），`configs/config.yaml` 带注释样例；`cmd/server.wireObjectStore` 翻译并赋值 `store.SkillsObjectStore`；依赖仅新增
`aws-sdk-go-v2` + `service/s3` + `config` + `credentials`（config 模块的 SSO/STS/IMDS 为默认凭证链传递依赖，非 GCS/Azure/MinIO）。
测试：`s3store_test.go`（fake-`api` outcome 矩阵、`If-None-Match: *`/body 透传、有界 Get、`New` 校验）+ 真实 SDK `httptest`
端到端（条件 PUT、412→`PutAlreadyExists`）；`internal/config/storage_test.go`（缺失即 nil、默认值、全量解析、非法矩阵、
http-with-allow、`CLOUD_STORAGE_BUCKET` env）。门禁：`go build`/`go vet`/`go test ./internal/... ./cmd/...`/`golangci-lint`/
`git diff --check` 全部通过（仅 `cmd/devsetup` 在 Windows 的文件权限既有失败，与本次无关）。**仍未实现**：`RetrievalCapability`、
Node 交付、Skills 看板 UI、AgentSkillBinding / ExecutionSkillBinding、GC/retention、MinIO dev fixture、真实 provider CI。

**Step 5A RetrievalCapability 契约已冻结（仅设计，实现 NOT started，改动保持 uncommitted）**：读取侧契约由
`specs/decisions/controller/skill-delivery/0-skill-retrieval-capability.md`（`proposed`）及其 Node 侧一致性契约
`specs/decisions/node/agent-runtime/0-skill-materialization-and-readiness.md`（`proposed`）闭合，镜像测试用例迁到
`specs/test-cases/controller/skill-delivery/` 与 `specs/test-cases/node/agent-runtime/`。Step 5A 将这两份 ADR 从此前的
`cloud/skills/` 迁到规范化的 Controller/Node 叶子域并闭合遗留开放问题：授权依据 = 仅对已冻结进 Execution
`ExecutionSkillBinding` 的 revision 签发，claim/dispatch 时绝不重新解析 `Skill.current_revision_id`；范围 = 单一 immutable
object、仅 GET、无 bucket/prefix/workspace 级或任意 key；表示 = short-lived signed HTTPS GET URL（bearer credential、
vendor-neutral、不叫 S3 名）；TTL = 默认 300s、硬上限 900s、`expires_at` 显式下发（覆盖 dispatch + Node 调度 + 获取 + 时钟
偏差 + 1–2 次 refresh-retry）；refresh = 同一 Execution + Attempt + 冻结 revision → 新 credential（不改 revision/locator/
binding）；capability 永不 durable、永不落日志（redaction）；Controller 协调 + 转发、不代理 bytes、data plane 为 Node→Object
Storage 直连；`ObjectStore` port 不变、新增 `RetrievalCapabilityIssuer` 式边界、`object_locator` 保持逻辑 key 而非 URL；
fencing 不吊销已签发 bearer URL、V1 撤销 = credential 过期（无即时撤销承诺）；失败分类
storage_not_configured / revision_not_bound / attempt_not_eligible / object_not_available / signing_failed /
invalid_locator / expired_or_retry_required / authorization_failed / temporary_control_plane_failure；缺失对象 = 签发前不
Stat/HEAD、Node 404 → `object_not_available` fail closed；**schema 影响 NONE**（`0015`/`0016` 不变）。**capability
mint/refresh、Node downloader/cache、READY barrier 的实现 NOT started（后续切片）。**

**Step 4A Agent/AgentSkillBinding 契约已冻结（仅设计，实现 NOT started，改动保持 uncommitted）**：最小 durable
`Agent` + `AgentSkillBinding` 权威模型由 `specs/decisions/cloud/agent/0-agent-skill-binding.md`（`proposed`）闭合。
审计确认当前**无任何 durable Agent 资源**（无 `agents` 表；`agent`/`team` 只是 issue 协作域 ActorRef 类型串；
`CollaborationDirectory` 是 nil/"Unavailable" seam；frontend `features/agents/` 是 mock-only）。冻结要点：Agent 属
`collab_workspaces`（非 Runtime `workspaces`/`projects`）；`agent_skill_bindings` 引用 `skill_id`（Skill identity）而非
`skill_revision_id`/digest/locator，`PRIMARY KEY(agent_id, skill_id)` = 至多一条 binding；mutation = add/enable/disable/remove
+ 乐观 version（428/409）+ Idempotency-Key；Skill/Agent soft-delete 不影响历史 `ExecutionSkillBinding`，future admission
排除 soft-deleted Skill、soft-deleted Agent 拒绝新 execution；Execution request 只携带 `agent_id`、绝不携带 Skill IDs，
admission 只读 durable `AgentSkillBinding`；授权复用 `collab_workspace_members` 角色（owner/admin 可写、member 可读、非
member 404）；`agents.id` 映射 `ActorRef{agent}`、taxonomy 不变、`CollaborationDirectory` 保持未接线。**Execution snapshot
ADR `specs/decisions/cloud/skills/20260927-execution-skill-snapshot.md` 已修订：删除 explicit-skill-id admission fallback，
冻结「准入只读 durable AgentSkillBinding」。** schema 方向仅设计（`agents` + `agent_skill_bindings` forward migration），
**未写 migration、未写实现**。**Step 5B 仍 BLOCKED**（Execution snapshot 依赖 durable `AgentSkillBinding`，其实现尚未开始）。

**Step 4B Agent/AgentSkillBinding 已实现（改动保持 uncommitted）**：Step 4A 冻结的最小 durable 权威模型落地为
代码 —— forward migration `0017_agents_and_skill_bindings.sql`（`agents` + `agent_skill_bindings`，`0015`/`0016`
不改）、`internal/core/agents.go`、workspace-scoped 公共 API + 生成的契约。`agents`（`workspace_id → collab_workspaces`
归属、`name` 1..128、`status active|disabled`、`version`、软删 `deleted_at`、`UNIQUE(workspace_id,name) WHERE
deleted_at IS NULL`、`agent_immutable_ownership` trigger 禁止跨 Workspace 原地移动）；`agent_skill_bindings`
（`PRIMARY KEY(agent_id, skill_id)`、`enabled`、`version`、无 `deleted_at`，membership-style 移除 = DELETE）。9 个
端点按既有 `/tenants/:tid/spaces/:spaceId/...` 约定：`GET/POST /agents`、`GET/PATCH/DELETE /agents/:agentId`、
`GET/POST /agents/:agentId/skills`、`PUT/DELETE /agents/:agentId/skills/:skillId`（attach POST `{skillId}`、PUT
`{enabled,version}`、PATCH `{name,status,version}`、DELETE `{version}`；POST/DELETE 走通用 Idempotency-Key，PUT/PATCH
走 version 428/409）。授权复用 `workspaceRole`：非成员 `404 not_found`（无泄漏）、member 非 owner/admin 写 `403
workspace_admin_required`、删除 creator 或 owner/admin；attach 强制同 Workspace + 未软删（跨 Workspace/软删 Skill →
`404`），duplicate → `409 binding_exists`。核心还落地 **`enabledAgentSkillBindings` 读缝**（enabled + live Skill、
按 `canonical_name` then `skill_id` 确定性排序、**不解析 revision/digest/locator**）——这是 Phase 5 Execution admission
的唯一天然选择权威（ADR D8/D10）；**无任何** Execution/Attempt/ExecutionSkillBinding/capability/signed-URL/object
locator 字段（ADR D13/D14）。contract/OpenAPI 同步（`Agent`/`AgentSkillBinding`/`AgentSkillBindingListItem` schema +
`enabled`/`skillId` 输入 + 描述，`api/openapi.json` 经 `go run ./cmd/openapi` 重生成并通过
`TestPublishedOpenAPIIsValidAndCurrent`；frontend 经 orval 重生成）。测试：
`integration/agent_skill_binding_test.go` 8 用例（fresh/upgrade-path/归属与名称唯一/binding 不变量/§17 §18 权威读缝
确定性读取 + CRUD/auth/binding lifecycle/cross-workspace/soft-deleted）全通过（真实 PostgreSQL）。门禁 `go build`/
`go vet`/`go test ./internal/... ./cmd/...`（仅 `cmd/devsetup` 的 Windows 文件权限既有失败，与本次无关）/`golangci-lint`/
`git diff --check` 全通过。**Step 5B（Execution snapshot）对 `AgentSkillBinding` 的依赖已解除**，Phase 5 已落地
Execution/Attempt/ExecutionSkillBinding。**仍未实现**：RetrievalCapability / Node 交付 / Agent 看板 UI。

**Step 5 Execution/Attempt/ExecutionSkillBinding 已实现（Phase 5，改动保持 uncommitted）**：在 Step 4B 落地的
durable `AgentSkillBinding` 之上，落地逻辑 Execution / 物理 Attempt / 不可变 ExecutionSkillBinding 快照 ——
forward migration `0018_execution_snapshot.sql`（`executions` + `attempts` + `execution_skill_bindings`，
`0015`/`0016`/`0017` 不改）、`internal/core/executions.go`、`internal/controlpb` proto 扩展（`SkillBundleRef` +
`SkillRunSpec` + `ExecutionInput.oneof spec` 新增 `skill_run = 2`，经 regenerate + currentness）。`AdmitExecution`
在**同一 database-only 事务**内原子写入 `executions` + 首个 `attempts`（ordinal=1、`eligible`、node_id/lease/epoch
均空）+ 全部 `execution_skill_bindings`，事务内**无**任何外部效果；准入只读 durable `agent_skill_bindings`
（enabled + live Skill，按 `canonical_name` then `skill_id` 确定性排序），逐 Skill 解析当时精确
`current_revision_id` → `skill_revision_id` + 冗余 `content_digest`/`size_bytes`/`package_format(_version)`，无
revision 时 fail closed（`skill_revision_not_available`），非 member/跨 workspace 一律 404、disabled Agent 拒绝；
`execution_skill_binding_immutable` trigger 拒绝 UPDATE，无 version/updated_at 结构、无 bearer-credential 字段；
`CreateRetryAttempt` 复用同一 Execution 的 frozen binding（ordinal 递增、不重读 AgentSkillBinding/current
revision），`RunAgainExecution` 新建 Execution 重新解析；dispatch descriptor 由 frozen `SkillBundleRef[]` 携带。
测试：`integration/execution_snapshot_test.go` 11 用例（fresh/upgrade-path/精确冻结/immutable/retry/run-again/
权威读缝/fail-closed/首 Attempt 原子/确定性排序/授权）全通过（真实 PostgreSQL）。门禁 `go build`/`go vet`/
`golangci-lint`/`git diff --check` 全通过（`-race` 仅 ubuntu amd64 CI 跑，本地 windows/386 不支持；`cmd/devsetup`
Windows 文件权限既有失败与本次无关）。**仍未实现**：RetrievalCapability mint/refresh、Controller dispatch/claim/
lease 管线、Node 下载/缓存投影/READY barrier、Agent 看板 UI。

**Step 5B RetrievalCapability 已实现（Cloud 侧 mint/refresh，改动保持 uncommitted）**：`0-skill-retrieval-capability.md`
（状态推进为 `implemented`）的 Cloud 侧读取契约落地为代码，**无持久化、无 schema 变更、签发前不 Stat、不记录
credential**。要点：`internal/skillstore/retrieval.go`（`RetrievalCapabilityIssuer` port `Issue(ctx, Locator, ttl) →
{URL, Method, ExpiresAt}` + 脱敏 `RetrievalCapability.String()`，与 durable `ObjectStore` 严格分离、`ObjectStore` port
不变不加 Presign）；`internal/skillstore/s3store/issuer.go`（`NewIssuer` + `Issue`：`PresignGetObject` 对 exact bucket +
逻辑 `locator` 签发，仅 GET、TTL 钳制、无 HeadObject/存在性探测，`expires_at = now.Add(ttl)` 保守下界）；
`internal/core/retrieval.go`（`MintSkillRetrievalCapabilities`：transact 解析 `(execution_id, attempt_id,
skill_revision_id[])` 权威——attempt 必须非 terminal（succeeded/failed/canceled/superseded 一律 409
`attempt_not_eligible`）、每个 revision 必须是冻结的 `ExecutionSkillBinding` revision 且其 `object_locator` 可
`ParseLocator`，否则 fail closed（404 `authorization_failed`/`revision_not_bound`、500 `invalid_locator`）；issuer 缺失 →
503 `storage_not_configured`、签发失败 → 503 `signing_failed`）；`internal/controlgrpc/retrieval.go`（`MintSkillRetrieval`
请求不含 locator/bucket/key/URL，Controller 只 request/refresh 不选 revision）+ `fault.go` 六类 D32 映射（provider
细节绝不外泄）；`internal/config/storage.go` 新增 `retrieval_capability_ttl`（默认 300s、上限 900s、`<=0`/超限拒绝）；
`cmd/server.wireRetrievalIssuer` 装配 `store.RetrievalCapabilityIssuer`/`RetrievalCapabilityTTL`。测试：
`internal/skillstore/retrieval_test.go`（脱敏 `String()`）、`internal/skillstore/s3store/issuer_test.go`（exact-object
GET / 签发错误 / 非正 TTL / 真实 SDK signed URL 端到端）、`integration/retrieval_capability_test.go` 9 用例（happy、
未绑定 revision、外源三元组、terminal fencing、storage_not_configured、signing_failed、invalid_locator、无持久化、
refresh 保持 revision）全通过（真实 PostgreSQL）。门禁 `go build`/`go vet`/`gofmt`/`git diff --check` 全通过（`cmd/devsetup`
Windows 文件权限既有失败与本次无关）。**仍未实现**：Node 下载/缓存投影/READY barrier、Controller dispatch/claim/lease
relay、Agent 看板 UI。

| 能力 | 状态 | 说明 |
| --- | --- | --- |
| `skills`（可变业务资源） | ✅ 持久化 | `workspace_id → collab_workspaces`，`skill_immutable` trigger 禁止跨 Workspace 原地移动；active `(workspace_id, canonical_name)` 部分唯一索引，软删除释放名称 |
| `skill_revisions`（不可变内容快照） | ✅ 持久化 | 刻意无 `version`/`updated_at`/`deleted_at`；`UNIQUE(skill_id, digest_algorithm, content_digest)`；`current_revision_id` 复合外键只指向本 Skill 的 revision |
| `skill_ingestions`（journal-first 上传/导入 saga） | ✅ 持久化 | 状态 `planned→storing→verified→committed/failed`；与 SkillRevision 严格分离 |
| 持久化约束测试 | ✅ | `integration/skill_persistence_test.go` 5 用例全通过（真实 PostgreSQL：fresh + upgrade path + 归属/名称唯一 + revision 不变量 + ingestion 状态） |
| **Canonical Skill Package v1（编码 + digest）** | ✅ IMPLEMENTED（Step 2A） | `internal/skillpkg`：canonical path 校验（reject-not-clean）、ManifestV1、per-file `SHA256`、tree digest（domain-separated）、`ora-skill-package` v1 encode/decode-as-verify、`Limits`/`DefaultLimits()`；6 组 golden vectors + path rejection + corruption + roundtrip + bounds 单测全通过（`go test ./internal/skillpkg/`，`go vet`、`gofmt` 干净） |
| **SKILL.md 发现 / directory·archive source adapter** | ✅ **IMPLEMENTED（Step 2C.1）** | ADR `20260927-source-intake-candidate-discovery.md`（`proposed`，已批准实施）落地为 `internal/skillsource`：directory / ZIP / 未压缩 TAR 三 transport 摄取为 `sourceEntry`、source 级安全校验（复用 `skillpkg.ValidatePath`、duplicate/case-collision、symlink/特殊条目、path traversal、limits）、精确 `SKILL.md` candidate 发现（root candidate owns 整树 / nested root 整 source 拒绝）、duplicate `canonical_name` 整 source 拒绝、invalid metadata → `PreparationFailure`（sibling 继续）、`PreparedSourceResult` 喂给 `core.Store.IngestSource`；无 schema 变更 |
| **Ingestion saga（candidate / 幂等 / 事务边界 / 崩溃恢复）** | ✅ **IMPLEMENTED（Step 2C）** | ADR `20260927-canonical-ingestion-saga.md`（`proposed`）冻结的语义已落地为 `internal/core` 的 `Store.IngestSkill` / `Store.IngestSkills`：candidate 模型、batch partial-success、语义 request 指纹 = `(target_skill_id, display_name, summary, content_digest, package_digest)`、per-candidate 幂等身份 `(workspace_id, idempotency_key, canonical_name)`、TX #1/TX #2 内容、CAS 冲突 durable（`activation_outcome`）、崩溃恢复 = client-driven continuation（无服务端 worker、无 autonomous payload recovery，bytes 由客户端重提交确定性重建）；`0016_*` forward migration 已落地（四变更，`0015` 不改） |
| **SKILL.md metadata contract（frontmatter / name / canonical_name）** | ✅ **IMPLEMENTED（Step 2C）** | ADR `20260927-skill-md-metadata-contract.md`（`proposed`）落地为 `internal/skillmeta`：`canonical_name = ASCII lowercase(TrimSpace(name))` 唯一变换、`name` 必填且必须是 YAML string（ASCII `[A-Za-z0-9._-]+`、不以 `.` 开头、≤ 200 bytes）、`description` 可选（string、≤ 4096 bytes）、重复 key 与非法 YAML fail closed、未知字段不透明；解析器归业务 metadata 层 `internal/skillmeta`，`internal/skillpkg` 保持冻结不解析 name |
| **上传 HTTP API / directory·archive source adapter** | ✅ **IMPLEMENTED（Step 2C.1 + 3B）** | source adapter（`internal/skillsource`：zip/tar 解码 + `SKILL.md` discovery）已由 Step 2C.1 落地；公共上传 API 已由 Step 3B 落地（ADR `20260927-public-upload-api-contract.md`：`POST /api/v1/tenants/:tid/spaces/:spaceId/skills/imports`、multipart 单 archive、`source_kind∈{zip,tar}`、route-local 256 MiB / 其余路由 64 KiB、双凭证 + workspace owner/admin 授权、saga 幂等语义、200 部分成功 envelope、schema NONE）；saga 接受 caller 提供的 `Files` 或经 `IngestSource` 喂入的 `PreparedCandidate` |
| **Object Storage 抽象（identity / key / write / reconcile）** | ✅ **IMPLEMENTED（Step 2B）** | ADR `20260927-object-storage-abstraction.md` 冻结的语义已落地为代码（`internal/skillstore`）：三层 identity 分离、`package_digest`、object key `skills/<format>/v<n>/<algo>/<package_digest>`（`Locator`）、最小 `ObjectStore` port（`PutImmutable`/`Stat`/`Get`）、create-only、五值结果分类（`error != nil` 不算分类）、按 `object_locator` probe 的 `Reconcile`、`MISMATCH` fail closed、crash adopt；`internal/skillstore/fakestore` 内存测试替身覆盖全部失败模式。**仍无**生产 provider、无 SDK 依赖、无配置键、无上传 HTTP API、无 schema 变更（`0015_skills.sql` 未修改） |
| **Object Storage 写入（saga 驱动 port）** | ✅ **IMPLEMENTED（Step 2C）** | 不可变包字节经 `Store.SkillsObjectStore` 的 `PutImmutable`/`Reconcile` 写入对象存储（D8），完全在 DB 事务外；集成测试经内存 fakestore 写入并验证真实 package bytes；DB 事务内不做对象存储副作用。**生产 provider 已由 Step 4B 落地**（`internal/skillstore/s3store`，`storage` 段存在且合法时由 `cmd/server` 注入，缺失则 nil → `object_store_unavailable`） |
| **Object Storage signed URL / RetrievalCapability** | ✅ **IMPLEMENTED（Cloud 侧 mint/refresh，Step 5B）** | 读取侧契约由 `specs/decisions/controller/skill-delivery/0-skill-retrieval-capability.md`（`implemented`）闭合；Cloud 侧 capability mint/refresh 已落地为 `internal/core/retrieval.go` + `internal/skillstore/s3store/issuer.go` + `MintSkillRetrieval` control-gRPC（`storage.retrieval_capability_ttl` 默认 300s / 上限 900s）；Node data plane 下载/cache 与 Controller relay 仍待 Phase 6/7；signed RetrievalCapability 属 bearer credential，永不持久化/记录 |
| **Skills 前端看板** | ❌ NOT IMPLEMENTED | 无 UI |
| **AgentSkillBinding / ExecutionSkillBinding** | ✅ **AgentSkillBinding（Step 4B）+ ExecutionSkillBinding（Phase 5）均已实现** | `agents`/`agent_skill_bindings` 权威模型由 `specs/decisions/cloud/agent/0-agent-skill-binding.md` 闭合并落地为 migration `0017` + `internal/core/agents.go`（CRUD + binding lifecycle + `enabledAgentSkillBindings` 读缝）；`executions`/`attempts`/`execution_skill_bindings` 由 `specs/decisions/cloud/skills/20260927-execution-skill-snapshot.md` 闭合并落地为 migration `0018` + `internal/core/executions.go`（admission 原子冻结 + 不可变 trigger + retry/run-again） |
| **执行快照 / RetrievalCapability / Controller 交付 / Node 缓存投影 / AgentRuntimeAdapter / READY-before-spawn** | 🚧 **执行快照（Phase 5）+ RetrievalCapability Cloud 侧 mint/refresh（Step 5B）已实现**；Controller 交付 / Node 缓存投影 / READY-before-spawn 仍 NOT IMPLEMENTED | 执行快照 ADR `20260927-execution-skill-snapshot.md` 已落地为 migration `0018` + `internal/core/executions.go`（admission 原子冻结 exact revision + 不可变 ExecutionSkillBinding + retry/run-again）；RetrievalCapability Cloud 侧 mint/refresh 已落地（`0-skill-retrieval-capability.md` `implemented` + `internal/core/retrieval.go` + `s3store` issuer + `MintSkillRetrieval` gRPC）；Node data plane 下载/cache、Controller dispatch/claim/lease relay 与 READY barrier 实现 NOT started；signed RetrievalCapability 属 bearer credential，永不持久化/记录 |

## Issue 看板（迁移自 Multica）

### 第一波 — 核心看板 ✅

| 功能 | 状态 |
| --- | --- |
| 7 个标准看板列 | ✅ |
| 5 级优先级 | ✅ |
| 标题 / 描述 / 负责人 / 创建人 / 父子任务 | ✅ |
| 拖拽排序（分数 position） | ✅ |
| 增删改查 + 跨列移动 | ✅ |

### 第二波 — 看板周边 ✅

| 功能 | 状态 | 备注 |
| --- | --- | --- |
| 自定义状态列（状态目录） | ✅ | 惰性播种 7 列，可加列、归档 |
| 卡片编号 `#42` | ✅ | 每租户递增 |
| 自定义字段 `properties` | ✅ | 无模式 jsonb |
| 评论 | ✅ | 增删改查 |
| 标签 | ✅ | 含挂/摘、软删复用名 |
| 订阅/关注 | ✅ | 存关系，暂无通知 |
| 搜索 `?q=` | ✅ | 标题/描述子串匹配 |
| 批量操作 | ✅ | 一次改多卡状态/优先级/负责人 |
| 保存视图 | ✅ | 存 filter，暂未在服务端执行 |
| 分组视图 | ✅ | 按状态/优先级/负责人分桶 |

### 第三波 — Issue 协作地基

架构方案见 [12-collaboration-architecture.md](../../migrations/multica-issue-board/12-collaboration-architecture.md)
（rev. 2：Issues 只拥有 Issue 域，外部能力一律走稳定 port；**§37 是交互模型的权威定义**，§6.4 是
端口的权威清单）。已落地 **3A — issue-owned 地基**（migration `0008` + 持久化/API-contract 脊柱）与
**3B-0 — 协作架构对齐**（仅文档）、**3B-1 — 协作交互地基**（migration `0009`）与 **3B-2 — Workflow
Interaction Shell**（migration `0010`）；其余为 3C 待编码。
（注：workspace integration Stage A 将 Issues 迁移 0005→0006 … 0009→0010 重编号，见
[workspace-integration-stage-a.md](../../migrations/workspace-integration-stage-a.md)。）

#### 3A — issue-owned 地基 ✅ IMPLEMENTED / REVIEWED

| 功能 | 状态 | 说明 |
| --- | --- | --- |
| 多态 assignee（Assign ≠ Execute） | ✅ | `assignee_type ∈ user/agent/team` + `assignee_id`，`user` 镜像写 `assignee_user_id`；agent/team 为 opaque ref |
| 评论线程 + author ActorRef | ✅ | `parent_id`（同 issue 校验）+ 统一 `author_type/author_id`（user/agent/team/system）；无 per-actor 列 |
| IssueRun（Issue 拥有） | ✅ | `issue_runs`：`executor_type/executor_id` 多态（agent/team/workflow）+ opaque 外引用；≠ operation/execution_ticket |
| Run 状态机 | ✅ | 7 态；SQL WHERE 守卫（非中心校验器）；terminal ≠ deleted，无 delete-run API |
| Timeline（Projection） | ✅ / ⚠️ | **持久化已实现**：`issue_activities` + 评论共享 per-issue `seq`（Option-C `GREATEST(MAX,MAX)+1`），投影非事件源；**公开 timeline 读接口未实现**（无 `/timeline` 路由） |
| 评论作者 actor | ⚠️ schema-ready | `author_type` CHECK 允许 `user/agent/team/system`，但 API 只写 `user`（尚无 agent/system 评论写入路径） |
| Sub-Issue context | ✅ | `issue_context_refs`（引用而非复制，硬删） |
| 迁移 / API-contract 脊柱 | ✅ | `0008` + `PublicRequest`/`router`/`validField`/OpenAPI（`IssueRun`/`ContextRef` schema） |

#### 3B-0 — Collaboration Architecture Alignment ✅ DONE（仅文档，无代码）

把交互模型正式冻结进 [12-collab §37](../../migrations/multica-issue-board/12-collaboration-architecture.md#37-wave-3b-0--collaboration-interaction-model-frozen)，
端口清单统一到 §6.4。**未改 production code、未建 migration、未改前端。**

冻结的核心语义（实现时直接照抄，不要重新推导）：

- `@` = **Collaboration Target Selection**，本身不等于执行；markdown `@xxx` 只是展示格式。
- `user` → **Mention Mode**（**不产生 IssueRun**）· `agent`/`team` → **Task Mode**（需显式 task）·
  `workflow` → **Configure / Form Mode**（动态表单，绝不自动执行）。
- `Comment ≠ Interaction ≠ IssueRun`：1 条评论 → 0..N interaction → 0..N run；**禁止 `Comment.run_id`**。
- AI Assist：Suggest → Review → Apply → Confirm → Execute。
- Context：`IssueContextRef`（显式引用）≠ `ContextBuilder`（调用期构造）≠ `IssueRun.input`（执行期快照）。
- Timeline = Comment + IssueActivity 的高层投影，与 Execution Logs 严格分离。

#### 3B-1 — Collaboration Interaction Foundation ✅ IMPLEMENTED（migration `0009`）

第一条真实端到端 `@` 协作链路（**无真实 Agent/Team/Workflow/Runtime**）：`Collaboration Directory →
@ Picker → Human Mention / Agent Task / Team Task → Context → Mock Execution → IssueRun / Activity /
Reply Comment → Timeline`。语义照抄 [12-collab §37](../../migrations/multica-issue-board/12-collaboration-architecture.md#37-wave-3b-0--collaboration-interaction-model-frozen)。

| 功能 | 状态 | 说明 |
| --- | --- | --- |
| 端口接口（consuming-side seams） | ✅ | `CollaborationDirectory` / `ContextBuilder` / `ExecutionDispatcher` / `ExecutionObserver` 等 port 已落到 Go 接口；权威清单见 [12-collab §6.4](../../migrations/multica-issue-board/12-collaboration-architecture.md#64-canonical-port-inventory-unified-by-wave-3b-0) |
| CollaborationTargetRef / Directory / InteractionDescriptor | ✅ | `GET /collaboration/targets?q=` 只读投影（members + 目录 fixture，**不是** Agent/Team/Workflow domain API） |
| Human Mention Mode | ✅ | 持久化 typed target，**不产生 Run**、无假回复、不重解析 markdown；通知留给未来模块 |
| Agent / Team Task Mode | ✅ | 共享 `target + task + context`；task 必填（缺 → 400 `task_required`）；`body ≠ task`；**不假设** leader/member/delegation/fan-out |
| ContextBuilder（确定性实现） | ✅ | issue 标题/描述/近期评论（≤10）/显式 context refs/task；**无 AI** |
| ExecutionDispatcher + ExecutionObserver + mock 执行适配器 | ✅ | 内存 fixture（`FixtureCollaborationDirectory`/`DeterministicContextBuilder`/`MockExecutionDispatcher`）实现同一 port；仅 dev/demo 配置，**生产默认关闭**；**不建 `sim_*` 表、不建 mock domain 表** |
| IssueRun lifecycle + 固定回复 → IssueComment | ✅ | `queued → dispatched → running → completed(/failed)` 走真实 Issue API→IssueRun→Dispatcher→adapter→observer→Activity/Comment；agent/team 回复落 `author_type='agent'/'team'` 评论（内部路径，无公开冒充） |
| Timeline 读 API | ✅ | `GET /issues/:iid/timeline`（Comment + Activity 按共享 `seq` 合并） |
| Interaction spine 读 API | ✅ | `GET /issues/:iid/interactions`（`issue_interactions` 表，migration `0009`） |
| 前端 `@` Picker + Mention/Task Mode | ✅ | Composer → Target Picker → Interaction Mode；workflow 目标显示「本阶段不可用」 |

> **封板前复核（2026-09-20）**：后端 `go build` / 单元测试 / 契约测试（OpenAPI 逐字节）/ 真实 PostgreSQL
> 集成套件全绿；前端在仓库要求的 Node 24 下跑完 `typecheck` / `test`(76) / `build` / `check:modules` /
> `check:docs` / `check:dead` / `check:dup`，全部通过。`lint` 与 `format:check` 仍为红，但**全部来自
> 既有欠债**（16 个 prettier、9 个 oxlint，均在本次未触碰的文件里，已用 HEAD 版本比对确认），Wave 3B-1
> 自身新增违规为 0。

#### 3B-2 — Workflow Interaction Shell ✅ IMPLEMENTED + VERIFIED（migration `0010`）

Issues-facing 契约与实现记录见 [12-collab §38 / §38.37](../../migrations/multica-issue-board/12-collaboration-architecture.md#38-wave-3b-2--workflow-interaction-design-frozen)：
`@Workflow → FormDescriptor → 动态表单 → 可选 AI Assist → Review → Confirm → IssueRun → mock 执行 → Timeline`
已端到端打通。**Workflow 内部架构仍 UNKNOWN**（真实 provider BLOCKED ON EXTERNAL DESIGN）。

| 决策 | 结论 |
| --- | --- |
| `@Workflow` UX | select → `mode=form` → 载入 FormDescriptor → 动态表单 → 可选 AI Assist → **显式 Confirm** → IssueRun。`选择 ≠ 执行`、`AI Assist ≠ 执行` |
| FormDescriptor | Issues-facing **渲染描述符**（`formRef/title/description/fields[]`），**不是** Workflow canonical schema，不绑定 JSON Schema/DSL/protobuf 等任何技术；无 conditions/表达式/嵌套组（那些是 FUTURE/OPEN） |
| 字段类型（首版） | `text` / `textarea` / `number` / `boolean` / `select` / `multi_select`；`context_ref` 选择器延后 |
| Descriptor 加载 | `InteractionDescriptor.formRef` + 单独只读 `GET /collaboration/forms/{formRef}`（不内嵌进 picker 投影） |
| 版本 | 首版**不做** descriptor 版本；`formRef` 对 Issues 不透明，Confirm 时按**当前** descriptor 重新校验 |
| Interaction 状态 | **不新增 status 列**：`run_id IS NULL` = 未确认，`run_id != NULL` = 已确认（+ `issue_runs.status` 管执行） |
| 草稿持久化 | ✅ **仅前端**：选中 workflow 后表单是纯 draft，服务端零写入；只有「确认执行」才创建 comment + interaction + run |
| 每个目标独立提交 | ✅ 用户 / Agent / Team 各自有留言框 + 「提交」按钮，点了才落库；未提交的草稿不进入操作历史 |
| Confirm 语义 | 唯一执行边界；校验 → 构建 effective input → 建 Run。编辑/Assist/存草稿都不建 Run |
| 幂等 / 并发 | 需 `Idempotency-Key`；同 key 同请求重放、同 key 异请求 409；并发 Confirm 用 `UPDATE … WHERE run_id IS NULL` 的 CAS（0 行 → 409 `interaction_already_confirmed`），复用现有事务约定，**不加分布式锁** |
| AI Assist | `InputAssistProvider` 只返回 **field-level patch** + suggested refs；只建议，不建 Run/不改状态/不写通知/不建永久 `IssueContextRef` |
| 校验分层 | 前端仅 UX；**Issues API 权威重校验**（Confirm 时按当前 descriptor）；Workflow 域校验归未来 Workflow service |
| 表单值表示 | `{fieldKey: scalar | string[]}` 的普通对象；**禁止**把 workflow 字段做成 Issue 列 |
| Workflow 输出 | 落 **`IssueActivity`**（`actor_type='system'` + `details` 带 run/executor/message），**不给 ActorRef 加 `workflow`**；node/raw log 不进 Timeline |
| Dispatcher / Observer | **复用**，不建 `WorkflowDispatcher`；新增 `ObserveProgress` → `run.progress` activity |
| 前端结构 | `WorkflowInteractionComposer → DynamicFormRenderer → FormFieldRenderer / AssistSuggestions / ConfirmReview`；**禁止** `if workflow.id == ...` 硬编码表单 |
| 迁移 | 需要 **一个** additive `0010`：`issue_interactions.input jsonb NOT NULL DEFAULT '{}'`（generic，非 workflow 专用）；不改 0009 |
| API | ✅ 已实现：`GET /collaboration/forms/{formRef}`、`POST /issues/{iid}/collaboration/assist`（**无状态**，未确认前即可用）、`POST /issues/{iid}/interactions/{ixid}/confirm`；`409 workflow_not_available` 已 **SUPERSEDED** |
| fixture | ✅ `FixtureFormDescriptorProvider`、`MockInputAssistProvider`、`MockExecutionDispatcher`（workflow 分支）；仍 dev/demo only、生产默认关闭、无 mock domain 表 |
| Workflow 输出 | ✅ 落 `IssueActivity`（`actor_type='system'` + `details{runId,executorType,executorId,message}`）；`ObserveProgress` → `run.progress`；**ActorRef 未加 `workflow`** |
| 前端 | ✅ `WorkflowInteractionComposer` → `DynamicFormRenderer`/`FormFieldRenderer`/`AssistSuggestions`/`ConfirmReview`；picker 不再禁用 workflow 目标；已确认的 interaction 只显示状态、不再给第二次确认 |

> 本轮新增的**唯一** deviation：多了一个 `409 interaction_not_confirmable`（对非 form interaction 调用
> confirm/assist），以及 OpenAPI 为所有路由补上 503 声明（端口 Unavailable 时 `form_descriptor_unavailable`
> / `assist_unavailable`）。其余与冻结设计一致。

#### 3C — Issue Detail & Collaboration UI 🧭（待编码）

| 功能 | 状态 | 说明 |
| --- | --- | --- |
| Issue Detail（协作产品面） | 🧭 | 左栏 Activity/Timeline + 右栏 Properties/Development/Execution（投影 + UI） |
| Timeline 分页/truncation、执行日志、PR、通知、实时 | 🧭⏸️ | 依赖 3B 端口 + 外部模块（logs/PR/通知/实时均非 issue-domain） |

#### 3B-3 / later — 真实 Agent / Team / Workflow 集成 — 🚫 BLOCKED ON EXTERNAL DESIGN

Issues **不定义** Agent / Team / Workflow 的**内部实现**（三者 internal design = **UNKNOWN**）。
真实 Agent/Team/Workflow 模块、Runtime/LLM、通知/实时/PR/日志的后端一律 **BLOCKED ON EXTERNAL
DESIGN** —— 只在未来这些模块落地时通过 3B-1 的同一 port 接入；Issues 只约束 consuming-side contract。

#### 尚未冻结（3B-0 明确留下的开放问题）

mention 候选来源 · Team 是否自持 executor · pending run 并发上限 · workflow **canonical** schema 归属 ·
AI Assist **由谁实现** · `ContextBundle` 生命周期 · `ConversationTarget` 作用域
—— 详见 [12-collab §37.17](../../migrations/multica-issue-board/12-collaboration-architecture.md#3717-open-questions-left-by-wave-3b-0)
与 [§38.34](../../migrations/multica-issue-board/12-collaboration-architecture.md#3834-open-questions-left-by-wave-3b-2)。

> `Interaction.task` 的落点**已解决**（3B-1 落在 `issue_interactions`；3B-2 为其加 generic `input` 列，§38.30）；
> workflow 的 **Issues-facing** 契约、AI Assist 的**权限边界**、Confirm 边界、草稿策略也已随 §38 冻结。

### 刻意暂缓（本期不做）

| 功能 | 状态 | 原因 |
| --- | --- | --- |
| 附件 / 文件上传 | ⏸️ | Cloud 无文件存储 |
| 卡片绑定 project | ⏸️ | Cloud 的 project 语义不同（开发环境，非轻量分组） |
| 关联 Pull Request | ⏸️ | 需接外部 Git 服务 |
| 实时推送（WebSocket） | ⏸️ | 无事件总线 |
| 机器人 / 小组负责人 / Workflow / Autopilot | 🧭⏸️ | agent/team 走 3B-1 的 port + fixture 适配器，workflow 走 3B-2 的 `FormDescriptorProvider`/`InputAssistProvider` + fixture（**真实模块与真实 AI 仍 BLOCKED ON EXTERNAL DESIGN**）；Autopilot 本体暂缓 |
| 富表格 / 图形视图 | ⏸️ | 分组视图已是一等端点，任意视图引擎未做 |

## Web 前端（正式）

正式前端在 [`frontend/`](../../frontend/README.md)（React 19 + TS + Vite + Tailwind 4 + TanStack
Query），从参考项目 `cloud前端/` 迁移而来，API 层由 orval 从 `api/openapi.json` 生成。合并 main 后
采用 **gateway-会话架构**：`SessionProvider`/`useSession`/`RequireSession` 管会话（`GET /api/v1/me`
探测 + 401 策略），路由为 `/onboarding` + `/w/:workspaceSlug/*`。**开发拓扑 = 双入口**：ora-web :8080
（DEV-only 本地演示边界：email login/register + 进程内 API 代理注入双 JWT + 托管构建后 SPA）与
gateway :8081（生产规范入口：provider 协议 `/auth/providers`、GitHub/dev login、PostgreSQL 会话，
vite 代理 `/auth,/api,/healthz` → :8081）。与 `cmd/demo-issue-board-web`（单文件看板演示，仅手工
验证接口）并存、互不替代。

| 能力 | 状态 | 说明 |
| --- | --- | --- |
| 看板（列 / 卡片 / 拖拽移动） | ✅ | 走真实 `/issues` + `/move`，分数 position |
| Issue 详情 | ✅ | 左 Activity/评论 + 右属性/执行占位 |
| 状态 / 优先级 / 负责人 / 描述 | ✅ | 真实接口；agent/team 负责人暂不可用 |
| 评论 / 标签 / 订阅 / 批量 / 视图 | ✅ | 对应第二波后端接口 |
| 评论线程回复 | ⚠️ | `CommentItem` 能渲染 `parentId` 缩进，但**输入框从不发 `parentId`**，UI 无法创建线程回复 |
| 搜索 | ⚠️ | `useIssues(tid, q?)` 支持 `q`，但**无调用方传参**、无搜索框 |
| `@` / target picker / Mention / Task / **Form Mode** | ✅ | Composer 内置 Target Picker（`GET /collaboration/targets`）：Mention 无 task、Agent/Team 需 task、Workflow 进入 `WorkflowInteractionComposer`（动态表单 + AI Assist + Review + Confirm）；评论携带 `targets[]` |
| IssueRun（运行） | ⚠️ 仅列表 | 详情页只渲染 runs 列表；「运行」按钮是 **disabled 占位**，`useCreateRun` 已定义但**前端无调用方**——即**没有**创建运行的入口 |
| ContextRef（上下文引用） | ✅ | 仅列表/增/删，不智能解析 |
| projectRef / agent / team / 执行 / 实时 / PR / 日志 | 🧭⏸️ | 后端无对应能力，前端给「暂不可用 / Coming later」占位 |
| 前端门禁（lint/typecheck/test/build） | ✅ | 需 Node >= 24；CI 与 `task frontend:check` 同门禁 |

## 已知限制

- 看板列表不分页（一次返回整个租户的所有卡片）。
- 搜索只是标题/描述的子串匹配，没有全文索引，也不搜标签/编号。
- 保存视图的 `filter` 只存不执行（由客户端解释）。
- 订阅只是存了关系，没有通知管线。
- 全局 advisory 锁是明确的吞吐上限。

## 里程碑时间线

| 时间 | 里程碑 |
| --- | --- |
| 阶段一 | Cloud 核心 + 模拟器（tenants/projects/workspaces/operations） |
| — | Issue 看板 第一波（核心看板，migration 0005） |
| 最近 | Issue 看板 第二波（周边功能，migration 0006）+ 文档体系 |
| — | Issue 协作地基 3A（issue-owned 地基，migration 0007） |
| 最近 | 正式前端迁移（`frontend/`，从 `cloud前端/` 迁入） |
| 最近 | **Wave 3B-0 协作架构对齐**（仅文档：交互模型冻结 + 端口清单统一） |
| 最近 | **Wave 3B-1 协作交互地基**（migration `0008`：`@` 协作端到端链路 + mock 执行） |
| 最近 | **Wave 3B-2 设计冻结**（仅文档：§38 Issues-facing 契约 + 规划 `0009`） |
| 最近 | **Wave 3B-2 Workflow Interaction Shell 实现**（migration `0009`：FormDescriptor + 动态表单 + AI Assist + Confirm → IssueRun + Timeline） |
| 最近 | **User Registration**（`POST /auth/register` 创建 User Identity + 会话） |
| 最近 | **Workspace Add Member**（email 添加已注册用户到协作空间；Project 访问 owner-only —— 当前实现） |
| 最近 | **Workspace Sharing Model（Step 2B 对齐）**（Workspace=资源共享边界；统一删除规则 creator OR owner/admin；最小谓词 foundation；旧 D1 产品规则 superseded） |
| 最近 | **Project Workspace Sharing（Step 3）**（项目访问切换为 workspace-shared：list/detail/runtime 继承；删除 = creator 或 owner/admin；unscoped 保持 owner-only；前端零改动） |
| 最近 | **Workspace Member Management & Onboarding（Step 3A）**（0-Workspace onboarding + 可选创建；成员角色 owner-only + owner immutable；移除 owner-only 硬删、membership-only；Project delete UI 对齐） |
| 最近 | **922GithubAuth ← main 会话架构合入**（`SessionProvider`/`useSession`/`/w/:slug` 路由 + `/onboarding`；删除 zustand 商店；保留 Step 3A 前端能力；双入口开发拓扑 ora-web + gateway；混合项目模型 optional space） |
| 最近 | **Cloud Skills 持久化地基（Phase 2 / Step 1A）**（migration `0015_skills.sql`：`skills` / `skill_revisions` / `skill_ingestions` 三表 + 归属/唯一/不可变约束 + 持久化测试；**无** API / Object Storage / 摄取 / 前端看板 / Binding / 执行链路） |
| 最近 | **Cloud Skills Canonical Package v1（Phase 3 / Step 2A）**（`internal/skillpkg` 纯内容层：canonical path 校验、ManifestV1、per-file SHA-256、tree digest、`ora-skill-package` v1 encode/decode/verify、bounds + golden vectors；ADR `20260924-canonical-skill-package-v1.md`；**无** Object Storage / 摄取 saga / 执行 / 前端） |
| 最近 | **Cloud Skills Object Storage 设计冻结（Phase 3 / Step 2B.0，仅设计）**（ADR `20260927-object-storage-abstraction.md` + `plan/plan_Skills.md` Phase 3 的 Step 2B 契约：immutable object identity、object key 推导、create-only `PutImmutable`、provider-neutral 验证义务、五值外部结果分类与按 `object_locator` probe 的 reconciliation、`MISMATCH` fail closed、crash adopt、transaction boundary、`skill_ingestions` 状态映射；**无**代码 / provider / SDK 依赖 / 配置键 / migration，`0015_skills.sql` 不需要修改） |
| 最近 | **Cloud Skills Object Storage 抽象实现（Phase 3 / Step 2B）**（`internal/skillstore` port / 语义类型 / reconciliation core + `internal/skillstore/fakestore` 测试替身：create-only `PutImmutable`/`Stat`/`Get`、五值结果分类、`Locator`、三项校验 `Reconcile`、`MISMATCH` fail closed；单测全通过 `go test ./internal/skillstore/...`，`go vet` / `golangci-lint` 干净；**无**生产 provider / 上传管线 / 摄取编排 / `RetrievalCapability` / Node 交付） |
| 最近 | **Cloud Skills Ingestion Saga 设计冻结 + 设计修正（Phase 3 / Step 2C.0 + 2C.0a，仅设计）**（ADR `20260927-canonical-ingestion-saga.md` + `plan/plan_Skills.md` Phase 3 的 Step 2C 契约：candidate 模型、batch partial-success、语义 request 指纹 = `(target_skill_id, display_name, summary, content_digest, package_digest)`、per-candidate 幂等身份 `(workspace_id, idempotency_key, canonical_name)`、TX #1/TX #2 内容、CAS 冲突 durable `activation_outcome`、崩溃恢复 = client-driven continuation（客户端幂等重提交 + bytes 确定性重建，无服务端 worker、无 canonical persistence 之前的 autonomous payload recovery）；明确需要 `0016_*` forward migration（`canonical_name` + `request_fingerprint` + `activation_outcome` 列 + 唯一索引，四变更），`0015_skills.sql` 不改；**无**代码 / provider / 上传 API / 摄取管线 / migration） |
| 最近 | **Cloud Skills SKILL.md Metadata Contract 冻结（Phase 3 / Step 2C.0b，仅设计）**（ADR `20260927-skill-md-metadata-contract.md`：关闭 `canonical_name` 派生缺口 —— `canonical_name = ASCII lowercase(TrimSpace(name))` 唯一变换、`name` 必填且必须是 YAML string（ASCII `[A-Za-z0-9._-]+`、不以 `.` 开头、≤ 200 bytes）、`description` 可选、重复 key 与非法 YAML fail closed、未知字段不透明；解析器归业务 metadata 层，`internal/skillpkg` 保持冻结；与 Desktop `0-static-skill-package.md` D3 及 5 个审计到的 multica `SKILL.md` 内容兼容；`plan/plan_Skills.md` + docs 同步；**无**代码 / 迁移） |
| 最近 | **Cloud Skills Ingestion Saga 实现（Phase 3 / Step 2C）**（`internal/skillmeta` 解析器 + `0016_skill_ingestion_idempotency.sql` forward migration（`canonical_name`/`request_fingerprint`/`activation_outcome` + 唯一索引，`0015` 不改）+ `internal/core` 的 `Store.IngestSkill`/`IngestSkills` journal-first saga（TX #1 → 事务外 `PutImmutable`/`Reconcile` → TX #2 + activation CAS；batch partial-success；client-driven continuation）；11 个集成用例全通过。**无**上传 HTTP API / 生产 provider / `RetrievalCapability` / Node 交付） |
| 最近 | **Cloud Skills Source Intake 实现（Phase 3 / Step 2C.1）**（`internal/skillsource` directory / ZIP / 未压缩 TAR 摄取 + source 级安全校验（复用 `skillpkg.ValidatePath`）+ 精确 `SKILL.md` candidate 发现（root candidate owns 整树 / nested root 与 duplicate `canonical_name` 整 source 拒绝）+ invalid metadata → `PreparationFailure`；`PreparedSourceResult` 经 `core.Store.IngestSource` 喂入既有 `IngestSkills` saga；无 schema 变更；source adapter 单测 + 6 个真实 PostgreSQL 集成用例全通过） |
| 最近 | **Cloud Skills Public Upload API Contract 冻结（Phase 4 / Step 3A，仅设计）**（ADR `20260927-public-upload-api-contract.md`：`POST /api/v1/tenants/:tid/spaces/:spaceId/skills/imports`（space-scoped，`spaceId`=saga `workspace_id`）；multipart 单 archive part、`source_kind∈{zip,tar}` 显式、directory 由客户端打包；request 字段白名单 + 禁传 canonical_name/digest/locator/revision_id；`Idempotency-Key` = saga 幂等 namespace；200 部分成功 envelope + `400 source_*` source structural failure；replay/continuation 沿用 saga；上传预算 ≤ 2 GiB（产品上限 256 MiB）、buffering ephemeral-only；schema NONE（`0015`/`0016` 不变）；**实现 NOT started**） |
| 最近 | **Cloud Skills 生产 Object Storage Provider 设计冻结（Phase 3 / Step 4A，仅设计）**（ADR `20260927-production-object-storage-provider.md`：审计确认仓库无既有对象存储约定 → 选单一 S3-compatible provider（不默认 AWS）+ 唯一生产实现 `aws-sdk-go-v2`/`service/s3`；`PutImmutable` create-only = 原生条件 `PutObject(IfNoneMatch="*")`（412 = `PutAlreadyExists`，禁止 HEAD-then-PUT TOCTOU）；V1 单次原子 PUT、不启用 multipart；port 保持 `[]byte` 全缓冲、无放大；冻结错误 taxonomy 与五值映射、connect 5s/request 30s、`storage` 配置段（bucket/region/endpoint/path_style/credential_mode/credentials_file/tls.verify/ca_file/allow_insecure_http/timeouts）、凭证仅部署平台机制、TLS 默认 HTTPS+校验、integrity = SHA256+Decode+TreeDigest、启动 fail-fast/nil 降级；schema NONE（`0015`/`0016` 不变）；**生产 provider 实现 NOT started**） |
| 最近 | **Cloud Skills 生产 Object Storage Provider 实现（Phase 3 / Step 4B）**（`internal/skillstore/s3store` adapter + `internal/config` `storage` 段 + `cmd/server` 装配：`PutImmutable` = 单次 `PutObject(IfNoneMatch="*")`、`Stat` = HEAD、`Get` = 有界读；错误分类经 `*smithyhttp.ResponseError` 状态码 → 五值结果、原始 SDK 错误绝不跨 port；connect 5s/request 30s + `http.DefaultTransport.Clone()` + `RetryMaxAttempts:3`；凭证三模式 environment/shared_credentials_file/workload_identity；`storage` 缺失→nil、存在但非法→fail fast；依赖仅 `aws-sdk-go-v2` + `service/s3` + `config` + `credentials`；`s3store_test.go`（fake-`api` 矩阵 + 真实 SDK `httptest` 端到端 412→`PutAlreadyExists`）+ `storage_test.go`；`go build`/`go vet`/`go test ./internal/... ./cmd/...`/`golangci-lint`/`git diff --check` 全通过；改动 uncommitted；**未做**：`RetrievalCapability` / Node 交付 / Skills 看板 / Agent·Execution 绑定 / GC / MinIO fixture / 真实 provider CI） |
| 最近 | **Cloud Skills RetrievalCapability 契约闭合（Phase 6 / Step 5A，仅设计）**（`specs/decisions/controller/skill-delivery/0-skill-retrieval-capability.md` + `specs/decisions/node/agent-runtime/0-skill-materialization-and-readiness.md` 从 `cloud/skills/` 迁到规范化的 Controller/Node 叶子域并闭合遗留开放问题；镜像测试用例迁到 `test-cases/controller/skill-delivery/` 与 `test-cases/node/agent-runtime/`；冻结：仅对已冻结进 `ExecutionSkillBinding` 的 revision 签发（绝不重解析 `Skill.current_revision_id`）、单一 immutable object 仅 GET、short-lived signed HTTPS GET URL（bearer credential、vendor-neutral）、TTL 默认 300s/硬上限 900s（`expires_at` 显式下发）、refresh 同 revision、capability 永不 durable/日志（redaction）、Controller 协调+转发不代理 bytes（data plane Node→Object Storage 直连）、`ObjectStore` port 不变 + 新增 `RetrievalCapabilityIssuer` 边界（`object_locator` 保持逻辑 key）、fencing 不吊销已签发 bearer URL + V1 撤销 = credential 过期、9 类失败分类、缺失对象签发前不 Stat/HEAD → Node 404 fail closed；schema NONE；**实现 NOT started**） |
| 最近 | **Cloud Skills Minimal Agent & AgentSkillBinding 实现（Phase 4 / Step 4B）**（`specs/decisions/cloud/agent/0-agent-skill-binding.md` 冻结的最小 durable 权威模型落地为 migration `0017_agents_and_skill_bindings.sql`（`agents` + `agent_skill_bindings`，`0015`/`0016` 不改）+ `internal/core/agents.go`（Agent CRUD + binding list/attach/enable-disable/detach + `enabledAgentSkillBindings` 读缝，按 `canonical_name` then `skill_id` 确定性排序、不解析 revision/digest/locator）；9 个 workspace-scoped 端点（`/agents[/:agentId[/skills[/:skillId]]]`）；授权复用 `workspaceRole`（非成员 404 / member 写 403 / 删除 creator 或 owner/admin）+ attach 同 Workspace/未软删；无任何 Execution/Attempt/capability/locator 字段；contract/OpenAPI/frontend 同步；`integration/agent_skill_binding_test.go` 8 用例（fresh/upgrade/归属与名称唯一/binding 不变量/§17 §18 权威读缝/CRUD+auth/binding lifecycle/cross-workspace+soft-deleted）全通过；`go build`/`go vet`/`go test ./internal/... ./cmd/...`/`golangci-lint`/`git diff --check` 全通过（仅 `cmd/devsetup` Windows 文件权限既有失败）；**ExecutionSnapshot（Phase 5）对 `AgentSkillBinding` 依赖已解除**但本体未实现） |
| 最近 | **Cloud Skills Execution/Attempt/ExecutionSkillBinding 实现（Phase 5 / Step 5）**（`specs/decisions/cloud/skills/20260927-execution-skill-snapshot.md`（`approved`→`implemented`）冻结的逻辑 Execution / 物理 Attempt / 不可变 ExecutionSkillBinding 落地为 migration `0018_execution_snapshot.sql`（`executions` + `attempts` + `execution_skill_bindings`，`0015`/`0016`/`0017` 不改）+ `internal/core/executions.go`（`AdmitExecution` 同事务原子写 executions + 首 Attempt（ordinal=1、`eligible`）+ 全量 bindings、事务内无外部效果；`CreateRetryAttempt` 复用 frozen binding；`RunAgainExecution` 重新解析）+ `internal/controlpb` proto 扩展（`SkillBundleRef`/`SkillRunSpec` + `ExecutionInput.oneof spec` 新增 `skill_run = 2`）；准入只读 durable `AgentSkillBinding`、逐 Skill 解析精确 `current_revision_id`、无 revision fail closed、非 member 404、`execution_skill_binding_immutable` trigger 拒绝 UPDATE、无 bearer-credential 字段、`SkillBundleRef[]` 确定性排序；`integration/execution_snapshot_test.go` 11 用例全通过；`go build`/`go vet`/`golangci-lint`/`git diff --check` 全通过（`-race` 仅 CI ubuntu amd64、`cmd/devsetup` Windows 文件权限既有失败均与本次无关）；**未做**：RetrievalCapability mint/refresh、Controller dispatch/claim/lease 管线、Node 下载/缓存投影/READY barrier、Agent 看板 UI） |
| 最近 | **Cloud Skills RetrievalCapability 实现（Phase 6 / Step 5B，Cloud 侧 mint/refresh）**（`specs/decisions/controller/skill-delivery/0-skill-retrieval-capability.md`（`proposed`→`implemented`）的 Cloud 侧读取契约落地：`internal/skillstore/retrieval.go`（`RetrievalCapabilityIssuer` port + 脱敏 `RetrievalCapability.String()`，与 durable `ObjectStore` 分离、port 不变不加 Presign）+ `internal/skillstore/s3store/issuer.go`（`PresignGetObject` exact-object GET、TTL 钳制、无存在性探测）+ `internal/core/retrieval.go`（`MintSkillRetrievalCapabilities` transact 解析 `(execution, attempt, skill_revision)` 权威，terminal fencing、fail closed）+ `internal/controlgrpc/retrieval.go`（`MintSkillRetrieval`，请求不含 locator/bucket/key/URL）+ `fault.go` 六类 D32 映射 + `internal/config/storage.go` `retrieval_capability_ttl`（默认 300s / 上限 900s）+ `cmd/server` 装配；无持久化、无 schema 变更、不记录 credential；`internal/skillstore/retrieval_test.go`（脱敏）+ `s3store/issuer_test.go`（exact-object GET / 签发错误 / 非正 TTL / 真实 SDK signed URL）+ `integration/retrieval_capability_test.go` 9 用例（happy / 未绑定 revision / 外源三元组 / terminal fencing / storage_not_configured / signing_failed / invalid_locator / 无持久化 / refresh 保持 revision）全通过；`go build` / `go vet` / `gofmt` / `git diff --check` 全通过（`cmd/devsetup` Windows 文件权限既有失败与本次无关）；**未做**：Node 下载/缓存投影/READY barrier、Controller dispatch/claim/lease relay、Agent 看板 UI） |
| 下一步 | **3C**（Issue Detail & Collaboration UI）；**Issue Workspace Scoping**（`issues.space_id`）；Agent / Team / Workflow / MCP / Skill Workspace Scoping；生产 Substrate / 看板分页与全文搜索 / 实时推送；真实 Agent/Team/Workflow/AI provider（BLOCKED ON EXTERNAL DESIGN）；**Cloud Skills 后续切片**（Node 交付 / Controller relay 实现（Cloud 侧 mint/refresh 已由 Step 5B 落地）/ Skills 看板 UI / GC·retention / MinIO dev fixture / 真实 provider CI，均未实现） |

> 想看每个功能对应的接口和表，去 [../agent/api-reference.md](../agent/api-reference.md) 和
> [../agent/database.md](../agent/database.md)。想看迁移的完整决策记录，去
> [../../migrations/multica-issue-board/](../../migrations/multica-issue-board/)。
