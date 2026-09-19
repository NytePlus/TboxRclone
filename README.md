# TboxRclone

目标使用约束见[单客户端并发契约](docs/concurrency-contract.md)：单个受控服务实例，同路径冲突直接拒绝；不同文件及分片允许并行。该约束尚未完整实现，不能据此认为当前写入能力已经可发布。

服务和恢复命令必须共享所有权注册目录与原状态目录。后端可设置 `ownership_dir`；`tbox-state` 使用相同 `--ownership-dir`。Compose 已设置 `RCLONE_SJTU_OWNERSHIP_DIR=/state/owners`，恢复命令也读取此环境变量。原生缺省位置为用户配置目录下的 `TboxRclone/owners`。一个空间首次绑定后，换 state_dir 会被拒绝，防止绕过未决数据；不要通过删除注册文件或换注册目录强行启动。跨宿主/容器迁移尚未提供自动流程。

Go 实现的交大云盘实验性 rclone backend。rclone v1.75.1 通过 `third_party/rclone` Git submodule 固定，主项目通过 Go `replace` 引用；必要的上游修复保存在 `patches/rclone/`，按固定版本重放。

**尚未达到 macOS 网盘发布标准。** 已执行部分真实云盘 API 实验；Finder、宿主机崩溃验收未完成。36 个系统验收分支没有任何一个被标记为通过。覆盖、文件删除和空目录删除默认关闭，非空目录递归删除仍拒绝；隔离实验目录可显式启用创建，以及单客户端契约下的实验性顺序覆盖（lab_overwrite=true）及文件/空目录回收站删除（lab_delete=true）。Compose 入口由 Tbox WebDAV guard 统一裁决请求冲突，内部 rclone WebDAV core 仍以 VFS cache off 和 no-recursive-delete 运行。功能阻塞项和测试结果见 [实施进度](docs/implementation.md)。

## 构建与离线测试

```sh
git submodule update --init --recursive
sh scripts/rclone-patches.sh --apply
docker compose run --rm go-tests go test -race ./...
docker compose run --rm go-tests go vet ./...
docker compose run --rm go-tests go build -o bin/tboxrclone-linux-arm64 ./cmd/tboxrclone
docker compose run --rm go-tests env GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go build -o bin/tboxrclone-darwin-arm64 ./cmd/tboxrclone
```

子模块 gitlink 保持上游固定提交，工作区带有可重放补丁；不要把这部分差异误当成未保存代码。补丁和对应回归测试由主仓库跟踪。Compose 构建及服务启动会先检查补丁是否就绪，不会修改只读服务卷。升级子模块前必须显式重做补丁及回归。

Linux 二进制架构取决于 Docker 主机架构。macOS Intel 构建用 `GOARCH=amd64`。当前 CLI 提供 `copy/copyto/cat/check/lsf/lsjson/mkdir/config/serve webdav`，并接入上游挂载命令。Linux 常规构建含 mount；macOS Apple Silicon 的 mount 需要下述原生 CGO 构建，纯 Go 交叉编译版本不含该命令。

## macOS 原生 mount 构建

```sh
sh scripts/build-macos-mount.sh
bin/tboxrclone-macos-mount mount --help
```

脚本需要本机 Xcode/Command Line Tools SDK。若没有 Go，会在项目 `.state/native-build` 下载并校验官方 Go 1.26.0；使用固定版本 macFUSE 头文件，以 `CGO_ENABLED=1 -tags cmount` 构建。缓存及下载不会修改全局 Go 环境。已有其他路径的 Go 可用 `TBOX_GO` 指定。

构建成功不代表运行时可挂载：本机实测缺少 FUSE 运行库，返回 `cgofuse: cannot find FUSE`；脚本不安装驱动、不修改系统安全设置。

**macOS 自带 WebDAV 文件系统当前只读路径已有实测，写入尚不可用。** 新文件操作会先在云端创建空文件，后续内容上传因后端禁止覆盖而失败。实测 `fsync` 报错但 `close` 成功、云端仍为空文件；完整内容保留在 Prepared 日志。不要将此实验版本用于需要“关闭即云端保存”的工作流。

## 配置实验环境

将有效的空间 accessToken 保存为 `.secrets/access-token`（单行原文、权限 0600），不要提交到 Git。`library_id` 与 `space_id` 必须来自同一空间。可选择配置 `user_token_file`，客户端自动取得个人空间 accessToken，在过期前刷新并合并并发刷新请求；此文件必须为私有普通文件（0600）。刷新响应必须匹配配置的 library_id/space_id，不能悄悄切换账户空间。accessToken 只缓存在内存，不写配置；SSO 交互登录仍未实现。只配置 `token_file` 时每次请求重读，可原子替换该文件来更新凭据。

`.secrets/rclone.conf` 示例：

