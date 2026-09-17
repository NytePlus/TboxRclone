# rclone 补丁

子模块 gitlink 固定在 `base` 中的上游提交。修复以源码差异和对应上游回归测试保存在本目录；不依赖未发布、无法 fetch 的本地子模块提交。

```sh
git submodule update --init --recursive
sh scripts/rclone-patches.sh --apply
sh scripts/rclone-patches.sh --check
```

应用后 `git status` 会显示 rclone 子模块工作区有修改，这是预期的补丁状态。主仓库跟踪补丁文件，不更新 gitlink。脚本先核对上游 HEAD，再检查是否已应用；无法匹配时退出，不 reset、不覆盖冲突。Compose 全部服务在运行命令前检查补丁，服务源代码卷保持只读。

当前 `0001-webdav-request-guards.patch` 包含：

- WebDAV Mkdir 在 VFS 已有资源时返回 os.ErrExist，使重复 MKCOL 返回 405；rclone 后端 Mkdir 仍保持幂等。
- PUT If-None-Match:* 在 VFS 可见目标时先返回 412，避免打开截断写句柄。
- 上游本地后端的两项 HTTP 回归测试，均已先观察失败，再验证修复后通过。

**边界：**这些检查使用 VFS 视图，不构成服务端原子条件写。外部客户端修改、缓存失效、条件头的其他形式和请求之间的竞争仍须验收。SJTU backend 的 ask 确认仍是新建文件的最终冲突保护；覆盖等未具备可靠服务端条件能力的操作仍被拒绝。

重建验证已从上游 `git archive HEAD` 导出两个原文件，在临时目录应用补丁，并与当前工作区逐字节比较。上游完整 WebDAV 测试通过；真实服务的 13 项 HTTP 检查通过，见 `docs/evidence/2026-09-17/webdav-check-patched.json`。这些结果不等于完整 Finder 验收。
