# 交大云盘接口与 rclone 接入研究

研究日期：2026-09-17。目标：把交大云盘接入原生 rclone backend，再通过 `rclone serve webdav` 接入 macOS Finder；直接 `rclone mount` 作为另一条独立验收路径。

本文保留接口调研基线。Go 实验实现与执行结果见 [实施状态](implementation.md) 和 [项目入口](../README.md)，仍不是已经完成验收的 rclone 后端。没有进行登录后的远端读写、故障注入或 macOS 挂载实测。不能据此宣称服务端原子性或崩溃一致性已经成立。

## 阅读入口

1. [接口文档](api.md)：当前调用、额外接口、参数、返回值和重试语义。
2. [rclone 与 WebDAV 接入及测试子集](rclone-webdav.md)：后端映射、上游真实测试名称、挂载成功边界与发布门槛。
3. [故障实验与条件覆盖矩阵](verification.md)：如何证明完整性、幂等性、中断恢复和效率。
4. [接口发现清单](discovered-endpoints.json)：43 个交大公开前端定义、110 个 SMH SDK 操作；包含重叠能力与同名操作，不是 153 个不同且可用的接口。
5. [来源锁定](sources.json)：源码版本、公开资源 URL 与 SHA-256。
6. [验收场景清单](test-manifest.json)：机器可读测试计划，所有场景均标记未执行。

## 已有实质发现

- 交大当前公开前端定义了 `DELETE .../{confirmKey}?upload`，可以作为中止上传的候选；Tbox 没有封装。
- `GET .../{confirmKey}?upload` 支持状态查询。Tbox 固定 `no_upload_part_info=1`，恢复分块时需要验证省略它能否取得分片明细。
- 前端提供目录专用复制、异步任务查询、历史版本与回收站恢复，不能继续仅凭文件接口推断目录操作语义。
- 前端还有 `/api/v1/fs-journal/...` 同步接口族，包含 `syncId`、`ssn`、目录日志和上传修改时间。这比“仅能列目录”更有价值，但同步会话建立与语义仍需进一步确认。
- SMH SDK 1.0.16 定义了 `content_cas`、`with_content_cas`、inode 查询和 `/fs-delta`。这些是并发保护与缓存失效的优先调查项，尚无交大实例支持证据。
- rclone v1.75.1 的 `serve webdav` 使用 `webdav.NewMemLS()`；内存锁不是跨重启、跨实例或网页端的云端锁。

## 推荐顺序

先验证上传确认、条件覆盖、服务端移动及结果查询，再写 backend。缺少原子覆盖或可核对的对象身份时，不应承诺无损多客户端覆盖。优先做到完整文件读写、空文件、正确 Range、目录列表、失败不误报；随后加入已验证的 Move/DirMove/Copy、恢复日志和 VFS 挂载验收。

rclone 泛化测试复用已有成熟实现，但额外的“提交成功后丢响应”“进程被杀”“网页端同时覆盖”测试仍是发布必需项。
