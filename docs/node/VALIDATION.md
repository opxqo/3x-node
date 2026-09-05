# 验证记录

状态：实施中。不得标为“128MB 可用”。

## 已知主面板限制

基准 `ed6bc1d8` 的 `Remote.ResetInboundTraffic` 直接发送主面板 `ib.Id`，
不按 tag 解析远端ID（与增删改的实现不同），因此两边ID不一致时，单入站清零可能失败或指向错误入站。
精简节点无法从此请求推断正确目标；主面板生产代码保持不变，暂不宣称此操作完全兼容。
账号清零按email、全部入站清零不依赖这一ID映射。后者按原源码只清入站计数，不清账号配额计数。

`go test ./...` 首轮：绝大部分包通过；macOS上的4项原有MTProto子进程测试超时。
仓库HTTP扫描测试另外需要排除独立节点入口（它将Serve错误传回停机协调器，不使用面板日志包装器）。
只修改该测试的作用域，没有修改主面板生产代码。
修正扫描范围、将临时Go工具链移出仓库，并以 `go test -p 2 ./...` 重新运行后，全部包通过。
`go test -race -tags mastercontract ./internal/node ./cmd/3x-ui-node ./tools/nodebench ./internal/web/network` 通过。

## 正在运行的24小时测试

2026-09-06 02:26（Asia/Shanghai）启动本地隔离负载测试，尚未完成。
目录 `.cache/node-lab.6dspOI`；服务 `node-service-6dspOI`、负载端 `node-driver-6dspOI`。
`resources.txt` 每30秒采集；结束后生成 `load.jsonl`、`driver.log`、`service-state.json`。
已创建仅在异常或完成时通知的跟进；休眠、Docker停止或明显采样间断不能算连续24小时通过。
当前版本初期 cgroup峰值60895232字节（58.08MiB），OOM=0；这不是最终结果。

## 已验证

- macOS arm64 / Go1.27.1：10 项状态、计数、配额、清零、损坏状态、API 与输入限制测试通过。
- Xray26.7.28：真实 REALITY/Vision 请求、持续请求中的其他账号热增删、PID不变、无效结构回退后再次请求成功。
- 上述真实连接测试启用 Go race detector，通过。
- 节点生产依赖图不包含 Xray服务注册、完整面板、Gin、GORM、gVisor或Amnezia。
- 原主面板 `Remote` 客户端（未修改）对精简节点：HTTPS指纹固定、入站与账号CRUD、排序、快照、清零、跨节点推送、空结果接口及拒绝完整更新，通过。
- Alpine3.24、128MiB容器、真实OpenRC（Docker `--init`负责回收子进程）：全新安装、服务启动、升级后启动和GUID不变，通过。不是Incus真机验收。
- 当前版本和一个回退版本、配置、状态合计94028KiB，约91.82MiB，小于200MiB目标（不含操作系统及临时安装包）。
- 未带init的第一组Docker安装测试在停机时遗留僵尸进程，导致OpenRC误报无法停止；修正测试容器的PID1后停机升级通过，未修改生产服务来绕过检查。

## 首轮 Linux arm64 数据（2026-09-06）

Docker Desktop 内 Alpine3.24，服务独立容器 `--cpus=1 --memory=96m --memory-swap=96m`，
`cpu.max=100000 100000`、`memory.swap.max=0`。Xray为标准发布版，没有裁剪。
客户端、HTTP/TLS目标、压测程序在另一个不计入预算的容器。
测试网络为 `--internal` 隔离网络，11.233.0.0/24只用于实验，不能路由到外部。
选择隔离网络中的非私网段，是为了保留 Xray 默认私有目标阻止策略，不修改生产策略。

| 场景 | 两进程RSS | cgroup当前 | cgroup峰值 | 结果 |
|---|---:|---:|---:|---|
| 无入站空闲 | 16.81MiB | 21.33MiB | 49.33MiB | OOM=0 |
| 两入站五账号、50连接，60秒 | 单次采样23.17MiB | 单次采样25.89MiB | 50.79MiB | 50/50连接、零失败、9.978Mbps |

负载采样 anon=15622144、file=9707520、sock=0字节；sock=0只是采样读数，不表示没有TCP缓冲。
RSS不是连续峰值测量；cgroup memory.peak是内核累计峰值，包含冷启动和少量诊断进程开销。
一分钟通过不能外推24小时或高吞吐。最终版本有后续安全修复，需重新构建复测。
Linux安装包初次构建约17MiB（arm64）/19MiB（amd64）；arm64两二进制未压缩约46MiB。

## 待验收（尚不能视为完成）

- 未修改主面板的真实 UI 添加/探测/导入/订阅链接流程。
- Alpine OpenRC 真机安装和升级回退、磁盘写满、状态文件异常及端口冲突故障矩阵。
- 1CPU、96MiB服务预算、禁用Swap：两入站五账号、50连接约10Mbps。
- 冷启动、配置变更与回退峰值、24小时稳定性、吞吐上限、完整面板同条件对比。
- VPS实际容器资源、NAT映射和环境核验（尚未提供访问方式）。

内存软限制：agent16MiB、Xray48MiB；GOMAXPROCS=1、GOGC=50。
软限制不是 RSS 或整机上限。RSS与cgroup总内存、file、sock、OOM事件必须同时报告。

完整主面板资源基线、主面板UI到订阅真实连接、吞吐上限和真VPS故障注入尚未实施；
已有接口契约测试与一分钟负载测试不能替代这些项目。
