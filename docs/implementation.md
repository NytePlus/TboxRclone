# 实施状态与发布阻塞项

日期：2026-09-17。状态：实验实现，未发布，目标未完成。

## 已有实现

- 主项目 Go module 与固定 Go 1.26.0 Docker Compose；rclone v1.75.1 submodule 保持干净。
- 注册 `sjtu` backend，提供元数据、分页、严格 Range/ETag 读取、受限创建目录、已知/未知长度的简单上传。
- 写前持久化完整内容、大小、SHA-256；日志文件 fsync、原子 rename、父目录 fsync；0700 目录与进程排他锁。
- 上传路径仅限显式启用的 `codex-api-lab/<run-id>`，使用 ask 策略并拒绝覆盖；确认后独立完整下载校验才返回成功。
- 写请求不自动重试；CommitSent/Unknown 保留本地文件；重复调用遇到 pending 日志直接报错。
- `tbox-state` 对账已发送的提交，只使用远端读取；遇到冲突保持 Unknown。
- 完整上游 `fstests.Run` 入口；未配置真实测试空间时明确 SKIP。
- 36 个系统分支的子场景目录与证据门禁。离线测试不会修改系统场景状态。
- HTTPS CONNECT 故障代理与 Compose faults 服务：双通道 allowlist、完整响应暂扣/释放、发送前/响应后断开、传输中截断。见 [实验说明](fault-proxy.md)。

## 已执行验证

具体命令及日志在工作区 `reports/`。`go test -race ./...` 共 34 个顶层测试 PASS，真实 TestIntegration 1 个 SKIP；`go vet ./...` PASS；上游 `cmd/serve/webdav` 测试 PASS（27.531s）；Linux 与 macOS ARM64 构建 PASS，macOS 本机已执行 version/backend help。离线测试涵盖 Unicode/保留字符、整数精度、JSON/HTTP 错误、异步受理不能当完成、控制面重定向不泄漏凭据、Range 被忽略/变化/短流、EOF/额外字节、分页重复、上传丢响应、日志重新打开、缓存损坏和进程锁。后端测试经过真实 HTTP/TLS 客户端与模拟服务器，不证明交大实例具有相同语义。

HTTPS 故障代理增加 9 个顶层测试，覆盖提交后丢响应的独立事实核对、控制/签名数据双通道、上传/下载截断、规则单次触发、authority 隔离和进程关闭。Compose faults 服务已启动并通过健康检查，已确认控制接口可用及非 allowlist CONNECT 返回 403；该服务验证没有访问交大云盘，检查后已停止。发现 `go run` 包装使 Compose 停止状态为 2 后，服务启动改为编译再 `exec` 二进制；重新健康启动并 SIGTERM 停止，已确认退出码为 0。

真实 SJTU fstests 已执行入口，但出现 6 个 PASS、19 个 FAIL、4 个 SKIP 子测试事件后在 240 秒处超时，完整套件未跑完；Finder、宿主机断电/崩溃及真实多客户端覆盖仍未完成。manifest 继续保持 NOT_RUN；证据门禁当前为 0/36 PASS，并按预期返回非零退出码。

## 已知缺陷/未实现能力（发布阻塞）

| 问题 | 影响条件 | 当前行为及后续要求 |
|---|---|---|
| B01：覆盖、条件删除、原子空目录删除未实现 | C-002/004/006/011/013/017 | 实测错误 content_cas/If-Match 删除条件被忽略，directory_only 删除非空目录后子项不可达。保持拒绝，需调查其他保护方式；属于发布功能缺失。 |
| B02：分片、签名续期、未提交上传续传、abort 未实现 | C-001/003/007/018 | 默认只接受最多 64 MiB 简单上传；未提交任务保留 spool 等待恢复。 |
| B03：SSO 与 accessToken 自动刷新未实现 | C-015 | token 文件可外部原子更新；无自动重放写请求。 |
| B04：服务端 Move/DirMove/Copy 与异步 task 未实现 | C-004/013/016 | 不注册可选能力，202/taskId 返回明确未完成错误。 |
| B05：WebDAV/VFS 安全保存与云端应答边界未验收 | C-005/008/012/013/014 | 上游测试通过也不能替代交大/Finder 实测；默认使用缓存 off，仅实验。 |
| B06：性能、空间管理不足 | C-018/015 | 上传先 spool、单日志目录串行、完成后全量下载验证；暂无自动 GC，总缓存会增长。 |
| B07：读与目录协议仍需实例确认 | C-009/010/014 | 强制要求返回 ETag；marker 分页异常直接失败。未验证服务端忽略参数及元数据/数据 ETag 是否相同。 |
| B08：宿主机掉电持久边界未验证 | C-003 | fsync 与日志重开测试不等于宿主机掉电测试；不能宣称掉电零丢失。 |
| B09：macOS 原生 mount 尚未接入 | 原生 mount 验收路径 | 已提供 macOS CLI 构建；Finder/WebDAV 和原生 mount 是不同路径。 |
| B10：上游静态 overview 未包含外部 backend | CLI 启动信息 | rclone 注册时输出 `no overview data found for "sjtu"`，随后能列出并配置 sjtu；需后续可复现的上游元数据接线，不能将此错误日志隐藏。 |

API `directory_only=1` 的 SDK 原文只承诺“不级联删除子文件和子目录”，并未承诺非空目录拒绝；不得用它直接实现 rmdir。SDK 1.0.16 multipart 响应描述是顶层 headers，而已有 Tbox 研究为分片签名列表，须通过真实响应确认部署版本，不能混合猜测。

已通过本机 Tbox UserToken 取得普通用户空间凭据，并完成隔离目录的简单/分片上传、重放、abort、错误条件删除、非空目录删除、同步注册及真实提交后丢响应实验。修复上传状态查询参数和外部模块 fstests fixture 定位。新增 10 个同步接口定义，见 [真实接口实验](live-api-findings.md)。

下一步：按实测 parts[number].headers 协议实现分片与续传；补充 1800 秒访问令牌自动刷新；继续调查可用的稳定身份、历史版本和 fs-journal 并发保护。真实证据应持续记录到 manifest，已知实现缺陷不能改写成符合预期。
