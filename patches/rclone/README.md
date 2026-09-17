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

`--no-recursive-delete` 为另一个默认关闭的通用选项，目录删除改走非递归 VFS Remove。本项目 Compose 开启它以保护 rmdir 的非空语义。新增 HTTP 回归先复现默认 RemoveAll 删除子文件，再验证选项开启后非空 DELETE 为405、子文件保留、清空后删除204。


`0003-backend-move-overwrite.patch` 新增默认 false 的 `Features.MoveOverwrites`：后端声明后，通用 operations.Move 不再先删除已有目标。Mask 保守交集传播。补丁0001同时在 MOVE 请求上下文中推迟文件目标删除到后端 Move，Overwrite:F仍由协议层拒绝；目录目标不套用此文件能力。两层回归均先复现“后端拒绝但目标已丢失”，再验证旧目标保留、正常覆盖成功和默认行为不变。已完整重放三个补丁并逐字节比较7个上游文件；上游WebDAV完整race32.838s、operations移动回归、主项目全量race/vet通过。能力名称不承诺服务端掉电原子性。


补丁0003还包含默认false的NoDirMoveFallback：后端拒绝目录移动时不再模拟逐文件移动；能力掩码按OR保留限制。SJTU因此可以按原生契约返回准确ErrorDirExists，同时保护已有目标及完整源树。operations回归先复现返回nil并搬走源子文件，再验证显式能力开启时保留源与目标。未声明能力的默认目录移动流程不变。

## 中断的 PUT 请求体

严格模式 `--exclusive-access` 现在跟踪 PUT 请求体的实际字节数、结束和读取错误。短于/长于 Content-Length，或未知长度流读取失败时，文件 Close 使用 VFS `CloseWithError`，不能把异常当正常 EOF。正常零字节、已知长度和未知长度请求仍支持；默认未开启严格模式的行为不变。

上游 `operations.Rcat` 在探测输入大小时曾忽略非 EOF 读取错误，将未填满的缓冲区继续送往 PutStream；即使取消上传，local backend 也可能先截断并删除旧目标。补丁0003在开始上传前返回该输入错误。HTTP 回归先复现短流返回201、断流把旧内容替换成前缀，以及仅改 Close 后旧目标被删除，再验证两处联动修复。另有 Rcat 回归验证源读取失败时不调用 PutStream、旧内容保留、新目标不存在。

完整上游 WebDAV race 测试和 Rcat 测试通过，7 个上游修改文件已从固定 HEAD 重放全部补丁并逐字节比较。这不宣称所有后端的大流上传具备原子覆盖；SJTU 仍依赖完整本地 spool 后才上传的后端契约。也不解决 Finder 成功发送独立零字节 PUT 后再上传内容的问题，ST-001-T 仍为 FAIL。

## COPY 失败保护

`--exclusive-access` 下，文件覆盖 COPY 不再先 RemoveAll 目标，而是让 VFS/backend 的写入处理替换。Overwrite:F、WebDAV 锁检查仍由原处理器执行；默认未启用严格模式时行为不变。COPY 输入的读取错误、提前 EOF 或长度不符会传到目标 CloseWithError，不能把前缀当正常完成。文件/目录类型冲突和已有目录替换明确拒绝；新目录递归复制仍支持，但尚不具备整树事务和失败回滚，不能把拒绝已有目录当成该验收场景已完成。

回归分别先复现“上传被拒绝前旧目标已删”和“源读取失败后目标变成前缀”；修复后覆盖成功、Overwrite:F、上传拒绝、源断流/短流、空文件、新目录及类型冲突均测试通过。源读取故障 fixture 额外断言确实调用了源 Open，避免用早期路由错误冒充故障覆盖。完整上游 WebDAV race 与项目 race/vet 通过。

严格模式的 COPY/MOVE 还在打开文件前拒绝规范化后相同、祖先或后代的源/目标，避免递归复制自身以及替换源的祖先。配置 BaseURL 时，Destination 先去除相同的公共 URL 前缀，再与请求路径使用同一锁命名空间；前缀外目标拒绝。回归先复现前缀导致目标 PUT 绕过冲突保护，随后验证目标读写拒绝、独立兄弟路径允许。真实本地 HTTP 服务覆盖带前缀的路径解码、目录自复制拒绝、文件树不变，以及兄弟目录正常 COPY/MOVE。该测试不访问云盘，不替代系统验收。
