# 配置、启动与恢复

根目录 [README](../README.md) 提供最短的构建和 macOS 挂载流程。本文记录完整运行配置与恢复命令。

## 凭据与配置

将空间 access token 保存为 `.secrets/access-token`，内容为单行原文，权限为 `0600`。`library_id` 与 `space_id` 必须来自同一空间。

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

可选的 `user_token_file` 会用于取得个人空间 access token，并在过期前刷新；刷新结果必须匹配配置的 library 和 space。access token 只缓存在内存中。仅配置 `token_file` 时，每次请求都会重新读取，因此可以原子替换文件更新凭据。SSO 交互登录不在当前范围。

## 启动与挂载

只读核对：

```sh
docker compose run --rm -v "$PWD/.secrets:/secrets:ro" go-tests \
  go run ./cmd/tboxrclone lsjson sjtu: --config /secrets/rclone.conf
```

启动本机 WebDAV 服务：

```sh
TBOX_RCLONE_CONFIG=/secrets/rclone.conf \
TBOX_LAB_REMOTE='sjtu:<remote-path>' \
docker compose --profile live up -d --wait webdav
```

挂载、核对和卸载：

```sh
python3 scripts/mount-webdav-macos.py mount
python3 scripts/mount-webdav-macos.py check
python3 scripts/mount-webdav-macos.py unmount
```

默认挂载点是 `/private/tmp/tboxrclone-webdav-<uid>`。可通过 `TBOX_WEBDAV_MOUNT` 选择其他项目外路径，但不能位于 Docker bind mount 内。每次服务重启后都应重新挂载并执行 `check`，不能仅凭目录存在判断挂载有效。

## 状态所有权

Compose 使用 `/state/owners` 作为所有权注册目录，服务与恢复命令都读取 `RCLONE_SJTU_OWNERSHIP_DIR`。原生默认位置为用户配置目录下的 `TboxRclone/owners`。

一个空间首次绑定后，更换 `state_dir` 会被拒绝，避免绕过未决数据。跨宿主或容器路径迁移没有自动流程；不要删除所有权文件强行启动。

## 查看和恢复未决操作

主机没有 Go 时，通过 Compose 执行恢复工具，并将原 state 和私有凭据挂载到相同容器路径：

```sh
docker compose run --rm go-tests go run ./cmd/tbox-state \
  -state-dir /state/sjtu
```

只读对账已经发送的提交：

```sh
docker compose run --rm go-tests go run ./cmd/tbox-state \
  -state-dir /state/sjtu \
  -library-id '<libraryId>' -space-id '<spaceId>' \
  -token-file /secrets/access-token -reconcile '<operation-id>'
```

显式恢复仍有效的分片会话：

```sh
docker compose run --rm go-tests go run ./cmd/tbox-state \
  -state-dir /state/sjtu -resume '<operation-id>'
```

恢复会重新验证 spool、远端会话和已确认分片，只补传缺失分片。已提交或结果未知的操作只允许只读对账；不会重发 confirm、初始化、删除或 abort。

`-abort <operation-id>` 只中止绑定的上传会话，不删除正式文件，也不自动清理本地数据。只有云端会话已确认、路径一致、远端完整内容与本地 spool 的 SHA-256 相同，操作才会记录为 `Committed`。
