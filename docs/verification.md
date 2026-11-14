# 验证实验与条件覆盖矩阵

产品采用[单读写客户端约束](concurrency-contract.md)，不包含额外 WebDAV guard。所有验收在单个受控服务实例、无外部写入的挂载根内执行；Finder 的初始零字节 PUT 可以到达云端，验收以 Finder 报告完成后的重新打开和 SHA-256 为准。不同文件及分片并行仍须支持；不要求固定 `423`、跨重启 HTTP claim 或持久 WebDAV lock token。

这是验收规格，**不是测试运行报告**。执行状态以 [`test-manifest.json`](test-manifest.json) 和生成报告为准。P0 不得因当前实现失败而降低预期；SKIP/未知不计 PASS。后端测试使用 rclone/Go 体系；macOS 用户路径由原生 UI 和真实终端执行。

## 1. 服务端语义发现实验（扩展参考，非本轮必跑清单）

创建独立实验根 `codex-api-lab/<run-id>/`，所有数据由实验生成；读写仅限此目录，删除进回收站，恢复仅使用本次 recycledItemId。记录服务端版本（若可获得）、客户端版本、操作时序、请求/任务标识、状态码、脱敏 JSON、旧/新对象身份、完整 SHA-256 和目录树。

1. **基线**：认证、info、分页、Range、空文件、单片、多片；对每个候选接口将 F/S 升为 V 或记录不支持，不把 403 直接判定为接口不存在。
2. **幂等性**：相同分片连续重放；相同 K confirm 两次；abort 两次；confirm 与 abort 同时；记录版本数/回收站数/临时项，不只比较最终文件名。
3. **提交丢响应**：使用代理让请求真正到达服务器并由独立读确认完成，再丢弃响应；分别覆盖 confirm、MOVE、DELETE、异步 task 受理。普通“发送前断网”不能代替此实验。
4. **发布边界**：旧文件 A、新文件 B，不同大小和内容；在后端确认和移动覆盖边界验证旧版不会被部分新版或混合字节替换。Finder 新建文件的初始零字节 PUT 是明确例外，保存完成后必须重开为完整内容。在提交边界反复注入断网/进程 kill；有限次数通过不是服务端掉电原子性的形式证明。
5. **单客户端并发**：验证 Finder 自身的 PROPFIND/LOCK/PUT/MOVE 等重叠请求不会损坏内容或静默覆盖；不规定必须返回 423。不同文件和同会话分片允许并行。历史服务端 CAS、guard claim 和第二客户端实验只保留为能力发现；客户端独占不替代提交丢响应和崩溃测试。
6. **目录与查询**：目录专用 move/copy、非空 rmdir 竞态、任务部分失败、游标分页变化、版本查询可见延迟、上传任务过期保留时间。
7. **同步接口**：进一步定位 fs-journal 的 syncId/dirNode 建立和清理流程，验证 ssn 的含义、重复操作以及 modificationTime 是否持久；不能仅凭名称就将其当事务序号。

V 证据记录模板：`run_id / operation / evidence_before / request_redacted / response_redacted / fault_phase / source_identity_before_after / destination_identity_before_after / sha256 / history_delta / recycle_delta / retry_count / observed_consistency / verdict / unresolved`。未得到服务端契约时把结论写成“在所测版本与条件下观察到”，不泛化为强保证。

## 2. 故障与测试环境

本轮仅执行第3节25项所需的注入点；下列故障能力描述不构成恢复已排除用例的要求。

服务级测试优先 Docker Compose：后端服务、可控代理、测试执行器；本地缓存与日志用持久卷。故障代理需要同时覆盖控制面 pan.sjtu.edu.cn 和签名数据面，且支持请求到达后单独丢响应。不能因为数据面绕开代理就声称已经覆盖断网。macOS Finder/webdavfs、Ctrl+C、SIGTERM、SIGKILL、系统重启在宿主 Mac 验收。

注入点：初始化前后、分片发送中/应答后、renew 前后、confirm 发送前/执行后/响应前、MOVE 执行后、task 受理/完成间、本地日志写入前/后、缓存落盘前/后。先建立无故障成功基线，再对相同数据集逐点注入。清理在完成核对之后执行，未知结果的源、缓存和临时文件先保留。

