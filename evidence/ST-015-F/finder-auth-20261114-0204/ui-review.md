# HTTP 401 故障阶段

隔离代理对新文件 multipart 初始化注入一次 HTTP 401。Finder 录像 44.982 秒，本轮检查 10..19 秒：10..11 秒仍在准备，12 秒出现要求本机密码的“访达”拷贝授权框。此处不是普通云端登录提示。未输入或读取密码；随后通过精确匹配 resource-control 和“取消”按钮取消 SecurityAgent 弹窗。

source_preserved=true，零字节占位 journal 为 Rejected，独立云端完整目录为空。零字节 spool 不代表完整源文件副本；完整内容保留在原源文件中。

恢复录像已核对 0..9、70..89 秒：拖拽后出现进度，89秒仍显示进度。三方完整读取均为 8,832 字节，SHA-256 为 7de313cc477a573442c7f1cd907c3b8df0a292492d046ffd06cd2f05b006d235；新 journal Committed，旧 Rejected 保留。

额外录制 29.968 秒 completion-followup.mov，检查 20..29 秒，Finder 已无进度或错误，目标文件显示正常。两段录屏之间有间隙，未捕捉准确完成瞬间，不能宣称首段90秒覆盖完成。保留完整录像与采样索引。错误提示、源文件保留、云端空目录、同路径重试和最终UI完成状态均已观察；http_401 子例 PASS。
