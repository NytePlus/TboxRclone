# scripts 目录

本目录只保留当前构建、运行、25项系统验收和独立性能实验需要的脚本。系统验收规范及条件覆盖矩阵见 [`docs/verification.md`](../docs/verification.md)。

## 构建与运行入口

- `rclone-patches.sh`、`check-rclone-patch-replay.py`：应用并核对固定 rclone 补丁。
- `with-rclone-patches.sh`、`run-go-service.sh`、`doh-dns.py`：Docker Compose 内部入口。
- `build-macos-webdav.sh`、`run-macos-webdav.sh`：构建并运行不依赖 Docker 的 macOS WebDAV 服务。
- `build-macos-mount.sh`：可选的 macFUSE 原生 mount 构建，不是推荐挂载路径。
- `mount-webdav-macos.py`：挂载、核对和卸载 macOS WebDAV。
- `live-webdav-state.py`：故障会话切换前后保存和恢复现行服务状态。

## 系统验收入口

- `system-check.sh`：离线回归和证据完整性门禁，不代替 Finder 用户操作。
- `finder-drag.sh`、`finder-browse.sh`：真实 Finder 操作的稳定入口。
- `finder-fault-session.py`：创建、切换和恢复隔离故障会话。
- `prepare-finder-fixture.sh`、`finder-session-guard.sh`：准备本地夹具和保持显示会话。
- `verify-multipart-recovery.sh`、`verify-service-signal-recovery.sh`、`verify-resource-statuses.sh`：当前恢复、进程中断和资源错误分支。

`finder/` 中可直接执行的场景驱动包括：

- 上传与恢复：`part-fault.py`、`signal-upload.py`、`read-fault.py`。
- 资源错误：`resource-fault.py`、`retry-resource.py`。
- 文件操作：`overwrite-cases.py`、`cross-disk-move.py`、`rmdir-cases.py`、`delete-fixture.swift`、`verify-delete.py`。
- 协议和事实核对：`verify-locks.py`、`verify-copy.py`、`verify-list.py`、`verify-absence.py`、`snapshot-journal.py`、`check-pending.py`。
- 统一收尾：`finish-upload.py`、`finish-part-evidence.py`、`retain-recordings.py`。

其余 `finder/` 文件是上述入口调用的 Swift/Python 内部组件，不应作为新的公共入口复制使用。

## 独立性能实验

`performance/` 与 Finder 系统验收分离：

- `performance/benchmark-webdav.py`：启动隔离的代码化 WebDAV 基准。
- `performance/compare-upload-performance.py`：与直接 API 证据比较。
- `performance/tests/`：只验证性能比较器本身。

性能结果不修改25项系统验收状态。

## 维护规则

- 新脚本应归入 `finder/`、`performance/` 或 `quality/`，只有稳定的跨类别入口留在本目录。
- 一次性诊断脚本应在结论写入文档或正式检查器后删除。
- 删除脚本前同时检查文档、Compose、其他脚本和当前验收清单的引用。
- `__pycache__` 和 `*.pyc` 是本地缓存，不属于源码。
