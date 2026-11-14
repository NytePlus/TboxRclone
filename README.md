# TboxRclone

TboxRclone 是交大云盘的实验性 rclone backend。macOS 用户可以直接运行原生 WebDAV 服务并使用系统文件管理器挂载，日常使用可独立于 Docker。

## 构建 macOS 版本

取得固定版本的 rclone 子模块并应用仓库补丁：

```sh
git submodule update --init --recursive
sh scripts/rclone-patches.sh --apply
```

构建原生 WebDAV 版本：

```sh
sh scripts/build-macos-webdav.sh
```

Apple Silicon Mac 会在没有 Go 时自动下载并校验固定的 Go 1.26.0，缓存只保存在项目的 `.state/native-build`。构建结果是：

```text
bin/tboxrclone-darwin-arm64
bin/tbox-state-darwin-arm64
```

脚本最后会输出程序版本和 SHA-256。Intel Mac 需要预先安装 Go 1.26.0，或通过 `TBOX_GO=/absolute/path/to/go` 指定工具链；产物名为 `bin/tboxrclone-darwin-amd64`。

## 配置

先创建私有配置目录，再将空间 access token 以单行原文写入 `.secrets/access-token`：

```sh
mkdir -p .secrets
chmod 0700 .secrets
# 写入 access-token 后执行：
chmod 0600 .secrets/access-token
```

创建 `.secrets/rclone-macos.conf`：

```ini
[sjtu]
type = sjtu
library_id = <libraryId>
space_id = <spaceId>
token_file = /absolute/path/to/TboxRclone/.secrets/access-token
lab_writes = false
lab_overwrite = false
lab_move = false
lab_delete = false
max_upload = 2G
max_spool = 4G
request_timeout = 2m
read_retries = 2
read_retry_delay = 200ms
transfer_retries = 3
transfer_retry_delay = 1s
multipart_part_size = 4M
multipart_concurrency = 4
multipart_batch_size = 50
max_active_mutations = 4
```

`token_file` 必须是本机绝对路径；`library_id` 和 `space_id` 必须属于同一空间。默认配置只读。

**实验性内容：`lab_writes`、`lab_overwrite`、`lab_move` 和 `lab_delete` 控制尚未作为稳定产品能力发布的云端写入、覆盖、移动和删除功能。这四项默认必须全部保持为 `false`；了解风险并阅读[产品功能边界](docs/product-boundaries.md)后，可在专用目录中按需启用。启动命令中的 `sjtu:<remote-path>` 同时是唯一的写权限边界；首次启用写入时请选择 `sjtu:mac_cloud/` 这类非空远端根。**

## 启动并挂载

在一个终端中启动原生 WebDAV 服务：

```sh
scripts/run-macos-webdav.sh 'sjtu:<remote-path>'
```

服务以前台方式监听 `127.0.0.1:8686`，运行状态、所有权记录和 VFS 临时目录保存在项目的 `.state/`。保持该终端运行；需要停止服务时按 `Ctrl-C`。需要更换本机端口时，同时设置启动端的 `TBOX_WEBDAV_ADDR` 和挂载端的 `TBOX_WEBDAV_URL`，两者都只允许回环地址，例如：

```sh
TBOX_WEBDAV_ADDR=127.0.0.1:9686 scripts/run-macos-webdav.sh 'sjtu:<remote-path>'
TBOX_WEBDAV_URL=http://127.0.0.1:9686/ python3 scripts/mount-webdav-macos.py mount
```

在另一个终端中使用 macOS 自带的 WebDAV 文件系统挂载：

```sh
python3 scripts/mount-webdav-macos.py mount
python3 scripts/mount-webdav-macos.py check
```

默认挂载点为：

```text
/private/tmp/tboxrclone-webdav-<uid>
```

也可以在挂载前用 `TBOX_WEBDAV_MOUNT=/absolute/path` 指定其他项目外目录。挂载点应位于项目和 `.state` 之外。

使用结束后先卸载，再停止 WebDAV 服务：

```sh
python3 scripts/mount-webdav-macos.py unmount
```

配置续期、未决操作恢复和使用限制见[文档索引](docs/README.md)。
