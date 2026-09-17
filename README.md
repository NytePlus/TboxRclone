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

将有效的空间 accessToken 保存为 `.secrets/access-token`（单行原文、权限 0600），不要提交到 Git。`library_id` 与 `space_id` 必须来自同一空间。SSO 自动登录及 token 自动续期尚未实现；token 文件每次请求重读，可原子替换该文件来更新凭据。

`.secrets/rclone.conf` 示例：

```ini
[sjtu]
type = sjtu
library_id = <libraryId>
space_id = <spaceId>
token_file = /secrets/access-token
state_dir = /state/sjtu
lab_writes = false
max_upload = 64Mi
```

只读查看：

```sh
docker compose run --rm -v "$PWD/.secrets:/secrets:ro" go-tests go run ./cmd/tboxrclone lsjson sjtu: --config /secrets/rclone.conf
```

开启写实验时，将 `lab_writes` 改为 `true`，并使用 `sjtu:codex-api-lab/<run-id>` 作为远端，挂载持久的 `/state` 卷。默认简单上传上限为 64 MiB；分片上传与自动续传尚未实现。所有上传先完整 spool，校验输入长度并 fsync，然后初始化会话、上传、确认、独立读取 SHA-256。失败后保留完整数据；不会先删除旧文件。

WebDAV 只读入口（本机 8686）：

```sh
docker compose --profile live up webdav
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

主机没有 Go 时通过 Compose 执行以上命令，并挂载对应私有目录。对账不会重发 confirm、初始化、删除或 abort。只有上传会话已确认、路径一致、完整远端内容和本地 spool 的 SHA-256 一致才记为 Committed。过期会话、InitSent 丢响应、未提交上传和内容冲突仍保留本地数据，尚不支持自动恢复。已提交 spool 也保留，不会自动垃圾回收；磁盘不足会导致上传失败。

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
