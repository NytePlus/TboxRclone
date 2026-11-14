# 配置、启动与恢复

根目录 [README](../README.md) 提供最短的原生构建和 macOS 挂载流程。日常使用不需要 Docker；Docker Compose 仅用于开发和测试。

## 凭据与配置

将空间 access token 保存为 `.secrets/access-token`，内容为单行原文，权限为 `0600`。`library_id` 与 `space_id` 必须来自同一空间。

原生配置 `.secrets/rclone-macos.conf`：

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

可选的 `user_token_file` 会用于取得个人空间 access token，并在过期前刷新；刷新结果必须匹配配置的 library 和 space。access token 只缓存在内存中。仅配置 `token_file` 时，每次请求都会重新读取，因此可以原子替换文件更新凭据。SSO 交互登录不在当前范围。

## 原生启动与挂载

构建当前 Mac 架构的服务和恢复工具：

```sh
sh scripts/build-macos-webdav.sh
```

启动服务：

```sh
scripts/run-macos-webdav.sh 'sjtu:<remote-path>'
```

启动脚本默认使用 `.secrets/rclone-macos.conf`，并把持久状态、所有权注册和 VFS 临时目录分别放在 `.state/sjtu`、`.state/owners` 和 `.state/vfs`。可以通过 `TBOX_RCLONE_CONFIG`、`TBOX_STATE_DIR`、`TBOX_OWNERSHIP_DIR` 和 `TBOX_VFS_CACHE_DIR` 覆盖，但状态与所有权路径必须始终保持一致。监听地址可通过 `TBOX_WEBDAV_ADDR` 配置，挂载端必须用 `TBOX_WEBDAV_URL` 指向同一个回环地址。

挂载、核对和卸载：

```sh
python3 scripts/mount-webdav-macos.py mount
python3 scripts/mount-webdav-macos.py check
python3 scripts/mount-webdav-macos.py unmount
```

默认挂载点是 `/private/tmp/tboxrclone-webdav-<uid>`。可通过 `TBOX_WEBDAV_MOUNT` 选择其他项目外路径。每次服务重启后都应重新挂载并执行 `check`，不能仅凭目录存在判断挂载有效。

## 状态所有权

原生启动脚本使用 `.state/owners` 作为所有权注册目录。服务与恢复命令必须使用同一个绝对 `state_dir` 和 `ownership_dir`。

一个空间首次绑定后，更换 `state_dir` 会被拒绝，避免绕过未决数据。跨宿主迁移没有自动流程；不要删除所有权文件强行启动。

## 查看和恢复未决操作

恢复前必须停止正在运行的 WebDAV 服务。Apple Silicon 使用以下命令；Intel Mac 将文件名中的 `arm64` 改为 `amd64`。

查看状态：

```sh
RCLONE_SJTU_OWNERSHIP_DIR="$PWD/.state/owners" \
  bin/tbox-state-darwin-arm64 -state-dir "$PWD/.state/sjtu"
```

只读对账已经发送的提交：

```sh
RCLONE_SJTU_OWNERSHIP_DIR="$PWD/.state/owners" \
  bin/tbox-state-darwin-arm64 \
  -state-dir "$PWD/.state/sjtu" \
  -library-id '<libraryId>' -space-id '<spaceId>' \
  -token-file "$PWD/.secrets/access-token" -reconcile '<operation-id>'
```

显式恢复仍有效的分片会话：

```sh
RCLONE_SJTU_OWNERSHIP_DIR="$PWD/.state/owners" \
  bin/tbox-state-darwin-arm64 \
  -state-dir "$PWD/.state/sjtu" \
  -library-id '<libraryId>' -space-id '<spaceId>' \
  -token-file "$PWD/.secrets/access-token" -resume '<operation-id>'
```

恢复会重新验证 spool、远端会话和已确认分片，只补传缺失分片。已提交或结果未知的操作只允许只读对账；不会重发 confirm、初始化、删除或 abort。

`-abort <operation-id>` 只中止绑定的上传会话，不删除正式文件，也不自动清理本地数据。`-prune-committed <operation-id>` 可在服务停止后释放已提交操作的本地 spool，同时保留 journal 回执。
