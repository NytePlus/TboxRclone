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

## 创建竞争、覆盖和移动

[并发及身份探测](evidence/2026-09-17/concurrency-identity.json) 使用全新隔离生成文件：

- 两个 ask 会话先初始化，第一个确认 200，第二个确认 409 `SameNameDirectoryOrFileExists`，第一份内容保留。对已存在文件再 ask 初始化仍 201；真正冲突点在 confirm。
- overwrite confirm 使用错误 `content_cas` 返回 200，新内容实际可见；目标旧内容被替换。
- 移动同时提供错误源 body.contentCas 和目标 query.content_cas，返回 200；源 404，目标内容与源一致。这些条件未阻止操作，不能把它们当作 CAS。
- 文件 info 仍无 inode/contentCas；versionId 在覆盖前后发生变化，但这不证明其能作为写入前置条件。
- 目录专用移动返回 204，源 404，目标子文件内容完整；尚未证明同名覆盖、丢响应和并发修改下的保护。
- `fs-delta/cursor` 返回 404。
- 历史列表只返回一个标为最新的条目；对应 history_id 可下载最新内容，虚构 history_id 返回 404。未取得旧内容保存的证据，不能宣称覆盖后能靠历史恢复旧版本。见 [历史读取](evidence/2026-09-17/history-read.json)。

## 所有文件统一使用可续签上传

[简单上传续签](evidence/2026-09-17/simple-renew.json) 返回：空 body 为 400/ParamInvalid，加片号为 404/NotMultipartUpload。该探测会话已通过 upload abort 204 结束。

[单片原始协议](evidence/2026-09-17/small-multipart.json) 证明空文件和 4 字节文件可以初始化 multipart、上传唯一分片、确认、完整下载。[真实 Go 客户端](evidence/2026-09-17/go-small-multipart.json) 随后对空文件和 15 字节文件完成相同验证。因此新上传统一使用可续签协议，旧版简单上传的遗留日志仍保留且不会猜测重建。

[授权列表边界](evidence/2026-09-17/signature-batch-boundary.json) 实际发送 50 和 51 个明确片号，服务端各返回 50 和 51 个授权；两次均 200。未推断更大上限，客户端仍使用至多 50 片/批。探测会话未上传数据，已 abort 204。

## 进程中断实测

[Linux SIGINT](evidence/2026-09-17/multipart-sigint.json) 与 [Linux SIGKILL](evidence/2026-09-17/multipart-sigkill.json)：真实数据面分片完整应答被代理暂扣时，对容器 PID 1 的编译后客户端发送信号；分别退出 2、137。日志仍为 Uploading；新的进程复用原 K/uploadId 进行恢复；最终 Committed 且绕开代理的完整下载 SHA-256 一致。

已确认片不重传另由离线请求计数测试验证；上述真实实验的恢复阶段使用直连，未采集恢复阶段逐片请求计数。

这两项是 Linux 进程中断证据，不是 macOS Finder 录屏、宿主机崩溃或掉电持久性证据；ST-003 等系统分支仍未标为 PASS。

## 真实 WebDAV 链路

Compose 服务指向隔离实验目录，Mac 原生 `davcheck` 通过 loopback 请求 rclone serve webdav，再由 sjtu backend 访问真实云盘。[13 项检查](evidence/2026-09-17/webdav-check.json) 中 11 项满足预期、2 项不满足：

- 新文件 PUT 201，完整 GET 内容一致，HEAD 长度正确；Range 为 206 且字节/Content-Range 正确，越界 416；错误 GET If-Match 为 412，命中 If-None-Match 为 304；目录 PROPFIND 为 207 且包含文件。
- 重复 MKCOL 仍返回 201，应为 405。
- 已有文件的 PUT If-None-Match:* 返回 405，应为 412。服务日志表明仍进入后端上传准备，产生 Prepared 日志后因禁止覆盖而失败；独立 GET 保留原内容。

协议工具会对这两项失败返回非零。这些实际失败不能被上游本地后端测试通过掩盖；也不能将 curl/HTTP 请求替代 Finder 用户操作。Finder 自动化目前两次返回 cgWindowNotFound，尚无可操作窗口或 UI 录屏。

### WebDAV 补丁后的复测

固定上游源码应用可重放补丁后，服务重新构建并通过健康检查。[复测记录](evidence/2026-09-17/webdav-check-patched.json) 为 13/13，通过状态分别包含重复 MKCOL 405、已有资源条件 PUT 412。检查前后上传日志差集只有一个 Committed，没有被拒绝 PUT 的 Prepared 记录。

这证明两个已复现的顺序请求问题已修复；VFS 视图可能陈旧，因此不能将此结果外推成云端原子条件写或跨客户端并发验收。原始失败证据保留，不回写为 PASS；系统 manifest 状态不变。

## 显式中止与提交竞争

[中止应答丢失](evidence/2026-09-17/abort-response-loss.json)：真实上传在数据面应答暂扣处中断，随后 Go `tbox-state -abort` 的 DELETE 应答被代理在服务端完成后丢弃；首次退出 1、日志 AbortUnknown。新的进程再次显式中止时先做读取，会话及正式路径均 404，记录 Aborted，spool 保留。