每个场景的共同前置：专用普通用户、隔离实验目录、确定内容 fixture、记录旧对象、配置已锁定、服务与 Finder 版本记录。宿主挂载点必须在项目及所有 Docker bind mount 之外（使用 `scripts/mount-webdav-macos.py`）；禁止将提供服务的网盘挂回 `.state` 或 `/src` 的宿主来源。每次宿主挂载测试操作前，必须重新核对系统挂载表中的源 URL、挂载点和文件系统类型；只有同名本地目录不能算已挂载。服务重启后的旧挂载状态不得沿用。Finder 夹具在拖拽前必须运行 `scripts/prepare-finder-fixture.sh FIXTURE_DIRECTORY`，确保文件由当前普通用户拥有，避免容器复制产生的属组导致系统授权对话框。真实 Finder 拖拽使用入口 `scripts/finder-drag.sh`（当前版本 `2026-11-12.1`）；它调用 Swift 脚本按精确窗口标题动态查找可见 Finder 窗口并校验几何信息，找不到有效窗口时立即失败，不使用历史硬编码窗口 ID。

共同后验：通过独立读路径获取完整内容并算 SHA-256；截图/录屏记录 Finder 成功、失败或同步状态；日志脱敏。API 只用于 setup、external-event、fact-check；Finder 点击操作必须真实走 UI，终端 mv/rmdir 场景真实走挂载文件系统。

## 3. 当前25项验收范围

根据用户2026-11-14的后续修正，已通过的正确性用例全部保留，并保留常见操作与故障。原36项先减为26项，随后将单文件 Finder 性能项 ST-018-T 从系统验收移除，最终为25项；同一路径并发访问仍不测试。概率判断基于使用场景，尚无生产故障频率统计。

本轮范围从18项恢复5个已通过项（ST-007-F、ST-008-F、ST-012-T、ST-014-T、ST-015-T），另恢复服务强制结束、跨盘移动、多文件/慢网络正确性3项。恢复已通过项不要求重复录制；新增范围内尚未验收的内容仍须实际核验。

历史规格见 [原36项规格](archive/acceptance-36-20261114/verification.md)。移出项不计PASS，已有失败和证据保留。内容完整性与原生UI证据要求不变。

