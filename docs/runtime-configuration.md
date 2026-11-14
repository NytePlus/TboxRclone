# 运行常量与配置项

生产代码的默认值和共享上限统一定义在 `internal/sjtu/defaults`。同一个值不得在 backend、transfer、recovery 或命令行工具中另行复制。

## rclone conf

下列重要运行策略可在 `[sjtu]` 中配置：

| 配置项 | 默认值 | 约束或作用 |
|---|---:|---|
| `endpoint` | `https://pan.sjtu.edu.cn` | 必须是无用户信息、查询和片段的 HTTPS origin |
| `organization_id` | `1` | UserToken 换取 AccessToken 时使用 |
| `max_upload` | `2G` | 单次 spool 上限，最大 1 TiB |
| `max_spool` | `4G` | journal 管理的总逻辑 spool 预算，最大 1 PiB |
| `request_timeout` | `2m` | 单个 SMH HTTP 请求超时 |
| `read_retries` | `2` | GET/HEAD 传输错误的重试次数，范围 0–10 |
| `read_retry_delay` | `200ms` | 只读重试的线性基础延迟 |
| `transfer_retries` | `3` | multipart 瞬态失败后的恢复次数，范围 0–10；不会重放 confirm |
| `transfer_retry_delay` | `1s` | multipart 恢复的指数退避基础延迟 |
| `multipart_part_size` | `4M` | 仅用于新任务；journal 持久化任务实际值，范围 1–64 MiB |
| `multipart_concurrency` | `4` | 单文件并发分片数，范围 1–32 |
| `multipart_batch_size` | `50` | 每次申请的签名分片数，范围 1–50 |
| `max_active_mutations` | `4` | 同一进程、同一空间的并发写操作数，范围 1–64 |

认证路径、状态目录、所有权目录、文件名编码和四个实验写入开关也属于 `[sjtu]` 配置。启动命令中的 `sjtu:<remote-path>` 是写权限边界，不另设重复的目录变量。

## 原生启动环境

监听与本机挂载不属于云端 backend conf，由启动环境控制：

| 环境变量 | 默认值 | 说明 |
|---|---|---|
| `TBOX_WEBDAV_ADDR` | `127.0.0.1:8686` | 原生服务监听地址，只接受回环地址 |
| `TBOX_WEBDAV_URL` | `http://127.0.0.1:8686/` | macOS 挂载 URL，只接受回环 HTTP URL |
| `TBOX_WEBDAV_MOUNT` | `/private/tmp/tboxrclone-webdav-<uid>` | 本机挂载点，不得与项目目录重叠 |
| `TBOX_DIR_CACHE_TIME` | `5s` | rclone WebDAV 目录缓存时间 |
| `TBOX_STATE_DIR` | `.state/sjtu` | 持久 journal/spool；不能随意切换 |
| `TBOX_OWNERSHIP_DIR` | `.state/owners` | 空间到状态目录的所有权注册 |
| `TBOX_VFS_CACHE_DIR` | `.state/vfs` | VFS 临时目录；内容缓存模式仍固定为 off |

Compose 的宿主 WebDAV 端口使用 `TBOX_WEBDAV_PORT`，容器内部端口保持为 Compose 私有实现细节。

## 不开放配置的常量

以下值是协议结构、安全上限或持久格式的一部分，只统一命名，不允许用户通过 conf 放宽：

- multipart 最大 10,000 个分片、每批最多 50 个签名；
- JSON 响应读取上限和目录分页上限；
- journal 字段、状态名、文件权限以及原子写入规则；
- API 路径、HTTP 方法和冲突策略枚举；
- AccessToken/UserToken 响应格式与凭据文件权限要求。

macOS 构建所需的 Go 版本、下载地址和 SHA-256 统一由 `scripts/select-macos-go.sh` 管理；它们是可复现构建输入，不属于运行 conf。
