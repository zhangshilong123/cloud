# internal/skillsource: Source 摄取与 Candidate 发现

[中文](README.md) | [English](README.en.md)

`internal/skillsource` 是 Cloud Skills 的 **source intake 层**：把 directory / ZIP / uncompressed TAR 三种
transport 摄取为规范化的 `sourceEntry`，执行 source 级安全校验与 `SKILL.md` candidate 发现，产出喂给既有
`IngestSkills` saga 的 `PreparedCandidate`。它本身**不落库**——身份（`skillpkg`）、元数据（`skillmeta`）、
持久化（`skillstore` / `core`）都留在各自包内。语义契约见
`specs/decisions/cloud/skills/20260927-source-intake-candidate-discovery.md`。

## 职责

- **transport 适配**：`PrepareDirectory(root)`、`PrepareZip(data)`、`PrepareTar(data)` 三个入口，把三种
  输入读成同一份 `sourceEntry{path, data}` 流；archive 只支持 ZIP 与未压缩 TAR（`.zip` / `.tar`），
  `.tar.gz` / `.tgz` / gzip 一律不支持（无 sniffing、无 ZIP↔TAR 回退）。
- **source 级安全校验（fail-whole-source）**：canonical path（复用 `skillpkg.ValidatePath`）、精确重复
  path、大小写碰撞、archive 的 symlink/特殊条目、路径遍历 / 绝对路径 / Windows drive 前缀，以及各
  limits（archive bytes / entries / 解压后总 bytes / 单文件 bytes / path bytes）。
- **candidate 发现**：精确匹配根 `SKILL.md`（root candidate，owns 整棵树）或 `*/SKILL.md`
  （nested root）；候选按 canonical root bytes 排序，`partition` 切分候选内文件并施加 per-candidate
  `skillpkg.Limits`。
- **元数据派生**：对每个候选用 `skillmeta.Parse` 派生 `canonical_name` / `package_name` /
  `package_description`；解析失败产出 `PreparationFailure`（非 durable、不阻塞兄弟候选）。
- **SourceType**：directory → `"directory"`，zip/tar → `"archive"`（由 caller 提供的 `SourceKind` 决定，
  不做 filename/MIME 推断）。

## 结果模型

```go
type PreparedSourceResult struct {
    Candidates          []PreparedCandidate   // 有效候选，喂给 IngestSkills saga
    PreparationFailures []PreparationFailure  // 仅候选级元数据失败（非 durable）
}
```

`Prepare*` 要么返回 `PreparedSourceResult`，要么返回 source 级结构错误（`ErrUnsupportedKind` /
`ErrInvalidArchive` / `ErrUnsupportedType` / `ErrUnsafePath` / `ErrDuplicatePath` / `ErrCaseCollision` /
`ErrNestedRoot` / `ErrDuplicateName` / `ErrNoCandidates` / `ErrLimit`）。结构错误意味着**整份 source
被拒绝**：不产出任何 `PreparedCandidate`，也不产生任何 `skills` / `skill_revisions` /
`skill_ingestions` 行（`CodeOf(err)` 给出稳定 code）。

## 失败分层

- **A source 结构**（invalid archive / unsafe path / unsupported type / duplicate path / case collision /
  nested roots / duplicate canonical_name / no candidates / limits）→ 整份 source 拒绝，零行。
- **B 候选准备**（invalid `SKILL.md` 元数据）→ `PreparationFailure{CandidateRoot, ErrorCode, Detail}`，
  非 durable，兄弟候选继续。
- **C 候选业务**（authz / idempotency / storage / CAS）→ 既有 `IngestSkills` partial-success，不在此包。

## 依赖方向

`skillsource → skillpkg（path / limits）+ skillmeta（metadata）`，**不反向**；`core` 通过
`IngestSource` 消费 `PreparedSourceResult`。`skillsource` 不 import `core` / `skillstore`。

## 非目标

- 不执行任何 file content；不推断 SourceKind / MIME / 文件名；不解 gzip；不做 NFC/NFD normalization。
- 不做 name 与父目录一致性校验；hardlink 按普通文件读取（可移植 API 无法区分）。
- 不实现 public upload HTTP API、生产 Object Storage provider、Node delivery、Agent-Execution binding。

参见 [AGENTS.md](../../AGENTS.md)、[internal 模块总览](../README.md) 与
`specs/decisions/cloud/skills/0-cloud-skills.md`。
