# 交大云盘接口文档（研究版）

## 1. 证据与边界

- **C**：当前 Tbox 源码存在调用/DTO，不表示本次已实测。
- **F**：交大当前公开网页 JS 中存在接口定义，不表示当前账户有权限，或该功能开关已启用。
- **S**：公开 `smh-js-sdk@1.0.16` 定义；可能比交大部署更新。
- **V**：登录后在隔离空间验证。本次已取得部分 V 级证据及明确不支持的行为，见 [真实接口实验](live-api-findings.md)。

Tbox 基线为 `4537e22adf241158783fe26661b8081f9bc45e94`。C 主要来源：原 Tbox 项目的 `Modules/Tbox/Services/TboxService.cs` 和 `Modules/Tbox/Models`（源码未包含在此仓库）。F 来源：[交大前端](https://pan.sjtu.edu.cn/js/index.41e36dd0.js)。S 来源：[npm 包](https://www.npmjs.com/package/smh-js-sdk) 与 [SDK 仓库](https://cnb.cool/tencent/cloud/smh/smh-javascript-sdk)。精确摘要在 `sources.json`；前端定义原文、偏移及 SDK operation 名在 `discovered-endpoints.json`。

SDK 生成代码中的 `#1`、`#2` 等是路径字面量中的 fragment，不能当作服务器路径后缀。清单保留原文并另给去 fragment 的路径。本文的 `?info` 等表示 action 查询键，实际是否需要值 `1` 应按部署验证，不能只拼接参数名就假定通用。

## 2. 认证、路径、错误模型

基址 `https://pan.sjtu.edu.cn`。以下记 `L=libraryId`、`S=spaceId`、`P=按段编码的相对路径`、`K=confirmKey`；所有 `{L}/{S}` 均来自空间凭据，不硬编码。`/user/v1/space/1` 的 `1` 是现有项目的部署常量，也要在多组织场景验证。

| 方法与路径 | 输入 | 现有响应/用途 | 证据 |
|---|---|---|---|
| GET `/user/v1/sign-in/sso-login-redirect/xpw8ou8y` | JAAuthCookie；auto_redirect/from/custom_state | 经 SSO 回调得到 code；可能发生跳转 | C |
| POST `/user/v1/sign-in/verify-account-login/xpw8ou8y` | type=sso、credential=code、device_id | userToken、status/message | C |
| POST `/user/v1/space/1/personal` | user_token | libraryId、spaceId、accessToken、expiresIn | C |
| GET `/user/v1/space/1` | user_token | 空间额度 | C |
| GET `/user/v1/organization` | user_token | 组织/用户信息数组 | C |

数据 API 一般使用 `access_token`；F 中部分定义还带 `user_id`、`space_org_id`。SDK 的 `library_secret` 属于另一种权限入口，不是普通用户接入所需凭据。禁止把 UserToken、AccessToken、签名 URL 写入日志/证据文件。不同凭据的缓存按用户与空间隔离；按 expiresIn 提前刷新并合并并发刷新。401/403 不统一当成“不存在”，也不无限刷新重试。SSO code 交换不是普通可重放读操作。

路径逐段 URL 编码一次，保留层级 `/`，JSON 内容用序列化器，不能把源路径插进 JSON 字符串。测试空格、中文、引号、反斜线、`% # ? +`、emoji、NFC/NFD、大小写冲突。Tbox 的 CreateDirectory 路径未统一按段编码，Copy/Move 源路径手拼 JSON，不能直接搬入 Go。

保留 HTTP 状态、服务端 code/status/message、请求标识与重试分类。2xx 仍需检查业务状态；异步 taskId/202 只代表受理。404 区分对象、目录、上传会话或任务不存在。已发现的 SDK 冲突约定是 HTTP 409 + `SameNameDirectoryOrFileExists`，交大错误码须补录实测。

## 3. 当前 Tbox 数据接口

下表省略共同的 `access_token`。部分调用与重放已有有限 V 实验；完整原子性、持久性和幂等性契约仍未证明，见真实接口实验。

| 方法与路径 | 参数/请求体 | 响应重点 | 来源/注意 |
|---|---|---|---|
| GET `/api/v1/directory/{L}/{S}/{P}?info` | 无 | type/name/path/size、eTag、crc64、modificationTime；文件 DTO 有 versionId | C；GetItemInfo/FileInfo/FolderInfo 共用 |
| GET `/api/v1/directory/{L}/{S}/{P}` | page/page_size/order_by=name/order_by_type=asc | 目录内容与分页信息 | C；现有调用每页 50 串行读取 |
| PUT `/api/v1/directory/{L}/{S}/{P}` | conflict_resolution_strategy=ask | 状态/错误 | C；重复建目录不等于新建成功 |
| GET `/api/v1/file/{L}/{S}/{P}` | Range: bytes=start-end | 内容流；验证 200/206/416、Content-Range | C；可重试但必须固定版本 |
| PUT `/api/v1/file/{L}/{S}/{P}` | strategy=overwrite，JSON `{}` | domain/path/headers/confirmKey 等上传参数 | C；简单上传初始化，不是直接上传内容 |
| POST `/api/v1/file/{L}/{S}/{P}?multipart` | strategy=rename；`{"partNumberRange":[1,2]}` | confirmKey、uploadId、domain/path、expiration、parts 的签名 headers | C+实测；片号是明确列表（[1,3] 不包含 2），客户端每批最多 50 片；实测 50/51 个授权均可返回，硬上限未证实 |
| PUT `https://{domain}{path}?uploadId=...&partNumber=N` | 返回的签名 headers + 分片字节 | HTTP 状态；应记录分片 ETag/大小 | C；数据面 URL 来自服务端，不自行猜测 |
| POST `/api/v1/file/{L}/{S}/{K}?renew` | `{"partNumberRange":[...]}` | 新的分片授权 | C+实测；返回相同 K/uploadId/path 的逐片新授权；续期不等于已传内容持久化 |
| GET `/api/v1/file/{L}/{S}/{K}?upload` | 现有代码额外 no_upload_part_info=1 | confirmed/path/type/uploadId/parts 等 DTO | C+F；DTO 有字段不代表该参数下实际返回 |
| POST `/api/v1/file/{L}/{S}/{K}?confirm` | strategy=overwrite；可选 `{"crc64":"十进制字符串"}` | path/name/size/eTag/crc64/时间/isOverwrittened | C；实测 ask 初始化允许同名会话，最终 confirm 冲突返回 409；错误 content_cas 覆盖保护被忽略 |
| DELETE `/api/v1/file/{L}/{S}/{P}` | permanent=0 | 204 或 recycledItemId | C；删除到回收站 |
| PUT `/api/v1/file/{L}/{S}/{目标P}` | strategy=ask；`{"from":"源路径"}` | path | C；服务端移动，目录不可据此推定可靠 |
| PUT 同上 | strategy=ask；`{"copyFrom":"源路径"}` | path | C；复制不保证单请求同步完成 |

`GetFileInfo(path, historyId)` 的 historyId 在当前请求构造中没有使用，因此当前封装并未真正提供历史版本读取。size/CRC64 常为 JSON 字符串；Go 使用 int64/uint64 或保留字符串，不能经浮点转换损失精度。ETag 不能直接当作 MD5；CRC64 的多项式、初始值、反射和编码均需与服务端已知向量核对。

## 4. 新发现的高价值能力

| 优先级 | 接口 | 参数/意义 | 证据 |
|---|---|---|---|
| P0 | DELETE `/api/v1/file/{L}/{S}/{K}?upload` | 取消上传；S 说明分块任务同时放弃 COS multipart；重复取消、确认竞争待测 | F+S |
| P0 | GET `/api/v1/file/{L}/{S}/{K}?upload` | 实测必须 no_upload_part_info=1；仍返回已上传分片列表 | C+F+S |
| P0 | GET `/api/v1/task/{L}/{S}/{taskIdList}` | 查询异步状态，不把受理当完成 | F+S |
| P0 | PUT `/api/v1/directory/{L}/{S}/{目标P}` | copyFrom + strategy；目录专用复制 | F+S |
| P0 | PUT 同上 | from + strategy；目录专用移动 | S；F 普通移动定义走 file 路径 |
| P0 | DELETE `/api/v1/directory/{L}/{S}/{P}` | permanent、directory_only；验证能否服务器端保证只删空目录 | S |
| P0 | 文件 confirm/move/copy/delete 等的 content_cas；info 的 with_content_cas | 候选条件写保护；实测错误条件在删除、覆盖 confirm、移动源/目标均被忽略，不能用作并发保护 | S |
| P0 | info 的 with_inode；GET `/api/v1/inode/{L}/{S}/{Inode}` | 候选稳定身份，用于移动结果核对 | S |
| P1 | HEAD `/api/v1/file/{L}/{S}/{P}` 与 `/directory/...` | 快速状态/权限检查；不能代替 GET 校验内容 | F+S |
| P1 | GET `/api/v1/file/{L}/{S}/{P}?info` | 下载信息/签名地址；history_id 等；减少内容代理开销 | F+S |
| P1 | 目录 GET 的 marker/limit | 避免仅依赖 offset page；是否快照一致仍待测 | F+S；S 另定义 by-marker/by-page |
| P1 | GET `/user/v1/history/{organizationId}/{S}/history-list/{P}` | user_token/page/space_org_id；可选 page_size/排序 | F |
| P1 | POST `/api/v1/directory-history/{L}/{S}/latest-version/{historyId}` | 历史版本恢复 | F+S |
| P1 | GET `/api/v1/directory-history/{L}/{S}/history-list/{P}` | SDK 数据面历史列表，和上面的 user API 不混用 | S |
| P1 | POST `/user/v1/recycled/{organizationId}/list` | user_token；可选空间过滤、分页 | F |
| P1 | POST `/user/v1/recycled/{organizationId}?restore` | recycledItems 数组；细项结构待抓取验证 | F |
| P1 | GET `/api/v1/recycled/{L}/{S}`、POST `.../{recycledItemId}?restore` | SDK 数据面回收站查询与恢复；支持恢复冲突策略 | S |
| P1 | GET `/api/v1/fs-journal/{L}/{S}/{dirNode}` | syncId；ssn_marker/journal_marker/journal_limit | F |
| P1 | `/api/v1/fs-journal/{L}/{S}/{dirNode}/fs/{relativePath}` | F 定义 HEAD、GET/info、列举、简单/分块上传；ssn/syncId；上传 body 可含 modificationTime | F |
| P1 | GET `/api/v1/fs-delta/{L}/{S}/cursor` 和 `/delta` | cursor/limit 增量日志，候选 ChangeNotify 实现 | S；不要与 fs-journal 当作同一协议 |
| P2 | GET `/api/v1/file-deletion-check/{L}/{S}/{Inode}` | 删除状态核对 | S |
| P2 | GET `/api/v1/task-result/{L}/{S}/{taskId}` | 新版任务结果接口 | S |

此外 SDK 还有 batch、share、search、favorite、quota、usage、HLS 等；完整原始操作清单随文档保存。管理组织、永久清空、历史清理、分享权限不属于最小网盘后端范围。

没有找到足以证明普通数据接口支持原子 compare-and-swap、POSIX fsync、持久锁或完整 xattr 的交大实测材料。`content_cas` 名称和 `ssn` 参数只构成调查线索。

## 5. 幂等性与结果未知

“重复请求最终同名同内容”弱于“只执行一次”：重放仍可能多出版本、回收站项、计费或异步任务。HTTP 方法不能替代服务端语义证明。

| 操作 | 重试原则 | 网络中断后的核对 |
|---|---|---|
| info/list/quota | 带退避可重试；列表需去重/检测分页漂移 | 读取不是时间点快照 |
| Range 下载 | 绑定内容版本/校验器；206 起止必须一致 | 云端已变更则中止该次拼接并重新开始 |
| 初始化上传 | 不盲目重复；第一次可能已生成 K 但响应丢失 | 本地 op_id 不能强行让远端幂等；未知孤儿等待发现/过期 |
| 分片 PUT | 仅同 uploadId、partNumber、相同字节重试，先验证覆盖规则 | 查询实际分片 size/ETag；重放必须从头读取该块 |
| renew | 用已有 K 续期；刷新签名后重试，不重新建整任务 | 续期结果/原 K 是否有效 |
| confirm | 响应丢失进入 Unknown；不马上再初始化/覆盖 | 先查 upload 状态，再查实际返回路径、身份、大小/完整内容 |
| abort | 只针对本次已知 K；确认竞争时不得转为 DELETE 正式路径 | 查询 confirmed/最终文件与会话是否已终止 |
| mkdir | 已有同名目录可映射 rclone 成功；已有文件必须报错 | rclone Mkdir 的语义不同于 WebDAV MKCOL |
| move | 响应丢失后不能把源 404 简单当失败/成功 | 源+目标身份/版本核对；目标仅同名同大小不够 |
| copy | rename 策略重放会创建副本，overwrite 会覆盖并发修改 | 查询目标身份、任务完成、字节完整性 |
| delete | 首次成功后同路径可能被别人重建 | 必须对原对象/版本删除；没有条件删除则禁止自动重放未知请求 |

退避只覆盖明确可重试错误与已经证明可重放的操作，使用指数退避、jitter、Retry-After 与总时限。遇到超时/5xx，写请求可能已执行；即使刷新 token，也不能绕过结果核对。

## 6. 崩溃一致性设计契约

本地持久操作日志：op_id、账户/空间、源和目标、旧对象身份/版本、预期大小/内容摘要、K/uploadId、分片大小与完成记录、任务 ID、最终路径、状态。持久状态不能保存明文长期凭据。写日志/缓存文件应先 fsync 再切换状态，必要时 fsync 父目录；具体 OS 与磁盘掉电保证仍需测试。

状态机：`Prepared → Uploading → ReadyToConfirm → CommitSent → Committed`。提交前明确取消进入 `Aborting → Aborted`；CommitSent 后无确定响应进入 `Unknown → Reconcile`。不能把 Unknown 清理成失败，也不能在此状态删除本地唯一完整副本。

正式路径只允许看到旧完整版本或新完整版本。可用“唯一临时路径上传→确认→服务端移动覆盖”实现的前提，是该移动覆盖的原子性已经验证；临时文件方案本身不创造原子性。禁止“先删旧文件再上传”作为安全覆盖。rclone/VFS/WebDAV 调用链也要检查是否会先删目标，再调用后端 rename。

Ctrl+C/SIGTERM：停止新任务，取消可取消请求，在时限内落盘状态；已交给远端的提交不可承诺撤销。SIGKILL/进程崩溃：下次用持久日志和云端状态收敛。恢复期间若对象身份无法证明，应报告冲突并保留数据，不能猜测成功后删除源。

跨盘移动是复制、确认、删除两个阶段；只有目标已完整且达到约定持久边界才能删源。VFS 先确认本地缓存的挂载路径不能默认提供“目标已云端落盘才删本地源”的保证，必须有明确同步完成信号或使用经过验收的直接传输命令。

## 7. 实施阶段补充核对（S 级，未实测）

- SDK 1.0.16 `directory-api.js` 对 `directory_only=1/true` 的解释是“只删除目录对象本身，不级联删除子文件和子目录”。这不是“非空目录返回错误”的保证；仍须验证子项可达性与并发创建竞态。当前后端拒绝 Rmdir。
- `multipart-upload-file201-response.d.ts` 声明顶层 `headers`、`uploadId`，没有分片签名 `parts` 字段；而已有 Tbox 研究使用分片授权列表。真实交大实例已确认使用逐片 parts 映射，不能直接照搬该 SDK。
- `get-file-upload200-response-parts-inner.d.ts` 定义已上传分片字段为 `PartNumber/LastModified/ETag/Size`（注意大小写）。不能与初始化返回的签名字段混用。
- `complete-file-upload-request.d.ts` 支持 `localCreationTime/localModificationTime`；这些是本地时间元数据，不等同于服务端 modificationTime，不应直接宣称支持 rclone SetModTime。
- `complete-file-upload200-response.d.ts` 的覆盖标识为 `isOverwritten`，与已有研究记录的 DTO 拼写不同；实际解析应以交大响应为准。

上述描述来自 sources.json 锁定的 SDK 包。后续交大实例探测已确认签名分片映射、上传状态参数要求，以及部分条件字段不起作用；以 [真实接口实验](live-api-findings.md) 的具体证据为准。

## 8. 小文件恢复和创建竞争的部署结论

简单上传 K 调用 `renew`：空 body 返回 400 `ParamInvalid`；提供 `partNumberRange:[1]` 返回 404 `NotMultipartUpload`。因此不能假设简单上传也可续签。0 字节和 4 字节文件均已通过单片 multipart 的初始化、PUT、confirm、完整读回；后端统一采用该协议，不再为小文件选择无法续签的简单上传。

两个先初始化的同目标 ask 会话中，第一个 confirm 200，第二个 confirm 409 `SameNameDirectoryOrFileExists`，独立读取保持第一个内容。初始化现有文件 ask 仍 201，不能把初始化成功当作预留文件名；必须在 confirm 处继续使用 ask 并处理冲突。

错误 `content_cas` 在 overwrite confirm 中返回 200 并实际替换内容；移动同时提供错误 query `content_cas` 和 body `contentCas` 也返回 200、源路径 404。这些实测不支持条件覆盖/条件移动。目录专用移动返回 204 并保留子文件，但其并发/未知结果契约仍未证明。`fs-delta/cursor` 本次返回 404。具体证据见 [真实实验](live-api-findings.md)。
