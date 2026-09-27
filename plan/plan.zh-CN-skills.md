# Ora Cloud Skills — 实施计划（中文版）

状态：**已批准的设计基线 / 可进入实施阶段**

用途：本文件是 Cloud Skills 工作的**执行依据、审计依据与验收依据**。  
实施 Agent 必须先阅读本文件，再阅读最近的 `AGENTS.md`、相关已批准 ADR、规格、测试与文档，然后才能修改代码。

---

## 0. Agent 工作协议

实施流程：

1. 先阅读本 `plan.md`。
2. 阅读仓库规则：
   - 根目录 `AGENTS.md`
   - 修改 `specs/` 前先读 `specs/AGENTS.md`
   - 修改 `frontend/` 前先读 `frontend/AGENTS.md`
   - 以及相关目录中最近的嵌套 `AGENTS.md`
3. 检查相关已批准 ADR、核心测试用例、当前实现、迁移、API contract 和现有测试。
4. 以满足本计划为目标，实现**最小、完整、语义一致**的改动。
5. 如果实现与以下任一内容冲突：
   - 本计划；
   - 已批准 ADR；
   - 仓库规则；
   - 不可变兼容性边界；
   - 当前权威实现事实；

   **必须立即 STOP 并报告冲突，不得自行重新解释计划。**
6. 普通实现细节可由 Agent 自主决定。
7. 下列语义变化必须先讨论并更新本计划，之后才能继续：
   - schema；
   - ownership；
   - authorization；
   - transaction boundary；
   - external effect；
   - recovery；
   - idempotency；
   - compatibility；
   - execution input；
   - protected behavior。
8. 运行所有适用的仓库 gate。
9. 最终必须按本计划逐项审计，并把每个验收项标记为 PASS / FAIL / NOT APPLICABLE。

本计划**不能覆盖**仓库中的 `AGENTS.md`、已批准 ADR 或兼容性要求。

---

# 1. 目标

新增一等公民级 **Cloud Skills** 能力，包含：

- Collaboration Workspace 归属；
- 目录与 archive 导入；
- 递归发现多个 Skills；
- Object Storage 中的不可变 Skill 内容修订；
- PostgreSQL 中的业务元数据与恢复证据；
- Agent 持久 Skill 绑定；
- 每次 Execution 的不可变 SkillRevision 快照；
- Node 通过短期只读 retrieval capability 直接获取内容；
- Node verified content cache；
- 每个 Attempt 的 Agent-native Skill materialization；
- Agent 启动前的强 READY barrier；
- 基于不可变 Execution input 与新 Attempt 的明确 retry 语义。

设计可以参考 Multica 的已实现经验，但必须遵循 Ora 自己的架构与不变量。

核心原则：

> 迁移能力，而不是复制架构。

---

# 2. 术语与归属边界

以下概念必须在实现中显式区分。

## 2.1 Collaboration Workspace

产品协作 / 授权边界。

- 每个 Skill 必须且只能属于一个 Collaboration Workspace。
- Workspace ownership 由 Cloud 解析并校验。
- Skill 不能通过普通 update 在不同 Collaboration Workspace 间移动。
- 跨 Workspace 转移等价于 copy/export/import，并产生新的业务资源。

不得将 Collaboration Workspace 与 Runtime Workspace 或 Desktop `EffectScope::Workspace` 混淆。

## 2.2 Runtime Workspace

Project 作用域的 runtime checkout/worktree/runtime lifecycle 概念。

Cloud Skill ownership **不得**归属于 Runtime Workspace。

## 2.3 Skill

由一个 Collaboration Workspace 拥有的稳定、可变业务资源。

## 2.4 SkillRevision

属于某个 Skill 的不可变 canonical package 快照。

## 2.5 AgentSkillBinding

可变的 Agent 配置，描述未来 Execution 默认应使用哪些 Skills。

## 2.6 ExecutionSkillBinding

不可变的执行事实，描述某次 Execution 实际绑定的精确 SkillRevision。

## 2.7 Execution

拥有不可变 resolved input 的逻辑执行。

## 2.8 Attempt

一次 Execution 的物理执行尝试。

Retry 会在同一个 Execution 下创建新的 Attempt。  
Retry 不得隐式创建新的 Execution，也不得重新解析可变的 Agent/Skill 配置。

## 2.9 SkillIngestion

某一个候选 Skill 的上传/导入 saga 持久证据。

## 2.10 VerifiedSkillBundle

Node 本地、已验证、由 SkillRevision 派生出的不可变内容。它不是权威业务状态。

## 2.11 ExecutionSkillProjection

Attempt 作用域、Agent 可见的文件系统投影，由已验证 Skill 内容派生。

---

# 3. 权威持久化与存储

## 3.1 PostgreSQL

PostgreSQL 作为以下内容的权威来源：

- Skill 业务元数据；
- SkillRevision 元数据；
- AgentSkillBinding；
- ExecutionSkillBinding；
- SkillIngestion / recovery evidence；
- Execution / Attempt 持久状态；
- authorization / lifecycle / version / idempotency 元数据。

PostgreSQL **不得**用于存储任意 Skill package bytes。

## 3.2 Object Storage

Object Storage 是不可变 canonical Skill package bytes 的权威存储。

对象发布后必须不可变。

不得覆盖已经存在的不可变 revision object。

## 3.3 Node 文件系统

Node cache、staging 目录和 execution projection 都属于派生状态。

即使所有 Node 本地 Skill 数据全部丢失，也不得损坏 Cloud 业务状态。

---

# 4. Skill 模型

建议的语义结构：

```text
Skill
├─ id
├─ workspace_id
├─ canonical_name
├─ display_name
├─ summary
├─ icon / tags / board metadata（按仓库现有约定）
├─ current_revision_id
├─ version
├─ created_by
├─ created_at
├─ updated_at
└─ deleted_at
```

要求：

- 唯一属于一个 Collaboration Workspace；
- 业务/UI metadata 可修改；
- soft delete；
- 使用现有仓库约定进行 optimistic concurrency；
- `display_name` 不定义 package identity；
- `canonical_name` 来源于经过验证的 `SKILL.md.name`；
- 修改 board/UI metadata 不得修改 SkillRevision 或 `SKILL.md`；
- 普通 update 不得修改 `workspace_id`。

---

# 5. Workspace 内 Skill identity 与匹配规则

## 5.1 Active name 唯一性

同一个 Collaboration Workspace 内，active Skill 必须拥有唯一 canonical package name。

语义约束：

```text
(workspace_id, canonical_name)
在 deleted_at IS NULL 的 Skill 中唯一
```

具体使用仓库现有支持的 PostgreSQL 机制实现。

## 5.2 Batch import 匹配

对于目录/archive batch import：

```text
(workspace_id, canonical SKILL.md.name)
```

作为 Skill matching key。

如果不存在 active 同名 Skill：

- 创建新 Skill；
- 创建或复用其第一个 SkillRevision；
- 激活最终 revision。

如果存在 active 同名 Skill：

- 将候选内容视为现有 Skill 的新内容导入。

## 5.3 显式 update 匹配

对于明确的“更新这个 Skill”操作：

```text
target_skill_id
```

是权威 identity。

即使 `SKILL.md.name` 发生变化，也不得使用启发式 rename 判断。

更新后仍必须满足同 Workspace canonical-name 唯一性。

## 5.4 禁止启发式 rename

不得基于以下信息猜测 rename：

- 目录路径；
- 相似 description；
- 相似 bytes；
- 内容相似度；
- 旧 display name。

## 5.5 Display name

`display_name` 是可变 Workspace metadata，**不得参与 import matching**。

---

# 6. SkillRevision 模型与去重

建议语义结构：

```text
SkillRevision
├─ id
├─ skill_id
├─ workspace_id             # 如有助于 auth/query，可冗余
├─ content_digest
├─ digest_algorithm
├─ size_bytes
├─ file_count
├─ package_format
├─ package_format_version
├─ object_locator
├─ package_name
├─ package_description
├─ manifest metadata
├─ created_by
└─ created_at
```

要求：

- 创建/ready 后不可变；
- 不提供“更新内容”的 API；
- 内容变化必须创建或复用另一个不可变 revision；
- revision identity 属于某个具体 Skill。

强唯一性不变量：

```text
UNIQUE(skill_id, digest_algorithm, content_digest)
```

## 6.1 同一 Skill + 同 digest

复用现有 SkillRevision。

不得创建重复 revision row。

重复导入可以产生新的 SkillIngestion 记录，但不得产生重复 SkillRevision。

## 6.2 重新导入旧 revision

如果：

```text
R1 = digest A
R2 = digest B（current）
```

之后再次导入 digest A：

- 复用 R1；
- activation 可以把 `current_revision_id` 切回 R1；
- 不得创建一个内容与 R1 相同的 R3。

## 6.3 不同 Skill + 同 digest

不同 Skill 业务资源仍然独立：

```text
Skill A / Revision A1 / digest X
Skill B / Revision B1 / digest X
```

Object Storage 和 Node cache 可以按 digest 做物理 dedup，但业务 identity/ownership 不得合并。

---

# 7. SkillIngestion 与 batch import

Skill 上传/导入是 saga，不是跨 PostgreSQL 与 Object Storage 的事务。

每个候选 Skill 拥有独立 SkillIngestion 和独立短 DB transaction。

概念字段：

```text
SkillIngestion
├─ id
├─ workspace_id
├─ target_skill_id?
├─ idempotency_key
├─ source_type
├─ expected/canonical digest（若已知）
├─ stable object identity / locator metadata
├─ state
├─ error_code?
├─ error_detail?
├─ created_by
├─ created_at
└─ updated_at
```

具体状态名可遵循仓库约定，但语义必须覆盖：

- 已记录 intent；
- 外部对象工作 pending/in-progress；
- 内容 verified；
- revision committed / activated；
- failed / recoverable evidence。

不得把 transient ingestion failure state 塞进不可变 SkillRevision row。

## 7.1 Batch 行为

递归目录/archive import：

```text
DISCOVER
→ VALIDATE CANDIDATES
→ APPLY EACH CANDIDATE INDEPENDENTLY
→ BATCH SUMMARY
```

Batch 语义为 **partial success**。

单个候选失败不得回滚其他已经成功的候选。

禁止使用跨全部 Skills 的 batch-level DB transaction。

除非现有 API/lifecycle 约定强制要求，否则不要求额外持久化 batch table。  
但每个 candidate 的 ingestion evidence 是必需的。

Batch response/report 至少包含：

- discovered candidates；
- created；
- updated / activated；
- unchanged；
- failed。

---

# 8. 上传/导入发现

V1 接受：

- 本地目录选择；
- archive 上传。

两者必须归一化为相同的 canonical virtual file tree 与 canonical package representation。

## 8.1 递归目录发现

递归查找 `SKILL.md`。

每个 `SKILL.md` 的直接父目录即为一个 candidate Skill root。

该 root 下整个 subtree 都属于该 candidate。

V1 不支持 nested Skill roots，发现时必须 fail，而不是猜测或合并。

示例：