| 条件 | 用例 | 用户目标与分支 | 验收信号 |
|---|---|---|---|
| C-001 | ST-001-T | 完整保存文件：正常上传 | 保存期间允许零字节占位；Finder 报告完成并重开后大小与 SHA-256 一致；故障后保留可恢复数据且错误可见 |
| C-001 | ST-001-F | 完整保存文件：上传第一个/中间/最后一个分片时断网 | 保存期间允许零字节占位；成功恢复后大小与 SHA-256 一致；不能恢复时保留已接受数据并向用户显示失败 |
| C-002 | ST-002-T | 取消不损坏旧文件：confirm 之前取消 | 提交前旧版不变；提交后允许新版但必须对账，禁止误删 |
| C-003 | ST-003-T | 恢复未同步数据：SIGTERM | 达到所声明持久边界的数据恢复；待传数据仍存在，不静默清空 |
| C-003 | ST-003-F | 恢复未同步数据：服务被强制结束（SIGKILL） | 达到所声明持久边界的数据恢复；待传数据仍存在，不静默清空 |
| C-004 | ST-004-T | 移动不丢数据：MOVE 响应送达 | 源目标身份可核对；不重复复制/不误删新对象；目录树完整 |
| C-005 | ST-005-T | 跨盘移动安全：目标已确认 | 必须如实显示未同步；严格云端先于删源需求若不满足则阻断发布或提供同步完成流程 |
| C-006 | ST-006-T | 单客户端保存不互相破坏：顺序保存 | 顺序操作成功，关闭后重开为用户最后确认的完整版本 |
| C-007 | ST-007-T | 分片恢复不重复整传：K 和签名有效 | 已验分片复用；过期正确续期/重建；不重用错误字节 |
| C-007 | ST-007-F | 分片恢复不重复整传：签名到期、K 过期、重复发送同一分片 | 已验分片复用；过期正确续期/重建；不重用错误字节 |
| C-008 | ST-008-T | 输入长度正确：零字节、完整长度、未知长度 | 空文件可用；长度错误不发布残缺文件，不无限等待 |
| C-008 | ST-008-F | 输入长度正确：提前 EOF、多余字节、流读取失败 | 空文件可用；长度错误不发布残缺文件，不无限等待 |
| C-009 | ST-009-T | 随机读取内容正确：固定版本 Range | 不混合不同版本；错误 Range 可检测；必要时重新打开 |
| C-010 | ST-010-T | 完整浏览目录：0/1/50/51/1000 条稳定列表 | 稳定目录无丢项/重复；变化时策略明确且不死循环 |
| C-011 | ST-011-T | 非空目录不会被误删：空目录 | 空目录删除；非空/竞态不递归删除用户内容 |
| C-011 | ST-011-F | 非空目录不会被误删：非空目录 | 非空目录删除明确拒绝，已有内容完整保留 |
| C-012 | ST-012-T | Finder 锁流程兼容：正确 token/有效时间 | 当前服务进程内正确 token 可完成保存；不虚报持久云端锁 |
| C-013 | ST-013-T | Finder 覆盖与目录复制正确：目标不存在 | 完整复制；F 拒绝覆盖；安全保存不先删旧版导致丢失 |
| C-013 | ST-013-F | Finder 覆盖与目录复制正确：目标已存在，Overwrite 缺省/T/F；文件目录冲突 | 完整复制；F 拒绝覆盖；安全保存不先删旧版导致丢失 |
| C-014 | ST-014-T | 特殊名字与元数据可重开：普通名称 | 路径可往返；冲突可见；不静默吞掉所需属性 |
| C-014 | ST-014-F | 常见特殊名字可重开：中文、emoji、引号、%#?+ | 路径可往返；冲突可见；文件主体内容完整 |
| C-015 | ST-015-T | 资源不足明确失败：正常权限、token、磁盘空间 | 保留可恢复副本；不把权限失败当空目录；退避有界 |
| C-015 | ST-015-F | 资源不足明确失败：401/403/429/5xx、云端配额、本地缓存满 | 保留可恢复副本；不把权限失败当空目录；退避有界 |
| C-017 | ST-017-T | 删除重试不误删新对象：删除成功且无重建 | 删除完成并核对后释放路径占用；回收站项可追踪 |
| C-018 | ST-018-F | 批量和受限网络下传输保持正确 | 批量、高 RTT 和限速条件下内容始终一致；失败可见且恢复后可用；请求和内存有界 |

仍不在本轮范围：同路径并发/读写竞争/第二实例竞争、真实主机断电、提交丢响应后取消、MOVE丢响应、缓存未提交时跨盘移动故障组合、异常Range与读取中外部替换、分页变化竞态、复杂锁异常、异步任务异常组合、删除丢响应后重建、网络丢包与恢复并发压力组合、目录检查与创建竞态、NFC/NFD及大小写冲突组合。已通过的416等子例证据仍保留，不因整项移出而删除。

移动、跨盘移动及文件/目录删除最后执行。

## 4. Finder 系统测试执行

Finder 验收必须由真实 macOS Finder 完成用户动作，并由 Computer Use 观察原生界面。云端 API 只用于准备、外部事件和独立事实校验，不能代替 Finder 写入。

### 4.1 执行边界

- 使用单个受控服务实例和全新的隔离 `TBOX_LAB_REMOTE`。
- 先执行不删除数据的场景；删除、回收站恢复和清理场景最后执行。
- Finder 的初始零字节 PUT 可以到达云端。通过信号是 Finder 报告完成后重新打开内容正确，且独立云端读取的大小和 SHA-256 一致。
- 录屏、拖拽成功、目标名称出现或 HTTP 2xx 都不能单独判定 PASS。
- 失败和未知结果保留现场，不复用目标路径，不删除未决状态。

优先使用现有 Compose 环境，并在项目及 Docker bind mount 之外挂载：

```sh
TBOX_RCLONE_CONFIG=/secrets/rclone-finder.conf \
TBOX_LAB_REMOTE='sjtu:codex-api-lab/<run-id>' \
docker compose --profile live up -d --wait webdav

python3 scripts/mount-webdav-macos.py mount
python3 scripts/mount-webdav-macos.py check
scripts/prepare-finder-fixture.sh /private/tmp/tboxrclone-finder-<run-id>
```

每次服务重启后都重新挂载，并用 `check` 核对系统挂载表中的 URL、挂载点和文件系统类型。fixture 必须属于当前登录用户，避免本地权限触发系统密码框。长批次可在已解锁的 Mac 上运行 `scripts/finder-session-guard.sh 1800`；它只阻止空闲睡眠，不绕过登录锁屏。

