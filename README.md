# TboxRclone

Go 实现的交大云盘实验性 rclone backend。rclone v1.75.1 通过 `third_party/rclone` Git submodule 固定，主项目通过 Go `replace` 引用；无需修改或复制上游源码。

**尚未达到 macOS 网盘发布标准。** 已执行部分真实云盘 API 实验；Finder、宿主机崩溃验收未完成。36 个系统验收分支没有任何一个被标记为通过。当前禁止覆盖、删除和目录删除；只在隔离实验目录开放显式启用的创建操作。功能阻塞项和测试结果见 [实施进度](docs/implementation.md)。

## 构建与离线测试

```sh
git submodule update --init --recursive
docker compose run --rm go-tests go test -race ./...
docker compose run --rm go-tests go vet ./...
docker compose run --rm go-tests go build -o bin/tboxrclone-linux-arm64 ./cmd/tboxrclone
docker compose run --rm go-tests env GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go build -o bin/tboxrclone-darwin-arm64 ./cmd/tboxrclone
```

Linux 二进制架构取决于 Docker 主机架构。macOS Intel 构建用 `GOARCH=amd64`。当前 CLI 提供 `copy/copyto/cat/check/lsf/lsjson/mkdir/config/serve webdav`；原生 mount 尚未接入。

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

Compose 使用 `--vfs-cache-mode off`。这只是实验配置，不代表已经满足 Finder 的随机写、安全保存或跨盘移动契约。不要用正式数据试验跨盘移动。

## 中断与对账

日志目录必须为绝对路径且权限 0700。每个上传有 `<id>.json` 和 `<id>.data`，目录级进程锁避免两个进程同时修改日志。当前单个日志目录串行上传；并发请求得到明确 busy 错误。

```sh
go run ./cmd/tbox-state -state-dir /absolute/private/state
# 在同一账户/空间上，仅通过读取对账已发送的提交：
go run ./cmd/tbox-state -state-dir /absolute/private/state \
  -library-id '<libraryId>' -space-id '<spaceId>' \
  -token-file /absolute/private/access-token -reconcile '<operation-id>'
```

主机没有 Go 时通过 Compose 执行以上命令，并挂载对应私有目录。恢复工具也支持 `-user-token-file /secrets/user-token -organization-id 1` 自动取得令牌。鉴权失败会使缓存失效，但不会自动重放刚才的请求，尤其不会重放写操作。对账不会重发 confirm、初始化、删除或 abort。只有上传会话已确认、路径一致、完整远端内容和本地 spool 的 SHA-256 一致才记为 Committed。分片 Uploading 状态可以使用相同命令的 `-resume <operation-id>` 显式恢复：重新验证本地数据、远端会话与已确认分片，续签后补传未确认片；已提交/Unknown 状态仅只读对账。片号列表不是区间；丢失应答的分片会在原 uploadId/partNumber 上重传相同字节。过期会话、InitSent 丢响应、旧版简单上传中断和内容冲突仍保留本地数据，不盲目创建新会话，也不自动恢复。已提交 spool 也保留，不会自动垃圾回收；磁盘不足会导致上传失败。

## WebDAV 协议检查

服务指向隔离实验目录后，可执行：

```sh
# Mac 本机没有 Go 时先用 Docker 构建原生检查器
# 此命令以 Apple Silicon 为例。
docker compose run --rm go-tests env GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go build -o bin/davcheck ./cmd/davcheck
bin/davcheck -url http://127.0.0.1:8686/ -allow-lab-writes > reports/webdav-check.json
```

检查器只接受 loopback IP，以随机目录创建固定测试数据并保留现场。任一 HTTP 状态或内容不满足契约就返回非零；当前实测 11 项通过、2 项失败：重复 MKCOL 为 201 而非 405，条件 PUT 为 405 而非 412。这是协议诊断，不是 Finder 验收，也不更新 manifest 为 PASS。

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
