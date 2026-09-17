# 实施状态与发布阻塞项

当前目标范围已按用户要求更新为[单客户端并发约束](concurrency-contract.md)：服务端 CAS 缺失不再单独阻塞此产品范围，但必须实现统一路径/子树占用、实例排他及跨重启恢复保护。下述拒绝覆盖/删除等是当前代码状态，不是新契约要求永久禁用；相关功能仍需实现和验收。

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
| B13：macOS webdavfs 新文件写入（已修复已测路径） | C-001/005/008/012/014 | 原测试 fsync 为 EPERM、云端 0 字节。启用单客户端实验性覆盖后，新测试 open/write/fsync/close/reopen 全成功，云端 61 字节匹配，主文件与 AppleDouble 日志均 Committed。Finder 与完整故障/并发验收仍未完成。 |

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

## 批量上传锁竞争

后端通过 `journal.OpenContext` 等待持久日志锁，可被 context 取消；其他文件系统锁错误不进入等待循环。显式状态命令仍使用非阻塞 Open。四路 rclone copy 的真实新建文件实验已通过独立 SHA 核验，修复前为锁忙/并发 Mkdir 409 失败。Mkdir 创建竞争只读确认目标类型，不重发创建、也不将文件碰撞接受为目录成功。每个日志目录的实际上传仍串行，资源管理与吞吐验收仍待完成。

## 单客户端文件冲突拒绝

按新契约，后端在读取上传输入及等待日志锁之前取得进程内写占用。同路径竞争直接返回不可自动重试的 busy 错误；只读允许多个句柄，直到最后一个 Close 才允许写入。占用按 Client 的 endpoint/library/space 和完整远端路径建立，在多个 Fs 对象间共享，不使用 remote 名称或 state_dir 作为隔离键。失败的 Open 释放占用，重复 Close 不释放其他读者的占用。

回归通过真实 backend 方法和模拟 HTTPS 服务验证：暂停首个上传的 spool 读取后，另一 Fs/状态目录的同路径写入未消费输入即失败，同路径读失败而另一文件读可用；首个上传完成后内容可重新读取；多读者及错误 Open 的释放正确。仍未完成 VFS 前端操作级保护、跨进程实例排他、子树锁、重启占用及不同文件实际并行上传。系统验收状态不变。

后续补齐持久未决日志的读取保护：Object.Open 在取得内存读占用后查询原子发布的日志；Prepared、Uploading、CommitSent、Unknown、AbortSent、AbortUnknown 均阻止读取该路径，返回不可自动重试的 ErrPending。终态 Committed/Aborted 不再占用。损坏或不安全的日志目录直接失败，不将其当成无未决操作。快照读取不等待全局上传锁，因此其他文件读取仍可进行。模拟 HTTPS 后端回归以新 Fs 对象和重新打开的持久日志覆盖这些状态及释放后的原内容读取；这是恢复代码验证，不是实际 Finder 重启或掉电验收，也不能阻止另选状态目录的独立进程。

## 服务与恢复进程排他

`internal/instance.Claim` 对私有状态目录取得非阻塞 flock，在进程内共享并持有至退出。NewFs 和有修改动作的 tbox-state 均接入；不能同时运行服务和独立恢复命令。instance.lock 拒绝符号链接及非私有普通文件，退出不 unlink，避免锁 inode 被替换后同时取得两个锁。跨进程测试验证持有时拒绝、正常退出/SIGKILL 后可接管、同进程复用、未决文件保留、锁 inode 不变和符号链接拒绝。Docker 全量 race/vet 与原生 macOS ARM64 子进程测试均通过。

此变化尚未重启当前在线 WebDAV 服务，不应将其当作已部署。它只约束共享同一状态目录的新版本进程；不同状态目录、旧版进程和跨主机不在该锁的保护内，管理入口统一以及 Finder 系统验收仍待完成。

后续通过 `ClaimScope` 补充共享注册目录的空间级进程锁及持久 state_dir 绑定。构造 Fs 和独立恢复动作均在远端请求前校验；同一注册目录中更换 state_dir 在旧进程存活及退出后均被拒绝，原目录可以接管恢复，不同空间可运行。绑定写入使用独占创建与文件/目录 fsync，残缺绑定失败关闭，不自动删除或迁移。Docker 全量 race/vet 及 macOS 原生子进程测试通过；新增测试由独立子进程持有空间，父进程分别在其存活、退出后尝试另一目录，并验证原目录恢复成功。不同注册目录/用户、endpoint 别名、跨机器和旧版客户端仍须由使用约束排除；不是分布式锁。在线服务尚未重建应用本变更。


## 单客户端顺序覆盖实现

新增显式 `lab_overwrite`（默认 false，仍受 lab_writes 与隔离根限制）。已存在的普通文件记录 overwrite 意图及旧 ETag/大小，两个 multipart 控制请求使用相同策略；提交前检查旧身份，确认后沿用独立完整 SHA-256 对账。目录目标拒绝；未知结果不重发确认，也不执行先删后写。恢复从日志读取覆盖意图，不依赖当前配置推断。取消覆盖在上传会话消失后核对旧版本，旧版本消失或变化不标成功。