```ini
[sjtu]
type = sjtu
library_id = <libraryId>
space_id = <spaceId>
token_file = /secrets/access-token
# 可选：使用已有 UserToken 自动取个人空间令牌，优先于 token_file
# user_token_file = /secrets/user-token
# organization_id = 1
state_dir = /state/sjtu
lab_writes = false
max_upload = 64Mi
```

只读查看：

```sh
docker compose run --rm -v "$PWD/.secrets:/secrets:ro" go-tests go run ./cmd/tboxrclone lsjson sjtu: --config /secrets/rclone.conf
```

开启写实验时，将 `lab_writes` 改为 `true`，并使用 `sjtu:codex-api-lab/<run-id>` 作为远端，挂载持久的 `/state` 卷。默认 spool 上限为 64 MiB，可配置；所有文件（包括空文件）统一采用可续签的 multipart；4 MiB 分片、最多 4 片并发，最多 10000 片。新建时直接使用初始化返回的签名，恢复时才查询并续签。已支持原会话显式续传，自动恢复尚未实现。所有上传先完整 spool，校验输入长度并 fsync，然后初始化会话、上传、确认、独立读取 SHA-256。失败后保留完整数据；不会先删除旧文件。

WebDAV 实验入口（本机 8686）：

```sh
TBOX_LAB_REMOTE='sjtu:codex-api-lab/<run-id>' docker compose --profile live up -d --wait webdav
```

Finder 验收必须使用隔离实验配置，并显式开启该配置中的 `lab_writes = true`；默认的 `/secrets/rclone.conf` 保持只读安全设置。将专用配置放在 `.secrets/rclone-finder.conf` 后，可通过 `TBOX_RCLONE_CONFIG=/secrets/rclone-finder.conf` 选择它：

```sh
TBOX_RCLONE_CONFIG=/secrets/rclone-finder.conf \
TBOX_LAB_REMOTE='sjtu:codex-api-lab/<run-id>' \
docker compose --profile live up -d --wait webdav
```

macOS 挂载必须放在项目及 Docker bind mount 之外，避免将服务自己的网盘再暴露给容器文件共享：

```sh
python3 scripts/mount-webdav-macos.py mount
python3 scripts/mount-webdav-macos.py check
# 完成后：python3 scripts/mount-webdav-macos.py unmount
```

默认位置为 `/private/tmp/tboxrclone-webdav-<uid>`。可用 `TBOX_WEBDAV_MOUNT` 指定其他项目外路径，但不能位于任何额外的 Docker bind mount 内。入口拒绝项目内及与项目重叠的路径；不再使用 `.state/webdav-mount`。临时目录仅用作挂载点，恢复日志仍位于原 `.state`，不会迁移或清除。每次测试先运行 check，不能只检查目录存在。

Finder 拖拽前还要准备本地 fixture，确保它由当前普通用户拥有，避免从 Docker 导出的文件触发系统密码提示：

```sh
scripts/prepare-finder-fixture.sh /private/tmp/tboxrclone-finder-<run-id>
```

`finder-drag.swift` 必须从已授予“屏幕录制”和“辅助功能”的 Terminal（或 Codex Computer Use）进程运行；受限 shell 启动的 Swift 子进程可能看不到 Finder 窗口。入口是版本化 wrapper `scripts/finder-drag.sh`，它会先执行 Swift 语法检查并使用可写 ModuleCache：

```sh
scripts/finder-drag.sh \
  SOURCE_TITLE TARGET_TITLE FILENAME
```

Compose 使用 `--vfs-cache-mode off`。这只是实验配置，不代表已经满足 Finder 的随机写、安全保存或跨盘移动契约。不要用正式数据试验跨盘移动。

## 中断与对账

日志目录必须为绝对路径且权限 0700。每个上传有 `<id>.json` 和 `<id>.data`，进程生命周期所有权锁排除第二服务或恢复进程。后端在持有路径占用后共享日志锁，不同文件可并行；同路径冲突立即拒绝。每个空间最多 4 个活跃变更，每个上传最多 4 个分片 worker；等待容量可取消。独立恢复仍独占日志锁。

```sh
go run ./cmd/tbox-state -state-dir /absolute/private/state
# 在同一账户/空间上，仅通过读取对账已发送的提交：
go run ./cmd/tbox-state -state-dir /absolute/private/state \
  -library-id '<libraryId>' -space-id '<spaceId>' \
  -token-file /absolute/private/access-token -reconcile '<operation-id>'
```