### 4.2 脚本化操作

```sh
scripts/finder-drag.sh --preflight SOURCE TARGET FILE MOUNT
scripts/finder-drag.sh --record SOURCE TARGET FILE MOUNT NEW_EVIDENCE_DIR
scripts/finder-drag.sh --record-overwrite SOURCE TARGET FILE MOUNT NEW_EVIDENCE_DIR
```

窗口准备通过原生 Workspace/Accessibility 定位本次测试目录，辅助进程有 30 秒超时。脚本负责窗口定位、鼠标序列、ScreenCaptureKit 显示器录屏和日志（默认 45 秒，可用 `TBOX_FINDER_RECORD_SECONDS=15..180` 调整）；Computer Use 负责读取真实 Finder 状态、处理确认框并继续观察耗时操作。录制前检查落点可见性，异常时仅重排测试窗口一次；发送鼠标事件前再次检查。录屏必须报告 RECORDING_STARTED，结束时检查实际媒体时长。拖拽期间不要切换前台应用，录制期间保持显示会话稳定。录制入口退出码 `1` 表示执行或录制失败，`2` 表示预检阻止执行，`3` 表示证据已采集但仍待审阅；它不会用退出码 `0` 宣布系统测试通过。

稳定大目录浏览使用 `scripts/finder-browse.sh SPEC.json NEW_EVIDENCE_DIRECTORY`。该脚本限制每个目录和整个批次的时长，但其机器结果仍须结合 Finder 录像和独立目录事实核对。

可复用校验器：

- `cmd/finder-observe`：上传期间独立采样云端对象。
- `cmd/finder-inspect`：递归检查云端目录和文件哈希。
- `cmd/webdav-range-verify`：固定 ETag 下验证首、中、尾 Range。
- `cmd/finder-list-fixture`：准备稳定目录规模。
- `cmd/webdav-length-fault`：构造普通文件无法产生的长度故障。
- `scripts/finder/verify-locks.py NEW_EVIDENCE_DIRECTORY`：直连回环 WebDAV 锁谓词补充验证，使用全新隔离目录，记录逐请求耗时并保留文件；不发送 DELETE/MOVE，不替代 Finder 验收。
- `scripts/finder/read-fault.py PATH EXPECTED_SHA256 NEW_EVIDENCE_DIRECTORY --mode status|hold|cut`：验证云端读取故障的响应完整性与恢复，保留响应体和时序，结束时释放挂起响应并清除故障；仅用于隔离测试服务。
- `scripts/finder/verify-absence.py`：交叉验证挂载与完整云端清单中的拒绝目标缺失，并对正常对照文件做完整哈希核验；目录枚举失败不视为空目录。

协议工具只能补充协议条件，不能替代要求 Finder 用户动作的子例。

### 4.3 操作陷阱

- **窗口与 Space**：先用 `finder-drag.sh --open` 创建并排列精确窗口。看见 Finder 进程或同名窗口不代表落点有效。
- **录屏范围**：单窗口视图不能证明桌面上没有密码或错误框；短时录像结束后仍需观察进度并校验完整内容。
- **加载状态**：目标列表未稳定时不要拖拽；先检查挂载和服务，不用延长鼠标按住时间掩盖问题。
- **AppleDouble（不属于必过范围）**：2026-11-14用户明确不需要此项。保留现有 `._name` 支持，但不保证扩展属性/resource fork完整往返。已有 opaque resource fork 丢失记录保留为兼容性限制，不改写为已修复；普通文件名与主体内容仍须完整验证。
- **Docker 文件共享**：不要把宿主 WebDAV 挂载 bind 进 Docker，否则 Docker VM 可能导致卸载持续 busy。
- **服务重启**：旧挂载、Finder 缓存和旧窗口均不能沿用；重新挂载、开窗并预检。
- **Finder 替换也可能删除**：直连实测中，确认“替换”后 Finder 先发送旧主文件的 DELETE，再准备写新版。即使已开启 `lab_overwrite`，禁用 `lab_delete` 仍会拒绝该流程并显示权限错误。此类重试归入最后的删除批次；启用后仍须验证失败时旧版可恢复，不能只凭成功覆盖判定安全。

## 5. 证据格式与门禁