```text
foo/
├─ SKILL.md
└─ child/
   └─ SKILL.md
```

应因 nested Skill root 判为 invalid。

## 8.2 Archive discovery

Archive entry 被解释成 virtual tree，并复用与 directory import 完全相同的 Skill-root discovery 规则。

在验证 entry path/type/limit 之前，不得把不可信 archive 直接解压到最终真实目录。

---

# 9. Canonical package contract

## 9.1 Root 要求

一个 canonical Skill package 是一棵独立文件树，其 root 必须包含：

```text
SKILL.md
```

`SKILL.md` 为必需。

## 9.2 Canonical path

package 内路径必须是 relative POSIX-style path。

示例：

```text
SKILL.md
scripts/deploy.sh
assets/icon.png
references/api.md
```

禁止：

- absolute path；
- `.` component；
- `..` component；
- empty path component；
- NUL；
- Windows drive prefix；
- ambiguous separator；
- path traversal。

Windows 输入的 `\` 可在 ingestion boundary 先转换，再进行 canonical validation。

原则：

> 只 normalize 一次，只校验 canonical form，只存 canonical form。

## 9.3 Collision

canonicalization 后：

- duplicate canonical path 非法；
- 为保证跨 filesystem portability，case-insensitive collision 非法。

Canonical identity 本身保持 case-sensitive，除非已有已批准 spec 明确规定其他行为。

## 9.4 Filesystem entry type

V1 只允许：

- regular file；
- directory。

V1 拒绝：

- symlink；
- archive hardlink entry；
- device；
- FIFO；
- socket；
- 其他 special filesystem node。

## 9.5 Text 与 binary

`SKILL.md`：

- 必须是有效 UTF-8；
- 必须能解析 required package metadata；
- 必须包含有效 `name`；
- 必须满足认可的 Skill metadata schema。

其他 package file 可包含任意 bytes。

## 9.6 Canonicalization 不改写用户内容

Canonicalization 不得静默：

- 重写 Markdown；
- 重排 YAML/frontmatter；
- normalize newline；
- 注入 name；
- 格式化用户文件。

Canonicalization 定义的是 tree/path/manifest/package representation，而不是作者文件格式。

---

# 10. Canonical digest

不得把用户上传的原始 zip/tar bytes 直接作为 Skill content identity。

Digest 必须基于 canonical file tree。

要求：

- digest scheme versioned；
- per-file digest；
- 按 canonical path 做确定性排序；
- 使用无歧义编码，例如 length-prefixed field 或其他明确 canonical encoding。

概念 manifest：

```text
CanonicalManifest
├─ manifest_version
└─ files[]
   ├─ path
   ├─ size
   └─ sha256
```

然后：

```text
content_digest = SHA256(canonical-manifest-encoding)
```

以下 archive metadata 不得影响 Skill content identity：

- mtime；
- uid/gid；
- entry order；
- compression method/level；
- archive comment。

只要 directory、ZIP 或未来其他 adapter 产生的 canonical 文件树和 bytes 相同，就必须得到相同 digest。

---

# 11. Canonical Object Storage package

权威 runtime object 必须是**版本化 canonical Ora Skill package**，而不是用户原始 transport archive。

概念 identity：

```text
package_format = ora-skill-package
package_format_version = 1
```

具体 serialization/container format 可在实现中决定，但必须：

- deterministic，或者与 canonical manifest digest 配套；
- 保留任意 file bytes；
- 支持安全、受限 extraction/materialization；
- versioned；
- 可独立做 integrity verification。

Node 只消费 canonical package。

Node 不得需要理解：

- browser directory upload；
- ZIP upload；
- 其他 source transport format。

---

# 12. Quota 与 archive 安全

V1 至少必须有可配置限制：

- 每个 Skill 最大文件数；
- 单文件最大大小；
- 总 uncompressed Skill size；
- 最大 `SKILL.md` size；
- 最大 path length；
- 最大 path component length；
- 最大 compressed archive request size（适用时）；
- 最大 uncompressed archive output。

所有限制必须在 SkillRevision commit/activate 之前生效。

Archive processing 必须防御：

- traversal / zip-slip；
- absolute path；
- special node；
- symlink/hardlink trick；
- duplicate normalized path；
- case-insensitive collision；
- decompression bomb。

具体默认数值属于 implementation detail，除非已有仓库/spec contract 明确规定。

---

# 13. 上传 / update transaction 语义

不得在 Object Storage / HTTP / filesystem / Node / process 工作期间持有 PostgreSQL transaction 或 DB lock。

必须采用：

```text
DB TX #1
  authorize
  validate stable request identity / Idempotency-Key
  persist ingestion intent and stable external identity/evidence
COMMIT

Object Storage
  upload canonical immutable object
  verify result / reconcile ambiguous outcome

DB TX #2
  create or reuse immutable SkillRevision
  finalize ingestion
  CAS Skill.current_revision_id using Skill.version
COMMIT
```

要求：

- 外部 mutation 前已有 stable object identity；
- Object Storage 结果不确定时，按 stable identity reconcile；
- 不得猜测成功；
- 不得覆盖 immutable revision object；
- 只有 verified content 才允许 activation；
- activation CAS 冲突不能让一个已经合法存在的 immutable revision 失效；
- external effect 必须在 DB transaction 外。

POST/DELETE 与 optimistic concurrency 必须遵守仓库现有约定。

---

# 14. Agent Skill 配置

V1 selection model：

```text
仅 Agent persistent Skill defaults
```

V1 不提供 per-run add/remove Skill override。

概念 binding：

```text
AgentSkillBinding
├─ agent_id
├─ skill_id
├─ enabled
├─ created_at
└─ updated_at
```

要求：

- binding 引用 Skill 业务 identity，而不是 revision；
- Agent configuration 表达“未来 Execution 默认使用哪些 Skills”；
- disabled binding 仍保留为配置，但从 effective execution selection 排除；
- binding 不包含 runtime path 或 Object Storage 细节。

---

# 15. Execution Skill snapshot

创建/准入 Execution 时，Cloud 必须：

1. authorize caller 与 Collaboration Workspace；
2. 读取 Agent 当前 enabled Skill assignment；
3. 如存在平台强制 Skill，加入 selection；
4. 把每个 Skill 解析为精确、不可变的 current SkillRevision；
5. 持久化 immutable ExecutionSkillBinding；
6. 在 dispatch 前 commit execution input snapshot。

概念模型：

```text
ExecutionSkillBinding
├─ execution_id
├─ skill_id
├─ skill_revision_id
├─ content_digest
├─ size_bytes
├─ package_format
├─ package_format_version
└─ created_at
```

commit 后，ExecutionSkillBinding 不可变。

强不变量：

- Controller claim/dispatch 不得从 mutable AgentSkillBinding 重新计算 Skills；
- Node 不得解析“current SkillRevision”；
- Agent Skill assignment 变化只影响未来 Execution；
- `Skill.current_revision_id` 变化只影响未来 Execution；
- Skill 被 soft delete 后，不得静默改变已经存在的 Execution input；
- retry 必须保留完全相同的 ExecutionSkillBinding。

---

# 16. Execution 与 Attempt recovery 模型

Execution 拥有不可变业务输入。

Attempt 表示一次物理执行尝试。

```text
Execution E
├─ immutable inputs
├─ immutable ExecutionSkillBinding[]
├─ Attempt 1
├─ Attempt 2
└─ ...
```

Retry：

```text
Retry
= 同一 Execution 下的新 Attempt
= 完全相同的 immutable inputs
```

Run Again / 新逻辑执行：

```text
= 新 Execution
= 重新读取当前 Agent 配置和 current SkillRevision
```

不得混淆 Retry 与 Run Again。

## 16.1 Attempt 内 local retry

Transient retrieval failure 可以在同一个 Attempt 内 retry。

例如：

- timeout；
- transient network failure；
- HTTP 429；
- retryable 5xx。

具体 retry schedule 属 implementation detail。

## 16.2 Failed Attempt

Attempt 一旦进入 failed，V1 不得恢复这个 failed Attempt。

可以按 retry policy 创建新的 Attempt。

## 16.3 Permanent failure 与 Node-local failure

failure taxonomy 必须区分：

- permanent Execution/input failure；
- transient retrieval failure；
- Node-local/environment failure。

例如 Node 本地磁盘空间不足，不得自动等同于 immutable Execution input 无效。

当当前 execution architecture 允许时，可把新 Attempt 调度到另一个 Node。

## 16.4 不做 file-level resume

V1 不实现持久化逐文件 materialization checkpoint。

partial staging/projection 均视为 disposable。

---

# 17. Cloud → Node Skill contract

持久 Execution identity 与临时 delivery authorization 必须分离。

## 17.1 SkillBundleRef

概念 runtime descriptor：

```text
SkillBundleRef
├─ skill_revision_id
├─ content_digest
├─ size_bytes
├─ package_format
├─ package_format_version
└─ retrieval
```

## 17.2 RetrievalCapability

概念 capability：

```text
RetrievalCapability
├─ method
├─ url
├─ expires_at
└─ optional required headers
```

V1 可以用短期 signed HTTPS GET URL。

协议模型不得命名成 S3-specific。

## 17.3 Credential / security classification

Signed URL 是 bearer credential。

因此：

- full capability URL 不得持久化到 PostgreSQL；
- 不得写入 durable journal；
- 不得写日志；
- 不得进入 user-facing error；
- 不得进入 durable result/evidence；
- 必须 short-lived；
- 必须 read-only；
- 必须只作用于精确 immutable object；
- 不得拥有 list/write/delete 权限。

这属于对当前“business protocol 不接收 secrets”模型的**显式演进**。  
实施前必须有 approved ADR/spec 变更。

不得把 credential 当普通字段偷偷加入既有协议。

## 17.4 Capability refresh

Capability 过期不得改变 Execution identity。

Refresh request 概念上包含：

```text
execution_id
attempt_id
skill_revision_id
```

Cloud 校验：

- Attempt 属于 Execution；
- Execution 确实绑定该 SkillRevision；
- Attempt 仍允许 retrieval。

之后只为**同一个 immutable object**重新签发 capability。

Refresh 不得：

- 查询 `Skill.current_revision_id`；
- 重新解析 AgentSkillBinding；
- 换成其他 revision。

具体 HTTP/gRPC 路由必须遵守当前 repository contract 与 Controller/Node transport 约定。

---

# 18. Authorization 模型

Authorization 始终由 Cloud 负责。

要求：

- Skill ownership 由服务器通过 Skill → Collaboration Workspace 推导；
- 不得信任客户端传入的 workspace/tenant ownership claim；
- cross-Workspace existence 不得通过错误差异泄漏；
- Execution admission 在 Skill 数据到达 Controller/Node 之前完成 authorization；
- Controller 与 Node 不负责 tenant/user/member authorization；
- Node 只消费已授权、不可变的 execution descriptor；
- capability mint/refresh 必须针对 durable ExecutionSkillBinding 授权。

Node 不独立重新校验 Collaboration Workspace membership。

---

# 19. Data plane

控制面：

```text
Cloud
→ Controller
→ Node
```

Skill bytes 数据面：

```text
Node
→ Object Storage
```

Controller 不得代理 Skill package bytes。

Cloud API 不得成为正常 Skill package byte proxy。

long-lived Object Storage credential 不得放入 execution business message。

---

# 20. Node verified content cache

V1 包含 Node-local、按 digest address 的 verified cache。

语义 identity：

```text
digest_algorithm + content_digest
```

不得从 cache identity 推导业务 ownership。

不同 Workspace/Skill 可以物理复用同一份 verified bytes，但业务资源仍保持独立。

行为：

```text
cache hit
→ 按要求验证 evidence/content
→ 使用 immutable cached content

