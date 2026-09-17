# 实施状态与发布阻塞项

日期：2026-09-17。状态：实验实现，未发布，目标未完成。

## 已有实现

- 主项目 Go module 与固定 Go 1.26.0 Docker Compose；rclone v1.75.1 submodule 固定上游提交，工作区应用主仓库跟踪的可重放补丁。
- 注册 `sjtu` backend，提供元数据、分页、严格 Range/ETag 读取、受限创建目录、已知/未知长度的持久化上传；包括空文件在内统一使用可续签分片协议。
- 写前持久化完整内容、大小、SHA-256；日志文件 fsync、原子 rename、父目录 fsync；0700 目录与进程排他锁。
- 上传路径仅限显式启用的 `codex-api-lab/<run-id>`，使用 ask 策略并拒绝覆盖；确认后独立完整下载校验才返回成功。
- 写请求不自动重试；CommitSent/Unknown 保留本地文件；重复调用遇到 pending 日志直接报错。
- `tbox-state -reconcile` 对账已发送的提交，只使用远端读取；遇到冲突保持 Unknown。
- 原会话分片续传：4 MiB 片、最多 4 个并发、每批至多 50 个明确片号；每片 SHA-256/ETag 持久化，不保存签名。`tbox-state -resume` 重新验证 spool、会话和已确认分片，续签后仅补传未确认片；提交后的状态只做只读对账。
- 完整上游 `fstests.Run` 入口；未配置真实测试空间时明确 SKIP。
- 36 个系统分支的子场景目录与证据门禁。离线测试不会修改系统场景状态。
- HTTPS CONNECT 故障代理与 Compose faults 服务：双通道 allowlist、完整响应暂扣/释放、发送前/响应后断开、传输中截断。见 [实验说明](fault-proxy.md)。

## 已执行验证

具体命令及日志在工作区 `reports/`。`go test -race ./...` 共 54 个顶层测试 PASS，真实 TestIntegration 1 个 SKIP；`go vet ./...` PASS；上游 `cmd/serve/webdav` 测试 PASS（27.531s）；Linux 与 macOS ARM64 构建 PASS，macOS 本机已执行 version/backend help。离线测试涵盖 Unicode/保留字符、整数精度、JSON/HTTP 错误、异步受理不能当完成、控制面重定向不泄漏凭据、Range 被忽略/变化/短流、EOF/额外字节、分页重复、上传丢响应、日志重新打开、缓存损坏和进程锁。后端测试经过真实 HTTP/TLS 客户端与模拟服务器，不证明交大实例具有相同语义。

HTTPS 故障代理增加 9 个顶层测试，覆盖提交后丢响应的独立事实核对、控制/签名数据双通道、上传/下载截断、规则单次触发、authority 隔离和进程关闭。Compose faults 服务已启动并通过健康检查，已确认控制接口可用及非 allowlist CONNECT 返回 403；该服务验证没有访问交大云盘，检查后已停止。发现 `go run` 包装使 Compose 停止状态为 2 后，服务启动改为编译再 `exec` 二进制；重新健康启动并 SIGTERM 停止，已确认退出码为 0。

真实 SJTU fstests 已执行入口，但出现 6 个 PASS、19 个 FAIL、4 个 SKIP 子测试事件后在 240 秒处超时，完整套件未跑完；Finder、宿主机断电/崩溃及真实多客户端覆盖仍未完成。manifest 继续保持 NOT_RUN；证据门禁当前为 0/36 PASS，并按预期返回非零退出码。

## 已知缺陷/未实现能力（发布阻塞）

