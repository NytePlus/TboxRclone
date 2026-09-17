# 真实交大实例实验记录

日期：2026-09-17；隔离运行 `run-20260917T020318Z-f0339b72`。所有写入仅作用于该运行在 `codex-api-lab/` 下创建的 fixture。凭据来自用户指定的本机 Tbox 配置，通过 personal-space 接口交换；token 有效期响应为 1800 秒，凭据未进入 Git 或报告。

这些是特定部署和测试条件下的 V 级观察，不是服务端形式保证，也不是 Finder 系统验收。36 个 ST 场景仍未执行完毕。

## 成功基线与协议修正

| 实验 | 观察 | 证据/实现影响 |
|---|---|---|
| 原生 sjtu 后端简单上传 | `rclone copyto` 上传 4096 字节成功，后端已独立读取 SHA-256 后返回 | 隔离目录 baseline.bin；当前提交日志 Committed |
| 分片初始化 | 必须提供 `partNumberRange`；`[1,2]` 返回 201，空请求体返回 400 | 实际响应是 `parts["1"].headers`，不是 SDK 顶层共享 headers |
| 分片签名 | 返回 x-amz-date、x-amz-content-sha256、authorization | 不能直接搬用新 SDK 的共享签名上传器 |
| 上传状态查询 | 省略 `no_upload_part_info=1` 返回 400 / ParamInvalid：`uploadPartInfo is unsupported under current deployment` | 已修复 `tbox-state` 对账请求并加入回归用例 |
| 已上传分片查询 | 加上上述 flag 仍返回 `parts` 数组，含 PartNumber/Size/ETag/LastModified | flag 禁止的是额外 uploadPartInfo 生成，不等于不返回已上传分片 |
| 重放分片 | 第 1 片连续上传两次，均 200 且 ETag 相同；第 2 片 200 | [multipart-baseline.json](evidence/2026-09-17/multipart-baseline.json) |
| 分片确认与内容 | 两片各 4 MiB，确认 200；独立下载 8 MiB，完整 SHA-256 一致 | 同上；未核对重复分片计费等不可见副作用 |
| 目录分页 | 51 个生成目录；page=1/page_size=50 得 50 项，page=2 得 1 项；marker+limit=1000 得全 51 项 | [pagination.json](evidence/2026-09-17/pagination.json)；另以 limit=1/20 观察到 nextMarker，marker 分页没有被服务端整体忽略 |
| abort 重放 | 未提交会话首次 DELETE ?upload 为 204，第二次 404/UploadNotFound；目标路径不存在 | [abort-rmdir.json](evidence/2026-09-17/abort-rmdir.json) |

## 提交后丢响应：真实双通道实验

通过 HTTPS 故障代理运行原生 `sjtu` 后端上传一个生成文件。代理分别记录到控制面与签名数据面请求，收到完整 confirm 响应后暂扣。使用不经过代理的独立读连接获取远端完整文件，SHA-256 与源 fixture 一致，随后显式丢弃响应。

客户端以错误退出，上传日志为 Unknown，未自动重试 confirm。原上传进程退出后，运行 `tbox-state`，通过上传状态和完整内容校验将日志改为 Committed；本地 spool 保留。

[response-loss.json](evidence/2026-09-17/response-loss.json) 包含脱敏代理事件、完整哈希、退出状态及对账前后状态。该实验是 CLI/backend/API 路径，**不能替代 ST-002-F 规定的 Finder 覆盖/取消用户动作**；也未验证覆盖旧文件时的原子性。

## 已证实不能依赖的并发保护

| 探测 | 实际结果 | 对实现的约束 |
|---|---|---|
| 文件 info + with_inode/with_content_cas | 响应有 versionId，无 inode 和 contentCas | 不能据此取得 CAS 或稳定 inode |
| DELETE + 错误 content_cas | 200，实验文件随后 info 为 404，返回回收站项 | 当前部署未执行该条件保护，禁止用它声称安全条件删除 |
| DELETE + 错误 If-Match | 同上 | HTTP 条件头也不能补足该删除接口的保护 |
| 非空目录 DELETE + directory_only=1 | 200，父目录和生成子项均变为 404 | 不满足非空 rmdir 拒绝要求；此观察只证明路径不可达，不宣称底层子项已永久删除 |

证据：[conditional-delete.json](evidence/2026-09-17/conditional-delete.json)、[abort-rmdir.json](evidence/2026-09-17/abort-rmdir.json)。删除仅针对本次从 baseline 复制出的专用文件和生成目录，使用 permanent=0；未触及原用户文件。

