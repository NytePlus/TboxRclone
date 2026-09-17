# rclone 接入、必要测试子集与 WebDAV 契约

当前验收采用[单客户端并发约束](concurrency-contract.md)。客户端冲突拒绝与未知结果的持久路径占用取代跨客户端 CAS 要求；不同文件和同一上传的分片并行仍是目标。

## 1. 架构与实现边界

建议 `macOS Finder → rclone serve webdav → rclone VFS → 原生 sjtu backend → 交大 SMH API/对象存储`。另一条路径为 `macOS 应用 → rclone mount → VFS → 同一 backend`，需要单独验证本机挂载支持与权限。

不建议把当前 Tbox 放在原生 backend 下游：否则旧实现的重试、缓存、覆盖和取消问题依然保留。现有 C# 作为请求格式来源，不作为正确性 oracle。

固定参考 [rclone v1.75.1](https://github.com/rclone/rclone/tree/v1.75.1)，不要依赖会变化的 master。源码 go.mod 要求 Go 1.26.0。原始 Tbox 参考项目为 C#。当前 TboxRclone 已建立 Go 实验后端与 Docker Compose，见 [实施状态](implementation.md)；以下是完整目标契约，不能因当前功能缺失而降低要求。

## 2. 后端必要接口映射

来源：[fs/types.go](https://github.com/rclone/rclone/blob/v1.75.1/fs/types.go)、[fs/features.go](https://github.com/rclone/rclone/blob/v1.75.1/fs/features.go)。

| rclone 接口 | 云盘实现 | 必须满足 |
|---|---|---|
| NewFs、Name/Root/String、Precision/Hashes/Features | 配置、空间凭据 | 根是文件要正确返回 ErrorIsFile；能力声明真实 |
| List | 目录分页 | 返回完整一级目录，不能只返回第一页；目录不存在区别于空目录 |
| NewObject | directory?info | 文件/目录区分；ErrorObjectNotFound 与权限错误不混淆 |
| Put / Object.Update | 简单/分块+confirm | 0 字节、大文件、短流、覆盖、中断；最终路径必须符合请求 |
| Object.Open | 下载+Range | SeekOption/RangeOption、内容版本绑定、取消、关闭响应体 |
| Mkdir | directory PUT | 已有目录成功，已有文件冲突，父目录策略明确 |
| Rmdir | 目录只删空 | 不能映射递归删除；先 List 再 Delete 有 TOCTOU，优先验证 directory_only |
| Object.Remove | file DELETE | 错误分类、回收站语义；未知结果不误删重建对象 |
| Size/ModTime/Hash/Storable/Remote/Fs/String | 元数据 | 大数精确、ETag 不冒充 MD5、修改时间不可伪造 |
| SetModTime | 调查持久时间设置接口 | 无能力时返回对应不支持错误；Precision 使用 fs.ModTimeNotSupported，而不是只改内存后成功 |

优先可选能力：Move、DirMove、Copy、About、PutStream。未经验证不注册可选能力；rclone 可能回退到复制/删除，此时必须明确失去服务端 rename 效率/原子性。ChangeNotify、ListR、元数据/xattr、分片写接口后置。`OpenChunkWriter` 不是实现内部并行 multipart 的前提。

rclone v1.75.1 内置 hash 集合没有通用的 SMH CRC64 类型；先将 CRC64 留在上传内部校验，`Hashes()` 不虚报 MD5/SHA。跨端验收仍下载计算 SHA-256。若增加 rclone hash 类型，另行验证算法与上游约定。

所有 I/O 接受 ctx；有界生产者消费者队列、每文件分片并发上限、全局请求/内存上限同时生效。重试重建可读分片流，不能复用已经读完的 Reader。未知长度先 spool 或明确错误；Finder 目标要求最终支持缺少 Content-Length 的写入场景。

## 3. 必要测试子集

“单元测试”需要分三层：后端离线单元测试；rclone `fstests` 真实后端集成测试；WebDAV/VFS/macOS 系统测试。`fstests.Run` 不是纯单元测试，会真的创建和删除远端数据。

### 3.1 后端离线单元测试（持续补齐）

| 编号 | 必测内容 |
|---|---|
| U01 | URL 按段编码、JSON 转义、根路径、NFC/NFD/大小写策略 |
| U02 | size/CRC64 无精度损失；错误映射；业务失败/异步受理不返回成功 |
| U03 | token 到期/并发刷新/401/403，账户隔离，日志脱敏 |
| U04 | 分片 0、1、边界±1、50/51 授权批次；短 EOF/流读取错误；每次重放完整字节 |
| U05 | 超时发生在发送前/发送中/提交后；confirm/move/delete Unknown 状态及对账 |
| U06 | Range 200/206/416、远端忽略 Range、内容变化、ctx cancel 和响应体释放 |
| U07 | 持久日志恢复、缓存不足、错误校验和；既有文件不被失败上传破坏 |
| U08 | 分页游标重复/空页/末页、条目新增删除，避免死循环和误认完整 |
| U09 | 同路径双写/读写拒绝、第二实例拒绝、未决路径跨重启占用、abort 与 confirm 竞争 |

用 Go `httptest`/可控 fake transport、故障点与可注入时钟；关键状态机用 `go test -race`。mock 可以验证客户端行为，不能证明真实服务器的原子性。

### 3.2 上游已有集成测试的最小关注集合

名称已经核对 [fstest/fstests/fstests.go](https://github.com/rclone/rclone/blob/v1.75.1/fstest/fstests/fstests.go)。多数是嵌套子测试，具有共享准备与清理；**初次验收运行整个 TestIntegration**，不能用一长串平铺 regex 跳过父测试建立的数据。

| 必要能力 | 已有子测试名称 |
|---|---|
| 根/目录基础 | FsRmdirNotFound、FsRmdirEmpty、FsMkdir、FsMkdirRmdirSubdir、FsListEmpty、FsListDirEmpty、FsListDirNotFound、FsRmdirFull |
| 编码/对象不存在 | FsEncoding、FsNewObjectNotFound、FsNewObjectDir、FsIsFile、FsIsFileNotFound |
| 上传 | FsPutError、FsPutZeroLength、FsPutFiles、FsPutShortEOF、FsPutChunked、FsUploadUnknownSize |
| 完整列表 | FsListDirRoot、FsListSubdir、FsListLevel2、FsListFile1and2 |
| 内容读取 | ObjectSize、ObjectOpen、ObjectOpenSeek、ObjectOpenRange、ObjectPartialRead、ObjectOpenFingerprint |
| 更新/删除 | ObjectUpdate、ObjectRemove、ObjectModTime、ObjectSetModTime、ObjectHashes |
| 服务端操作（启用对应能力时必测） | FsCopy、FsMove、FsDirMove、ObjectAbout、FsPutStream |

未注册能力而 SKIP，不算该能力通过。未知长度允许按后端合同返回错误的测试，也不等于 Finder 未知长度写入需求已满足。分片大小配置需要接入 fstests 的相关选项，单纯内部实现 multipart 不保证 chunk 测试实际覆盖边界。

后端集成测试采用下述完整接线；实际入口见 `backend/sjtu/integration_test.go`，必须配置隔离空间后显式运行：

```go
func TestIntegration(t *testing.T) {
    fstests.Run(t, &fstests.Opt{
        RemoteName: "TestSjtu:",
        NilObject:  (*sjtu.Object)(nil),
    })
}
```

配置 `TestSjtu:` 的根只指向专用实验目录。当前 Compose 提供 `go-tests`（固定 Go 工具链）、`webdav`、`fault-proxy` 等服务；源码、配置只读挂载，缓存与日志单独持久卷，secrets 不写入镜像或 git。在项目根目录执行：

```sh
go test -race ./...
go test ./backend/sjtu -run '^TestIntegration$' -count=1 -v
go test github.com/rclone/rclone/cmd/serve/webdav github.com/rclone/rclone/vfs/... -count=1
```

第一条会运行 backend/sjtu、SMH、日志、恢复和故障代理等离线测试；未配置真实空间时 TestIntegration 明确跳过。日常精简回归须保留父层，例如上游短 EOF 的路径是 `TestIntegration/FsMkdir/FsPutShortEOF`；运行记录应核对实际 RUN/PASS/SKIP 数，不能以“没有跑任何测试的 exit 0”过关。

### 3.3 已有 WebDAV 回归

[rclone WebDAV tests](https://github.com/rclone/rclone/blob/v1.75.1/cmd/serve/webdav/webdav_test.go) 包含 TestWebDav、TestHTTPFunction、TestCompressedTextFile、TestCompressedPROPFIND、TestRangeRequestNotCompressed、TestMoveDefaultsToOverwrite、TestMoveOverwriteFalseStillRejects。整包运行优先，后两项尤其对应 Finder 覆盖行为。默认本地测试后端通过不代表交大端到端通过，要将协议场景在 sjtu 后端上再执行。

## 4. Finder 所需 WebDAV 需求子集

规范依据：[RFC 4918](https://www.rfc-editor.org/rfc/rfc4918)、[HTTP Semantics RFC 9110](https://www.rfc-editor.org/rfc/rfc9110)。下表是目标产品验收要求，不是已证明的 Finder 所有版本请求清单。

| 方法/能力 | 最小正确语义 |
|---|---|
| OPTIONS | 只声明真实支持的 DAV class 与 Allow；Class 2 必须有实际锁约束 |
| PROPFIND | Depth 0/1，allprop/propname/显式属性；207 内各属性状态；collection、size、mtime、ETag，href 正确编码；无限深度可按规范拒绝，不静默当成 1 |
| GET/HEAD | 完整内容；HEAD 无 body；Range 单段/后缀/越界正确；If-None-Match/If-Match/If-Range；稳定版本防混读 |
| PUT | 新建 201、覆盖 200 或 204；完整长度验证、空文件、未知长度、失败不破坏旧版本；If-Match/If-None-Match |
| MKCOL | 新建 201；已有资源 405；缺父目录 409；不假报创建 |
| DELETE | 完成后 204；权限/锁错误正确；异步远端任务尚未完成不能返回成功 |
| MOVE | Destination 校验，Overwrite 缺省 T，F 时已有目标 412；创建 201/覆盖 204；父目录/后代环路/文件与目录冲突；源与目标均检查条件/锁 |
| COPY | 文件与目录、Depth、Overwrite、部分失败明确；不能只创建空目录却声称复制完成 |
| LOCK/UNLOCK | exclusive write 最小支持、刷新、Timeout、未存在资源锁定、Depth 锁、If token、423；跨请求强制检查；发现信息真实 |
| PROPPATCH | 支持的属性真正持久化；不支持按属性报告失败；单请求失败遵循原子处理和 failed dependency，不能 DTO 修改即报成功 |

额外验收：`.DS_Store`、`._` AppleDouble 文件、隐藏文件、扩展名、拖拽目录、快速查看、应用“临时文件+重命名”安全保存。不能声称完整 POSIX ACL、xattr、hardlink 或 symlink 兼容；支持范围由对应实测决定，不静默丢失元数据。

## 5. rclone 不自动提供的保证

`serve webdav` 源码创建 `webdav.NewMemLS()`。这是单服务进程锁；服务重启、多实例、直接网页修改都会突破其保护边界。发生重启后客户端必须能重取锁；如果产品要求锁跨重启持久化，需要单独实现并测试，不能只改 backend。

[VFS 文档](https://github.com/rclone/rclone/blob/v1.75.1/vfs/vfs.md) 说明缓存模式下关闭文件并达到 write-back 延迟后才上传，重启时同配置可重试缓存中的未上传数据；这不等价于掉电时任意已应答字节都已 fsync 到磁盘。`--vfs-write-back 0s` 也不能自行证明 HTTP 成功与远端提交严格同步。

产品必须展示/记录：本地待同步、正在上传、云端已确认、冲突、失败。对“云端已确认”只能在确认及必要内容验证完成后给出信号。需要严格同步 PUT 成功语义时，审计并可能修改 WebDAV/VFS 响应边界；仅设置缓存选项不够。

## 6. 效率指标与发布门槛

初始实验矩阵：1 KiB/4 MiB/256 MiB/2 GiB 文件；1/4/8 文件并发；1/2/4/8 分片并发；4/16/32 MiB 分片（先验证服务端允许）；目录 0/1/50/51/1000 项。记录直连原始 API、rclone copy、WebDAV、Finder 四条路径。

指标：端到端吞吐、首字节、p50/p95 元数据延迟、API 次数、重试字节、RSS、CPU、缓存占用、恢复时间、残留 session 数。建议初始大文件吞吐目标为同条件直连 API 的 ≥80%，属于待测工程目标，不是当前性能结论。元数据操作与大文件传输应独立限流，防止 Finder 列目录被大上传挤死。

通过门槛：完整内容 SHA-256 一致；P0 场景无数据丢失/静默损坏/错误成功；Unknown 均可收敛或明确保留冲突；缓存容量不足不误报成功；源不会在目标达到约定持久边界前被适配器删除；所有 skip 与未验证能力显式列出；最后在真实 macOS/Finder 版本上验收。Linux Docker 测试不能替代 macOS webdavfs 与进程信号行为。
