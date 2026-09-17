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
- PUT 在打开截断写句柄前检查 If-Match/If-None-Match 的星号、标签列表及强弱比较，条件不满足返回 412。
- 上游本地后端的 MKCOL、创建条件以及实体标签条件 HTTP 回归测试，先观察失败，再验证修复后通过。
- 可选 `--exclusive-access` 在请求入口拒绝读写/双写及源目标子树冲突，要求 VFS cache off。Compose 已开启；原生调用需显式传入该选项。回归覆盖真实请求体暂停、并发读取、目录操作与释放，不改变默认关闭时的上游并发行为。

**边界：**这些检查使用 VFS 视图，不构成服务端原子条件写。外部客户端修改、缓存失效、其他修改方法的条件头及请求之间的竞争仍须验收。SJTU backend 的 ask 确认仍是新建文件的最终冲突保护；单客户端范围的顺序覆盖已通过显式实验开关开放，不具备跨客户端 CAS 保证。

重建验证已从上游 `git archive HEAD` 导出两个原文件，在临时目录应用补丁，并与当前工作区逐字节比较。上游完整 WebDAV 测试通过；真实服务的 13 项 HTTP 检查通过，见 `docs/evidence/2026-09-17/webdav-check-patched.json`。这些结果不等于完整 Finder 验收。

`0002-external-backend-overview.patch` 修复通用注册流程覆盖显式 `RegInfo.Overview` 的问题：提供值时保留它；nil 时仍按上游逻辑加载嵌入元数据并报告缺失。没有硬编码 SJTU 名称，也没有关闭错误日志。回归测试先复现失败，随后验证外部后端及别名保留同一元数据、内置 local 仍载入原值。SJTU 自身只声明实验状态，不虚报完整集成测试或数据安全评级。

补丁 0002 已从固定上游原文件在临时目录应用并逐字节比对；Docker Compose 中上游 `go test -race ./fs -count=1`、项目 `go test -race ./...` 和 CLI version 均通过。完整系统验收仍未通过。