当前后端保持拒绝 Remove/Rmdir/覆盖。它们是发布功能缺陷，不能算作 C-006/C-011/C-017 通过。需要继续调查其他可核对身份、历史版本与 fs-journal 机制；不能把安全预期改成“有竞态也允许删除”。

## 新增同步接口线索

在已锁定的同一前端源码中补充发现 10 个 `/user/v1/sync` 操作，包含 create/delete/modify/query/default-rule/migrate；定义总数从 43 增为 53，仍不代表都可用。

- `PUT /user/v1/sync/1`：cloudPath、mode、localPath、clientId、spaceTag。为本次隔离目录创建 `cloud_to_local` 会话，201，返回 syncId。
- `GET /user/v1/sync/1?id_list=<本次id>`：200，返回 status=ok，以及 cloudPath/mode/creationTime/localPath/deviceId。
- 前端的单项 GET 路由返回 404；按 client_id + marker + limit 查询返回 400，不能继续按新前端参数假定部署一致。
- `DELETE /user/v1/sync/1/<本次id>`：204。注销后隔离云端目录仍可读取（200）。本次创建的同步注册已注销。
- 尚未取得 dirNode，未建立 fs-journal 数据访问或证明 ssn 的并发语义。

证据：[sync-registration.json](evidence/2026-09-17/sync-registration.json)。没有修改全局同步默认规则、迁移既有会话或启动实际桌面同步。

## 上游 fstests 的真实执行结果

已显式运行完整 `TestIntegration` 入口。修复了外部 Go module 调用上游 testserver 时必须从 rclone 源码目录定位 fixture 的问题。之后真实运行产生 6 个 PASS、19 个 FAIL、4 个 SKIP 子测试事件，并在 240 秒上限处终止；仍有未执行子测试。原始 JSON 日志在工作区 `reports/live-fstests.jsonl`。

早期失败涉及 ErrorDirNotFound/删除能力以及编码场景的目录清理，未清理目录又使后续用例观察到额外条目。不能将这些连带失败当成已分别验证的服务端编码缺陷，也不能通过移除失败子测试获得全通过。完整套件尚未完成、更未通过。

## 分片续签、原会话恢复（第二轮）

- `partNumberRange` 是**明确片号列表**，不是起止区间。请求 `[1,3]` 只返回 `parts["1"]` 与 `parts["3"]`；三片请求必须使用 `[1,2,3]`。初始两片基线无法区分这两种语义。
- `POST K?renew=1` 返回新的逐片签名，K、uploadId、对象 path 保持不变。实验 `[1,50]`、`[1,51]` 各返回两个授权，并不是 50/51 个授权，因此**未测得服务端批次上限**。见 [续签探测](evidence/2026-09-17/multipart-renew.json)。客户端保守每批至多 50 片。
- 未提交状态返回 uploadId；提交成功后的状态仍 `confirmed=true`，但 uploadId 清空。已提交对账以持久化 K 的查询结果、正确目标路径及独立完整读取 SHA-256 为依据；若服务端返回非空且不一致的 uploadId，仍拒绝。
- Go 实现用三片（4194304、4194304、13 字节）完成真实上传。首次因错误区间假设停止，修正后原 K/会话恢复；提交后只读对账成功，独立下载 8388621 字节 SHA-256 一致。见 [Go 三片恢复](evidence/2026-09-17/go-multipart.json)。

这些是 CLI/API 层证据，不替代 Finder 场景、掉电持久性、过期会话或长时间吞吐验收。

真实 HTTPS 代理在一个分片 PUT 的服务端完整 200 应答后断开客户端连接。客户端返回错误、日志停在 Uploading；另一个进程 `-resume` 复用相同 K/uploadId，完成后为 Committed，独立下载 SHA-256 一致。见 [分片丢应答](evidence/2026-09-17/multipart-response-loss.json)。该次只验证一个分片应答丢失，不等于全部中断点或系统验收通过。

## 自动获取个人空间令牌

Go 客户端已按真实 `POST /user/v1/space/1/personal?user_token=...` 协议集成私有 UserToken 文件，验证返回 libraryId/spaceId 与既定空间一致后只在内存缓存 accessToken。通过故意不存在的 accessToken 文件验证配置优先级及真实请求成功，见 [证据](evidence/2026-09-17/automatic-token.json)。1800 秒过期的提前刷新仅完成受控时钟测试，真实长时运行尚待验收；401/403 不触发原业务请求重放。
