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

官方 3x-ui v3.11.0 已改为按 tag 解析远端ID后再发 `inbounds/{id}/resetTraffic`，上述限制只存在于旧基准，不影响 v3.11.0 主面板。

## 官方 3x-ui v3.11.0 主面板契约（2026-10-31，0.1.26-node）

对象：上游 MHSanaei/3x-ui `v3.11.0`（`c621ba90`）未修改的 `internal/web/runtime/remote.go`，以及 `service/node.go` 的探测与证书指纹请求。
方法：从该标签只读导出源码，用其 `Remote` 依次调用全部节点方法（含 XHTTP + REALITY 入站），对记录用 TLS 服务录下每个请求的方法、路径、Content-Type 与正文，
存为 `internal/node/testdata/official-3x-ui-v3.11.0-remote.json`；`TestOfficialMasterV3110RecordedWire` 带 Bearer、`X-Config-Sha256`、`X-3x-Master-Push` 原样回放到节点，要求29个请求全部 HTTP 200 且 success。
仓库内 `-tags mastercontract` 测试使用的是本仓库较早的 runtime 副本，覆盖不到以下 v3.11.0 新增差异。

| 差异                                                                  | 0.1.25 行为                | 0.1.26 处理                                                                              |
| --------------------------------------------------------------------- | -------------------------- | ---------------------------------------------------------------------------------------- |
| 入站 add/update 表单每次都带 `excludeFromSub`                         | 未知字段，所有入站推送失败 | 作为布尔字段接收并保存，原样回传                                                         |
| `pushClientTraffics` 行带 `resetWeekday`                              | 未知字段，全局流量推送失败 | 接收；账号上非零 `resetWeekday` 与 `resetDay` 同样拒绝为不支持的功能                     |
| 待清零账号较多时调用 `POST clients/bulkResetTraffic {"emails":[...]}` | 404                        | 单事务清零并重新启用；跳过本节点没有的账号，返回 `{"affected":n}`                        |
| 请求头 `X-3x-Master-Push: 1`                                          | 忽略                       | 不变（无需处理）                                                                         |
| 响应头 `X-3x-Node-Caps`                                               | 节点不发送                 | 不变：主面板不压缩推送；内核模式 AmneziaWG 入站被主面板拒绝推送，与节点只支持 VLESS 一致 |

- 状态探测 `GET server/status` 只读取 cpu/mem/xray/panelVersion/panelGuid/uptime/netIO，节点已提供；证书指纹获取不带 token，节点返回401但 TLS 握手已完成，指纹可取。
- `subSortIndex=0`（主面板默认值）与 `trafficResetDay` 默认值均已兼容。
- 未覆盖：真实主面板进程与节点的多轮同步（官方 `internal/nodee2e`），以及 AmneziaWG/MTProto 等节点本就不支持的协议。

## 已取消的24小时测试

2026-09-06 02:26（Asia/Shanghai）启动本地隔离负载测试，后按用户要求提前停止并删除测试容器，不能作为24小时验收结果。
目录 `.cache/node-lab.6dspOI`；服务 `node-service-6dspOI`、负载端 `node-driver-6dspOI`。
`resources.txt` 每30秒采集；结束后生成 `load.jsonl`、`driver.log`、`service-state.json`。
已创建仅在异常或完成时通知的跟进；休眠、Docker停止或明显采样间断不能算连续24小时通过。
当前版本初期 cgroup峰值60895232字节（58.08MiB），OOM=0；这不是最终结果。

## 已验证

