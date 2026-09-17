# 实施状态与发布阻塞项

日期：2026-09-17。状态：实验实现，未发布，目标未完成。

## 已有实现

- 主项目 Go module 与固定 Go 1.26.0 Docker Compose；rclone v1.75.1 submodule 保持干净。
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

具体命令及日志在工作区 `reports/`。`go test -race ./...` 共 47 个顶层测试 PASS，真实 TestIntegration 1 个 SKIP；`go vet ./...` PASS；上游 `cmd/serve/webdav` 测试 PASS（27.531s）；Linux 与 macOS ARM64 构建 PASS，macOS 本机已执行 version/backend help。离线测试涵盖 Unicode/保留字符、整数精度、JSON/HTTP 错误、异步受理不能当完成、控制面重定向不泄漏凭据、Range 被忽略/变化/短流、EOF/额外字节、分页重复、上传丢响应、日志重新打开、缓存损坏和进程锁。后端测试经过真实 HTTP/TLS 客户端与模拟服务器，不证明交大实例具有相同语义。

HTTPS 故障代理增加 9 个顶层测试，覆盖提交后丢响应的独立事实核对、控制/签名数据双通道、上传/下载截断、规则单次触发、authority 隔离和进程关闭。Compose faults 服务已启动并通过健康检查，已确认控制接口可用及非 allowlist CONNECT 返回 403；该服务验证没有访问交大云盘，检查后已停止。发现 `go run` 包装使 Compose 停止状态为 2 后，服务启动改为编译再 `exec` 二进制；重新健康启动并 SIGTERM 停止，已确认退出码为 0。

真实 SJTU fstests 已执行入口，但出现 6 个 PASS、19 个 FAIL、4 个 SKIP 子测试事件后在 240 秒处超时，完整套件未跑完；Finder、宿主机断电/崩溃及真实多客户端覆盖仍未完成。manifest 继续保持 NOT_RUN；证据门禁当前为 0/36 PASS，并按预期返回非零退出码。

## 已知缺陷/未实现能力（发布阻塞）

| 问题 | 影响条件 | 当前行为及后续要求 |
|---|---|---|
| B01：覆盖、条件删除、原子空目录删除未实现 | C-002/004/006/011/013/017 | 实测错误 content_cas/If-Match 删除条件被忽略，覆盖 confirm 与移动的源/目标 CAS 也被忽略；directory_only 删除非空目录后子项不可达。保持拒绝，需调查其他保护方式；属于发布功能缺失。 |
| B02：分片恢复尚未完成产品级验收 | C-001/003/007/018 | 已实现及实测原会话分片续签/显式恢复；默认 spool 上限 64 MiB 可配置，固定 4 MiB 片最多 10000 片。尚缺自动恢复调度、过期会话处理、abort 命令、批量/超大文件与完整故障矩阵。 |
| B03：SSO 交互登录与长时会话验收未完成 | C-015 | 已有 UserToken 文件可自动获取/刷新个人空间 accessToken，缓存仅在内存；并发合并、提前续期、账户空间核验、凭据轮换检测；401/403 只使缓存失效，不重放原请求。真实 1800 秒到期和 SSO 重新认证仍待验收。 |
| B04：服务端 Move/DirMove/Copy 与异步 task 未实现 | C-004/013/016 | 不注册可选能力，202/taskId 返回明确未完成错误。 |
| B05：WebDAV/VFS 安全保存与云端应答边界未验收 | C-005/008/012/013/014 | 上游测试通过也不能替代交大/Finder 实测；默认使用缓存 off，仅实验。 |
| B06：性能、空间管理不足 | C-018/015 | 上传先 spool、单日志目录串行、完成后全量下载验证；暂无自动 GC，总缓存会增长。 |
| B07：读与目录协议仍需实例确认 | C-009/010/014 | 强制要求返回 ETag；marker 分页异常直接失败。未验证服务端忽略参数及元数据/数据 ETag 是否相同。 |
| B08：宿主机掉电持久边界未验证 | C-003 | fsync 与日志重开测试不等于宿主机掉电测试；不能宣称掉电零丢失。 |
| B09：macOS 原生 mount 尚未接入 | 原生 mount 验收路径 | 已提供 macOS CLI 构建；Finder/WebDAV 和原生 mount 是不同路径。 |
| B10：上游静态 overview 未包含外部 backend | CLI 启动信息 | rclone 注册时输出 `no overview data found for "sjtu"`，随后能列出并配置 sjtu；需后续可复现的上游元数据接线，不能将此错误日志隐藏。 |
| B11：WebDAV 重复 MKCOL 返回错误状态 | C-010/014，WebDAV MKCOL | 真实端到端连续两次 MKCOL 均 201，应对已存在目录返回 405；上游 VFS 的幂等 Mkdir 泄漏到了 WebDAV。 |
| B12：WebDAV PUT 条件头未正确阻止写入流程 | C-006/014，If-None-Match | 已有文件 PUT If-None-Match:* 返回 405 而非 412，且产生 Prepared 上传日志；原内容由后端拒绝覆盖保住，不能当作条件请求已实现。 |

