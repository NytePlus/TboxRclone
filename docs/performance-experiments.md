# 代码驱动的性能实验

性能是独立工程项目，不属于25项 Finder 系统验收。调优使用代码化测试，不操作 Finder、不录屏、不修改桌面挂载；结果用于定位与比较性能，不能改写系统验收结论。2026-11-14 用户明确删除原第26项性能验收，并要求保留历史 Finder 未达标记录。

历史记录位于 `evidence/ST-018-T/result.json` 及其证据目录：256 MiB Finder 路径的内容核验通过，但端到端吞吐低于当时设定的直接 API 80%目标，因此原结论保持 FAIL。该记录不计入当前25项系统验收，也不会因代码基准达标而改写。

运行：

```sh
python3 scripts/performance/benchmark-webdav.py /absolute/path/payload.bin \
  evidence/ST-018-T/code-UNIQUE --run-id perf-code-UNIQUE --repeat 2
```

脚本复用 Compose 定义，启动独立 `performance-webdav` 服务（仅监听 127.0.0.1:8688），使用新的云端实验目录和 journal。实验结束停止该服务，不影响 8686 的桌面挂载服务，不删除源文件或待恢复数据。异常退出也保存结果；新一轮使用新的运行 ID。源文件最大 2 GiB，每组最多五次，开始前检查保留 spool 的磁盘容量。

| 条件 | 代码覆盖 | 成功证据 | 失败处理 |
| --- | --- | --- | --- |
| 已知输入长度 | `length`：HTTP PUT Content-Length | 201/204、同路径唯一 Committed、云端完整 SHA-256 等于源文件 | 保存失败结果与状态，不自动重复写入 |
| 未知输入长度 | `chunked`：HTTP 分块传输 | 同上，不把接收完成当作远端确认完成 | 同上 |
| 上传结束后源文件保持完整 | 末尾重新计算源文件 SHA-256 | 与初始值一致 | 实验不通过 |
| 云端内容一致 | 每次上传后独立云端完整读取 | 大小与 SHA-256 一致 | 不将较快的错误结果算作收益 |
| 持久状态完成 | 读取隔离 journal | 唯一匹配记录 Committed 且 SHA-256 一致 | 不接受 Unknown 或仅 HTTP 成功 |
| 内存及并发有界 | 输入块 1 MiB；请求顺序执行；既有后端四分片并发 | 代码约束；正式资源结论需另采样 | 不从计时推断内存峰值 |

每次记录客户端输入发送时长、HTTP 完整返回时长、吞吐、输入模式、内容核验和后端阶段耗时。独立云端复核在计时结束后进行；后端自己的 confirm 与完整读回仍包含在 HTTP 返回计时中。比较必须报告相同源文件及代码版本，并保留请求差异；少量重复只能作诊断，不宣称统计显著。

直接 API 对照继续使用 `cmd/upload-benchmark`；其请求计时不会记录签名 URL 或凭据。对照与 WebDAV 都包含持久 staging、confirm、后端独立读回，独立内容检查不得被省略来改善数字。

## Spool 读取块实验

原始 32 KiB 复制块基线位于 `evidence/ST-018-T/code-baseline-20261114`。有长度/分块上传各执行一次，256 MiB 内容和 journal 全部核验一致，总耗时分别22.61/24.06秒。单次结果只用于定位，不能作稳定收益结论。

曾尝试写端 bufio 缓冲，但 `TestQuotaProcessDeath` 检出进程中止时已消费字节没有进入 spool；该方案已撤回，未部署到桌面服务。随后测试把 `io.CopyBuffer` 的读取块提高到1MiB，每次读取仍立即写入文件，配额、大小、SHA-256、fsync及journal发布顺序不变；全量 `go test -race ./...` 通过，见 `evidence/ST-018-T/profile-20261114/larger-read-race.log`。真实云端对比使用相同文件和请求模式，结果保留在 `code-larger-read-20261114`。

实验结果：1MiB读取块使调用次数由8194降为261，但有长度/分块输入的落盘耗时仍为1.84/1.82秒（基线1.88/1.82秒），总耗时25.68/24.98秒；无实质落盘收益，已撤回该候选，spool源码哈希与基线完全一致。网络波动不允许将总耗时差异直接归因于读取块；保留阶段计时和脚本，后续实验继续使用代码方式。

## 自动性能比较

```sh
python3 scripts/performance/compare-upload-performance.py API_RESULT.json WEBDAV_RESULT.json NEW_COMPARISON.json
```

比较器固定使用80%吞吐门槛，要求源文件大小和SHA-256相同、所有云端内容核验成功且journal已提交。输入证据不合格返回2，性能未达标返回1，达标返回0；“很快但内容错误”不能通过。回归测试位于 `scripts/performance/tests/test_compare_upload_performance.py`，覆盖慢结果、错误内容/不同输入和80%边界。

2026-11-14连接诊断：256MiB直接API全流程22.67秒，64个分片PUT有58次复用连接、6次新建；未发现大量重复建连，不据此修改连接池。与同日代码WebDAV基线比较，有长度输入22.61秒（API吞吐的100.3%）、分块输入24.06秒（94.2%），两个模式均超过80%。证据为 `evidence/ST-018-T/connection-profile-20261114/webdav-comparison.json`。这是各模式单样本的工程检查，不代表统计显著，也不证明Finder/webdavfs端到端性能达标；已有Finder未达标结果保留，系统验收范围变更须明确。
