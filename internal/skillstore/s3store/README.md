# internal/skillstore/s3store: 生产 S3-compatible Object Storage adapter

[中文](README.md) | [English](README.en.md)

`internal/skillstore/s3store` 是 `skillstore.ObjectStore` 的**唯一生产实现**，用
`aws-sdk-go-v2/service/s3` 对接单一 S3-compatible 端点（AWS S3 / MinIO / Ceph RGW / 阿里云 OSS 等），
选型与契约冻结在 `specs/decisions/cloud/skills/20260927-production-object-storage-provider.md`。

核心语义：

- **create-only = 单次条件写入**：`PutImmutable` 发一个 `PutObject`，带 `If-None-Match: *`；`412` 映射为
  `PutAlreadyExists`，**不存在** HEAD-then-PUT 的 TOCTOU 路径，也不启用 multipart（ADR D3/D4）。
- **仅三操作**：`PutImmutable` / `Stat`（HEAD）/ `Get`（有界读）；无 List / Delete / Presign / GC。
- **保守错误分类**：把 SDK 错误按冻结 taxonomy（ADR D6/D7）归入 not_found / already_exists / temporary /
  permanent / ambiguous，再映射到五值结果；原始 SDK 错误绝不跨 port 泄漏。凡「远端可能已受理」一律
  ambiguous，交给 saga 的 `Reconcile` 探针收敛，绝不盲目重放。
- **有界超时与传输**：connect 5s / request 30s（可配）；每个操作派生 caller context 的 deadline，禁止无
  deadline；HTTP transport 由 `http.DefaultTransport.Clone()` 派生，**不修改任何全局默认值**。
- **凭证来自部署平台**：`credential_mode` = environment（`AWS_ACCESS_KEY_ID` 等）/ shared_credentials_file /
  workload_identity（IMDS / ECS / IRSA）；凭证永不进 DB、日志、请求或响应。

## 配置

本包只接受**已解析**的 `Config`（与 `internal/config` 解耦，避免 `core → skillstore → s3store → config →
core` 的 import cycle）。`cmd/server` 在装配期把 `storage` 段翻译成它：

| 字段 | 含义 |
| --- | --- |
| `Region` / `Bucket` | region 占位与单一 bucket（必填） |
| `Endpoint` / `PathStyle` | 可选自定义端点；`PathStyle=true` 走 path-style 寻址 |
| `CredentialMode` / `CredentialsFile` | 凭证模式及 shared_credentials_file 的文件路径 |
| `InsecureSkipVerify` / `CAFile` | TLS 校验开关（已由 `tls.verify` 解析）与自定义 CA |
| `ConnectTimeout` / `RequestTimeout` | 连接 / 单请求超时（已套默认 5s / 30s） |

`New(ctx, cfg)` 只做**本地**校验与客户端构造，**不探测 bucket 可达性**（ADR D14）；可达性在首次实际
调用时按 taxonomy 分类。配置缺失时 `cmd/server` 根本不会构造本包（`SkillsObjectStore` 保持 nil）。

## 测试

`s3store_test.go` 通过一个最小 `api` 接口注入 fake，逐条断言：`If-None-Match: *` 与 body 原样透传、
错误矩阵到五值结果的映射、有界 `Get`（present / oversize / 恰好 maxBytes / absent）；另用一个
`httptest.Server` 作为 fake S3 端点，**经真实 SDK** 端到端证明条件写入与 412 → `PutAlreadyExists`。

参见 [AGENTS.md](../../../AGENTS.md)、[skillstore 说明](../README.md) 与
`specs/decisions/cloud/skills/20260927-production-object-storage-provider.md`。
