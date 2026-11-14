# TboxRclone 文档

当前文档以现行实现、验收要求和可重复操作为准。系统测试状态以
[`test-manifest.json`](test-manifest.json) 为唯一机器清单；文档中的示例和说明不单独构成 PASS 证据。

## 使用与边界

- [配置、启动与恢复](operations.md)
- [产品功能边界](product-boundaries.md)

## 产品与接口

- [单读写客户端与恢复约束](concurrency-contract.md)
- [交大云盘 API 约束](api.md)
- [rclone 与 WebDAV 契约](rclone-webdav.md)

## 验收

- [系统测试规格、Finder 流程与证据格式](verification.md)
- [`test-manifest.json`](test-manifest.json)：25项系统测试分支与状态
- [`acceptance-cases.json`](acceptance-cases.json)：`cmd/verify` 使用的子例目录
- [独立性能实验](performance-experiments.md)：不属于 Finder 系统验收，保留历史未达标记录

## 运行环境

- [HTTPS 故障注入与 Docker DNS](fault-proxy.md)

历史36项范围保存在 [`archive/acceptance-36-20261114/`](archive/acceptance-36-20261114/)；它不属于当前25项验收。