| 问题 | 影响条件 | 当前行为及后续要求 |
|---|---|---|
| B01：覆盖、条件删除、原子空目录删除未实现 | C-002/004/006/011/013/017 | 实测错误 content_cas/If-Match 删除条件被忽略，覆盖 confirm 与移动的源/目标 CAS 也被忽略；directory_only 删除非空目录后子项不可达。保持拒绝，需调查其他保护方式；属于发布功能缺失。 |
| B02：分片恢复尚未完成产品级验收 | C-001/003/007/018 | 已实现及实测原会话分片续签/显式恢复；默认 spool 上限 64 MiB 可配置，固定 4 MiB 片最多 10000 片。已提供显式 abort 与未知结果核对；尚缺自动恢复调度、过期会话处理、批量/超大文件与完整故障矩阵。 |
| B03：SSO 交互登录与长时会话验收未完成 | C-015 | 已有 UserToken 文件可自动获取/刷新个人空间 accessToken，缓存仅在内存；并发合并、提前续期、账户空间核验、凭据轮换检测；401/403 只使缓存失效，不重放原请求。真实 31 分钟内 63 次读取及第 29 分钟刷新已观察；旧令牌第 31 分钟仍有效，过期断言 FAIL，硬过期/空闲过期/宽限语义未确定。SSO 重新认证仍待验收。 |
| B04：服务端 Move/DirMove/Copy 与异步 task 未实现 | C-004/013/016 | 不注册可选能力，202/taskId 返回明确未完成错误。 |
| B05：WebDAV/VFS 安全保存与云端应答边界未验收 | C-005/008/012/013/014 | 上游测试通过也不能替代交大/Finder 实测；默认使用缓存 off，仅实验。 |
| B06：性能、空间管理不足 | C-018/015 | 上传先 spool、单日志目录串行、完成后全量下载验证；暂无自动 GC，总缓存会增长。 |
| B07：读与目录协议仍需实例确认 | C-009/010/014 | 强制要求返回 ETag；marker 分页异常直接失败。已实测两次 Range 之间替换内容，旧对象拒绝读取、新对象读回完整新内容；稳定 0/1/50/51/1000 项及强制 50 项分页已实测；修复整数 nextMarker 解码。单流覆盖、异常 Range 和变化目录分页仍待验收。 |
| B08：宿主机掉电持久边界未验证 | C-003 | fsync 与日志重开测试不等于宿主机掉电测试；不能宣称掉电零丢失。 |
| B09：macOS 原生 mount 运行环境和验收未完成 | 原生 mount 验收路径 | 已接入 cmount 并成功原生 CGO 构建；本机缺少 FUSE 运行库，实际挂载失败。macOS 内置 webdavfs 已成功挂载读取，但与原生 FUSE、Finder UI 是不同验证路径。 |
| B10：外部 backend overview 被覆盖（已修复） | CLI 启动信息 | 上游通用注册函数保留显式提供的 Overview；SJTU 提供实验状态元数据。缺省仍读取内置配置。新增测试先失败后通过，CLI version 无原错误；见补丁 0002。 |
| B11：WebDAV 重复 MKCOL（已修复已测路径） | C-010/014，WebDAV MKCOL | 补丁在 WebDAV 层区分已有资源；上游回归及真实重复 MKCOL 均返回 405。其他客户端并发创建仍待验收。 |
| B12：WebDAV 完整条件写尚未验收 | C-006/014，If-None-Match/If-Match | 已修复 VFS 可见目标的 PUT If-None-Match:*，真实返回 412 且不产生上传日志；其他条件头、陈旧 VFS 缓存、网页端竞争和云端 CAS 仍未解决。 |
| B13：macOS webdavfs 新文件写入失败 | C-001/005/008/012/014 | open 创建空文件后，write 数据的后续覆盖被拒绝。fsync 为 EPERM、close 成功，独立云端仍 0 字节；完整数据在 Prepared 日志。须解决安全覆盖及 macOS 提交语义后才能发布。 |

API `directory_only=1` 的 SDK 原文只承诺“不级联删除子文件和子目录”，并未承诺非空目录拒绝；不得用它直接实现 rmdir。SDK 1.0.16 multipart 响应描述是顶层 headers，但真实实例已确认使用逐片签名映射；`partNumberRange` 是明确片号列表，并非区间端点。

已通过本机 Tbox UserToken 取得普通用户空间凭据，并完成隔离目录的简单/分片上传、重放、abort、错误条件删除、非空目录删除、同步注册及真实提交后丢响应实验。修复上传状态查询参数和外部模块 fstests fixture 定位。新增 10 个同步接口定义，见 [真实接口实验](live-api-findings.md)。

下一步：完善分片自动恢复和会话过期行为；完成 1800 秒真实到期和 SSO 续期验收；继续调查可用的稳定身份、历史版本和 fs-journal 并发保护。真实证据应持续记录到 manifest，已知实现缺陷不能改写成符合预期。

本轮新增分片续签和恢复实现，离线覆盖逐片签名列表、应答状态、丢分片应答后日志重开、丢 confirm 应答不重放、续签身份变化、账号/缓存/分片摘要/远端内容不一致。真实三片文件完成独立 SHA-256 校验；真实代理丢失数据面 200 应答后，另一个进程复用原会话恢复并通过独立校验，见 [分片丢应答证据](evidence/2026-09-17/multipart-response-loss.json)。并发实验中第 3 片已持久化确认；其他未确认片补传，未重新初始化。

新增 UserToken 驱动的个人空间令牌管理，受控时钟验证提前刷新，竞态测试覆盖并发刷新与等待者取消、凭据轮换和账户隔离；真实 Go 客户端在 accessToken 文件不存在时成功列出隔离目录，见 [自动取令牌](evidence/2026-09-17/automatic-token.json)。未用该实验替代真实令牌过期/SSO UI 场景。

新增实验确认简单上传不能通过 renew 续签，而空文件和微小文件可用单片 multipart 完整提交。后端统一走分片恢复状态机，并直接利用初始化签名，省去新建时重复的状态查询、续签及 spool 重读。新增 0/1/4 MiB 边界±1 覆盖。真实 Linux 容器内 SIGINT 和 SIGKILL 在数据面应答暂扣时分别执行，新进程均从原会话恢复，独立 SHA-256 一致；这不是 macOS/Finder 或掉电测试。

新发现 `ask` 只在 confirm 才阻止竞争创建（第二个会话 409），同名初始化仍 201；错误 CAS 不能保护覆盖和移动。目录专用移动 204 后子项仍可达，但缺少可靠身份及并发前置条件，因此暂不注册 Move/DirMove。签名接口可一次返回明确列举的 50 和 51 个授权，前端“50”不能当成服务端硬上限。详见 [真实接口实验](live-api-findings.md)。

