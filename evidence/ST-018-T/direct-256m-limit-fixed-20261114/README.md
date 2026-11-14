# 256 MiB Finder 上传优化复测

修复：单文件默认上限从 64 MiB 提升到 2 GiB，Compose 暴露 TBOX_MAX_UPLOAD，保留 4 GiB 总预算。未知长度输入按上限预留逻辑预算，不能据此声称支持任意并发。服务已以 direct 模式重启。

原失败：large.bin 超过预留长度，Finder 报 -36；旧观察器仍运行 241 秒。新增独立原生弹窗观察器，每 0.5 秒采样；匹配阻塞弹窗即停止观察，录屏使用现有提前停止信号。错误路径新行为尚未另行故障复测。

新结果：large-256m-fixed.bin 从拖拽结束到观测 Committed 约 38.9 秒；进度窗口约 46 秒后消失。源、挂载点重读、独立云端完整回读 SHA-256 全部一致。已有六项 pending 的 journal/payload 哈希不变。

同传输引擎直连 API 基准为 20.61 秒（包含完整暂存、分片、confirm 和回读校验），与 Finder 路径开销不同。此结果不能声称达到 API 80% 的端到端性能目标。上传期间目录浏览脚本返回 FAIL；录像抽查显示目录最终出现，但不能替代该时间段的成功判据。ST-018 仍未通过，整体已通过数不变。

证据：result.json、content-check.json、observer/、progress-watch.json、server.log。录像只保留本次最新版本；旧失败日志仍保留。
