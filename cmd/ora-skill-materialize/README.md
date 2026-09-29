# cmd/ora-skill-materialize: Node-local 一次性 Skill 物化 helper

[中文](README.md) | [English](README.en.md)

`ora-skill-materialize` 是被派发 Node 的沙盒本地的**一次性 Go 物化 helper**（6B.2B）。它把 Cloud 冻结的
`internal/skillpkg`（canonical codec）+ `internal/skillruntime`（canonical materialization）原样复用在
Node 本地文件系统上，绝不引入第二套 Rust codec/materializer。冻结边界见
`specs/decisions/cloud/skills/20260929-node-runtime-materialization-placement.md`（Candidate B）。

## 用途

Rust `ora-node` 的 host/guardian 在**被派发 Node 沙盒内**按绝对路径、经**封闭 stdin/stdout channel** 派生
本 helper。helper 消费一个携带「冻结 Attempt + 冻结 Skill bundle 元数据 + 短期 `RetrievalCapability`」的
JSON 请求，执行 canonical 链：

```
EnsureVerified → Project → Ready → SpawnGate.Open
```

并仅输出一个窄、非秘密的 prepared 事实。**它绝不启动真实 Agent 进程**（那是 6B.2C）。

## 调用约定

- argv/env：**不含任何 credential**。capability（signed URL）只经 stdin 的 JSON 传入。
- stdin：单条 JSON 请求（见下），有界读取（`maxRequestBytes = 4 MiB`，超限 → `request_too_large`）。
- stdout：单条 JSON 结果。stderr 不写任何 URL/签名。
- 退出码：`0` prepared；`1` preparation_failed（stdout 已写出带 `stable_error_code` 的结果）；`2` 请求畸形
  （stdin 读不出/JSON 非法/请求超限，未尝试物化）。

## 请求（stdin）

```json
{
  "attempt_id": "<uuid>",
  "cache_root": "/var/lib/ora/cache",
  "attempt_root": "/var/lib/ora/runtime",
  "skills": [
    {
      "skill_id": "<uuid>",
      "canonical_name": "alpha",
      "skill_revision_id": "<uuid>",
      "content_digest_algorithm": "sha256",
      "content_digest": "<64 lowercase hex>",
      "package_digest_algorithm": "sha256",
      "package_digest": "<64 lowercase hex>",
      "package_format": "ora-skill-package",
      "package_format_version": 1,
      "size_bytes": 1234
    }
  ],
  "capabilities": [
    {
      "skill_revision_id": "<uuid>",
      "method": "GET",
      "url": "https://…（bearer signed URL）",
      "expires_at": "2026-09-29T12:00:00Z"
    }
  ]
}
```

`skills` 即 `execution_skill_bindings` 冻结快照（不可变交付身份，绝不含可变 current revision 或 object
locator）；`capabilities` 按 `skill_revision_id` 精确绑定，`url` 是 bearer、仅存在于这条封闭 channel。

## 结果（stdout）

成功：

```json
{"attempt_id":"<uuid>","prepared":true,"local_root":"/var/lib/ora/runtime/attempts/<attempt_id>"}
```

失败（`prepared:false` + 稳定非秘密错误码；不含 URL/路径）：

```json
{"attempt_id":"<uuid>","prepared":false,"stable_error_code":"package_digest_mismatch"}
```

`local_root` 是 Node 本地的已发布 Attempt projection 根目录 —— 与未来 6B.2C 执行进程同一文件系统宿主。
`LaunchSpec` 是纯 runtime-local handoff，**不**被本 helper 返回、不落盘、不进结果。

## 秘密处理（红线）

- signed URL 只出现在 stdin；绝不出现在 argv、env、stdout、stderr、任何文件、marker、projection、结果或
  `attempt_result` 持久化行。
- 物化失败只回传 `stable_error_code`（`errors.Is` 分类到 `skillruntime` sentinel），原始 transport 错误——
  可能内嵌 signed URL/签名——被丢弃。
- `RetrievalCapability.String()` 只打印 `method`/`expires_at`，永不打印 URL。

## 稳定错误码（§41）

| 错误码 | 触发 |
| --- | --- |
| `retrieval_unauthorized` / `retrieval_not_found` / `retrieval_redirect` / `retrieval_transient` | 有界下载的 4 类 outcome |
| `size_mismatch` | 超界 body 或解码内容总量 ≠ 冻结 `size_bytes` |
| `package_digest_mismatch` | `SHA256(bytes) ≠ package_digest` |
| `decode_failed` / `content_digest_mismatch` | 包解码失败 / 树 digest ≠ `content_digest` |
| `cache_corrupt` / `cache_publish` | 已存在坏 cache 条目 / 原子发布失败 |
| `projection_mismatch` / `projection_collision` | 投影身份不符 / 同名 runtime 冲突 |
| `attempt_corrupt` / `not_ready` / `projection_publish` | Projection 已发布不一致 / 未过 READY / 投影发布失败 |
| `unsupported_digest_algorithm` / `unsupported_package_format` | 非 sha256 / 非 `ora-skill-package` v1 |
| `internal` | 其余（含请求内部不一致：缺 capability） |

## 不做的事

- **不**启动 Agent 进程 / 容器（6B.2C）；**不**发心跳/exit/cancel/恢复（6B.3）。
- **不**写任何持久化业务状态；Attempt 状态推进只经 Controller 的 fenced `attempt_result`。
- **不**产出 Rust codec/materializer 副本；本 helper 即 Go canonical 实现的唯一 Node 侧消费点。

参见 [cmd 模块总览](../README.md)、[internal/skillruntime](../../internal/skillruntime/README.md)、
[AGENTS.md](../../AGENTS.md)。