主机没有 Go 时通过 Compose 执行以上命令，并挂载对应私有目录。恢复工具也支持 `-user-token-file /secrets/user-token -organization-id 1` 自动取得令牌。`-abort <operation-id>` 可显式中止未提交会话；只删除绑定的上传 K，不删除正式文件，也不清理本地数据。中止应答丢失后再次执行该命令，会先读取会话和正式路径；会话消失但正式路径存在时保持 AbortUnknown。鉴权失败会使缓存失效，但不会自动重放刚才的请求，尤其不会重放写操作。对账不会重发 confirm、初始化、删除或 abort。只有上传会话已确认、路径一致、完整远端内容和本地 spool 的 SHA-256 一致才记为 Committed。分片 Uploading 状态可以使用相同命令的 `-resume <operation-id>` 显式恢复：重新验证本地数据、远端会话与已确认分片，续签后补传未确认片；已提交/Unknown 状态仅只读对账。片号列表不是区间；丢失应答的分片会在原 uploadId/partNumber 上重传相同字节。过期会话、InitSent 丢响应、旧版简单上传中断和内容冲突仍保留本地数据，不盲目创建新会话，也不自动恢复。已提交 spool 也保留，不会自动垃圾回收；磁盘不足会导致上传失败。

## WebDAV 协议检查

服务指向隔离实验目录后，可执行：

```sh
# Mac 本机没有 Go 时先用 Docker 构建原生检查器
# 此命令以 Apple Silicon 为例。
docker compose run --rm go-tests env GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go build -o bin/davcheck ./cmd/davcheck
bin/davcheck -url http://127.0.0.1:8686/ -allow-lab-writes > reports/webdav-check.json
```

检查器只接受 loopback IP，以随机目录创建固定测试数据并保留现场。任一 HTTP 状态或内容不满足契约就返回非零；固定上游原始版本曾有两项失败；应用当前补丁后实测 13/13 通过，重复 MKCOL 为 405、已有资源的 PUT If-None-Match:* 为 412。补丁只检查 VFS 可见状态，外部修改、缓存失效和其他条件头仍待验证。这是协议诊断，不是 Finder 验收，也不更新 manifest 为 PASS。

## 验收

[原始调研](docs/README.md) · [接口](docs/api.md) · [验证规格](docs/verification.md) · [机器清单](test-manifest.json) · [证据格式](docs/evidence-format.md) · [HTTPS 故障代理](docs/fault-proxy.md)

```sh
# 运行完整上游 WebDAV 测试（上游本地后端；不是交大/Finder 验收）
docker compose run --rm go-tests go test github.com/rclone/rclone/cmd/serve/webdav -count=1
# 输出全部分支和子场景；未全通过时退出 1
# 这是证据完整性门禁，不会代替执行 Finder 实验。
docker compose run --rm go-tests go run ./cmd/verify
```

完整 SJTU `fstests` 使用 `backend/sjtu/integration_test.go`。必须显式提供 `TBOX_LIVE_REMOTE=TestSjtu:codex-api-lab/<run-id>` 及私有配置。未提供时显示 SKIP/BLOCKED，不能当成通过。真实全套入口已执行并观察到删除/清理相关失败，240 秒处超时，尚未覆盖全部子测试；不能通过筛掉失败项来改变结果。见 [真实接口实验](docs/live-api-findings.md)。

Finder 测试入口固定版本 `2026-09-20.2` 支持先打开并排列两个独立列表窗口：

```sh
scripts/finder-drag.sh --open SOURCE_DIRECTORY TARGET_DIRECTORY
```

然后使用上述标题及文件名执行拖拽。2026-09-20 的 sequential-001 实测完成 Finder 拷贝，独立云端下载 65,536 字节与源 SHA-256 一致；用户确认全过程无弹窗。证据位于 `docs/evidence/2026-09-20/sequential-001`。这不代表全部系统场景或旧未决路径已通过。

固定测试流程：

```sh
scripts/finder-drag.sh --preflight SOURCE TARGET FILE MOUNT
scripts/finder-drag.sh --record SOURCE TARGET FILE MOUNT NEW_EVIDENCE_DIR
```

预检拒绝复用已有目标文件或带未决 guard 记录的路径，不删除记录。录制模式先打开窗口，再启动所有活动显示器的 15 秒录屏，正常等待录制结束，并保存命令日志。文件存在仅证明录像已输出，还需验证可解码、审阅全过程、确认拷贝结束和内容一致；若录制结束时操作仍在进行，录像不能证明全过程无弹窗；须单独记录用户全程观察或其他完整证据。当前指定的 20260919125000 目标已有未决记录，预检会阻止重复基线测试。

录制入口退出码：`1` 表示执行或录制失败，`2` 表示预检或参数阻止执行，`3` 表示证据已采集但尚待完整审阅和内容校验。录制入口不会返回代表验收通过的 `0`。

若目的是复现原路径的未决记录错误，可显式使用 `finder-drag.sh --record-repro SOURCE TARGET FILE MOUNT NEW_EVIDENCE_DIR`。它只放行未决记录这一项前置条件，记录警告和 `fresh_acceptance_eligible=false`；不删除记录，也不构成正式验收。录屏不可用时仍会在拖拽前停止。

该已验证服务配置要求启用零字节 guard 及 `TBOX_LAB_OVERWRITE=true`，仅用于既定单客户端隔离测试根。覆盖使用持久日志和内容校验，旧未决状态不得清除。
