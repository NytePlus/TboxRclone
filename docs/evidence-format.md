# 验收证据格式

`test-manifest.json` 是指向 `docs/test-manifest.json` 的符号链接，只有一份验收规格。所有原有预期保留。[acceptance-cases.json](acceptance-cases.json) 把正负分支中要求分别运行的子场景展开，不能只执行一个断网位置就将整个负分支算作通过。

`go run ./cmd/verify` 生成 `reports/acceptance.json` 和 `reports/coverage.md`，列出全部 36 个分支及子场景；存在非 PASS 时退出 1，清单结构错误退出 2。它检查证据完整性，不会自动驱动 Finder，也不能仅靠文件存在证明内容真实性；PASS 必须来自实际运行与证据审阅。

每个场景按 manifest 的 `evidence_directory` 保存 `result.json`，且 `recording_path` 指向非空的真实 macOS 操作录屏。结果格式：

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

例子仅展示一个子场景；ST-001-F 实际还必须含 `disconnect_middle_part` 和 `disconnect_last_part`。每项 artifacts 为相对本场景证据目录的文件路径。JSON 不允许用空对象或单个 PASS 字符串代替记录。

按 verification.md 保留：故障注入前后时序、完整文件 SHA-256、目录树、源/目标 inode/CAS/version、上传会话/任务标识、版本与回收站增量、重试数、网络与进程故障点、错误码和截图。证据必须脱敏：无 accessToken/userToken、JAAuthCookie、签名 URL 或认证头。日志和录屏不默认提交 Git。

verification.md 第 1 节的服务端语义发现实验、第 2 节的控制面/数据面故障覆盖，以及第 6 类效率对比仍需独立执行。门禁遍历 manifest 不是这些实验已执行的证据。