模拟服务修正为分片仅写暂存、confirm 才发布，回归验证零字节旧文件的正常保存、确认已执行后丢响应只提交一次、分片失败和取消均保留旧内容与完整新 spool、目标变化阻止确认。取消回归验证旧版本不变、变化、缺失及丢响应恢复。Docker 全量 race/vet 通过。真实服务尚未重建，也尚未启用此开关；B13 的真实 macOS 写入失败仍未复测，系统验收不变。

## 顺序覆盖的真实 macOS 复测

随后以 `f1591d2` 重建 Compose WebDAV，仍限定原隔离根，显式设置 `TBOX_LAB_OVERWRITE=true`。HTTP WebDAV 依次保存 0/60/72 字节，三次日志均 Committed，最终独立云端下载 SHA-256 与 72 字节源一致，见 [顺序覆盖](evidence/2026-09-17/sequential-overwrite.json)。

macOS 内置 mount_webdav 挂载后，用新生成文件复测原 B13 路径：open/write(61)/fsync/close 全部成功，关闭后通过挂载重读正确，独立云端下载也为完整 61 字节。旧失败证据保留，新证据见 [macOS 写入复测](evidence/2026-09-17/macos-webdav-overwrite.json)。此结果修复了已测的“创建空文件后写入被拒绝”路径；不证明全部 Finder 保存、并发、AppleDouble、网络故障或服务器发布原子性。Finder 可见挂载，但尚未执行 UI 复制或录制验收；系统分支不标 PASS。

服务与实验挂载当前保留供后续 Finder 测试使用。覆盖开关为本次启动显式启用，Compose 缺省仍为 false。

## PUT 实体标签条件修复

启用顺序覆盖后，真实 WebDAV 错误 If-Match 请求曾返回 201 并替换生成文件，见 [失败证据](evidence/2026-09-17/webdav-if-match.json)。原补丁仅检查 If-None-Match:*，不能满足一般 PUT 前置条件。

补丁 0001 现于打开截断写句柄前检查 PUT 的 If-Match/If-None-Match。支持星号、标签列表及强弱比较，拒绝格式不完整的列表；ETag 与上游 WebDAV 使用相同生成规则。新增回归先复现失败，再验证匹配条件成功、错误/弱 If-Match 拒绝、命中强弱 If-None-Match 拒绝和原内容保留。上游完整 WebDAV race 测试（27.090s）、主项目 race/vet 均通过。

重新构建隔离服务后，四种真实拒绝请求均为 412，独立云端内容未变，日志只有初始创建记录，见 [修复后证据](evidence/2026-09-17/webdav-if-match-patched.json)。这是 VFS 当前视图的前置检查，不解决检查与提交之间的竞争、陈旧缓存、默认元数据 ETag 的碰撞、其他修改方法或完整前端操作占用；不能据此标记 C-006/C-012 完成。Finder 本轮仍未执行复制。

## WebDAV 请求冲突拒绝

补丁 0001 新增可选 `exclusive_access`；Compose 正常和故障服务显式开启。请求入口在读取输入、条件检查、截断写入之前取得全部路径占用，冲突返回 423，源目标原子取得，目录变更覆盖子树。请求结束释放，包括失败。GET 读者并存，普通目录浏览不因子项上传而拒绝；ZIP 下载读占子树。非 off 的 VFS 缓存模式拒绝启用该选项，避免后台写回提前释放请求占用。

回归先复现进行中上传可被 GET/DELETE/MOVE 等穿透，修复后覆盖双写/读写、目录变更、COPY 源及 MOVE 目标、路径别名、失败后无残留占用、兄弟文件及目录浏览、多个读者及读完释放、异步缓存拒绝。上游完整 WebDAV race 测试（31.989s）、主项目 race/vet 与补丁重放逐字节比较通过。

真实服务复测通过 Expect:100-continue 暂停首个 PUT 正文，期间同路径 PUT/GET/DELETE、以其为目标的 MOVE 均 423；目录 PROPFIND 207。放行首个正文后 PUT 201、重开 GET 200、独立云端字节正确，仅一条 Committed 日志，见 [并发请求证据](evidence/2026-09-17/webdav-exclusive-access.json)。这不是 Finder 双编辑器或长期文件句柄验收，系统分支仍未通过。

开启请求冲突控制后，macOS webdavfs 普通保存再次通过 open/write/fsync/close/reopen 和独立云端 61 字节匹配，见 [普通保存回归](evidence/2026-09-17/macos-webdav-exclusive-write.json)。该回归仅证明所测正常保存路径未被新保护阻断。

## 未决文件不能被目录创建绕过