[提交/中止竞争](evidence/2026-09-17/confirm-abort-race.json) 使用 5 个全新隔离文件，两个确定顺序与三次并发：

- confirm 先完成：confirm 200、abort 204，K 404，但正式文件为 200 且完整。
- abort 先完成：abort 204、confirm 404/UploadNotFound，K 与正式路径 404。
- 三次并发：一次两请求成功且正式文件完整；两次 confirm 404/UploadIncomplete、abort 204、正式路径 404。

**不能将 abort 204 等同于取消成功，也不能仅依据 K 404 判断未发布。** 当前实现会保留“会话消失但正式路径存在”的 AbortUnknown；已进入 CommitSent/Unknown 的任务先对账，不发送中止。所有分支均保留本地数据。

这是 CLI/API 证据；未完成 Finder 取消操作、所有时序、历史/回收站数量增量或暂存对象物理回收核验，系统场景状态不变。

## macOS 实际文件系统路径

macOS 26.5.2 使用系统 mount_webdav 连接本机 Compose 服务。只读挂载后通过文件系统读取：4096 字节完整内容 SHA-256、pread 偏移内容、空文件、51 个目录条目均符合预期。[读取证据](evidence/2026-09-17/macos-webdav-read.json)。

可写挂载的新文件实验暴露不同于单次 HTTP PUT 的流程：macOS 先创建云端空文件，再提交实际内容和 AppleDouble。open/write 返回成功，fsync 为 EPERM，close 仍成功；独立直接云端读取为 0 字节。61 字节正文和 4096 字节 AppleDouble 均在本地 Prepared spool 中完整保留。[写入失败证据](evidence/2026-09-17/macos-webdav-write.json)。这属于发布缺陷，不能因为 HTTP 检查 13/13 而将 macOS 写入标为通过。

原生 FUSE 另一条路径已完成 cmount/CGO 接线和本机构建，但缺少运行库而无法挂载。[构建证据](evidence/2026-09-17/macos-native-build.json)。所有上述测试均不是 Finder UI 录屏或完整系统验收。

## local_sync_id 令牌补充实验

在同一份固定前端中补录两个空间令牌定义（目录现有 55 个前端操作定义，并非 55 个均可用的独立接口）：`POST /user/v1/space/{organizationId}/personal` 和 `POST /user/v1/space/{organizationId}/token/{spaceId}`。二者 query 均列出可选 `local_sync_id`。`Ge.fetch` 的普通空间令牌调用 body 为可选 `spaceOrgId`；本地配置 `localSyncId` 由客户端生成，与服务端登记 `syncId` 分开保存，不能将二者混同。

新建一个隔离实验目录并登记 cloud_to_local 同步（201），分别将新生成的本地 ID、刚返回的服务端 syncId 作为 local_sync_id 调用两个令牌端点：四次均 200，返回字段仅 libraryId/spaceId/accessToken/expiresIn。使用各自令牌读取该实验目录，四次均 200、localSync=null，无 inode/ssn。查询本次登记列表为 200，单条详情仍 404。最后仅删除本次登记（204），保留实验目录；未修改既有同步或全局配置。

证据：[sync-token-probe.json](evidence/2026-09-17/sync-token-probe.json)。参数被接受不证明有同步语义，404 也不能证明所有 fs-journal 能力均不可用。现阶段仍未得到可用于原子条件覆盖的对象身份或序号，不能据此开放覆盖。

## HTTP 条件头不能保护覆盖

在两个全新生成文件上，分别对 multipart 初始化及 confirm 都添加错误 `If-Match`、`If-None-Match: *`，并使用 overwrite 策略。两组初始化 201、确认 200；独立完整下载均为新字节，旧字节未保留。见 [HTTP 条件头实验](evidence/2026-09-17/http-mutation-conditions.json)。因此，这些标准 HTTP 头在所测控制面操作中没有阻止覆盖，不能作为 content_cas 的替代保护。这里只验证这两组请求，不声称所有服务端接口均无条件能力。

## 真实时钟令牌过期测试（进行中）

新增 `internal/smh/TestLiveTokenExpiry`，默认 SKIP。显式启用后，在同一 Client 中每 30 秒读取隔离实验目录，持续至少 31 分钟，记录成功读取次数与令牌值是否发生变化（不输出令牌或摘要）。结束时用最初的令牌只读请求，要求其返回 401/403；同一进程的新令牌须持续可读。本测试不修改云端数据，也不替代 SSO、Finder 或故障情况下的认证验收。

```sh
docker compose run --rm \
  -e TBOX_AUTH_SOAK=1 \
  -e TBOX_SPACE_FILE=/secrets/space.json \
  -e TBOX_USER_TOKEN_FILE=/secrets/user-token \
  -e 'TBOX_AUTH_PATH=codex-api-lab/<run-id>' \
  -v "$PWD/.secrets:/secrets:ro" \
  go-tests go test -json ./internal/smh -run '^TestLiveTokenExpiry$' -count=1 -timeout=35m
```

2026-09-17 05:02:49 UTC 首次运行已成功读取，日志在忽略目录 `reports/auth-soak.jsonl`。尚在等待真实过期边界，**未判 PASS**。
