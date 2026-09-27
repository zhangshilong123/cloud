# internal/skillstore: Object Storage 抽象（port / 语义类型 / reconciliation core）

[中文](README.md) | [English](README.en.md)

`internal/skillstore` 是 Cloud Skill canonical package 的 **provider-neutral Object Storage 语义层**：
它把 ADR 冻结的 immutable create-only write、稳定物理 identity、模糊结果分类与按 `object_locator`
probe 的 reconciliation 表达为可直接测试的代码契约。它**不**实现任何具体云厂商 SDK、HTTP 上传、摄取
管线、`SKILL.md` 发现或 `RetrievalCapability`。规范契约见
`specs/decisions/cloud/skills/20260927-object-storage-abstraction.md`。

## 职责

- **物理 identity**：`PackageDigestHex(b) = SHA256(exact ora-skill-package bytes)`（lowercase 64-hex），
  即 `package_digest`（D4），与 Step 2A tree content digest 严格区分。
- **稳定逻辑 key**：`Locator` 编码 `skills/<package_format>/v<package_format_version>/<digest_algorithm>/<package_digest_hex>`
  （provider-independent、business-identity-free、≤ 277 bytes < 1024 schema 上界）；`NewLocator` /
  `ParseLocator` 拒绝路径穿越、非 lower-hex、未知 format/algorithm。
- **ObjectStore port**：`PutImmutable`（create-only，绝不覆盖）、`Stat`（仅存在性，evidence 非 identity）、
  `Get`（有界，`maxBytes`），无 List/Delete/Copy/Move/Presign。
- **结果分类**：三操作各返回 typed outcome（`PutCreated`/`PutAlreadyExists`/
  `PutDefiniteFailureTransient`/`PutDefiniteFailurePermanent`/`PutAmbiguous`；`Stat…`；`Get…`），
  **业务调用者不能只靠 `err != nil` 判断是否 retry**。
- **Reconcile**：按 `want.Locator` probe（Stat → 有界 Get → 本地三项校验），返回统一 `Verdict`
  （`ConfirmedPresentMatching`/`ConfirmedAbsent`/`Mismatch`/`DefiniteFailure{Transient,Permanent}`/
  `Indeterminate`）；三项校验 = `SHA256(fetched)==package_digest` → `skillpkg.Decode` 成功 →
  `TreeDigest==content_digest`，任一失败即 `Mismatch`（fail closed）。

## 不变量与边界

- **create-only**：不 overwrite/upsert/replace；无 `Stat(); if absent: Put()`（TOCTOU）；provider 不支持
  条件创建即 unsupported，不降级正确性。
- **identity 三层分离**：`SkillRevision.id`（业务）、`digest_algorithm+content_digest`（tree 内容）、
  `package_digest`（物理字节）；`content_digest != package_digest`。
- **timeout 不是 absent**：`Stat` 的 timeout/could-not-check 绝不映射为 `StatAbsent`。
- **`MISMATCH` fail closed**：不覆盖/删除/改写/采用/改 key/重试 PUT。
- **provider-neutral 层不做重试/调度**：无 retry loop / worker / scheduler / DB 编排。
- 纯语义库：不碰数据库、具体 provider、HTTP handler、摄取 saga 或执行链路。

参见 [AGENTS.md](../../AGENTS.md)、[internal 模块总览](../README.md)、
`specs/decisions/cloud/skills/20260927-object-storage-abstraction.md`，以及测试替身
[fakestore](fakestore/README.md)。
