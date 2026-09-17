# HTTPS 故障注入实验

`cmd/faultproxy` 提供实验专用 HTTP CONNECT 代理，能够在 TLS 内对控制面和签名数据面分别注入故障。其 CA 私钥仅在进程内存中，公钥证书写入显式指定的文件；不会修改 macOS 或系统的信任设置。代理仅监听 loopback 地址，只允许显式指定的上游 `host:port`，上游仍按正常 TLS 根证书验证。

这是一项测试设施。离线模拟上游的测试不等于交大服务器或 Finder 的实际验收。

## Docker Compose

先配置 README 中的私有实验配置，再设置真实数据面域名；域名只能来自本次上传初始化的有效响应，不可猜测：

```sh
export TBOX_FAULT_HOSTS='pan.sjtu.edu.cn:443,<本次签名上传域名>:443'
export TBOX_LAB_REMOTE='sjtu:codex-api-lab/<run-id>'
docker compose --profile faults up -d --wait fault-proxy
docker compose --profile faults up -d webdav-faults
```

`webdav-faults` 与代理共享网络命名空间，通过 `HTTPS_PROXY=http://127.0.0.1:8787` 和 `SSL_CERT_FILE=/state/faultproxy-ca.pem` 显式接入代理。Finder 入口是本机 `http://127.0.0.1:8687/`。Compose 通过 `scripts/run-go-service.sh` 编译并 exec 服务二进制，使 SIGTERM 直接到达服务的退出处理器。代理控制端口 8788 不发布到宿主机，用 `docker compose exec fault-proxy` 操作。与无故障的 `webdav` 服务（8686）分开。

缺少签名数据域名时代理会拒绝该连接，不能当作“数据面故障覆盖通过”。更改 allowlist 或重启代理后 CA 会变化，必须重启 `webdav-faults`，避免继续使用旧进程缓存的证书池。Docker 默认代理环境不等于接线证明；必须在 `/events` 中看到控制面及数据面两类 authority 的请求。

## 请求已完成后丢响应

把以下规则保存为 `.state/confirm-fault.json`：

```json
[
  {
    "id": "confirm-after-origin",
    "host": "pan.sjtu.edu.cn:443",
    "method": "POST",
    "path_prefix": "/api/v1/file/",
    "action_key": "confirm",
    "nth": 1,
    "action": "hold_response"
  }
]
```

```sh
docker compose exec fault-proxy curl --fail --silent \
  -X PUT -H 'Content-Type: application/json' \
  --data-binary @/state/confirm-fault.json http://127.0.0.1:8788/rules
```

然后通过真实 Finder 或规定的用户路径进行操作。观察代理事件：

```sh
docker compose exec fault-proxy curl --fail --silent http://127.0.0.1:8788/events
```

出现 `origin_response_complete` 表示代理完整收到上游响应；**并不单独证明操作已成功提交**。`held` 表示客户端仍未收到响应。此时使用绕开代理的独立读路径核对远端完整内容 SHA-256、身份、版本/回收站副作用。只有独立事实核对确定本次操作已执行，才能把本次实验记为“提交已执行后丢响应”。将下列 request 替换为本次 held 事件的 request 数字：

```sh
docker compose exec fault-proxy curl --fail --silent \
  -X POST -H 'Content-Type: application/json' \
  --data '{"request":1,"forward":false}' http://127.0.0.1:8788/release
```

`forward=false` 丢弃响应并关闭连接；`true` 交付原响应。提交调用应保持 Unknown，不能自动删除本地 spool 或重复提交。恢复后使用已有 `tbox-state` 进行对账。代理默认在两分钟后结束未释放的 held 请求，此类事件是 `hold_timeout`，不能冒充独立事实核对已完成。

## 其他注入模式

| action | 注入点 | 可观察结果 |
|---|---|---|
| drop_request | 发送上游前关闭客户端连接 | 上游未收到请求，与提交后丢响应分开 |
| drop_response | 收到完整上游响应后关闭客户端连接 | 可用作丢响应，但仍需独立证明副作用 |
| hold_response | 收到完整响应后暂扣 | 独立核对完成，再显式 forward/drop |
| cut_upload | 上游请求体最多读 bytes 字节后报错 | 不声称上游一定收到了这些字节；必须核对实际状态 |
| cut_download | 交付客户端的响应体最多 bytes 字节后报错 | 客户端必须检测短响应/连接断开 |

每条规则仅作用于第 `nth` 次匹配，之后不再触发。用 method、path_prefix 和可选 action_key 区分 confirm、upload、renew 等请求。数据面规则使用签名 URL 的 origin/path，但规则和证据不得包含签名查询值。规则配置有 held 响应时返回 409，防止重置规则遗失待核对请求。

## 边界与证据

- 最多 64 条活动隧道，100 条规则；头部最大 64 KiB，held/drop-response 缓冲上限 1 MiB。响应超限或未收完整会记为 `response_not_complete_or_over_limit`，不是完成证据。
- 事件保存最近 4096 条，序号缺口表示有事件过期；实验应在每个场景及时导出，不能用缺记录的快照声称完整覆盖。
- 事件不保存 URL、路径、query 值、请求/响应正文、cookie、token 或签名。规则 ID 由实验者命名，应使用非敏感标签。
- 每个 TLS 隧道只执行一次 HTTP/1.1 交换，降低连接复用对故障相位的歧义；该代理不适合作为直连吞吐基线。带宽/RTT/丢包曲线等性能注入尚未实现。
- 没有自动执行 Finder 点击、系统重启或云端读写。实际实验仍遵守 verification.md 的隔离空间、独立读取、截图和录屏要求。
- 默认不会自动把代理事件写入磁盘；每次运行应导出到对应 evidence_directory，连同独立事实核对的原始证据一起保存。

## 当前离线条件覆盖

| 验收条件 | 测试设施用例 | 已证明的范围 |
|---|---|---|
| C-001/C-007 | TestSignedDataPlaneUsesSameFaultProxy、TestCutUploadAndDownload | TLS 控制/数据双通道确实进入代理，截断可被客户端发现 |
| C-002/C-004/C-017 | TestHoldConfirmUntilIndependentFactCheck | 实际上游处理完成、独立读核对、丢响应、无自动写重放 |
| C-003 | TestControlReleaseAndShutdown | 代理退出时关闭已接管的 TLS 连接，不等同宿主机崩溃验收 |
| C-015 | TestBeforeAndAfterOriginAreDifferent、TestNthRuleAndAuthorityIsolation | 故障相位与次数明确，上游 authority 不可被 Host 绕过 |
| 凭据保护 | TestHoldConfirmUntilIndependentFactCheck、TestSignedDataPlaneUsesSameFaultProxy | 日志中不出现测试 token、confirmKey 或签名值 |

全部这些是设施测试。ST-xxx 的系统场景状态不因此改变。