WebDAV 服务已通过 Compose 使用可配置的 TBOX_LAB_REMOTE 指向隔离目录，加入 OPTIONS 健康检查。`cmd/davcheck` 在 macOS 原生执行对该服务的 13 项协议检查，11 项 PASS、2 项 FAIL，并以非零退出；失败是 B11/B12，见 [协议检查记录](evidence/2026-09-17/webdav-check.json)。这是 rclone→真实 SJTU 的协议链路，不是 Finder 用户路径。Finder Computer Use 用应用名及 com.apple.finder 均返回 cgWindowNotFound，未取得可操作窗口，因此本轮没有产生 Finder 录屏，也没有标记任何系统分支通过。

通过 `patches/rclone/0001-webdav-request-guards.patch` 修复两个已复现路径；上游测试先失败后通过，完整 WebDAV 测试包通过（27.582s）。固定上游归档可逐字节重建当前补丁工作区。真实重新构建服务后 13/13 HTTP 检查通过；本次状态目录只新增一个 Committed 记录，被拒绝条件 PUT 不再进入上传准备。见 [补丁后协议记录](evidence/2026-09-17/webdav-check-patched.json)。这不解决服务端 CAS 缺失，不证明外部并发或全部条件头正确；系统门禁仍 0/36。

显式 `tbox-state -abort` 已实现：Prepared 只做本地状态转换；Uploading 验证账户、路径和 uploadId 后持久化 AbortSent，再对 K 发 upload DELETE。CommitSent/Unknown 只对账，Committed 拒绝撤销；所有路径均保留 spool。Aborted 表示已观察会话不存在且正式路径不存在，不宣称对象存储所有暂存字节已经物理回收。中止后路径仍存在时保留 AbortUnknown。

七项离线测试覆盖幂等中止、丢应答、confirm 竞争、身份/状态不匹配、会话消失但目标存在和本地 Prepared 取消。真实代理中止后丢应答从 AbortUnknown 核对至 Aborted，独立确认 K 和路径均 404，spool 保留。并发探测还确认：confirm 200 后 abort 可以 204 并删除会话记录，而正式文件仍完整存在；该分支必须保留未知状态，不能以 204 判定撤销成功。见 [中止丢应答](evidence/2026-09-17/abort-response-loss.json) 和 [提交/中止竞争](evidence/2026-09-17/confirm-abort-race.json)。不是 Finder 取消、完整历史/回收站增量或宿主机掉电验收。

已使用官方 Go 1.26.0 和固定 macFUSE 头文件构建原生 macOS ARM64 cmount 二进制，mount help 成功；实际 FUSE 探针因运行库缺失失败。没有安装驱动或修改系统安全设置。macOS 26.5.2 内置 webdavfs 只读挂载成功，完整读取、pread、空文件和 51 项目录正确；不是 Finder UI 验收。

随后用 webdavfs 创建隔离文件并写入 61 字节：open/write 成功、fsync errno=1、close 成功；独立云端仍 0 字节。状态日志同时存在 0 字节 Committed 和 61 字节 Prepared，后者 SHA-256 与测试源一致；AppleDouble 同样留下 0 字节提交和 4096 字节 Prepared，全部 spool 校验通过。普通卸载被系统进程只读句柄占用阻挡；在确认所有生成数据已持久化后，仅对此实验卷强制卸载成功，服务随后停止。证据见 [原生构建](evidence/2026-09-17/macos-native-build.json)、[webdavfs 读取](evidence/2026-09-17/macos-webdav-read.json)、[webdavfs 写入失败](evidence/2026-09-17/macos-webdav-write.json)。Finder 仍无法取得窗口，已异步请求用户打开窗口，不以系统调用替代 UI 验收。

## 本地日志落盘错误修复

`Prepare` 曾在记录 rename 已成功、随后的目录 fsync 返回错误时删除完整 spool，导致重开日志后无法恢复数据。新增故障注入测试已先复现该失败。现在完整 spool 和其目录已同步后，在尝试发布日志前便保留 spool；任何日志保存错误仍向调用者返回失败，但不再删除可能已被记录引用的数据。保存较早失败时允许留下孤立 spool，仍不自动 GC。

回归测试在第二次目录同步处注入错误，关闭并重新打开 Store，验证记录仍阻止盲目重试、完整内容和 SHA 校验可恢复。这是文件系统调用故障测试，不是宿主机掉电保证，C-003 的真实掉电验收仍未完成。

## 同进程自动恢复

后端通过 `transfer.StartWithRecovery` 处理暂时网络失败：原初始化只执行一次，恢复最多三次；分片只在核对原会话后续传，确认结果未知只读对账。失败、取消或次数耗尽均保留日志及 spool。`smh.ErrTransport` 提供脱敏的错误分类，不依赖错误字符串，也不把权限/协议/本地错误当成传输失败。重启恢复调度、长时间离线、expired K 和完整 Finder 交互仍待验收。