[`test-manifest.json`](test-manifest.json) 是唯一系统验收状态清单；[`acceptance-cases.json`](acceptance-cases.json) 定义每个正负分支必须分别运行的子场景。

唯一证据根为仓库顶层 `evidence/`。完整场景使用 `evidence/ST-xxx-T/` 或 `evidence/ST-xxx-F/`，派生报告写入 `evidence/reports/`；未执行场景不创建空目录。每个场景按 manifest 的 `evidence_directory` 保存 `result.json`，`recording_path` 可指向真实 macOS 操作录像；录像不可用时，各子例记录 `final_state_image` 和 `final_state_reviewed:true`，同时保留独立事实核验。

```json
{
  "run_id": "唯一运行标识",
  "scenario_id": "ST-001-F",
  "versions": {
    "client": "具体客户端版本",
    "backend": "Git commit SHA",
    "server": "实际版本或明确说明无法获得",
    "macos": "实际系统版本",
    "finder": "实际 Finder 版本"
  },
  "cases": [
    {
      "id": "disconnect_first_part",
      "status": "PASS",
      "observation": "真实观察、故障时机、独立核对结果和未解决事项",
      "artifacts": ["first-part/redacted-log.json", "first-part/checksums.json"]
    }
  ]
}
```

示例只展示一个子场景；实际结果必须包含 `acceptance-cases.json` 要求的全部子例。`artifacts` 使用相对本场景证据目录的路径，不能用空对象或单个 PASS 字符串代替记录。

证据至少包含运行和组件版本、故障时序、完整 SHA-256、目录树、源/目标身份、上传会话或任务标识、副作用计数、重试次数、错误码和最终状态截图；录像是辅助证据。必须脱敏，不得保存 accessToken、userToken、JAAuthCookie、签名 URL 或认证头。

```sh
docker compose run --rm go-tests go run ./cmd/verify
```

`cmd/verify` 生成 `evidence/reports/acceptance.json` 和 `coverage.md`。存在非 PASS 时退出 1，清单结构错误退出 2。它不会执行 Finder、理解录像或仅凭文件存在判定真实性；PASS 必须来自实际运行与证据审阅。

## 6. 全量报告与准入

每个 ST 保存版本、日志、故障点、截图/录屏、原始与最终 SHA-256、远端身份和副作用计数，写入 test-manifest 对应 evidence_directory。manifest 当前 recording_path=null 是未录制，不代表无需证据。

报告应列出当前25个用例及各自保留的必需子场景，状态 PASS/FAIL/BLOCKED/NOT_RUN；同时列出上游集成测试 RUN/PASS/SKIP。已知实现缺陷按 FAIL 记录并关联问题；没有环境证据按 NOT_RUN/BLOCKED，不能写 PASS。

本轮验收完成要求第3节列出的25项及其保留子例全部通过；不再要求已归档的其余分支完成。保留用例仍须验证实际云端内容，不能把“缓存里有”算作云端保存成功。未覆盖的并发与低频异常不得宣称已获本轮验证。性能作为独立工程项目，不影响这25项系统验收的结论。

### Finder 出站故障会话

使用 `python3 scripts/finder-fault-session.py start evidence/ST-xxx-T/<run> --run-id <unique-run>` 保存正常服务配置、切换独立云端根和状态目录，并重新挂载。Finder 仍直接连接 rclone WebDAV；fault-proxy 只拦截后端的出站 HTTPS。默认允许 Finder 写入其零字节占位文件，保持 delete/move 关闭。

用 `scripts/finder/fault-control.py rules RULES.json` 设置故障，`events NEW.json` 保存脱敏事件，`release REQUEST_ID --drop` 丢弃被暂停的响应。分片规则应指定 `part_number`，避免误命中 Finder 占位或 AppleDouble。用 `scripts/finder/snapshot-journal.py STATE EXACT_CLOUD_PATH NEW.json` 保存允许字段和上传会话摘要，不输出签名 URL。

完成后执行 `python3 scripts/finder-fault-session.py restore evidence/ST-xxx-T/<run>` 恢复原配置和挂载，保留实验云端文件和本地状态。故障事件、最终 Finder 状态、journal 前后快照、挂载重读及独立云端全量哈希必须共同核对；脚本成功不代表验收通过。

### 受限网络正确性与证据采样

