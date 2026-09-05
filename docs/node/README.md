# 3x-ui-node（实验性精简副节点）

兼容基准：完整主面板 `ed6bc1d8`；Xray 固定 `26.7.28`。
这是独立程序，不是完整面板的低内存启动参数，也不提供网页。
**尚未完成全部资源及主面板端到端验收，不能声明 128MB 生产可用。**

## 功能范围

仅 VLESS、TCP/RAW、REALITY（可选 Vision）、直连出口。
保留入站/账号 CRUD、分享元数据、流量、配额、固定到期、首次使用计时、清零。
用户修改走复用的本机 gRPC；监听结构变化串行重启 Xray。
不支持其他协议、订阅服务、自动续期、IP/设备限制、嗅探、区域规则、通知及完整面板更新。
管理接口和 VLESS 需要独立 TCP 映射；62789 是仅监听回环的内部 Xray API，禁止映射。

## 构建与安装

开发机需要 Go（以仓库 go.mod 为准）、curl、unzip、OpenSSL、tar：

```sh
sh scripts/node/build.sh
```

生成 `dist/node/3x-ui-node-0.1.0-linux-{amd64,arm64}.tar.gz` 和 SHA256。
构建下载固定 Xray 发布包并验证固定摘要，不附带 GeoIP/GeoSite。
VPS 不安装编译器、Go、Node 或 Docker。将匹配架构的包和仓库中的安装脚本传到 VPS：

```sh
sh install.sh install ./3x-ui-node-0.1.0-linux-amd64.tar.gz TRUSTED_SHA256
3x-ui-node credentials
3x-ui-node status
```

`TRUSTED_SHA256` 替换为可信发布渠道取得的 64 位十六进制摘要；摘要不是签名。
安装器只接受精简包，在 Alpine/OpenRC 中检查架构、容器内存、剩余磁盘及默认 API 端口。
尚需人工确认 VLESS 监听端口和供应商的公网映射；不依赖 Swap、TUN 或防火墙权限。
默认 HTTPS `0.0.0.0:2053`、随机令牌、自签证书，配置 `/etc/3x-ui-node/config.json`。
数据和轮转日志位于 `/var/lib/3x-ui-node`；日志最多约 4MiB。

## 主面板接入与 NAT

添加节点时选择 HTTPS，地址填写公网管理入口，端口填写其**公网映射端口**。
认证使用 `credentials` 显示的 Bearer Token，TLS 模式选择证书指纹固定并录入 TLS SHA256。
`basePath` 默认 `/`；修改配置后执行 `rc-service 3x-ui-node restart`。
不应选择跳过证书校验，不要公开令牌、证书私钥或 REALITY 私钥。

例如供应商把公网 `32053 → 容器2053`、`32443 → 容器443`：
主面板节点管理地址使用公网地址与32053；VLESS 入站监听443；分享配置必须使用公网地址与32443。
在主面板保留并设置 externalProxy/分享地址元数据，检查生成链接的地址和端口。
管理端口和代理端口不可混用，容器的10.x地址不能作为公网分享入口。

## 命令和升级

`init` 初始化（拒绝覆盖）；`check` 校验本地配置；`status` 查询本地 HTTPS API；
`credentials` 显示接入信息；`rotate-token` 原子替换令牌，随后需要重启服务及更新主面板令牌。
所有命令支持 `-config /absolute/path/config.json`。

```sh
sh install.sh upgrade ./3x-ui-node-0.1.0-linux-amd64.tar.gz TRUSTED_SHA256
```

升级停止服务后保留状态，切换 current 链接，启动失败回到原二进制。
`previous` 指向上一版本；数据额外留有 `state.pre-upgrade.json`。
成功升级后只保留当前与上一版本；更早的、路径和标识通过核验的回退目录会被删除。
不要通过主面板的完整面板更新按钮升级该程序。

## 计数与恢复边界

5 秒采样非重置计数，仅变化时保存；配置和清零立即保存。
本地计数与主面板跨节点总量分开；跨节点总量24小时过期，不叠加到回传值。
主面板失联继续运行最后有效配置，总量配额只能依据最后收到的跨节点数据。
正常停止结算；进程或主机异常退出可能损失上次成功快照之后的流量，通常约5秒，
调度或磁盘延迟可能拉长窗口。绝不声称异常退出零损失。
状态损坏时拒绝启动，不自动以旧快照恢复以免静默恢复已撤销账号；管理员确认后恢复 `.previous`。
耗尽/到期阻止新认证；存量连接遵循 Xray 的原生行为。
默认保留 Xray 对私有目标地址的阻止，不能用回环 HTTP 目标误判代理可用性。

## 验证

```sh
go test -race ./internal/node ./cmd/3x-ui-node
NODE_XRAY_TEST_BINARY=/absolute/path/xray go test -race -v ./internal/node
go test -race -tags mastercontract ./internal/node
sh scripts/node/run-lab.sh 60s
# 24小时运行（保持开发机和Docker持续运行）
sh scripts/node/run-lab.sh 24h
```

第二项使用真实固定版本 Xray，验证 REALITY/Vision、API 热增删、不重启和失败配置恢复。
测试仅放行自身临时回环 HTTP 目标，不改变生产配置。
资源结果及未完成项见 [资源与验收记录](VALIDATION.md)。

`run-lab.sh` 使用Docker开发测试环境；服务限制96MiB/1CPU/无Swap，客户端和目标位于预算之外。
脚本不接触VPS。其隔离11.233.0.0/24网段必须未被其他Docker网络占用；旧测试网络需先确认后清理。
产物目录含临时测试私钥和令牌，整个 `.cache` 不应发布；CI只上传指定的无凭据测量文件。
Xray源代码和许可证随其上游 [v26.7.28发布](https://github.com/XTLS/Xray-core/releases/tag/v26.7.28) 提供，安装包含 `XRAY-LICENSE`。
