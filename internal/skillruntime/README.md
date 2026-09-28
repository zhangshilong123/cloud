# internal/skillruntime: 服务端 Skill 获取 / 校验 / 已验证缓存 / Attempt 投影 / READY / Spawn 闸

[中文](README.md) | [English](README.en.md)

`internal/skillruntime` 是生产服务端 Skill materialization 的**运行时切片（6A.3 + 6A.4）**：把 ADR 冻结的
「消费 `RetrievalCapability` → 有界下载 → `package_digest` 校验 → 单一 codec decode → `content_digest`
校验 → 已验证 immutable cache → **Attempt-scoped projection → runtime/provider adapter → READY 屏障 →
spawn gate 抽象 → cleanup/bounded GC**」表达为可直接测试的代码。它是 6B dispatch 经
`Store.SkillMaterializer` + `Store.SkillProjector` 消费的 seam。规范契约见
`specs/decisions/cloud/skills/20260928-server-side-skill-retrieval-verification-cache.md` 与
`specs/decisions/cloud/skills/20260928-attempt-projection-runtime-adapter-ready-spawn-gate.md`。

## 职责（6A.3 — verified immutable cache）

- **输入**：`FrozenSkillBundle`（不可变交付元数据：`skill_revision_id`、`content_digest`、
  `package_digest`、`package_format`、`size_bytes`）+ 调用方注入的 `RetrievalCapability`（bearer）。
- **获取**：bounded fail-closed HTTPS GET（clone transport 不替换、`CheckRedirect` 不跟随 3xx、
  `readBounded` 有界读、过期预检、状态 → typed sentinel）。
- **校验（顺序固定）**：`package_digest`（`skillstore.PackageDigestHex` 字节身份）→ `skillpkg.Decode`
  （唯一 codec，verifier）→ `content_digest`（`TreeDigest`）→ `size_bytes`（内容总量）一致。
- **缓存**：identity `sha256/<content_digest>`；`<root>/.staging/` 同 fs staging + 原子 rename publish +
  `.ora-skill-complete` 标记（不含 capability）；命中零对象存储请求；corruption fail-closed。
- **输出**：`VerifiedSkillBundle`（`content_digest` + `digest_algorithm` + `CacheDir`），只读引用，是
  6A.4 projection 的 copy source。

## 职责（6A.4 — Attempt projection / READY / spawn gate）

- **Projection**（`projection.go`）：`Projector.Project(ctx, attemptID, []ProjectionSkill)` 从 verified
  cache **copy**（绝不可写 hardlink）整个 canonical 树到
  `<attempt_root>/attempts/<attempt_id>/<skill_revision_id>/`；确定性排序
  `canonical_name→skill_id→skill_revision_id`，目录名一律 `skill_revision_id`（不可变冻结身份）；先
  staging 再单次 `os.Rename` 原子发布，partial projection 永不 runtime 可见。产出 `PreparedAttempt`。
- **READY 屏障**（`projection.go`）：`Ready(ctx, PreparedAttempt)` 重读 `.ora-attempt-ready` ownership/
  READY marker 并逐个确认 required Skill 目录，绝不从“文件存在”推断；marker 缺失 → `ErrNotReady`、
  损坏/身份不符 → `ErrAttemptCorrupt`。产出 `ReadyAttempt`。
- **Runtime adapter**（`adapter.go`）：`AgentRuntimeAdapter`（`AttemptRoot` / `Provisions`）是 READY 状态到
  provider 布局的消费边界，只读不执行；默认 `FSAdapter`（provider-neutral）。
- **Spawn gate**（`gate.go`）：`SpawnGate.Open(ReadyAttempt) → LaunchSpec` 是 preparation→spawn 的类型级闸门，
  只接受 `ReadyAttempt`，本切片不 exec、不 byte-proxy、不设 daemon。
- **Cleanup / GC**（`cleanup.go`）：`CleanupAttempt` 幂等且只删自己拥有的投影；`CollectStaging` 清理
  staging 残留；`Materializer.Collect` 按 age/oldest-first count bounded 回收 cache（与 projection 独立）。

## 不变量与边界

- 全链 fail-closed：任一环失败即丢弃字节，不出现 partial cache / partial projection 可见。
- `package_digest`（字节层）与 `content_digest`（内容树）两条独立校验，互不替代。
- cache key = `digest_algorithm + content_digest`，**绝不含** `package_digest` 或任何 business 身份。
- 仅 fully-verified tree 原子 publish 后可见；corruption 不 re-download、不原地修改。
- Verified cache 与 Attempt projection 是两个独立概念：cache 永不被 projection 变异（copy 非 hardlink）。
- Projection 恰好归属一个 Attempt（key 只有 `attempt_id`）；retry = 新 Attempt、复用 cache。
- READY 是显式 barrier，绝不从 partial 文件系统状态推断；spawn 只能由 `ReadyAttempt` 打开。
- capability URL/signature 永不持久化/记录/返回/入错误（transport 错误被丢弃）。
- 不执行包内脚本/二进制；external IO 不在 DB 事务内；不 resolve 可变 Skill；无 `os/exec`/`net/http`/DB。
- 包只 import `internal/skillpkg` + `internal/skillstore`（不 import `internal/config`）；装配由
  `cmd/server` 从 `runtime` 配置节完成（`skill_cache_root` + `skill_attempt_root`）。

参见 [AGENTS.md](../../AGENTS.md)、[internal 模块总览](../README.md)、
`specs/decisions/cloud/skills/20260928-web-runtime-skill-materialization-ownership.md`、
`specs/decisions/cloud/skills/20260928-server-side-skill-retrieval-verification-cache.md` 与
`specs/decisions/cloud/skills/20260928-attempt-projection-runtime-adapter-ready-spawn-gate.md`。