受限网络场景使用干净云端根与独立 state；`finder-fault-session.py start SESSION --run-id perf-... --transport direct` 可直接启动。旧故障会话的 journal/spool 必须保留并用 `check-pending.py` 验证，不能清空待恢复状态。`prepare-performance.py NEW_OUTPUT` 仅准备六文件浏览夹具，不替代 Finder 用户动作；脚本沿用历史名称，不表示该用例属于性能准入。

`performance-observe.py SESSION NEW_OUTPUT FILE BYTES --browse-spec SPEC --metadata-path /browse/item-00.txt` 同时记录 journal、原生目录浏览、阻塞弹窗和串行元数据延迟。验收只使用它证明上传期间仍可浏览、错误可见及最终内容正确，不设吞吐门槛。必须分别报告主文件 Committed、Finder 完整复制结束和独立内容检查；前者不能代替后者。

`network-profile.py` 生成高请求延迟或每流限速规则，交给既有 fault-control 启用。每次慢网络测试须检查注入事件、上传/浏览重叠、完整哈希及有界资源行为。代理存在额外开销，其绝对吞吐不参与系统验收。`retain-recordings.py --latest-only` 验证最新录屏后替换该用例保留录像，同时留下旧索引出处；未通过的子例不得因录像替换而改成 PASS。

截图审阅按用户要求固定为每10秒一次，并必须查看任务最终状态截图。录屏审阅脚本另输出 `review.final.png`；若录像提前结束，跳过录像采样和审阅，仅拍摄并审阅任务最终状态，同时完成独立事实核验。不再默认生成或查看每秒/每2秒截图。

### 一条指令完成文件或目录上传收尾

跨盘移动复用 `python3 scripts/finder/cross-disk-move.py SESSION NEW_EVIDENCE`，要求隔离 `ops-*` 故障会话。脚本确认源/挂载设备号不同，保留独立原始副本，真实执行 `mv`，在数据分片后暂停 confirm 响应并检查源文件仍完整，再释放并记录 journal/source 存在性采样。完整内容须另行独立回读；采样不声称提供原子文件系统事件追踪。

挂载内文件/目录移动复用 `python3 scripts/finder/rmdir-cases.py --case move SESSION NEW_EVIDENCE`。脚本保存本地原始副本，执行真实 `mv`，输出命令和初步内容检查；仍需独立云端全量读取、源路径缺失核验、move/dirmove journal 确认及最终 Finder 截图审阅。

删除/移动放在最后的独立 `ops-*` 会话执行：`finder-fault-session.py start EVIDENCE --run-id ops-UNIQUE --transport direct --file-operations` 仅对新会话启用操作权限，保留原会话快照。`python3 scripts/finder/rmdir-cases.py SESSION NEW_EVIDENCE` 使用真实挂载上的终端 `rmdir` 检查非空拒绝与空目录删除，并记录命令、退出码和内容。随后以 `finish-upload.py` 收集独立云端和最终界面证据，使用 `verify-absence.py` 确认空目录消失、哨兵文件保留；命令成功本身不代表验收通过。

```sh
python3 scripts/finder/finish-upload.py SESSION RUN_EVIDENCE
```

读取现有 `run.json`，核对当前服务与会话一致后，一次收集代理事件，并行执行云端完整回读/挂载重读、journal哈希、10秒录像采样、最终原生界面检查和截图。最终返回 `finish/summary.json` 及其 `review_images`；一次性查看这些图片，必须包含 `required_final_image`。最终图提供1920像素预览以减少图像传输与读取开销，原始截图保留在 `original_final_image`。

错误框、进度、Finder目标窗口在一次Accessibility遍历中检查；已有目标窗口时跳过重新打开和排列。发现阻塞框或仍在复制时保留当前界面，不通过重新开窗掩盖状态。原生读取不完整会明确报错；退出0也不自动判验收PASS。录屏仅为辅助证据；中止、不可用或切屏后跳过录像审阅，不为补录重跑。最终状态截图、journal、独立内容及场景特定后验充分时可通过。

最终截图的就绪检查必须在目标窗口内找到预期文件名；目录用例检查一个非隐藏子项，不能仅凭窗口标题判断内容已加载。重新开窗后有界等待内容可见，超时保留截图并报错。特殊文件名用例可在 `run.json` 指定 `cloud_name_map`，复用既有明确的一对一编码映射核验；挂载侧仍按原始文件名逐项重读。