cache miss
→ 获取 capability
→ 下载到 staging
→ 验证 size
→ 验证 digest
→ atomic publish to cache
```

要求：

- per-digest population concurrency control；
- atomic publish；
- corrupt entry 不得被消费；
- verified publish 后 cache immutable；
- cache 是 derived/disposable state；
- 删除 cache 只会触发重新下载，不会丢失业务状态。

Cache GC 与 Execution projection cleanup 分离。

V1 必须有 bounded retention 或明确安全的初始 GC policy；不得故意制造无限增长的永久 cache。

具体 quota/LRU/age policy 属 implementation detail，除非已有 Node 约定。

---

# 21. Node materialization contract

## 21.1 三层状态必须分离

```text
Object Storage
= authoritative immutable bytes

Node Verified Cache
= verified reusable derived bytes

ExecutionSkillProjection
= Attempt-scoped Agent-visible filesystem
```

不得混用。

## 21.2 AgentRuntimeAdapter

Agent-specific Skill discovery 约定归 Node Agent runtime adapter 层所有。

概念职责：

```text
VerifiedSkillBundle[]
→ provider/runtime-specific filesystem/config projection
→ launch specification
```

Cloud / SkillRevision 不得编码 Claude/Codex/OpenCode 等 Agent 的具体 filesystem path。

## 21.3 Per-Attempt projection

每个 Attempt 都拥有独立 managed Skill projection。

Agent 不得直接读取或修改 shared verified cache。

Projection 实现可以使用 copy/reflink/mount 等方式，但必须保持 cache immutability。

任何可能通过 writable hardlink 修改 shared cache 的方式都禁止。

## 21.4 Projection staging 与 publish

不得直接在最终 Agent-visible path 里逐步构建 required Skill。

必须：

```text
projection staging
→ write complete tree
→ validate
→ finalize
→ atomic publish
→ projection ready
```

Partial projection 不得对 Agent 可见。

Crash 遗留的 stale staging 可直接视为 disposable。

## 21.5 Content preservation

V1 默认保持 canonical Skill package file byte-preserving。

正常 projection 不得静默改写 `SKILL.md`。

如果某个 Agent runtime 确实要求 deterministic transformation，则必须显式建模为 AgentRuntimeAdapter projection transformation，并有测试/spec evidence。

## 21.6 Ownership

Node 只能删除/替换当前 Attempt projection 中自己拥有的路径。

unknown/user-created file 必须 Preserved，不得隐式覆盖或删除。

不得照搬 Desktop Effect architecture；只迁移必要能力，如 ownership、evidence、fail-closed readiness。

---

# 22. Preparation readiness barrier

Attempt preparation 必须有硬 pre-spawn barrier。

概念 durable phase 可以是：

```text
PLANNED
→ PREPARING
→ READY
→ STARTING
→ RUNNING
→ terminal
```

如果现有状态机已有可复用状态，优先复用；不得无必要引入平行状态机。

`READY` 语义必须同时满足：

- repository/runtime input ready；
- 所有 required SkillRevision 已解析；
- 所有 required Skill bundle 已在本地 verified；
- 所有 required Agent-native Skill projection 已 atomic publish；
- required Agent runtime configuration 已准备完成。

强不变量：

> Agent process 在所有 required Skill materialization ready 之前不得 spawn。

任何 required Skill projection failure 必须使 preparation 失败。

不得降级成 warning 并继续使用 stale/missing Skill。

V1 不存在 optional Skill，除非本计划被显式更新。

READY 是 consumption/audit barrier，不表示 failed Attempt 可在 crash 后恢复。

---

# 23. Script 执行边界

以下阶段：

```text
Upload
Normalize
Validate
Hash
Store
Retrieve
Cache
Materialize
```

**都不得执行 Skill 内容。**

例如：

- shell script；
- Python；
- package manifest；
- Makefile；
- binary；
- WASM；

在 ingestion/materialization 阶段都只是 bytes。

实际执行只能发生在后续 Agent/runtime tool boundary。

当前 Node 环境并不提供通用 security sandbox。  
不得声称存在当前实际上不存在的 Skill isolation。

未来若引入 sandbox/containment，必须另做 approved architecture decision。

---

# 24. Secret handling

必须检查：

- signed retrieval capability 不进入 DB；
- 不进入日志；
- 不进入 telemetry/error string；
- 不进入 fixture/snapshot；
- 不进入 process journal；
- long-lived Object Storage credential 不进入 business protocol；
- repository/Skill URL 不携带 credential；
- Cloud/Controller/Node boundary error 必须 sanitize。

应尽可能用测试覆盖 accidental logging/serialization。

---

# 25. Soft delete 与 retention

Skill 按仓库现有约定 soft delete。

Soft delete：

- 应从未来 ordinary selection/import matching 中移除；
- 不得使已有 immutable ExecutionSkillBinding 失效；
- 不要求立即删除 immutable revision object。

Object retention/GC 与 business delete 分离。

不得删除仍被以下内容需要的对象：

- active/nonterminal Execution；
- audit/recovery retention；
- policy 要求保留的 immutable revision。

V1 如果暂时不需要完整长期 retention policy，可以延后，但禁止 unsafe eager deletion。

---

# 26. Idempotency

Create/import API 必须遵循现有 `Idempotency-Key` 规则。

重要区别：

```text
Idempotency-Key
!=
content deduplication
```

同 key + 同 request：

- 按仓库规则 replay stored response。

同 key + 不同 request：

- conflict。

不同 idempotency key 但 canonical Skill content 相同：

- 可以产生多个 SkillIngestion attempt；
- 对同一 Skill/digest 必须收敛到同一个 existing SkillRevision。

Object Storage mutation 必须使用 stable object identity，使 ambiguous result 可安全 reconcile。

---

# 27. Compatibility 与 ADR 要求

本次改动引入当前 Cloud/Controller/Node specs 中还没有完整表示的新能力：

- first-class Cloud Skill；
- Object Storage 中 immutable SkillRevision package；
- 精确 SkillRevision execution snapshot；
- Node 非 Git artifact retrieval；
- digest verification；
- Node Skill cache；
- Agent runtime Skill materialization；
- preparation READY barrier；
- ephemeral signed retrieval capability / credential transport；
- 与 Skill input 相关的 Execution/Attempt retry semantics。

实现前或实现同时，必须新增/更新相关 approved ADR 和 core test evidence。

尤其 signed retrieval capability：

> 当前 clone protocol 约定 business protocol 不接收 secret。Signed URL 属于 bearer credential。本计划有意引入受限的 ephemeral retrieval credential，因此 architecture/spec 必须显式批准这一演进。

不得将其当成 backward-compatible implementation detail。

---

# 28. API 与 contract discipline

当 public/internal route 或 payload 发生变化：

- 更新 router route 定义；
- 更新 internal contract；
- regenerate OpenAPI；
- regenerate frontend API client；
- 不得手改 generated artifact；
- 同时补 contract/integration test。

继续使用 strict request decoding。

以下 server-owned field 不得接受客户端作为权威输入：

- workspace ownership；
- revision object locator；
- canonicalization 后 digest；
- execution binding identity。

---

# 29. Migration 规则

- 修改前先读 migration conventions；
- applied migration immutable/checksummed；
- 只能新增 forward migration；
- 能由 PostgreSQL 可靠 enforce 的 invariant 尽量交给 constraint；
- 必须测试：
  - fresh database；
  - previous schema upgrade。
- server startup 不得动态执行 DDL。

可能新增约束（具体 table name 按现有 domain naming convention）：

- active Workspace/canonical-name uniqueness；
- SkillRevision `(skill_id, digest_algorithm, content_digest)` uniqueness；
- FK ownership；
- Attempt numbering / Execution relationship invariant。

---

# 30. Transaction 与 external effect 规则

所有 DB transaction 必须保持短、纯数据库。

绝不能跨以下工作持有：

- transaction；
- row lock；
- advisory lock；

跨越：

- Object Storage；
- HTTP；
- filesystem；
- Controller；
- Node；
- process；
- Git。

需要恢复时，external mutation 前先持久化 stable effect intent/evidence。

ambiguous external result 必须用 stable identity reconcile。

stale writer 必须继续受到现有 lease/epoch/version fencing 约束。

---

# 31. Frontend 要求

如果本 wave 包含 Skills board/import UI：

- 先读 `frontend/AGENTS.md`；
- 遵守 module 文档规则；
- 按要求更新中文 `README.md` 和英文 `README.en.md`；
- 增加 module test；
- 使用 generated API client；
- 不手改 generated API output。

UI 语义必须与 backend 一致：

- 一个 batch 只能针对一个 Collaboration Workspace；
- recursive discovery；
- per-candidate result；
- created / updated / unchanged / failed summary；
- display name 修改不改变 package metadata；
- revision/content error 以 candidate 为单位。

除非另有 approved product flow，否则 signed retrieval capability 不得暴露给浏览器。  
Node retrieval capability 不是用户 download link。

---

# 32. 测试要求

测试必须覆盖真实 contract boundary。

至少包括：

## 32.1 Canonicalization

- directory 与 archive 相同文件 → 同 canonical digest；
- Windows separator normalization；
- traversal rejection；
- absolute path rejection；
- duplicate normalized path rejection；
- case-insensitive collision rejection；
- nested Skill root rejection；
- symlink/special node rejection；
- `SKILL.md` UTF-8 requirement；
- binary supporting file preservation；
- file/size/decompression limit；
- archive-bomb behavior。

## 32.2 Skill identity / revision

- same Workspace + same canonical name → same Skill；
- explicit target_skill_id update；
- display_name 不影响 matching；
- same Skill + same digest → reuse existing revision；
- 旧 digest re-import → reuse old revision；
- different Skill + same digest → distinct business revision identity；
- concurrent same-content ingestion 在 DB constraint/idempotency 下收敛。

## 32.3 Ingestion / recovery

- object upload 成功但 DB finalization 失败；
- ambiguous Object Storage result 按 stable identity reconcile；
- activation CAS conflict 不破坏 valid revision；
- batch partial success；
- failed candidate 不回滚 sibling。

## 32.4 Authorization

- cross-Workspace Skill lookup/import/update 不泄漏存在性；
- Execution snapshot authorization 在 Cloud 完成；
- 未绑定 revision 不能 mint/refresh capability；
- expired capability refresh 保持同一 revision；
- Skill delete/update current 不改变已有 Execution binding。

## 32.5 Execution snapshot

- Execution 创建后 Agent binding 改变不影响它；
- Execution 创建后 Skill current revision 改变不影响它；
- Retry/new Attempt 使用完全相同 ExecutionSkillBinding；
- Run Again/new Execution 重新读取当前配置。

## 32.6 Node retrieval/cache

- cache miss → download → verify → publish；
- cache hit 避免 retrieval；
- wrong size fail；
- wrong digest fail；
- corrupt cache 不被消费；
- concurrent population 安全；
- capability expiry/refresh 仍是同一 revision；
- credential 不持久化/不日志；
- cache 删除后安全 re-download。

## 32.7 Projection / readiness

- projection 在 staging 中构建；
- partial staging 不对 Agent 可见；
- AgentRuntimeAdapter 生成正确 native layout；
- required Skill failure 阻止 spawn；
- READY 必须先于 spawn；
- crash/stale staging rebuild deterministic；
- writable projection 不得污染 cache；
- unknown/user file 不被覆盖/删除。

## 32.8 Race / concurrency

由于此工作涉及 recovery、shared cache、persistence lifecycle 与 concurrent ingestion/materialization，必须运行 `AGENTS.md` 要求的 race gate。

---

# 33. V1 非目标

除非本计划显式更新，V1 不包含：

- 脱离 Collaboration Workspace 的 global Skills；
- personal/user-only Skills；
- 通过普通 update 跨 Workspace 移动 Skill；
- per-run 临时 add/remove Skill override；
- nested Skill package；
- symlink support；
- ingestion/materialization 时执行 Skill scripts；
- 通用 Node security sandbox；
- file-level resumable materialization；
- resumable partial package download；
- mutable SkillRevision；
- Node 解析 `current_revision`；
- Controller 代理 Skill package bytes；
- business protocol 中 long-lived Object Storage credential；
- 整套复制 Desktop Effect architecture；
- 把 Workflow 当作 actor。

---

# 34. 实施顺序

建议顺序：

## Phase 1 — Specs / ADR 收敛

正式实现新语义前：

1. 阅读 `specs/AGENTS.md`。
2. 新增/更新 ADR：
   - Cloud Skill ownership/storage/revision；
   - execution immutable SkillRevision snapshot；
   - Object Storage retrieval capability；
   - credential handling exception/evolution；
   - Node verified bundle/cache/materialization；
   - preparation READY barrier 与 Attempt recovery semantics。
3. 新增/更新 core test case，覆盖本计划不变量。
4. 保持 status/evidence marker 准确。

如果已批准 ADR 与本计划冲突，立即 STOP 并报告。

## Phase 2 — Cloud persistence/domain

- migration；
- Skill；
- SkillRevision；
- AgentSkillBinding；
- ExecutionSkillBinding；
- SkillIngestion；
- constraint/index；
- persistence test。

## Phase 3 — Canonical ingestion + Object Storage

- directory/archive adapter；
- virtual tree；
- discovery；
- validation；
- canonical manifest/package；
- digest；
- Object Store port/adapter；
- journal-first ingestion saga；
- partial-success batch behavior。

### Step 2B Object Storage 抽象 —— port / 语义类型 / reconciliation core 已实现；saga 现已驱动该 port

状态：**冻结的抽象已落地为代码** —— `ObjectStore` port、语义类型（`PutRequest`/`PutResult`/
`StatResult`/`GetResult`、`Locator`、`Verdict`）、provider-neutral `Reconcile` 核心，以及
`internal/skillstore/fakestore` 测试替身，全部位于 `internal/skillstore`（含子包）。`internal/core`
的摄取 saga（下方 Step 2C）现已通过 `Store.SkillsObjectStore` 驱动 `PutImmutable`/`Reconcile`。仍然
**没有**任何生产 Object Storage provider、SDK 依赖、配置键、上传 HTTP API、`SKILL.md` 发现或
`RetrievalCapability`，`0015` 不需要改动。由
`specs/decisions/cloud/skills/20260927-object-storage-abstraction.md`（状态 `proposed`）冻结。本小节
是本阶段 Object Storage 工作的验收契约。Step 2B **只**实现了 write-and-verify 抽象层及其测试；它没有
把摄取管线、provider 集成或 Node 交付标记为已实现（管线/saga 已在下方 Step 2C 实现；provider 集成与
Node 交付仍未实现）。

下方验收门是全阶段门；抽象切片只满足其中纯语义项（key 无需外部 I/O 即可推导、无覆盖路径、不以
`error != nil` 决定重试、`DEFINITE_FAILURE` 仅在可证无副作用时产生、`MISMATCH` fail closed、测试替身
可表达各结果），管线/DB 编排项（ingestion 状态迁移、DB TX #2、crash adopt、凭证脱敏）留待后续步骤。

**ObjectStore 语义面。** 只有三个操作：`PutImmutable(request)`、`Stat(locator)`、`Get(locator, bound)`。
request 必须携带完整的 immutable expectation 集合——稳定 locator、expected package digest、expected
package size、expected content digest、package format、package format version、digest algorithm——且每个
操作的结果类型必须能表达下文五值分类的全部取值，`(value, error)` 不足以承载。本切片没有 `List`、
`Delete`、`Copy`、`Move`、`Presign`；不得出现 provider 专有命名（`S3*`、`Bucket*`、`AWS*`）。bucket /
region / 凭证来自被注入的 adapter 配置，永不来自调用参数或持久化数据。port 定义在消费边界一侧，
实现私有放在 `internal/` 下；provider 客户端在 `cmd/*` 装配期构造并注入，领域层永不读取 provider
配置。`PutImmutable` 是 create-only：provider 不支持条件写入时必须 fail closed，不得退化为无条件覆盖。
它的 `created` 与 `already-exists` 都是**非验证性**结果——`already-exists` 只表示"该 key 被占用"，
不表示"内容相同"——只有三条验证同时通过才能产生 verified 结论。provider 的 `ETag` / checksum /
length 只是 evidence，不是 identity，也不能单独产生"已验证"结论。

**Identity 模型。** 三层严格分离：business identity = `SkillRevision.id`；content identity =
`(digest_algorithm, content_digest)`（Step 2A 的 tree digest）；physical identity = object key。
`content_digest` 是 tree digest，**不是** `SHA256(package bytes)`，两者永不互换。content identity
决定业务去重（`UNIQUE(skill_id, digest_algorithm, content_digest)`），physical identity 决定物理
去重。`size_bytes` / `file_count` 描述 canonical tree，不描述 container。

**Package checksum 决策。** `package_digest = SHA256(exact ora-skill-package v1 bytes)`，小写十六
进制。它只有三个用途：(a) 推导 object key；(b) reconcile ambiguous 写入；(c) 检测字节级损坏。
它**不是** business identity，**不是** revision 去重键，**不是** Node cache 键（后者仍是
`digest_algorithm + content_digest`）。它不需要新列，因为 object key 已内嵌它。

**Object key 决策。**
`skills/<package_format>/v<package_format_version>/<digest_algorithm>/<package_digest_hex>`
（例如 `skills/ora-skill-package/v1/sha256/…`；当前 99 bytes，在 `0015` 列边界下最坏 277 bytes，
因此既有的 `<= 1024` CHECK 成立）。key 必须在任何 DB 事务与任何外部调用之前即可确定；`skillpkg.Build`
是确定性纯函数，因此成立。key 不含 tenant / workspace / skill / revision / ingestion 身份，不含
时间戳与随机值。`PutImmutable` 是 create-only：provider 不支持条件写入时必须 fail closed，不得退化
为无条件覆盖。provider 的 `ETag` / checksum / length 只是 evidence，不是 identity，也不能单独产生
“已验证”结论。

**Reconciliation 算法。** ambiguous 结果永不猜测，一律 probe。probe 目标是被持久化的
`object_locator`（不得重新推导 key）：`Stat` → `absent` ⇒ `CONFIRMED_ABSENT`；present ⇒ 有界读取
`Get`，然后要求 `SHA256(bytes) == package_digest`、`skillpkg.Decode` 成功、
`TreeDigestHex() == content_digest` 三条同时成立 ⇒ `CONFIRMED_PRESENT_MATCHING`；任一不符 ⇒
`MISMATCH`；出错 ⇒ `INDETERMINATE`，并对 probe 做有界重试。只有
`CONFIRMED_PRESENT_MATCHING` 才允许进入 DB TX #2。

**Failure taxonomy。** 五类确定性/非确定性结果，**不得**用 `error != nil` 代替：
`CONFIRMED_PRESENT_MATCHING` / `CONFIRMED_ABSENT` / `MISMATCH` / `DEFINITE_FAILURE` /
`INDETERMINATE`。`DEFINITE_FAILURE` 必须能证明请求未生效（受理前拒绝：DNS、连接被拒、429、
授权/参数拒绝）；只要请求可能已被处理，就必须是 `INDETERMINATE`。`MISMATCH` 是确定性的，必须 fail
closed：不覆盖、不删除重写、不 adopt、不改用其他 key。`INDETERMINATE` 是唯一的 ambiguity，保持可
恢复。只有 `ABSENT` 之后、或 transient `DEFINITE_FAILURE` 之后才允许重试 PUT；`INDETERMINATE`
未经 probe 不得重试，`MISMATCH` 永不重试。重试单元是 ingestion，不是 HTTP 请求。PUT 成功但
DB TX #2 之前崩溃时，必须 **adopt** 已存在且正确的对象——外部对象是内容寻址且不可变的，adopt 是
安全的，重新 PUT 不是。`created` 与 `already-exists` 都是**非验证性**的 PUT 结果：只有三条验证
同时通过才能产生 `CONFIRMED_PRESENT_MATCHING`。

**Transaction boundary。** 与 §13 一致：`skillpkg.Build` 在任何事务之外执行；DB TX #1 在任何外部
mutation 之前提交 ingestion intent 与稳定的 `object_locator`；所有 Object Storage I/O 都在任何事务、
行锁、advisory lock 之外执行；DB TX #2 只在 `CONFIRMED_PRESENT_MATCHING` 之后执行。
`skill_ingestions.state` 映射为：`planned`（TX #1 之后、PUT 之前）→ `storing`（PUT 已发起或结论未收敛，
即 reconcile-required；来源有两种：全部 `INDETERMINATE`，以及重试预算尚未耗尽的
`DEFINITE_FAILURE(transient)`）→ `verified`（已证明 `MATCHING`）→ `committed`（存在 durable
`SkillRevision`）/ `failed`（确定性永久失败或 `MISMATCH`；transient `DEFINITE_FAILURE` 在预算耗尽后
也落到这里，`error_code` 标记为 transient 以区分）。ambiguous 永不记为 `failed`；没有 durable
revision 就不得记 `committed`。

**Migration 要求。** 本设计**不需要**任何 migration。`internal/core/migrations/0015_skills.sql` 不得
被修改；其 `object_locator` 列已能容纳推导出的 key。未来若需要可索引或有约束的 `package_digest`
列，或需要超出 `UPDATE … WHERE state = <expected>` 的并发 fencing，必须新增 forward migration
（`0016_*` 或更后）。

**实现验收门（全部成立才视为本阶段完成）：**

- [ ] 没有任何 Object Storage 调用发生在 DB 事务、行锁或 advisory lock 之内；
- [ ] object key 无需外部 I/O 即可推导，且不含任何业务身份；
- [ ] 不存在任何覆盖已存在对象的代码路径；缺少条件写入的 provider 被拒绝启用；
- [ ] `CONFIRMED_PRESENT_MATCHING` 必须三条验证同时成立，任何 provider evidence 单独都不能产生它；
- [ ] 重试/失败判定由五值分类驱动，代码中不存在用 `error != nil` 决定重试的路径；
- [ ] `DEFINITE_FAILURE` 只在能证明请求未生效时产生，否则归入 `INDETERMINATE`；
- [ ] 测试替身能表达 create-only、`already-exists`、`definite-failure`、`ambiguous` 四种结果，否则不构成 D7/D10/D11 的有效证据；
- [ ] ambiguous 结果让 ingestion 停在 `storing`，永不记为 `failed`；
- [ ] `MISMATCH` fail closed：不创建 revision，`Skill.current_revision_id` 不变；
- [ ] PUT 成功与 DB TX #2 之间的崩溃，通过 adopt 已存在对象恢复；
- [ ] provider 细节、凭证与 `object_locator` 永不进入用户可见错误；
- [ ] `0015_skills.sql` 与其 Step 1A 状态逐字节一致；本设计没有新增 migration。

### Step 2C Canonical ingestion saga —— 已实现

状态：**已实现**。`specs/decisions/cloud/skills/20260927-canonical-ingestion-saga.md`（状态 `proposed`）
冻结的 candidate 模型、幂等、事务边界与崩溃恢复语义已落地为代码：`internal/skillmeta`（`SKILL.md` 元数据
解析器）、forward migration `0016_skill_ingestion_idempotency.sql`（`skill_ingestions` 四个 additive 变更），
以及 `internal/core` 中 journal-first 的 `Store.IngestSkill` / `Store.IngestSkills`（经
`Store.SkillsObjectStore` 驱动 `internal/skillstore` port），并配 11 个真实 PostgreSQL 集成测试
（`integration/skill_ingestion_test.go`）。仍然**没有**上传 HTTP API、生产 Object Storage provider 或
`RetrievalCapability`；`0015_skills.sql` 与其 Step 1A 状态逐字节一致。directory/archive source adapter 与
递归 `SKILL.md` 发现由 Step 2C.1 交付（`internal/skillsource` + `Store.IngestSource`）。本小节是 Step 2C
实现切片的验收契约。

**SKILL.md metadata contract —— 已实现（`internal/skillmeta`）。** `canonical_name` 派生缺口由
`specs/decisions/cloud/skills/20260927-skill-md-metadata-contract.md`（状态 `proposed`）关闭，解析器现已
在 `internal/skillmeta` 落地：`canonical_name = ASCII lowercase(TrimSpace(name))` 是**唯一**变换；`name`
必填、必须是 YAML string、匹配 ASCII `[A-Za-z0-9._-]+`（不以 `.` 开头、≤ 200 bytes）；`description` 可选
（string、≤ 4096 bytes）；重复 key 与非法 YAML fail closed；未知字段不透明、原样保留；`internal/skillpkg`
保持冻结、不解析 `name`。与 Desktop `0-static-skill-package.md` D3 及全部审计到的 multica 内容兼容。

**Candidate 模型。** directory/archive source 在纯 CPU 下归一化为一个**确定有序**的 candidate 列表（递归
`SKILL.md` 发现；每个直接父目录 = 一个 candidate root；按 canonical path 排序）。嵌套 `SKILL.md` root 非法，
fail closed。batch 为 partial-success（DISCOVER → VALIDATE → APPLY EACH → SUMMARY）；一个 candidate 失败不
回滚 sibling；每个 candidate 恰好产生一条 `skill_ingestions` 行（其 durable saga journal）。batch 匹配键为
`(workspace_id, canonical_name)`；显式 update 以 `target_skill_id` 为权威；不做 rename 推断。

**幂等。** 请求级 `Idempotency-Key` 与 content identity 严格区分（根 D20）。per-candidate 幂等身份为
`(workspace_id, idempotency_key, canonical_name)`（**定位键**）；语义请求指纹是对
`(target_skill_id, display_name, summary, content_digest, package_digest)` 的 domain-separated、
length-prefixed SHA-256 hex（**相等判定键**）——既不是原始上传 bytes，也不是 key，也**不是**单独的
`(content_digest, package_digest)`（同一 package 可服务不同业务请求：不同 `target_skill_id`、或 caller 选择
的 `display_name`/`summary`，都会改变最终状态）。排除传输层字段（multipart boundary、原始 zip/tar 字节序、
压缩元数据、mtime、HTTP header 顺序、传输文件名）；相同 canonical content + 相同业务语义的 directory 与
archive 收敛到同一指纹。`same key + same fingerprint → resume/replay`；`same key + different fingerprint →
conflict`；`different keys + same content → 多条 ingestion、一条 revision、一个对象`。

**恢复模型 = CLIENT-DRIVEN CONTINUATION（Step 2C.0 的核心）。** Step 2C.0 设计将 *durable state recovery*
（提供：ingestion 的 identity/state/幂等身份/activation 结果都在 DB）与 *durable payload recovery*
（不提供：canonical package bytes 无法从服务端恢复）严格分开。V1 **没有**服务端后台 recovery worker（Step
2B.0 D18：`Idempotency-Key` 在 HTTP 层收敛同 key 请求）——saga 是同步、由客户端驱动的，因此 **Step 2C 设计在
canonical Object Storage 持久化之前不提供 autonomous package-byte recovery**。因为 `skillpkg.Build` 是确定
性纯函数（Step 2B.0 D5），崩溃恢复为：客户端以同一 `Idempotency-Key` 重新提交同一 source；新请求**确定性
重建** exact canonical package bytes，对照 `skill_ingestions` 里已持久化的 identity（`object_locator` +
`expected_digest` + `request_fingerprint`）验证，然后 resume——probe → `ABSENT` ⇒ PUT（用刚重建的 bytes），
`MATCHING` ⇒ adopt 直接进 DB TX #2。不存在任何依赖「原请求还在 / 服务端临时目录还在 / 进程内 byte slice
还在」的 recovery root。这保持根 D19 / Step 2B.0 不变式 19（TX #1 先于任何外部 mutation）成立：bytes 是
**重建**出来的，从不服务端持久化，因此无需 staging store、无需 PUT-before-journal、无需 source blob。

**Stranded ingestion 语义。** 若客户端永不再重提交，`planned`/`storing` 的 ingestion 会无限期 stranded；服务
端**不会**、也**不能**（无 payload、无 worker）自主推进它。resume 只能经**同一** `Idempotency-Key`（新 key =
新 ingestion）；V1 不自动清理（留给 object retention/GC 决策），stranded 行必须可观测（按 `workspace_id` +
`state IN ('planned','storing')` 列出），且不得据此推断外部对象状态。

**事务边界。** source 读取 / archive 解析 / `SKILL.md` discovery 是 I/O，在任何事务之外执行；`skillpkg.Build`
是确定性内容处理（纯 CPU）。DB TX #1（短、仅 DB）authorize、bind 幂等键、插入
`skill_ingestions(state='planned', canonical_name, idempotency_key, request_fingerprint, source_type,
expected_digest=content_digest, digest_algorithm, object_locator=key)`——不含 package bytes、无 Object
Storage I/O。外部阶段（`PutImmutable` → probe → D9 验证）完全在任何事务/锁之外。DB TX #2（短、仅 DB，只在
`CONFIRMED_PRESENT_MATCHING` 之后）create/reuse Skill 与 SkillRevision（复用靠
`UNIQUE(skill_id,digest_algorithm,content_digest)`），新建 Skill 回填 `target_skill_id`，finalize
`state='committed'`，写入 `activation_outcome`（CAS 成功 → `'activated'`，版本冲突 → `'activation_conflict'`），
并按 `Skill.version` CAS 推进 `current_revision_id`。CAS 冲突时 revision 仍有效、指针不动、ingestion 仍
`committed`，activation 结果 durable 记录供 replay。

**Migration 要求。** 不得修改 `0015_skills.sql` —— 且确实未修改。设计新增了**一个新的 forward migration**
（`0016_skill_ingestion_idempotency.sql`，已落地），含四个 additive 变更：
`ALTER TABLE skill_ingestions ADD COLUMN canonical_name text`（nullable，带
`IS NULL OR length(...) BETWEEN 1 AND 200` CHECK，使 Step 1A 未设置该列的 ingestion 测试仍通过）；
`ALTER TABLE skill_ingestions ADD COLUMN request_fingerprint text`（`IS NULL OR length(...) BETWEEN 1 AND
128` CHECK）；
`ALTER TABLE skill_ingestions ADD COLUMN activation_outcome text`（`IS NULL OR activation_outcome IN
('activated','activation_conflict')` CHECK）；以及
`CREATE UNIQUE INDEX skill_ingestion_candidate_uniq ON skill_ingestions(workspace_id, idempotency_key,
canonical_name)`。不新增 `package_digest` / `version` / `size_bytes` / `file_count` 列，也不新增
`display_name`/`summary` 列（它们被编码进 `request_fingerprint`）。

**实现验收门（Step 2C 实现全部成立才算完成）：**

- [x] 每个 candidate 有独立、durable、可重放的 `skill_ingestions` 身份（`workspace_id, idempotency_key,
      canonical_name` 唯一）；
- [x] 语义请求指纹 = `(target_skill_id, display_name, summary, content_digest, package_digest)`；same key 但
      fingerprint 不同的重提交判为 conflict，而非 replay；
- [x] 崩溃恢复从同 key 重提交**确定性重建** canonical package bytes 并对照 durable identity（`object_locator`
      + `expected_digest` + `request_fingerprint`）验证——不存在重读 ephemeral upload 或进程内 byte slice 的
      路径；
- [x] TX #1 与 PUT 之间的崩溃按 probe 收敛：`ABSENT` ⇒ PUT（重建 bytes），`MATCHING` ⇒ adopt；
- [x] PUT 成功但 TX #2 之前崩溃 adopt 已存在对象，不重新 PUT；
- [x] 任何 Object Storage / filesystem / HTTP / process 工作都不发生在 DB 事务、行锁或 advisory lock 之内
      （source 读取/archive 解析/discovery 在事务外；`skillpkg.Build` 是纯 CPU）；
- [x] `MISMATCH` fail closed（不创建 revision、`current_revision_id` 不变、稳定错误码）；
- [x] activation 用 `Skill.version` CAS；冲突时保留 immutable revision 与未动的指针，durable 写入
      `activation_outcome='activation_conflict'`，replay 不重试 CAS（新业务尝试用新 `Idempotency-Key`）；
- [x] `planned`/`storing` 的 ingestion 永不自主推进（无 worker）——只能由同一 `Idempotency-Key` 重提交 resume；
- [x] `0016_skill_ingestion_idempotency.sql` 已落地（四个变更全部）；`0015_skills.sql` 与 Step 1A 逐字节一致。

### Step 2C.1 Source intake & candidate discovery —— 已实现

状态：**已实现**（`internal/skillsource` + `core.Store.IngestSource` 接缝）。阻塞 Step 2C.1 的四个
source-intake 语义已由 `specs/decisions/cloud/skills/20260927-source-intake-candidate-discovery.md`
（状态 `proposed`，已批准实施）落地为代码；未加任何 migration。

- [x] **archive 支持集** = 仅 ZIP + 未压缩 TAR（`.zip` / `.tar`）；`.tar.gz` / `.tgz` / gzip 不支持（无
      sniffing、无 parser fallback）。archive 编码由 caller 显式 `SourceKind ∈ {zip, tar}` 决定——绝不按
      filename/MIME/sniffing 推断。
- [x] **candidate 根之外的游离文件**：先过 source-level 安全校验，再对 candidate construction 忽略：
      safe 但游离 → 忽略；unsafe 游离 → 拒绝整个 source。source root 自身有 `SKILL.md` 时即单一 root
      candidate（owns 整棵树）。
- [x] **nested candidate root** = source-level structural error（`ErrNestedRoot`）：整个 source 在任何
      candidate 进入 saga 前被拒绝。nested 判定按 canonical root path 的 component（`a`/`a/b` 是 nested；
      `a`/`ab` 不是）。
- [x] **同一 source 内 duplicate canonical_name** = source-level structural conflict（`ErrDuplicateName`）：
      整个 source 在 TX #1 前被拒绝——绝不 first/last wins，绝不让 DB `UNIQUE` 决定。
- [x] **失败分层**：source-level structural failure → zero `SkillIngestion` 行；invalid `SKILL.md` metadata
      → candidate-local `PreparationFailure{CandidateRoot, ErrorCode, Detail}`（无 ingestion 行、sibling
      继续、不构造 synthetic `canonical_name`），由 `PreparedSourceResult{Candidates[],
      PreparationFailures[]}` 承载；只有确定的 `PreparedCandidate[]` 才经 `core.Store.IngestSource` 进入
      现有 `IngestSkills` saga（partial-success 不变）。
- [x] **SourceType**：directory → `"directory"`，zip/tar → `"archive"`；同一逻辑树的 directory / ZIP / TAR
      在 candidate path、bytes、`canonical_name`、manifest、content digest、package bytes、package digest
      上完全收敛。
- [x] **schema 影响**：NONE —— `0015_skills.sql` 与 `0016_skill_ingestion_idempotency.sql` 均不变，无 `0017`。
- [ ] public upload HTTP API / 生产 Object Storage provider / 前端 / RetrievalCapability / Node 交付 /
      Agent-Execution binding 仍未实现（后续阶段）。

### Step 3A Public upload API contract —— 契约已冻结（仅设计）

状态：**API 契约已冻结**；实现**尚未开始**。由
`specs/decisions/cloud/skills/20260927-public-upload-api-contract.md`（状态 `proposed`，仅设计）冻结。

- [x] **endpoint**：`POST /api/v1/tenants/:tid/spaces/:spaceId/skills/imports` —— 按仓库约定 tenant-prefixed、
      space-scoped（`spaceId` 即 saga 的 `workspace_id` = `collab_workspaces.id`）；**不是**无 tenant 前缀的
      `/api/v1/spaces/{workspace_id}/...` 形式。
- [x] **auth**：复用 Collaboration Workspace 双凭证 + `workspaceRole`；import / 显式 update 需 space
      owner/admin（`403 workspace_admin_required`）；跨 workspace `target_skill_id` 与 space 非成员读取 →
      `404`（无泄漏）。
- [x] **transport**：`multipart/form-data` 单 archive part。`source_kind ∈ {zip, tar}`（显式，无
      filename/MIME/sniffing/fallback）；`directory` 不是 public wire kind——客户端本地目录先打包为 zip/tar
      再上传（canonical content 相同；`KindDirectory`/`PrepareDirectory` 仍是服务器端摄取面）。
- [x] **request 字段**：`source_kind` + `source`（archive bytes）+ 可选 `target_skill_id`（显式 update，
      要求单 candidate source）+ 可选 `display_name`/`summary`（新建 Skill 元数据）。禁止 client 传
      `canonical_name`/`content_digest`/`package_digest`/`object_locator`/`revision_id`。
- [x] **Idempotency-Key**：必填 header，语义 = saga 幂等 namespace `(workspace_id, idempotency_key,
      canonical_name)` + `request_fingerprint`；upload endpoint **不**写通用 `idempotency_records` 行（二进制
      body + continuation 语义）。
- [x] **response**：`200` envelope `{sourceKind, preparationFailures[], ingestions[]}`。source structural
      failure → `400` + `source_*`（zero ingestion rows）；candidate preparation failure → per-item 无任何
      Skill identity；candidate saga failure / `activation_conflict` → per-item 在 `200` envelope 内表达。
- [x] **partial success**：source 结构有效即 overall `200`；仅 catastrophic DB 失败 → `500`。
- [x] **replay / continuation**：same key + same fingerprint → durable per-candidate 结果（`replayed`）或
      `planned`/`storing` resume（client-driven，saga D14）；same key + different fingerprint → `409`。
- [x] **limits**：上传预算 ≤ 2 GiB（provisional 产品上限 256 MiB）；archive/entry/expanded/candidate 上限继承
      `skillsource.Limits`；per-file/path 上限继承 `skillpkg.Limits`；buffering 是 ephemeral-only（无 durable
      staging）。超预算 → `413 upload_too_large`。
- [x] **schema 影响**：NONE —— `0015_skills.sql` / `0016_skill_ingestion_idempotency.sql` 不变，无 `0017`。
- [x] 实现（multipart transport + router/gateway body 预算协调 + contract/OpenAPI/frontend 同步，见 ADR
      D17）已完成——见下方 Step 3B。

### Step 3B Public upload API —— 已实现

状态：**已实现**。Step 3A 冻结的契约端到端落地：multipart transport、route-local body 预算、双凭证授权、
saga 幂等、`200` envelope、contract/OpenAPI/frontend 同步——**无 schema 变更**。

- [x] **route + transport**：`POST /api/v1/tenants/:tid/spaces/:spaceId/skills/imports` 登记进 `router.Routes()`
      并由专用 `uploadSkillSource` 处理（`internal/api/router/skill_upload.go`）；`multipart/form-data` 单 `source`
      file part + 严格文本字段白名单（`source_kind`/`target_skill_id`/`display_name`/`summary`）。未知或重复字段
      ——包括禁传的 `canonical_name`/`content_digest`/`package_digest`/`object_locator`/`revision_id`——被拒
      （`unknown_field`/`duplicate_field`）；`source_kind` 显式并映射到 `skillsource.SourceKind`，绝不
      filename/MIME/sniffed。
- [x] **body 预算**：route-local 256 MiB 上限（`skillsUploadBudget`，超限 `413 upload_too_large`），
      ephemeral-only（无 durable staging，绝不把超限 body 整段读入内存）。**其余所有路由保持 64 KiB** JSON 上限；
      gateway 路由感知（`requestBodyLimit`/`isSkillsUploadPath` 仅豁免 8 段 `skills/imports` 形态），全局
      `maxBodyBytes` 绝不调大。
- [x] **授权**：双凭证（gateway service role + `X-Ora-User-Token`，`user.Caller == service.Subject`）→
      `store.ResolveIdentity` → `core.Store.IngestSource`。whole-request 校验零副作用快速失败：非成员 → `404
      not_found`；成员但非 owner/admin → `403 workspace_admin_required`；`target_skill_id` 跨 workspace/不存在 →
      `404 not_found`（无泄漏）；`target_skill_id` 且多 candidate → `400 single_candidate_required`。这些都是
      HTTP 级拒绝，绝不进 `200` envelope 的 per-item `errorCode`。
- [x] **幂等**：`Idempotency-Key` 必填；语义 = saga namespace。same key + same fingerprint → replay /
      continuation；same key + different fingerprint → `409 idempotency_conflict`。`IngestSkills` 现上抛
      `*Fault`（auth/conflict/scope），不再折入 per-candidate `errorCode`。
- [x] **envelope**：`200 {sourceKind, preparationFailures[], ingestions[]}`；source structural failure → `400
      source_*`（零行）；partial success → overall `200`；`activation_conflict` 仍是 `activation` 字段，绝不作为
      `errorCode`。
- [x] **contract 同步**：`internal/contract/openapi.go`（`SourceUploadResult`/`SourcePreparationFailure`/
      `SourceIngestionItem` schema、multipart request body、`413` 描述）；`api/openapi.json` 经 `go run
      ./cmd/openapi` 重生成并由 `TestPublishedOpenAPIIsValidAndCurrent` 把关；frontend 经 orval 重生成
      `sourceKind`/`preparationFailures`/`ingestions` 类型——无看板 UI。
- [x] **schema 影响**：NONE —— `0015_skills.sql` / `0016_skill_ingestion_idempotency.sql` 不变，无 `0017`。
- [x] **测试**：router multipart 严格性/body 预算单测（`internal/api/router/skill_upload_test.go`）；gateway
      body-limit 钉死单测（`internal/gateway/bodylimit_test.go`）；E2E `integration/skill_upload_test.go`（zip/tar
      happy、replay、`409`、member `403` / non-member `404` / cross-workspace `404`、`single_candidate_required`、
      partial-success 映射、fatal source 零副作用、>64 KiB 接受）。
- [ ] 生产 Object Storage provider / `RetrievalCapability` / Node 交付 / Skills 看板 UI / AgentSkillBinding /
      ExecutionSkillBinding 仍未实现（后续阶段）。

### Step 4A 生产 Object Storage provider —— 已冻结（仅设计）

状态：**provider 契约已冻结；生产 provider 实现 NOT started**。生产 Object Storage 缺口由 ADR
`specs/decisions/cloud/skills/20260927-production-object-storage-provider.md`（状态 `proposed`）在设计层关闭。
审计发现仓库**没有任何**既有权威对象存储 provider 或部署约定（`go.mod` 无 S3/AWS/MinIO/GCS/Azure SDK；
`compose.yaml`/CI 只有 PostgreSQL；无 Helm/K8s；`config` 无 storage 段；`scripts/`/`Learn/`/`configs/`/
`Taskfile.yml` 无对象存储引用），因此新选一个 V1 provider 契约——**单一 S3-compatible provider，不默认 AWS**，
唯一生产实现（`aws-sdk-go-v2` + `service/s3`，依赖仅在实现切片引入）。

- [x] **provider**：单一 S3-compatible 契约，经自定义 `endpoint` + path-style；唯一生产 adapter。
- [x] **`PutImmutable` create-only**：原生条件 `PutObject(IfNoneMatch="*")` → `412` = `PutAlreadyExists`；
      禁止 HEAD-then-unconditional-PUT（TOCTOU）；无条件写入则 fail closed。
- [x] **multipart**：V1 用**单次原子 PUT**，不启用 multipart（保住 create-only 原子性；单 PUT 5 GiB 上限
      ≫ `MaxPackageBytes` ≈1 GiB 与 256 MiB 产品上限）。
- [x] **内存**：当前 `ObjectStore` port 保持 `[]byte` 全缓冲；单次 PUT 写入同一份 bytes、无放大——256 MiB
      不构成操作不安全。
- [x] **错误映射/taxonomy**：200→`PutCreated`；412→`PutAlreadyExists`；400/403/size-reject→
      `PutDefiniteFailurePermanent`；DNS/连接被拒/429→`PutDefiniteFailureTransient`；5xx/发送后超时→
      `PutAmbiguous`。冻结类别：not_found / already_exists / temporary / throttled / unauthorized / forbidden /
      invalid_configuration / integrity_mismatch / ambiguous / permanent_failure；原始 SDK 错误绝不泄漏到领域层。
- [x] **超时**：connect 5s / request 30s；每次调用携带 caller context；禁止无 deadline 的
      `context.Background()`；无后台 retry worker。
- [x] **配置**：新增 `storage` 段（provider/bucket/region/endpoint/path_style/credential_mode/credentials_file/
      tls.verify/ca_file/allow_insecure_http/timeouts）。bucket 单一配置；桶名不进 durable state / 请求 /
      workspace 设置。
- [x] **凭证**：仅部署平台机制（环境 / workload identity / 共享凭证文件）；禁止进 DB / `SkillRevision` /
      API 响应 / 日志。
- [x] **TLS**：生产 HTTPS + 证书校验（`tls.verify` 默认 true）；`allow_insecure_http` 默认 false（仅显式
      dev/test `http://minio`）。
- [x] **integrity**：`SHA256 == package_digest` + `skillpkg.Decode` + `TreeDigestHex == content_digest`；
      provider ETag 只是 evidence，永不作为权威。
- [x] **启动行为**：`storage` 存在但非法 → fail fast；缺失 → 进程启动、`SkillsObjectStore` nil、saga 返回
      `object_store_unavailable`（已实现）；不新增远程 bucket 启动探活。
- [x] **schema 影响**：NONE —— `0015_skills.sql` / `0016_skill_ingestion_idempotency.sql` 不变。
- [ ] 生产 provider **实现**（adapter + SDK 依赖 + `cmd/server` 装配 + `CLOUD_STORAGE_*` 绑定 + 启动校验）
      NOT started（后续切片）。

### Step 4B 生产 Object Storage provider —— 已实现

状态：**已实现并通过门禁；所有改动保持 uncommitted**。Step 4A 的契约落地为生产 adapter——新包
`internal/skillstore/s3store`、`internal/config` 的 `storage` 段、`cmd/server` 装配——**不改变** `ObjectStore`
port、schema、摄取 saga 语义。

- [x] **adapter**（`internal/skillstore/s3store`）：用 `aws-sdk-go-v2/service/s3` 实现 `skillstore.ObjectStore`；
      最小未导出 `api` seam（`*s3.Client` 满足之）让测试无需真实端点。`PutImmutable` 只发一次 `PutObject` 且
      `IfNoneMatch: "*"`（无 HEAD-then-PUT）；`Stat` = HEAD；`Get` = 有界读 + 1 字节 oversize 探测（绝不无界
      `io.ReadAll`）。
- [x] **错误分类**：`classify` 经 `*smithyhttp.ResponseError` 取 HTTP 状态（每个非 2xx 都能通过
      `*smithy.OperationError` 到达），再按 DNS / `dial` 被拒 → transient、建立后中断 / deadline / cancel →
      ambiguous、未知 → ambiguous。404→absent、412→already-exists、429→transient、5xx→ambiguous、其余 4xx→
      permanent。原始 SDK 错误绝不跨 port。
- [x] **超时/传输**：connect 5s / request 30s（冻结默认，可配）；每个操作从 caller context 派生 deadline；
      transport 为 `http.DefaultTransport.Clone()`（不改全局）；`RetryMaxAttempts: 3`（仅有界传输重试）。
- [x] **凭证**：`environment`（`AWS_*` 静态）/ `shared_credentials_file` / `workload_identity`（默认链），
      在 adapter 内解析，永不持久化、不进日志。
- [x] **配置**（`internal/config` `storage` 段 + `CLOUD_STORAGE_*`）：指针 `StorageConfig`（nil = 缺失 →
      `SkillsObjectStore` nil）；`applyDefaults`（credential_mode→environment、tls.verify→true、timeouts→5s/30s）
      后 `Validate`（provider=="s3"、bucket/region 必填、endpoint scheme 与 http-vs-allow_insecure_http、
      credential_mode 白名单、credentials_file 必填、verify=false ⊥ ca_file、timeouts ≥0）。`configs/config.yaml`
      带注释样例。
- [x] **装配**：`cmd/server.wireObjectStore` 把 `storage` 翻译为 `s3store.Config` 并赋值
      `store.SkillsObjectStore`；存在但非法 fail fast，缺失则 nil（`object_store_unavailable` 路径不变）。
- [x] **测试**：`s3store_test.go`（fake-`api` outcome 矩阵、`If-None-Match: *`/body 透传、有界 Get、`New`
      校验）+ 真实 SDK `httptest` 端到端（条件 PUT、412→`PutAlreadyExists`）；`internal/config/storage_test.go`
      （缺失即 nil、默认值、全量解析、非法矩阵、http-with-allow、`CLOUD_STORAGE_BUCKET` 环境变量）。门禁：
      `go build`/`go vet`/`go test ./internal/... ./cmd/...`/`golangci-lint`/`git diff --check` 全部通过（仅
      `cmd/devsetup` 在 Windows 的文件权限既有失败，与本次无关）。
- [ ] **未做**：`RetrievalCapability`、Node 交付、Skills 看板、Agent/Execution 绑定、GC/retention、MinIO dev
      fixture、真实 provider CI —— 均属后续切片。

## Phase 4 — Public API / Skills board backend

- Workspace-scoped CRUD/import；
- revision activate/update；
- Agent Skill assignment；
- idempotency/version；
- generated contract/OpenAPI/client。

### Step 4A Agent/AgentSkillBinding 契约 —— 已冻结（仅设计）

状态：**契约已冻结；实现 NOT started；所有改动保持 uncommitted**。最小 durable `Agent` + `AgentSkillBinding`
权威模型由 `specs/decisions/cloud/agent/0-agent-skill-binding.md`（`proposed`）闭合。**不写实现代码、无 schema 变更。**
Execution snapshot ADR `specs/decisions/cloud/skills/20260927-execution-skill-snapshot.md` 已修订为只消费 durable
`AgentSkillBinding` —— 删除其临时 explicit-skill-id admission fallback，caller-supplied `skill_id[]` 被禁止。

- [x] **Agent ownership**：durable `agents` 资源属于一个 Collaboration Workspace（`collab_workspaces`），
      绝不属于 Runtime `workspaces`/`projects`。
- [x] **AgentSkillBinding**：引用 `skill_id`（Skill identity），不引用 `skill_revision_id`/digest/locator；
      `PRIMARY KEY(agent_id, skill_id)` = 至多一条 binding。
- [x] **mutation 语义**：add/enable/disable/remove + 乐观 version（428/409）+ Idempotency-Key。
- [x] **soft-delete**：历史 `ExecutionSkillBinding` 保持有效；future admission 排除 soft-deleted Skill；
      soft-deleted Agent 拒绝新 execution。
- [x] **snapshot authority**：Execution request 只携带 `agent_id`，绝不携带 Skill IDs；admission 只读 durable
      `AgentSkillBinding`。
- [x] **授权**：复用 `collab_workspace_members` 角色（owner/admin 可写；member 可读；非 member 404）。
- [x] **ActorRef**：`agents.id` 映射 `ActorRef{agent}`；taxonomy 不变；`CollaborationDirectory` 保持未接线。
- [x] **schema 影响**：仅设计（`agents` + `agent_skill_bindings` forward migration）；未写 migration。
- [x] **实现**（Agent CRUD/binding API、migration）—— 由下方 Step 4B 交付。

### Step 4B Agent & AgentSkillBinding —— 已实现

状态：**已实现**。`specs/decisions/cloud/agent/0-agent-skill-binding.md` 冻结的最小 durable `Agent` +
`AgentSkillBinding` 权威模型现已落地为代码：forward migration `0017_agents_and_skill_bindings.sql`、
`internal/core/agents.go`，以及 workspace-scoped 公共 API + 生成的契约。它**只**把 durable `AgentSkillBinding`
作为 Execution selection authority（ADR D8）；不实现任何 Execution/Attempt/ExecutionSkillBinding/capability/locator
字段（ADR D13/D14）。

**repo-consistent API 契约**（按 plan §13 记录；endpoint path 沿用现有 `/tenants/:tid/spaces/:spaceId/...`
workspace-scoped 约定，与 `0015`/`spaces` 一致）：

| Method | Path（`/api/v1/tenants/:tid/spaces/:spaceId` 之下） | Body 字段 | 授权 | 幂等 |
| --- | --- | --- | --- | --- |
| GET | `/agents` | — | member+ | — |
| POST | `/agents` | `name` | owner/admin | Idempotency-Key |
| GET | `/agents/:agentId` | — | member+ | — |
| PATCH | `/agents/:agentId` | `name`,`status`,`version` | owner/admin | version 428/409 |
| DELETE | `/agents/:agentId` | `version` | creator 或 owner/admin | Idempotency-Key + version |
| GET | `/agents/:agentId/skills` | — | member+ | — |
| POST | `/agents/:agentId/skills` | `skillId` | owner/admin | Idempotency-Key |
| PUT | `/agents/:agentId/skills/:skillId` | `enabled`,`version` | owner/admin | version 428/409 |
| DELETE | `/agents/:agentId/skills/:skillId` | — | owner/admin | Idempotency-Key（幂等 detach） |

授权复用 `workspaceRole`（`internal/core/space_permission.go`）：非 member → `404 not_found`（无泄漏，ADR D11）；
member 但非 admin 写 → `403 workspace_admin_required`；删除用 `workspaceCanDelete`（creator 或 owner/admin，ADR D11）。
attach 强制同 workspace（`agent.workspace_id == skill.workspace_id`）且未 soft-delete（ADR D4）；允许绑到尚无
`current_revision_id` 的 Skill（意图 vs 可用性，ADR D4），失败点延后到 Execution admission
（`skill_revision_not_available`，本阶段不实现）。

binding mutation 语义（ADR D5）：attach INSERT `enabled=true`（重复 `(agent_id,skill_id)` → `409 binding_exists`；
同 idempotency key 回放）；enable/disable = `PUT {enabled,version}`（缺 `428`、不匹配 `409`）；detach = DELETE（不存在
→ `404 binding_not_found`；同 idempotency key 回放）。soft-delete 保留 binding 行；下方 selection helper 排除
soft-deleted Skill 与 disabled binding。

- [x] **Migration** `0017_agents_and_skill_bindings.sql`：`agents`（workspace-owned、`name` 1..128、
      `status active|disabled`、`version`、soft `deleted_at`、`UNIQUE(workspace_id,name) WHERE deleted_at IS NULL`、
      immutable-ownership trigger）+ `agent_skill_bindings`（`PRIMARY KEY(agent_id,skill_id)`、`enabled`、`version`、
      无 `deleted_at`）。`0015_skills.sql`/`0016` 不变。
- [x] **Core** `internal/core/agents.go`：Agent 的 create/read/list/patch/archive；binding 的
      list/attach/enable-disable/detach；外加 `enabledAgentSkillBindings` 读 seam（enabled binding join 其 live
      Skill，按 `canonical_name` 再 `skill_id` 确定性排序）—— 未来 Execution admission 会调用它，它**不**解析
      revision/digest（ADR D8/D10/D13；非 snapshot）。
- [x] **无 execution surface**：Agent 表与 API 中无 `Execution`/`Attempt`/`ExecutionSkillBinding`、无
      capability/signed-URL/object locator、无 caller-supplied `skill_ids`（ADR D8/D13/D14）。
- [ ] RetrievalCapability / Execution snapshot / Node 交付 / Agent UI 仍 NOT implemented（后续阶段）。

Step 5B 仍 BLOCKED：Execution snapshot（Phase 5）现已具备其 durable `AgentSkillBinding` selection authority，
但它本身尚未实现。

## Phase 5 — Execution snapshot

> 状态：**BLOCKED** —— 依赖 durable `AgentSkillBinding`（Phase 4A 契约已冻结；Phase 4B 实现已落地 ——
> `enabledAgentSkillBindings` 读 seam 即 selection authority）。契约是
> `specs/decisions/cloud/skills/20260927-execution-skill-snapshot.md`（`proposed`），已修订为只消费 durable
> `AgentSkillBinding`。

- Execution 创建时解析 enabled Agent Skills；
- exact revision binding；
- immutable dispatch descriptor；
- retry/new Attempt semantics。

## Phase 6 — Retrieval capability

### Step 5A RetrievalCapability 契约 —— 已冻结（仅设计）

状态：**契约已冻结；实现 NOT started；所有改动保持 uncommitted**。RetrievalCapability 契约由
`specs/decisions/controller/skill-delivery/0-skill-retrieval-capability.md`（`proposed`）及其 Node 侧一致性契约
`specs/decisions/node/agent-runtime/0-skill-materialization-and-readiness.md`（`proposed`）闭合，镜像测试用例在
`specs/test-cases/controller/skill-delivery/` 与 `specs/test-cases/node/agent-runtime/`。Step 5A 将这两份 ADR（此前
登记在 `cloud/skills/`）迁到规范化的 Controller/Node 叶子域，并闭合遗留开放问题。**不写实现代码、无 schema 变更。**

- [x] **授权依据**：仅对已冻结进 Execution `ExecutionSkillBinding` 的 revision 签发 capability；claim/dispatch
      时绝不重新解析 `Skill.current_revision_id`。
- [x] **范围**：单一 immutable object，仅 GET；无 bucket/prefix/workspace 级或任意 key 访问。
- [x] **表示**：short-lived signed HTTPS GET URL（bearer credential，vendor-neutral，不叫 S3 名）。
- [x] **TTL**：默认 300s，硬上限 900s；`expires_at` 显式下发；覆盖 dispatch + Node 调度 + 获取 + 时钟偏差 +
      1–2 次 refresh-retry 循环。
- [x] **refresh**：同一 Execution + Attempt + 冻结 revision → 新 capability；变的是 credential，不是
      revision/locator/binding。
- [x] **持久化/日志**：capability 永不成为 durable 业务状态，也永不落日志（redaction）。
- [x] **Controller**：协调 + 转发，不代理 bytes；data plane 是 Node→Object Storage 直连。
- [x] **Provider 边界**：`ObjectStore` port 不变；新增 `RetrievalCapabilityIssuer` 式边界；`object_locator`
      保持逻辑 key，绝不成为 URL。
- [x] **Fencing/撤销**：fencing 不吊销已签发的 bearer URL；V1 撤销 = credential 过期（TTL 即暴露窗口）；
      不承诺即时撤销。
- [x] **失败分类**：storage_not_configured / revision_not_bound / attempt_not_eligible /
      object_not_available / signing_failed / invalid_locator / expired_or_retry_required /
      authorization_failed / temporary_control_plane_failure。
- [x] **缺失对象**：签发前不 Stat/HEAD；Node 404 → `object_not_available`，fail closed。
- [x] **schema 影响**：NONE。
- [ ] **实现**（capability mint/refresh、Node downloader/cache、READY barrier）NOT started（后续切片）。

### 实现（后续切片）

- capability mint/refresh；
- short-lived exact-object read authorization；
- secret redaction/non-persistence；
- Controller/Node transport contract。

## Phase 7 — Node verified cache

- retrieval；
- staging；
- digest/size verify；
- atomic publish；
- concurrency；
- bounded GC。

## Phase 8 — Agent runtime projection

- AgentRuntimeAdapter；
- per-Attempt projection staging；
- atomic publish；
- ownership/preservation；
- READY barrier；
- spawn gate。

## Phase 9 — Frontend

- Skills board；
- directory/archive import UX；
- batch summary；
- Skill metadata/revision display；
- 本 wave 范围内 Agent assignment UI。

## Phase 10 — End-to-end audit

- full gates；
- core-test evidence；
- OpenAPI/client drift；
- 两个 Git repo status/diff；
- secret scan/log review；
- plan acceptance audit。

可以拆成多个 commit，但每个阶段都必须保持 coherent、可解释、语义正确。

---

# 35. STOP 条件

出现以下情况时，实施 Agent 必须停止并报告：

- approved ADR 与 Workspace ownership 冲突；
- 当前 execution model 无法表达 immutable SkillRevision input，且需要超出本计划的语义变化；
- Agent persistence model 与假设的 integration point 存在重大差异；
- signed capability 无法在不违反尚未更新的 approved protocol 的情况下传输；
- Object Store client 缺少安全 stable-identity reconciliation；
- Runtime/Agent discovery 必须以非确定方式修改 canonical Skill package bytes；
- 当前 Node lifecycle 无法在不做更大架构变更的情况下保证 READY-before-spawn；
- schema naming/ownership 与已经存在的一等 Skills model 冲突；
- 任何实现需要在 DB transaction 中执行 filesystem/HTTP/Object Storage；
- 任何 shortcut 会削弱 auth、idempotency、version、fencing、migration check 或 test gate。

---

# 36. 验收标准

只有全部适用项满足，才算完成。

## Domain

- [ ] 每个 Skill 唯一属于一个 Collaboration Workspace。
- [ ] 同 Workspace active canonical Skill name 唯一。
- [ ] Skill metadata 可变，SkillRevision 内容不可变。
- [ ] Same Skill + same canonical digest 复用一个 SkillRevision。
- [ ] 不同 Skill 即使内容相同，也保持独立业务 identity。
- [ ] Skill soft delete 后已有 execution snapshot 仍稳定。

## Ingestion

- [ ] Directory 与 archive 进入同一 canonical virtual-tree pipeline。
- [ ] Recursive Skill discovery 正确。
- [ ] Nested Skill root 被拒绝。
- [ ] Unsafe path / special node / symlink 被拒绝。
- [ ] Binary supporting file 被完整保留。
- [ ] Canonical digest 不受 transport/archive metadata 影响。
- [ ] Batch import 为 partial success。
- [ ] 每个 candidate 都有独立 durable ingestion evidence。
- [ ] Object Storage work 在 DB transaction 外。
- [ ] Ambiguous external outcome 按 stable identity reconcile。

## Execution

- [ ] AgentSkillBinding 是 mutable future-execution configuration。
- [ ] Execution 创建时解析 exact immutable SkillRevision。
- [ ] ExecutionSkillBinding immutable。
- [ ] dispatch/claim 不从 mutable Agent config 重新计算 Skills。
- [ ] retry 在同一 Execution 下创建新 Attempt。
- [ ] retry 保持完全相同的 SkillRevision binding。
- [ ] Run Again/new Execution 重新读取当前配置。

## Retrieval / security

- [ ] Node 接收 abstract short-lived retrieval capability，而非 long-lived storage credential。
- [ ] signed capability handling 已在 specs/ADR 中显式批准。
- [ ] capability 不持久化、不日志。
- [ ] refresh 只能为同一个 bound revision 重新 mint。
- [ ] Node 独立验证 size 与 digest。
- [ ] Controller/Cloud 不代理正常 Skill package bytes。

## Node

- [ ] Verified cache digest-addressed、immutable、atomic、concurrency-safe、disposable、bounded。
- [ ] Agent 不得修改 shared cache。
- [ ] Projection 为 Attempt-scoped。
- [ ] Projection staging 后 atomic publish。
- [ ] partial projection 不对 Agent 可见。
- [ ] required Skill failure 阻止 spawn。
- [ ] READY barrier 先于 Agent spawn。
- [ ] crash recovery 采用 rebuild/revalidate，不恢复 partial filesystem mutation。
- [ ] Agent-specific discovery 逻辑位于 AgentRuntimeAdapter。
- [ ] materialization 不执行 Skill package 内容。

## Compatibility / quality

- [ ] 相关 specs ADR 与 core tests 同步。
- [ ] 未修改 applied migration。
- [ ] fresh DB migration test 通过。
- [ ] upgrade migration test 通过。
- [ ] OpenAPI/frontend generated artifact 同步（适用时）。
- [ ] narrow test 通过。
- [ ] `task format` 通过。
- [ ] 行为/仓库级变化时 `task check` 通过。
- [ ] concurrency/recovery/cache/persistence 改动时 `task test:race` 通过。
- [ ] command/startup 改动时 `task build` 通过。
- [ ] frontend 改动时 frontend gates 通过。
- [ ] `git diff --check` 通过。
- [ ] complete diff 已 review。
- [ ] root repo 与 `specs/` repo status 已检查。
- [ ] 日志、fixture、snapshot、commit file 中无 secret/capability 泄漏。
- [ ] 未覆盖无关用户改动。

---

# 37. 最终实施报告格式

实施 Agent 完成后必须输出：

```text
## Plan Audit

### Implemented
- <plan section> — PASS — <files/tests/evidence>

### Not Applicable
- <plan section> — N/A — <reason>

### Deviations
- NONE
```

如有 deviation：

```text
### Deviations
- <plan section>
  Planned:
  Implemented:
  Reason:
  Approval/reference:
```

未经批准的语义偏离视为失败，不属于“实现选择”。

还必须报告：

- 新增 migrations；
- ADR/spec 变化；
- API/contract 变化；
- 新增/更新 tests；
- 执行过的命令/gate 及结果；
- root `git status --short`；
- `git -C specs status --short`；
- 已知 residual risk / deferred non-goal。