API `directory_only=1` 的 SDK 原文只承诺“不级联删除子文件和子目录”，并未承诺非空目录拒绝；不得用它直接实现 rmdir。SDK 1.0.16 multipart 响应描述是顶层 headers，但真实实例已确认使用逐片签名映射；`partNumberRange` 是明确片号列表，并非区间端点。

已通过本机 Tbox UserToken 取得普通用户空间凭据，并完成隔离目录的简单/分片上传、重放、abort、错误条件删除、非空目录删除、同步注册及真实提交后丢响应实验。修复上传状态查询参数和外部模块 fstests fixture 定位。新增 10 个同步接口定义，见 [真实接口实验](live-api-findings.md)。

下一步：完善分片自动恢复和会话过期行为；完成 1800 秒真实到期和 SSO 续期验收；继续调查可用的稳定身份、历史版本和 fs-journal 并发保护。真实证据应持续记录到 manifest，已知实现缺陷不能改写成符合预期。

本轮新增分片续签和恢复实现，离线覆盖逐片签名列表、应答状态、丢分片应答后日志重开、丢 confirm 应答不重放、续签身份变化、账号/缓存/分片摘要/远端内容不一致。真实三片文件完成独立 SHA-256 校验；真实代理丢失数据面 200 应答后，另一个进程复用原会话恢复并通过独立校验，见 [分片丢应答证据](evidence/2026-09-17/multipart-response-loss.json)。并发实验中第 3 片已持久化确认；其他未确认片补传，未重新初始化。

新增 UserToken 驱动的个人空间令牌管理，受控时钟验证提前刷新，竞态测试覆盖并发刷新与等待者取消、凭据轮换和账户隔离；真实 Go 客户端在 accessToken 文件不存在时成功列出隔离目录，见 [自动取令牌](evidence/2026-09-17/automatic-token.json)。未用该实验替代真实令牌过期/SSO UI 场景。

新增实验确认简单上传不能通过 renew 续签，而空文件和微小文件可用单片 multipart 完整提交。后端统一走分片恢复状态机，并直接利用初始化签名，省去新建时重复的状态查询、续签及 spool 重读。新增 0/1/4 MiB 边界±1 覆盖。真实 Linux 容器内 SIGINT 和 SIGKILL 在数据面应答暂扣时分别执行，新进程均从原会话恢复，独立 SHA-256 一致；这不是 macOS/Finder 或掉电测试。

新发现 `ask` 只在 confirm 才阻止竞争创建（第二个会话 409），同名初始化仍 201；错误 CAS 不能保护覆盖和移动。目录专用移动 204 后子项仍可达，但缺少可靠身份及并发前置条件，因此暂不注册 Move/DirMove。签名接口可一次返回明确列举的 50 和 51 个授权，前端“50”不能当成服务端硬上限。详见 [真实接口实验](live-api-findings.md)。

WebDAV 服务已通过 Compose 使用可配置的 TBOX_LAB_REMOTE 指向隔离目录，加入 OPTIONS 健康检查。`cmd/davcheck` 在 macOS 原生执行对该服务的 13 项协议检查，11 项 PASS、2 项 FAIL，并以非零退出；失败是 B11/B12，见 [协议检查记录](evidence/2026-09-17/webdav-check.json)。这是 rclone→真实 SJTU 的协议链路，不是 Finder 用户路径。Finder Computer Use 用应用名及 com.apple.finder 均返回 cgWindowNotFound，未取得可操作窗口，因此本轮没有产生 Finder 录屏，也没有标记任何系统分支通过。