`--output NEW_DIRECTORY`用于另存复测；`--movie PATH`选择已有录像；`--skip-video`用于已知切屏等情况，录屏中断日志或提前结束标记会自动跳过视频；故障测试可加`--clear-faults`，在保存事件之后清除规则再核验内容。脚本不重传、不删除源文件、不改验收状态。`run.json` 设置 `tree:true` 时复用目录树核验工具，检查整个复制目录、其子文件journal，并在最终截图中展开目标目录。

2026-11-14实测：一条指令收尾17.08秒（内容核验16.73秒，其他工作并行）；该轮各步骤耗时相加25.43秒。随后将三个原生界面探针合并，独立实测扫描约2.01秒，取代原来合计6.59秒的三次命令。前后网络缓存不同，不能把云端下载耗时变化当作代码优化收益；完整收尾耗时不含人工/模型审阅图片的时间。

同日双文件目录实测：完整收尾23.14秒，内容核验23.14秒；并行分支中journal为0.12秒、录像采样1.23秒、原生检查2.86秒、展开目标目录3.20秒、最终截图及预览0.56秒。这些分支已被内容核验耗时覆盖，继续压缩截图生成对这一轮总耗时帮助有限。图片审阅仅需打开10秒采样拼图及最终状态图；该轮两张图均已审阅。明细见 `evidence/ST-018-F/multiple-files-20261114/finish/summary.json`，不将图片审阅时间计入脚本耗时。

### 2026-11-14 覆盖协议补验（ST-013-F）

`evidence/ST-013-F/protocol-20261114` 保留真实直连服务的 COPY/MOVE 请求台账。缺省/T 返回 204 并替换完整内容；F 返回 412 且双方原内容保留；文件→目录及目录→文件返回 403，双方哨兵保留。18 个文件经本地参考、重新打开挂载、独立云端全量读取核验一致，MOVE 成功后的源路径在挂载和云端均不存在。最终 Finder 截图已查看，无进度窗或错误弹窗。原生 Finder 顺序替换证据复用 ST-006-T；不据此声称中断中的 Finder Replace 具有原子性。

### 删除用例收尾（ST-017-T）

使用 `scripts/finder/rmdir-cases.py --case delete-prepare SESSION NEW_EVIDENCE` 仅准备独立 `DeleteCases` 夹具；保留 `reference/delete-me.txt`，预期最终目录仅含 `keep.txt`。`scripts/finder/delete-fixture.swift` 通过 Finder 唯一选中行解析后的完整文件 URL 核对目标，再发送 Command-Delete；它不确认删除弹窗。若自动审批要求当次确认，必须等待用户回复，不能改用 API 绕过。

确认并完成删除后，依次使用已有 `finish-upload.py --skip-video`（录屏未覆盖操作时）、`verify-absence.py`（要求 `delete-me.txt` 不存在、`keep.txt` 完整）以及 `verify-delete.py SESSION DeleteCases/delete-me.txt NEW_RECEIPT.json` 收尾，最终截图必须实际查看。新脚本只读检查恰好一个已提交删除记录、非空回收站 ID、无同路径 pending 记录及 WebDAV HEAD 404；不能把回收站 ID 等同于已验证恢复，也不能把 HEAD 404 等同于后续写入成功。未点击确认时的负向检查已保存于 `evidence/ST-017-T/delete-20261114/before-confirm-receipt-check.json`，应为不通过，不能当作产品故障或已通过删除用例。

2026-11-14 ST-017-T 已在用户当次明确确认后完成：Finder 删除成功，目标在挂载与独立云端清单中均不存在，邻近 keep.txt 三方内容一致；唯一删除记录 Committed，回收站 ID 为 61182908，无同路径 pending，后续 HEAD 返回 404。最终截图已查看，无错误或进度弹窗。证据位于 `evidence/ST-017-T/delete-20261114`；本次未重建同路径文件，未声称完成回收站恢复测试。此前等待授权记录及负向检查作为过程记录保留。

### 独立性能项目

ST-018-T 已从系统验收删除。性能实验统一使用 `scripts/performance/benchmark-webdav.py` 与 `docker-compose.performance.yml`，不操作 Finder；流程、代码测量结果及历史 Finder 未达标记录见 [performance-experiments.md](performance-experiments.md)。性能结果不修改本页25项系统验收结论。
