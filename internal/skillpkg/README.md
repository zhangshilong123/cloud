# internal/skillpkg: Canonical Skill Package v1 内容层

[中文](README.md) | [English](README.en.md)

`internal/skillpkg` 是 Cloud Skill canonical package 契约（`ora-skill-package` v1）的纯内容实现：它把
Skill 的 canonical virtual file tree 确定性地映射为 ManifestV1、tree content digest 与
`ora-skill-package` v1 container bytes。Cloud ingestion 与未来 Node verification 共享这一份字节布局。
规范契约见 `specs/decisions/cloud/skills/20260924-canonical-skill-package-v1.md`。

## 职责

- **canonical path 校验**：reject-not-clean —— 绝对路径、`.`/`..`、空组件、NUL、Windows drive prefix、
  trailing `/` 均拒绝；source 边界的 `\` → `/` 映射后重新执行全部校验，不静默改写。
- **ManifestV1**：big-endian `manifest_version` + `file_count`，每文件 `path_length` / `path` /
  `file_size` / `file_sha256`（raw 32 bytes）。无 JSON/protobuf/padding/可选字段/timestamp。
- **digest**：per-file `SHA256(exact file bytes)`；tree digest =
  `SHA256("ora-skill-tree-v1" || 0x00 || manifest)`，内部 `[32]byte`、文本 lowercase hex。
- **container**：`"ORASKILL"` + `package_version` + `manifest_length` + manifest + 按 manifest 顺序拼接
  的 file bodies，无 footer/trailer/central directory。
- **decode 即 verify**：magic、版本、各 length/count bounds、排序、去重、case-collision、per-file
  SHA-256、精确 EOF 全校验；digest 由 manifest 重算，绝不当作可信输入。
- **bounds**：`Limits` / `DefaultLimits()`（library 级 provisional 默认值，非产品 quota）。

## 不变量与边界

- 纯内容库：不碰数据库、Object Storage、Node 文件系统或任何进程/网络状态，只读、校验、哈希、编码、
  解码、验证字节。
- 相同 canonical tree（相同 path、相同 bytes）⇒ 相同 manifest / digest / container（与顺序、分隔符、
  archive metadata 无关）。
- 不执行任何 file content（含 `.sh` / `.py` / `.js` / binary）。
- `SKILL.md` 元数据解析与 directory/archive 发现（zip/tar 解码）属后续切片，本包不实现。

参见 [AGENTS.md](../../AGENTS.md)、[internal 模块总览](../README.md) 与
`specs/decisions/cloud/skills/0-cloud-skills.md`。