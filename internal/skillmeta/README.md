# internal/skillmeta: SKILL.md 元数据解析与 canonical_name 派生

[中文](README.md) | [English](README.en.md)

`internal/skillmeta` 是 Cloud Skills 的**业务 metadata 层**：解析 `SKILL.md` 的 YAML frontmatter，
并确定性派生 `canonical_name`。它是纯 CPU、确定性、无 I/O，与 identity 层 `internal/skillpkg`
（Step 2A 冻结，只校验 `SKILL.md` 存在 + UTF-8，**不**解析 name）严格分离。契约见
`specs/decisions/cloud/skills/20260927-skill-md-metadata-contract.md`。

## 职责

- **envelope**：frontmatter 必须从字节 0 的 `---` 行开始，闭合 fence 是其后第一个内容恰为 `---`
  的行；BOM / 前导内容 / 缺闭合 fence → fail closed（`skill_md_no_frontmatter`）。
- **name（唯一身份字段）**：必填、必须为 YAML string scalar；ASCII whitespace trim 后匹配
  `[A-Za-z0-9._-]+`、不以 `.` 开头、≤ 200 bytes。缺失 / 非 string / 非法字符 / 超长分别产出稳定
  `error_code`，**不回退**到目录名 / archive 文件名 / `display_name`。
- **canonical_name**：`ASCII lowercase(name)` 是唯一变换（无 NFC、无 slugify、无额外 trim）。
- **description**：可选、string、Unicode trim 后 ≤ 4096 bytes；缺失或 trim 后为空返回 `""`
  （对应 `SkillRevision.package_description NOT NULL DEFAULT ''`，绝不产生 `NULL`）。
- **重复 key / 非法 YAML fail closed**：解析进 `yaml.Node` AST 后自行检测重复顶层 key
  （`skill_md_duplicate_key`），不依赖 `yaml.v3` 的 map decode 行为；未知字段与正文不透明、原样保留
  （只读抽取，从不 rewrite）。

## API

```go
meta, err := skillmeta.Parse(skillMDBytes)   // (Metadata, error)，err 为 *ParseError 时带稳定 Code
meta.Name                                     // 必填、trim 后、已校验的 name
meta.Description                              // 可选 description，trim 后；缺失为 ""
meta.CanonicalName()                          // ASCII lowercase(name)
skillmeta.CodeOf(err)                         // 稳定 error_code（非 ParseError 返回 ""）
```

失败码：`skill_md_no_frontmatter` / `skill_md_invalid_yaml` / `skill_md_missing_name` /
`skill_md_name_not_string` / `skill_md_name_invalid_chars` / `skill_md_name_too_long` /
`skill_md_description_invalid` / `skill_md_duplicate_key`。

## 边界

- 只读抽取：不修改输入 bytes、不重写 frontmatter、不做 NFC/NFD normalization、不做 slugify。
- 失败码字符串是本实现对 metadata 契约「失败模式」的落地（契约只冻结失败条件、不冻结字符串）。
- 不做 name 与父目录名一致性的校验（`canonical_name` 必须从 content bytes 派生，见契约未解决问题）。

参见 [AGENTS.md](../../AGENTS.md)、[internal 模块总览](../README.md) 与
`specs/decisions/cloud/skills/20260927-skill-md-metadata-contract.md`。
