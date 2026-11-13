# TboxRclone 文档

当前文档以现行实现、验收要求和可重复操作为准。测试状态以
[`test-manifest.json`](../test-manifest.json) 为唯一机器清单；文档中的示例和说明不单独构成 PASS 证据。

## 产品与接口

- [单读写客户端与恢复约束](concurrency-contract.md)
- [交大云盘 API 约束](api.md)
- [rclone 与 WebDAV 契约](rclone-webdav.md)

## 验收

- [系统测试规格、Finder 流程与证据格式](verification.md)
- [`test-manifest.json`](../test-manifest.json)：系统测试分支、状态和必需子例
- [`acceptance-cases.json`](acceptance-cases.json)：`cmd/verify` 使用的子例目录

## 运行环境

- [HTTPS 故障注入与 Docker DNS](fault-proxy.md)

历史实验日志、旧实现进度、已删除 WebDAV guard 的诊断记录及其专用范围文件不再保留。
