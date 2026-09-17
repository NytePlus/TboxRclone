# Docker 与本机 VPN 的 DNS 冲突

2026-09-17 实测：宿主将 `pan.sjtu.edu.cn` 解析为 VPN 地址 `172.19.0.183`，但 Docker 中其他项目已有 `172.19.0.0/16` 网段，容器连接因此超时。宿主 HTTPS 正常；直接查询公共 UDP DNS 仍返回同一虚拟地址。没有修改其他项目网络，也没有重置 Docker。

可选 Compose 覆盖文件使用项目内 DNS relay，通过 `https://8.8.8.8/dns-query` 获取真实 DNS wire-format 回答。HTTPS 验证默认开启，该端点有合法 IP 证书；云盘及数据面连接仍使用原主机名验证 TLS。没有固定云盘 IP，也没有关闭证书检查。DNS 查询会交给 Google 公共解析器；仅在公共域名可用时采用，私有域名应使用组织认可的解析方案。

```sh
docker compose -f docker-compose.yml -f docker-compose.doh.yml run --rm go-tests \
  sh -c 'getent ahostsv4 pan.sjtu.edu.cn; curl --fail --silent --show-error --max-time 10 -o /dev/null -w "%{http_code}\n" https://pan.sjtu.edu.cn'
```

本次返回真实公网地址 `202.120.35.250` 和 HTTP 200；该地址只作为观察证据，不写进配置。随后真实 Finder 上传和两个独立完整下载成功，证明控制面和此次数据面均可访问。

运行 live/faults 服务和观察器时须一并指定两个 `-f` 参数。固定项目 DNS 网段 `10.253.253.0/29` 在本机已核查无已有 Docker 网段冲突；换环境前应重新检查。relay 不发布宿主端口、不写查询日志、不持久缓存；它是实验环境工具，不是生产 DNS 服务。恢复默认配置前先卸载测试网盘，再以基础 Compose 文件重新创建相关服务。任何服务重启后重新挂载并核验，不能沿用旧挂载状态。