后端 Mkdir 现在逐级检查持久未决记录；未决文件路径以及需要经过它的子目录创建均拒绝。缺失目录在发送创建请求前取得进程内路径写占用，并重新检查日志及远端类型，避免初次查询后的占用变化。已有目录直接返回，不占用共享父目录来阻塞不同文件上传。服务端创建竞争仍只读核对类型，不重发 mutation。

回归以模拟 HTTPS 服务验证：首个上传尚在读取 spool 时，同路径和子路径 Mkdir 均报 busy；Unknown 日志关闭重开并换用新 Fs 后，两种目录创建均报 ErrPending，服务器没有收到任何目录 PUT，原 spool 完整。原 Mkdir 409 类型核对、批量相关路径及全量 Docker race/vet 通过。在线服务尚未重新部署此变化，Finder 与真实重启恢复验收仍待执行。

## 文件删除的持久意图与只读核对

新增默认关闭的 lab_delete，限 lab_writes 的隔离根。Remove 核对旧对象后先保存 DeleteSent，再发一次非永久删除；正常或丢响应均进入只读核对，路径仍存在/重建时保留 DeleteUnknown，不自动重放。kind 字段区分上传与删除，防止恢复命令把删除误作上传；reconcile 支持删除，上传 resume/abort 明确拒绝。零字节 intent spool 不代表原文件备份，CLI 对删除单独报告回收站凭据是否存在。

模拟 HTTPS 回归检查服务端收到 DELETE 时日志已经是 DeleteSent，并覆盖正常删除、执行后丢响应、同路径重建、权限拒绝、对账读失败后恢复、陈旧对象、开关未启用、终态再次对账不误删新文件及上传命令类型拒绝。全量 Docker race/vet 通过。

在线隔离服务随后重建并显式开启 TBOX_LAB_DELETE=true（同时包含上一轮 Mkdir 修复）。真实 WebDAV 创建201→删除204→独立云端 info404→同路径新建201，新文件独立内容匹配，删除日志 Committed，见 [文件删除证据](evidence/2026-09-17/go-trash-delete.json)。本次未取得回收站 ID，未做恢复；也未做真实删除丢响应和 Finder 删除，不能标记完整 C-017 通过。

## 数字回收站 ID 修复与恢复实测

找到此前缺失凭据的原因：实例返回整数 recycledItemId，删除响应用 Go string 解码失败，随后只读核对虽然确认路径消失，却丢了回收站 ID。现以 smh.Identifier 兼容整数/字符串并保留精确十进制，不接受负数、浮点或指数格式。回归通过完整 Remove 路径验证 9007199254740993 正确落盘。全量 Docker race/vet 通过，重建服务后新的真实删除日志已保存回收站凭据，见 [修复后删除](evidence/2026-09-17/go-trash-delete-receipt.json)。

停止服务后，回收站列表精确匹配上一轮生成条目的 originalPath/name/size。ask 恢复到已有同名新文件返回409且新内容不变；rename 恢复返回200和实验根内新名字，独立旧内容 SHA-256 正确，原路径新内容也保留，见 [回收站恢复](evidence/2026-09-17/recycle-restore.json)。随后恢复了在线服务。没有改变此前未取到 ID 的历史日志，也没有标记系统场景 PASS。恢复丢响应、自动回收站定位及 Finder 撤销仍待实现和验收。

## 空目录删除与防递归绕过

Rmdir 已在 lab_delete 下实现：取得后端子树写占用，检查所有未决子项，确认目录存在且完整列表为空，持久记录 rmdir/DeleteSent 后仅发一次非永久 directory_only 删除。与文件删除共用只读对账；未决 rmdir 对整个子树保持持久占用。上传日志统一使用 Client 规范化后的账户/空间键，避免选项与客户端 origin 形式不同绕过 pending 检查。

发现 WebDAV 的默认 RemoveAll 会先递归删除子文件，从而绕过后端非空目录拒绝。补丁新增默认关闭的 no_recursive_delete，Compose 显式开启；目录改走 VFS Remove，非空目录405，文件和空目录仍可删除。该选项也会拒绝用户有意递归删除非空目录，当前作为明确的实验限制。

回归覆盖空/非空、删除丢响应、未知结果、已有未决子项、空检查期间创建子项、后续子树上传/Mkdir 被阻止。全量 Docker race/vet、上游 WebDAV race（32.113s）及补丁重放比较通过。真实 HTTP 非空删除405且子内容不变，移除子项后空目录删除204；重新验证挂载表并挂载后，原生 macOS mkdir/rmdir 成功，两个目录均独立查询404、两条 rmdir 日志 Committed 且有回收站凭据，见 [空目录删除](evidence/2026-09-17/empty-directory-delete.json)。

首次本机尝试时旧挂载已消失，仅操作到本地挂载点目录；日志计数不符揭示该问题，证据已标无效并保留在 [首次尝试](evidence/2026-09-17/empty-directory-delete-attempt.json)。验证规格补充每次重新核对挂载表，不能沿用旧挂载状态。没有将这次无效本地操作计为网盘验收，Finder 与完整竞态场景仍未通过。
