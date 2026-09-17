# 验证实验与条件覆盖矩阵

产品范围修订（2026-09-17，用户明确要求）：采用[单客户端并发约束](concurrency-contract.md)。所有验收在单个受控服务实例、无外部写入的挂载根内执行；明确标注的外部版本变化测试仅验证防御性错误检测。不同文件及分片并行仍须支持，同路径冲突改验客户端拒绝。下文优先于历史跨客户端写入要求；原始 API 实验和失败证据保留，范围调整不自动产生 PASS。

这是原始验收规格，**不是测试运行报告**。所有用例未执行，P0 不得因当前实现失败而降低预期；SKIP/未知不计 PASS。已有 Go 实验后端、普通用户隔离实验空间和部分真实 API 证据，见 [真实接口实验](live-api-findings.md)；这不代表下述系统场景已完成。后端测试使用用户指定的 rclone/Go 体系；macOS 用户路径由原生 UI 和真实终端执行。

## 1. 服务端语义发现实验

创建独立实验根 `codex-api-lab/<run-id>/`，所有数据由实验生成；读写仅限此目录，删除进回收站，恢复仅使用本次 recycledItemId。记录服务端版本（若可获得）、客户端版本、操作时序、请求/任务标识、状态码、脱敏 JSON、旧/新对象身份、完整 SHA-256 和目录树。

1. **基线**：认证、info、分页、Range、空文件、单片、多片；对每个候选接口将 F/S 升为 V 或记录不支持，不把 403 直接判定为接口不存在。
2. **幂等性**：相同分片连续重放；相同 K confirm 两次；abort 两次；confirm 与 abort 同时；记录版本数/回收站数/临时项，不只比较最终文件名。
3. **提交丢响应**：使用代理让请求真正到达服务器并由独立读确认完成，再丢弃响应；分别覆盖 confirm、MOVE、DELETE、异步 task 受理。普通“发送前断网”不能代替此实验。
4. **原子发布**：旧文件 A、新文件 B，不同大小和内容；另一读者持续完整读取正式路径，观察只有 A 或 B，不能 404/部分文件/混合字节。在移动覆盖与确认时反复注入断网/进程 kill。有限次数通过是运行证据，不是服务端掉电原子性的形式证明。
5. **并发约束**：在同一受控实例中验证同路径读写、双写、移动/删除冲突立即拒绝；另测第二实例及重启后未决路径占用。不同文件和同会话分片允许并行。历史服务端 CAS 实验保留为能力发现，不再要求支持外部多写入者；客户端独占不替代提交丢响应和崩溃测试。
6. **目录与查询**：目录专用 move/copy、非空 rmdir 竞态、任务部分失败、游标分页变化、版本查询可见延迟、上传任务过期保留时间。
7. **同步接口**：进一步定位 fs-journal 的 syncId/dirNode 建立和清理流程，验证 ssn 的含义、重复操作以及 modificationTime 是否持久；不能仅凭名称就将其当事务序号。

V 证据记录模板：`run_id / operation / evidence_before / request_redacted / response_redacted / fault_phase / source_identity_before_after / destination_identity_before_after / sha256 / history_delta / recycle_delta / retry_count / observed_consistency / verdict / unresolved`。未得到服务端契约时把结论写成“在所测版本与条件下观察到”，不泛化为强保证。

## 2. 故障与测试环境

服务级测试优先 Docker Compose：后端服务、可控代理、测试执行器；本地缓存与日志用持久卷。故障代理需要同时覆盖控制面 pan.sjtu.edu.cn 和签名数据面，且支持请求到达后单独丢响应。不能因为数据面绕开代理就声称已经覆盖断网。macOS Finder/webdavfs、Ctrl+C、SIGTERM、SIGKILL、系统重启在宿主 Mac 验收。

注入点：初始化前后、分片发送中/应答后、renew 前后、confirm 发送前/执行后/响应前、MOVE 执行后、task 受理/完成间、本地日志写入前/后、缓存落盘前/后。先建立无故障成功基线，再对相同数据集逐点注入。清理在完成核对之后执行，未知结果的源、缓存和临时文件先保留。

每个场景的共同前置：专用普通用户、隔离实验目录、确定内容 fixture、记录旧对象、配置已锁定、服务与 Finder 版本记录。共同后验：通过独立读路径获取完整内容并算 SHA-256；截图/录屏记录 Finder 成功、失败或同步状态；日志脱敏。API 只用于 setup、external-event、fact-check；Finder 点击操作必须真实走 UI，终端 mv/rmdir 场景真实走挂载文件系统。

## 3. 条件覆盖矩阵

