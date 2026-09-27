# internal/skillstore/fakestore: 内存测试替身

[中文](README.md) | [English](README.en.md)

`internal/skillstore/fakestore` 是 `skillstore.ObjectStore` 的**确定性内存测试替身**（test double），
不是生产 provider。默认行为诚实：`PutImmutable` 在锁下原子 create-only 写入，`Stat` 报告 present/absent
并带 size 提示，`Get` 返回精确字节或在 `maxBytes` 处有界截断。测试可覆盖 `PutFn` / `StatFn` / `GetFn`
注入脚本化 outcome，覆盖 reconciliation 契约要求的每一种失败模式（ADR D25）。

## 用途

- **create-only 断言**：同一 key 二次 `PutImmutable` / `Seed` 均报告已存在且不覆盖既有字节。
- **有界 Get**：`maxBytes` 小于对象长度 → `GetOversize`。
- **scripted outcome**：`StatFn`/`GetFn` 返回 `StatIndeterminate`/`GetIndeterminate` 等，验证
  `Reconcile` 的分类透传。
- **`Seed`**：不经过 `PutFn` 直接落一个对象，模拟「此前一次 ambiguous PUT 实际已/未落盘」或在该 key
  植入损坏对象。

## 边界

- 仅测试用：无网络、无真实存储、无并发安全保证之外的语义；生产 provider（S3/GCS/Azure/MinIO SDK）
  属后续步骤，本包不实现。

参见 [AGENTS.md](../../../AGENTS.md)、[skillstore 说明](../README.md) 与
`specs/decisions/cloud/skills/20260927-object-storage-abstraction.md`。