- macOS arm64 / Go1.27.1：10 项状态、计数、配额、清零、损坏状态、API 与输入限制测试通过。
- Xray26.7.28：真实普通 VLESS/TCP 与 REALITY/Vision 请求；REALITY 测试还覆盖持续请求中的其他账号热增删、PID不变、无效结构回退后再次请求成功。
- 上述真实连接测试启用 Go race detector，通过。
- Xray26.7.28：VLESS + XHTTP 四种模式（auto、packet-up、stream-up、stream-one）× REALITY/无加密，共 8 组真实请求通过（Alpine3.24 arm64 容器）；含热增账号 PID 不变与逐账号计数；容器内为 CGO_ENABLED=0 构建，未启用 race detector。存储 Vision flow 的账号若原样下发到 XHTTP 入站，8 组均被 Xray 拒绝，故节点对 XHTTP 入站置空 flow。
- 节点生产依赖图不包含 Xray服务注册、完整面板、Gin、GORM、gVisor或Amnezia。
- 原主面板 `Remote` 客户端（未修改）对精简节点：HTTPS指纹固定、入站与账号CRUD、排序、快照、清零、跨节点推送、空结果接口及拒绝完整更新，通过。
- Alpine3.24、128MiB容器、真实OpenRC（Docker `--init`负责回收子进程）：全新安装、服务启动、升级后启动和GUID不变，通过。不是Incus真机验收。
- 当前版本和一个回退版本、配置、状态合计94028KiB，约91.82MiB，小于200MiB目标（不含操作系统及临时安装包）。
- 未带init的第一组Docker安装测试在停机时遗留僵尸进程，导致OpenRC误报无法停止；修正测试容器的PID1后停机升级通过，未修改生产服务来绕过检查。

## Podman 小鸡真机：XHTTP + REALITY（2026-10-31）

环境：Alpine 3.24.2 arm64（Podman），cgroup 1CPU、memory.max 256MiB、允许 256MiB swap，磁盘 3GB。
程序为 `7911c064` 构建的测试包（版本串仍为 0.1.25-node），Xray 26.7.28；管理口与入站经服务商 NAT 端口映射访问。
客户端为开发机 Docker 内的 Xray 26.7.28，经公网连接；带宽受开发机链路限制（约 1–2MB/s），不代表节点吞吐上限。

| 项       | 结果                                                                                                                         |
| -------- | ---------------------------------------------------------------------------------------------------------------------------- |
| 入站     | 28762：VLESS + XHTTP(auto) + REALITY，两个账号（alice 存储 Vision flow，bob 空 flow）；61936：VLESS + TCP + REALITY + Vision |
| 连通     | 三个客户端出口 IP 均为节点；alice 以空 flow 走 XHTTP 成功                                                                    |
| 单次计数 | XHTTP 下载 10MiB / 5MiB 开销 0.19% / 0.25%，上传 3MiB 开销 0.15%；TCP 下载 10MiB 开销 0.20%；未使用账号读数不变              |
| 并发计数 | XHTTP 60 个 4MiB 下载（两账号各 30）与 TCP 60 个 4MiB 下载全部完成，各账号开销均为 0.27%；入站级与账号合计一致               |
| 内存     | 空闲 anon 22MiB；负载期间 anon 峰值 32MiB，Xray RSS 峰值 45MiB，agent RSS 峰值 17MiB；swap 最多约 0.03MiB；OOM=0             |
| 异常     | TCP 60 并发期间节点 1 秒超时的统计 RPC 超时一次（DeadlineExceeded），下一周期恢复，计数无丢失                                |

- cgroup memory.current 曾短暂触及 256MiB，随即回落到约 70–80MiB，为安装包与日志的文件缓存；判断内存余量应看 anon 与 RSS。
- XHTTP 多路复用：60 个并发请求在节点上只对应约 6 条 TCP 连接；TCP 入站为每请求一条连接。
- 未测：128MiB 且禁用 swap 的限制、24 小时稳定性、高吞吐上限、stream-one/packet-up 等其他模式的公网表现。

## 首轮 Linux arm64 数据（2026-09-06）

Docker Desktop 内 Alpine3.24，服务独立容器 `--cpus=1 --memory=96m --memory-swap=96m`，
`cpu.max=100000 100000`、`memory.swap.max=0`。Xray为标准发布版，没有裁剪。
客户端、HTTP/TLS目标、压测程序在另一个不计入预算的容器。
测试网络为 `--internal` 隔离网络，11.233.0.0/24只用于实验，不能路由到外部。
选择隔离网络中的非私网段，是为了保留 Xray 默认私有目标阻止策略，不修改生产策略。

| 场景                       |        两进程RSS |       cgroup当前 | cgroup峰值 | 结果                         |
| -------------------------- | ---------------: | ---------------: | ---------: | ---------------------------- |
| 无入站空闲                 |         16.81MiB |         21.33MiB |   49.33MiB | OOM=0                        |
| 两入站五账号、50连接，60秒 | 单次采样23.17MiB | 单次采样25.89MiB |   50.79MiB | 50/50连接、零失败、9.978Mbps |

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