每行有两个具体用例：`ST-nnn-T`（正分支）和 `ST-nnn-F`（负分支）。多种负场景须分别运行并留子记录，不能任选一个就算全覆盖。这里的“正负”指条件分支，不指一定成功/失败。

| 条件 ID | 用户目标 | 条件 | 正分支用例/场景 | 负分支用例/场景 | 用户动作 | API 支持角色 | 验收信号 | Gap/Risk |
|---|---|---|---|---|---|---|---|---|
| C-001 | 完整保存文件 | 连接在传输期间可用 | ST-001-T：正常上传 | ST-001-F：上传第一个/中间/最后一个分片时断网 | Finder 拖入固定内容文件 | setup / external-event / fact-check | 远端正式文件为完整版本或尚未出现；恢复后 SHA-256 一致 | 未执行；见 [001](api.md#6-崩溃一致性设计契约) |
| C-002 | 取消不损坏旧文件 | 覆盖确认尚未提交 | ST-002-T：confirm 之前取消 | ST-002-F：confirm 已执行后丢响应再取消 | 在 Finder 覆盖同名文件时取消 | setup / external-event / fact-check | 提交前旧版不变；提交后允许新版但必须对账，禁止误删 | 未执行；见 [002](api.md#5-幂等性与结果未知) |
| C-003 | 恢复未同步数据 | 进程正常退出 | ST-003-T：SIGTERM | ST-003-F：SIGKILL；另做宿主机崩溃/掉电实验 | Finder 保存后重启服务 | setup / external-event / fact-check | 达到所声明持久边界的数据恢复；待传数据仍存在，不静默清空 | 未执行；见 [003](rclone-webdav.md#5-rclone-不自动提供的保证) |
| C-004 | 移动不丢数据 | 源和目标在同一挂载 | ST-004-T：MOVE 响应送达 | ST-004-F：MOVE 已执行但响应丢失，Ctrl+C 后重试 | 终端 mv 两个挂载内目录之间的文件/目录 | setup / external-event / fact-check | 源目标身份可核对；不重复复制/不误删新对象；目录树完整 | 未执行；见 [004](api.md#5-幂等性与结果未知) |
| C-005 | 跨盘移动安全 | 目标达到云端持久边界 | ST-005-T：目标已确认 | ST-005-F：目标仅进入本地 VFS 缓存时断网/杀服务 | 终端 mv 本地文件到挂载盘，观察源和同步状态 | setup / external-event / fact-check | 必须如实显示未同步；严格云端先于删源需求若不满足则阻断发布或提供同步完成流程 | 未执行；见 [005](rclone-webdav.md#5-rclone-不自动提供的保证) |
| C-006 | 同路径冲突明确拒绝 | 路径没有冲突占用 | ST-006-T：顺序保存、完成后重开 | ST-006-F：双写、读写冲突、第二服务实例、恢复中重建 | Finder/编辑器经同一挂载操作，另启动第二实例；重启后尝试同路径写入 | setup / external-event / fact-check | 无冲突操作成功；冲突直接拒绝且不改变已接受数据；未决路径跨重启保留占用 | 待实现；见 [并发契约](concurrency-contract.md) |
| C-007 | 分片恢复不重复整传 | 原会话与授权有效 | ST-007-T：K 和签名有效 | ST-007-F：签名到期、K 过期、重复发送同一分片 | Finder 大文件上传暂停网络后恢复 | setup / external-event / fact-check | 已验分片复用；过期正确续期/重建；不重用错误字节 | 未执行；见 [007](api.md#3-当前-tbox-数据接口) |
| C-008 | 输入长度正确 | 实际长度等于声明长度 | ST-008-T：零字节、完整长度、未知长度 | ST-008-F：提前 EOF、多余字节、流读取失败 | 挂载写文件；协议层补充构造短流 | setup / external-event / fact-check | 空文件可用；长度错误不发布残缺文件，不无限等待 | 未执行；见 [008](rclone-webdav.md#3-必要测试子集) |
| C-009 | 随机读取内容正确 | 读取期间远端版本未变 | ST-009-T：固定版本 Range | ST-009-F：Range 被忽略、416、读取中网页端替换 | Finder 快速查看/应用 seek 大文件 | setup / external-event / fact-check | 不混合不同版本；错误 Range 可检测；必要时重新打开 | 未执行；见 [009](api.md#5-幂等性与结果未知) |
| C-010 | 完整浏览目录 | 遍历期间目录稳定 | ST-010-T：0/1/50/51/1000 条稳定列表 | ST-010-F：分页期间新增/删除/重命名、重复游标 | Finder 打开超过一页的目录 | setup / external-event / fact-check | 稳定目录无丢项/重复；变化时策略明确且不死循环 | 未执行；见 [010](rclone-webdav.md#2-后端必要接口映射) |
| C-011 | 非空目录不会被误删 | 目录为空 | ST-011-T：空目录 | ST-011-F：非空目录；同一实例检查后尝试新建子项 | 终端 rmdir 挂载目录 | setup / external-event / fact-check | 空目录删除；非空拒绝；删除期间受控子项创建被拒绝，不丢已有内容 | 未执行；见 [011](api.md#4-新发现的高价值能力) |
| C-012 | 锁真正保护写入 | 请求持有效锁 | ST-012-T：正确 token/有效时间 | ST-012-F：缺 token、错误 token、过期、服务重启、父目录锁 | 编辑器打开并保存，第二客户端修改；协议层补充 token 条件 | setup / external-event / fact-check | 无权写操作被拒绝；重启后行为明确；不虚报持久云端锁 | 未执行；见 [012](rclone-webdav.md#4-finder-所需-webdav-需求子集) |
| C-013 | Finder 覆盖与目录复制正确 | 目标不存在 | ST-013-T：目标不存在 | ST-013-F：目标已存在，Overwrite 缺省/T/F；文件目录冲突 | Finder 替换文件、复制目录；协议层设置 Overwrite | setup / external-event / fact-check | 完整复制；F 拒绝覆盖；安全保存不先删旧版导致丢失 | 未执行；见 [013](rclone-webdav.md#4-finder-所需-webdav-需求子集) |
| C-014 | 特殊名字与元数据可重开 | 名称无需特殊编码 | ST-014-T：普通名称 | ST-014-F：中文、emoji、引号、%#?+、NFC/NFD、大小写、AppleDouble | Finder 新建重命名；应用保存并重新打开 | setup / external-event / fact-check | 路径可往返；冲突可见；不静默吞掉所需属性 | 未执行；见 [014](api.md#2-认证路径错误模型) |
| C-015 | 资源不足明确失败 | 权限和容量足够 | ST-015-T：正常权限、token、磁盘空间 | ST-015-F：401/403/429/5xx、云端配额、本地缓存满 | Finder 保存/浏览后重开 | setup / external-event / fact-check | 保留可恢复副本；不把权限失败当空目录；退避有界 | 未执行；见 [015](api.md#2-认证路径错误模型) |
| C-016 | 异步操作完成才报成功 | 服务端任务完成 | ST-016-T：task 完成且全成功 | ST-016-F：仅受理、部分失败、task 查询超时 | Finder 复制大目录并立即重开 | setup / external-event / fact-check | 未完成不显示云端成功；部分失败逐项可追踪 | 未执行；见 [016](api.md#4-新发现的高价值能力) |
| C-017 | 删除恢复不误删新对象 | 删除结果已明确 | ST-017-T：删除成功且无重建 | ST-017-F：删除响应丢失后经同一实例尝试重建；重启后再尝试 | Finder 删除后断网，在同一挂载重建并重启服务 | setup / external-event / fact-check | 未决结果阻止重建；对账后才释放占用；后续新文件不被旧操作重试删除 | 待实现；见 [并发契约](concurrency-contract.md) |
| C-018 | 传输高效且交互响应 | 并发负载较低 | ST-018-T：单文件、稳定低延迟网络 | ST-018-F：多文件/高 RTT/限速/丢包/恢复并发 | Finder 复制文件同时浏览目录 | setup / external-event / fact-check | 内容始终一致；请求/内存有界；记录吞吐和 p95，不以提速牺牲正确性 | 未执行；见 [018](rclone-webdav.md#6-效率指标与发布门槛) |

## 4. 全量报告与准入

每个 ST 保存版本、日志、故障点、截图/录屏、原始与最终 SHA-256、远端身份和副作用计数，写入 test-manifest 对应 evidence_directory。manifest 当前 recording_path=null 是未录制，不代表无需证据。

报告应列出全部 36 个分支及每个负分支子场景，状态 PASS/FAIL/BLOCKED/NOT_RUN；同时列出上游集成测试 RUN/PASS/SKIP。已知实现缺陷按 FAIL 记录并关联问题；没有环境证据按 NOT_RUN/BLOCKED，不能写 PASS。

发布至少要求 C-001 至 C-017 全部满足各自产品契约，C-018 达到定下的效率目标。若 rclone 原生 VFS 不能达到严格云端写入应答或跨盘移动要求，应记录缺陷、修改上层或提供明确同步工作流，不能将测试预期改成“缓存里有就算云端成功”。
