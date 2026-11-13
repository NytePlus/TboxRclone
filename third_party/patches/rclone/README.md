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
- PUT 在后端关闭写句柄返回 `os.ErrExist` 时将 x/net/webdav 的通用405修正为409；实现 `HTTPStatus() int` 的清洗后端错误保留明确的4xx/5xx状态，其中云端401转成挂载侧403，避免错误触发本地WebDAV认证挑战。其他不透明关闭错误仍返回405。响应仅在PUT完成前暂存，因此不会缓存GET下载内容。
- 上游本地后端的 MKCOL、创建条件以及实体标签条件 HTTP 回归测试，先观察失败，再验证修复后通过。
- 项目采用单读写客户端模型，不再增加 `--exclusive-access`、Tbox 专用锁表或外部 WebDAV guard；请求级语义由 rclone 与 `x/net/webdav` 负责。

**边界：**这些检查使用 VFS 视图，不构成服务端原子条件写。外部客户端修改、缓存失效、其他修改方法的条件头及请求之间的竞争仍须验收。SJTU backend 的 ask 确认仍是新建文件的最终冲突保护；单客户端范围的顺序覆盖已通过显式实验开关开放，不具备跨客户端 CAS 保证。

重建验证已从上游 `git archive HEAD` 导出两个原文件，在临时目录应用补丁，并与当前工作区逐字节比较。上游完整 WebDAV 测试与真实服务的 13 项 HTTP 检查均曾通过；该轮旧格式运行记录已在证据协议统一时清理。这些结果不等于完整 Finder 验收。

注册器 overview 修复已经通过 rclone/rclone#9935 进入当前固定的上游提交；其旧格式实验记录已清理。当前补丁序列只保留尚未进入上游的 WebDAV 输入/条件/删除保护和必要的通用移动安全修复。

`--no-recursive-delete` 为另一个默认关闭的通用选项，目录删除改走非递归 VFS Remove。本项目 Compose 开启它以保护 rmdir 的非空语义。新增 HTTP 回归先复现默认 RemoveAll 删除子文件，再验证选项开启后非空 DELETE 为405、子文件保留、清空后删除204。


`0003-backend-move-overwrite.patch` 新增默认 false 的 `Features.MoveOverwrites`：后端声明后，通用 operations.Move 不再先删除已有目标。Mask 保守交集传播。补丁0001同时在 MOVE 请求上下文中推迟文件目标删除到后端 Move，Overwrite:F仍由协议层拒绝；目录目标不套用此文件能力。两层回归均先复现“后端拒绝但目标已丢失”，再验证旧目标保留、正常覆盖成功和默认行为不变。当前固定 HEAD 重放两个补丁并逐字节比较上游修改文件；能力名称不承诺服务端掉电原子性。


补丁0003还包含默认false的NoDirMoveFallback：后端拒绝目录移动时不再模拟逐文件移动；能力掩码按OR保留限制。SJTU因此可以按原生契约返回准确ErrorDirExists，同时保护已有目标及完整源树。operations回归先复现返回nil并搬走源子文件，再验证显式能力开启时保留源与目标。未声明能力的默认目录移动流程不变。

## 中断的 PUT 请求体

WebDAV adapter 现在无条件跟踪 PUT 请求体的实际字节数、结束和读取错误。短于/长于 Content-Length，或未知长度流读取失败时，文件 Close 使用 VFS `CloseWithError`，不能把异常当正常 EOF。正常零字节、已知长度和未知长度请求仍支持。这是通用错误传播修复，不承担 Tbox 的并发契约。

上游 `operations.Rcat` 在探测输入大小时曾忽略非 EOF 读取错误，将未填满的缓冲区继续送往 PutStream；即使取消上传，local backend 也可能先截断并删除旧目标。该修复及其 Rcat 回归已经通过 rclone/rclone#9937 进入当前固定的上游提交，因此不再由补丁0003重复携带。WebDAV HTTP 补丁仍覆盖短流、长流及未知长度断流的异常关闭路径。

补丁现在修改 5 个上游文件；更新基线后已重新运行 WebDAV 与 operations race 测试并通过。这不宣称所有后端的大流上传具备原子覆盖；SJTU 仍依赖完整本地 spool 后才上传的后端契约。Finder 的独立零字节 PUT 直接进入 rclone；保存期间允许零字节占位，完成后必须重开并核对完整内容。

## COPY 失败保护

文件覆盖 COPY 不再先 RemoveAll 目标，而是让 VFS/backend 的写入处理替换。Overwrite:F、WebDAV 锁检查仍由原处理器执行。COPY 输入的读取错误、提前 EOF 或长度不符会传到目标 CloseWithError，不能把前缀当正常完成。文件/目录类型冲突和已有目录替换明确拒绝；新目录递归复制仍支持，但尚不具备整树事务和失败回滚，不能把拒绝已有目录当成该验收场景已完成。

回归分别先复现“上传被拒绝前旧目标已删”和“源读取失败后目标变成前缀”；修复后覆盖成功、Overwrite:F、上传拒绝、源断流/短流、空文件、新目录及类型冲突均测试通过。源读取故障 fixture 额外断言确实调用了源 Open，避免用早期路由错误冒充故障覆盖。完整上游 WebDAV race 与项目 race/vet 通过。

COPY/MOVE 的同路径、祖先/后代、BaseURL 和 Destination 语义由固定版本的 rclone 与 `x/net/webdav` 处理。Tbox 不再叠加入口 guard；已确认的覆盖、源断流和部分目标缺陷仍由本补丁及上游回归保护。
