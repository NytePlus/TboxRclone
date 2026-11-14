# TboxRclone

TboxRclone 是交大云盘的实验性 rclone backend。下面只说明如何构建并在 macOS 挂载；详细资料统一收录在 [`docs/`](docs/README.md)。

## 构建

先取得固定版本的 rclone 子模块并应用仓库补丁：

```sh
git submodule update --init --recursive
sh scripts/rclone-patches.sh --apply
```

推荐通过 Docker Compose 构建：

```sh
docker compose run --rm go-tests go build -o bin/tboxrclone-linux-arm64 ./cmd/tboxrclone
docker compose run --rm go-tests env GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 \
  go build -o bin/tboxrclone-darwin-arm64 ./cmd/tboxrclone
```

Linux 产物的架构取决于 Docker 主机架构；Intel Mac 请将 `GOARCH` 改为 `amd64`。纯 Go 的 macOS 交叉编译产物可提供 WebDAV 服务，但不含 FUSE mount 命令。

如需原生 FUSE mount，可在 macOS 上构建 CGO 版本：

```sh
sh scripts/build-macos-mount.sh
bin/tboxrclone-macos-mount mount --help
```

该脚本需要 Xcode 或 Command Line Tools SDK。它不会安装 macFUSE 驱动，也不会修改系统安全设置。

## 在 macOS 挂载

将空间 access token 写入 `.secrets/access-token`，文件内容为单行原文，并限制权限：

```sh
chmod 0600 .secrets/access-token
```

创建 `.secrets/rclone.conf`：

```ini
[sjtu]
type = sjtu
library_id = <libraryId>
space_id = <spaceId>
token_file = /secrets/access-token
state_dir = /state/sjtu
lab_writes = false
max_upload = 2G
```

`library_id` 和 `space_id` 必须属于同一空间。默认配置为只读；只有在隔离实验目录中需要写入时，才将 `lab_writes` 设置为 `true`。

启动本机 WebDAV 服务：

```sh
TBOX_LAB_REMOTE='sjtu:<remote-path>' \
  docker compose --profile live up -d --wait webdav
```

使用 macOS 自带的 WebDAV 文件系统挂载并核对：

```sh
python3 scripts/mount-webdav-macos.py mount
python3 scripts/mount-webdav-macos.py check
```

默认挂载点为 `/private/tmp/tboxrclone-webdav-<uid>`。不要把挂载点放在项目目录或 Docker bind mount 内。使用结束后卸载：

```sh
python3 scripts/mount-webdav-macos.py unmount
```

更完整的配置、运行恢复与限制见[文档索引](docs/README.md)。
