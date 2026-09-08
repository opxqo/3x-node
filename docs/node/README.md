# 3x-ui-node（实验性精简副节点）

兼容基准：完整主面板 `ed6bc1d8`；Xray 固定 `26.7.28`。
这是独立程序，不是完整面板的低内存启动参数，也不提供网页。
安装器和运行时按 128MiB 容器约束设计；仍需结合供应商限制、NAT 和持续运行数据做真实验收。

## 功能范围

仅 VLESS + TCP/RAW，安全层支持无加密（`none`）或 REALITY（可选 Vision），直连出口。主面板 VLESS 入站携带的 Vision `testseed` 元数据会校验后保留在节点状态，节点服务端使用 Xray 默认参数；VLESS fallback 仍不支持。
保留入站/账号 CRUD、分享元数据、流量、配额、固定到期、首次使用计时、清零。
用户修改走复用的本机 gRPC；监听结构变化串行重启 Xray。
不支持其他协议、订阅服务、自动续期、IP/设备限制、嗅探、区域规则、通知及完整面板更新。
管理接口和 VLESS 需要独立 TCP 映射；62789 是仅监听回环的内部 Xray API，禁止映射。

### 0.1.14：仅保留主面板主动下发

已移除副节点主动拉取的配置、令牌读取、菜单 15–19、sync 命令、HTTP 接口和后台任务。
副节点不需要主面板 API 令牌；在主面板配置副节点地址、副节点 Token 和证书指纹即可。
旧配置中的 masterSync 会被忽略，已有入站和客户端继续保留。升级后可删除旧 master.token。
VLESS 客户端新增/更新及整入站新增/更新统一忽略其他协议的 password、auth、secret，
真实未实现的访问限制仍会拒绝。

## 构建与安装

### VPS 一行安装

以 root 身份在 Alpine/OpenRC 上执行（支持 amd64、arm64）：

```sh
apk add --no-cache ca-certificates curl && curl -fLSs https://raw.githubusercontent.com/opxqo/3x-node/main/install-node.sh -o /root/install-node.sh && sh /root/install-node.sh
```

入口自动识别架构，下载 `v0.1.24-node`，验证脚本内固定 SHA256，安装并启动服务。
兼容 Alpine 默认的 `sh`，无需先安装 Bash；下载完整成功后才执行脚本。
安装包暂存在 `/usr/local/lib`，避免占用可能为内存盘的 `/tmp`，结束后自动清理。
已经安装时首次安装命令会拒绝执行；升级请显式传入 `upgrade`：

```sh
apk add --no-cache ca-certificates curl && curl -fLSs https://raw.githubusercontent.com/opxqo/3x-node/main/install-node.sh -o /root/install-node.sh && sh /root/install-node.sh upgrade
```

已经是目标版本则直接返回，不重启服务。后续发布节点版时需同步更新入口中的版本及两个架构摘要。

安装完成后执行 `3x-ui-node credentials`，将节点 Token 和 TLS 指纹填入主面板。
根目录 `install.sh` 是完整面板安装器，精简节点使用 `install-node.sh`。

### 本地构建与手动安装

开发机需要 Go（以仓库 go.mod 为准）、curl、unzip、OpenSSL、tar：

```sh
sh scripts/node/build.sh
```

生成 `dist/node/3x-ui-node-0.1.24-linux-{amd64,arm64}.tar.gz` 和 SHA256。
构建下载固定 Xray 发布包并验证固定摘要，不附带 GeoIP/GeoSite。
VPS 不安装编译器、Go、Node 或 Docker。下载发布包后只提取其中的安装脚本，避免在低内存容器中把整个包预解压一遍：

```sh
PKG=/tmp/3x-ui-node-0.1.24-linux-amd64.tar.gz
DIR=$(mktemp -d /tmp/3x-ui-node-install.XXXXXX)
tar -xzf "$PKG" -C "$DIR" install.sh
cd "$DIR"
sh ./install.sh install "$PKG" TRUSTED_SHA256
3x-ui-node credentials
3x-ui-node status
```

`TRUSTED_SHA256` 替换为可信发布渠道取得的 64 位十六进制摘要；摘要不是签名。
安装器只接受精简包，在 Alpine/OpenRC 中检查架构、容器内存、剩余磁盘及默认 API 端口。
尚需人工确认 VLESS 监听端口和供应商的公网映射；不依赖 Swap、TUN 或防火墙权限。
默认 HTTPS `0.0.0.0:2053`、随机令牌、自签证书，配置 `/etc/3x-ui-node/config.json`。
数据和轮转日志位于 `/var/lib/3x-ui-node`；日志最多约 4MiB。

## 主面板接入与 NAT

源码新增接入反馈（尚未发版）：TUI「服务状态」的「主面板接入」区域显示最近远程认证请求、成功配置下发时间及各自直连来源 IP。共享 Token 不提供唯一主面板身份，故该反馈不能证明绑定归属；其他持有 Token 的远程工具也会留下记录。仅已认证且命中节点 API 的请求会记录；本机回环查询、未授权访问不记录，不信任 X-Forwarded-For。只有入站/客户端配置修改成功才更新下发时间，查询、流量统计推送和失败写入不会更新。数据仅保留在内存，节点重启后重新观察；未观察到请求或长时间未请求不等于解绑。反向代理/NAT 下来源 IP 可能是代理地址，本地反代的回环请求不会记录。

添加节点时选择 HTTPS，地址填写公网管理入口，端口填写其**公网映射端口**。
认证使用 `credentials` 显示的 Bearer Token，TLS 模式选择证书指纹固定并录入 TLS SHA256。
`basePath` 默认 `/`；修改配置后执行 `rc-service 3x-ui-node restart`。
不应选择跳过证书校验，不要公开令牌、证书私钥或 REALITY 私钥。

例如供应商把公网 `32053 → 容器2053`、`32443 → 容器443`：
主面板节点管理地址使用公网地址与32053；VLESS 入站监听443；分享配置必须使用公网地址与32443。
在主面板保留并设置 externalProxy/分享地址元数据，检查生成链接的地址和端口。
管理端口和代理端口不可混用，容器的10.x地址不能作为公网分享入口。

## 命令和升级

### 系统体检

```sh
3x-ui-node doctor
3x-ui-node doctor --fix
3x-ui-node doctor -config /absolute/path/config.json
```

`doctor` 输出 PASS、WARN、FAIL 与需外部验证的 MANUAL 项；存在 FAIL 时退出码为 1，否则为 0。即使配置损坏也可从命令行启动。检查配置、状态文件、敏感文件权限、TLS 有效期、本地认证 API、Xray 状态、OpenRC/PID、内部 API 监听、数据目录磁盘及可读取的 cgroup/OOM 信息。公网端口映射必须从外部验证。TUI 的诊断分组和数字菜单 15/16 提供检查与修复入口。

`--fix` 目前仅修复敏感普通文件权限为 0600，每项展示方案并要求 y/yes 确认，默认不执行。修改前在同目录创建 0600 的 `.doctor-permissions-*.json` 原路径/权限记录，不复制秘密内容；拒绝符号链接、硬链接和非当前用户所有的文件，结束后重新检查。必要时管理员可按记录人工恢复原权限。服务重启、PID 清理、系统依赖、证书更换、网络和 OOM 不自动修复，避免误处理容器系统状态；WARN 不代表必须修改系统。该命令不增加后台任务，也不会上传诊断数据。

`init` 初始化（拒绝覆盖）；`check` 校验本地配置；`status` 查询本地 HTTPS API；
`credentials` 显示接入信息；`rotate-token` 原子替换令牌，随后需要重启服务及更新主面板令牌。
所有命令支持 `-config /absolute/path/config.json`。

`menu` 提供与 `x-ui` 风格一致的分区管理菜单：状态、入站、客户端、日志、服务控制与默认客户端配置。`13` 是手动添加客户端，逐步询问入站 ID、UUID、名称和启用状态，并在写入前要求确认：

源码中的新版交互菜单在支持 ANSI 的终端自动进入全屏 TUI。菜单按概览、诊断、配置、服务四组排列，每组一条分隔标题，选中项整行反显，底部显示该项说明与按键提示；`↑/↓` 或 `j/k` 选择，Enter 打开，`q` 退出。结果页用 `↑/↓` 滚动、空格与 `b` 翻页、`g`/`G` 跳到首尾，标题右侧显示当前行范围，Esc 返回。

入站列表是可选择的两级页面：`↑/↓` 或 `j/k` 移动光标（整行反显），Enter 打开该入站的详情，Esc 从详情退回列表、再按一次才回到主菜单。详情按基本、传输、REALITY 或 TLS、客户端分段列出监听地址、传输与安全、目标与 SNI、Short IDs、客户端 UUID 与 flow。REALITY 的公钥不读配置里存的 `settings.publicKey`，而是由节点上实际运行的 `privateKey` 现场推导——Xray 只用私钥认证、从不读那个字段，两者不一致时页面直接告警并给出客户端应当使用的 `pbk`。该页面自 `0.1.19-node` 起提供。

版面随终端尺寸自适应：终端够高时显示 `3X NODE` 字符 Logo，否则依次降级为单行字标；内容块水平居中、整体垂直居中，窗口缩放时自动重绘，退出后恢复终端。停止、重启使用居中圆角确认弹窗：背景菜单弱化，默认选中取消，`←/→` 或 Tab 切换按钮，Enter 执行当前选择，Esc 取消。执行中显示进度并阻止重复提交，完成后在弹窗内反馈结果，失败详情在关闭后展示。小于 44×12 的窗口提示扩大终端，并禁用隐藏按钮的确认操作。「连接凭据」主动打开后在同款信息弹窗展示，长 Token/指纹自动换行，超出高度可用 ↑↓ 滚动，Enter/Esc 关闭。「检查更新」使用同款确认弹窗，明确提示下载升级及服务中断；执行期间显示状态，结束后在可滚动弹窗内展示完整输出。客户端写操作保留原有确认。

服务分组新增 `检查更新` 与 `卸载节点`（自 `0.1.21-node` 起）。前者下载最新的 `install-node.sh` 并以 `upgrade` 执行，效果与手动运行文档中的一行升级命令一致，始终采用该脚本当时最新固定的版本号与校验和；后者停止并移除开机启动项、`/usr/local/lib/3x-ui-node`、`/usr/local/bin/3x-ui-node`（以及指向它的 `x-ui` 兼容入口）、`/etc/3x-ui-node` 与 `/var/lib/3x-ui-node`，删除前需输入 `uninstall` 二次确认，且该操作不可恢复。两项菜单单项查询命令为 `3x-ui-node menu update` 与 `3x-ui-node menu uninstall`。

TUI 只在菜单打开时运行，不增加节点后台任务。非终端输入、输出重定向或 `TERM=dumb` 时使用原数字菜单；单项查询命令仍输出普通文本。基础菜单已包含在 `0.1.18-node` 安装包中。

实时状态仪表盘每 2 秒请求一次本地 API，显示 CPU/内存占用条、最近 30 次成功采样的趋势与网络收发速率（B/s，非累计流量）。CPU 来自系统采样；内存主指标采用探针同口径 `MemTotal - MemAvailable`，并同时列出含缓存总占用、文件缓存、匿名内存、Swap、PSI 等明细。仅页面打开时采样，`p` 暂停/继续，`r` 手动刷新，返回或退出会取消请求；请求失败保留上次数据并标记过期。布局宽屏双栏、窄屏单栏，趋势刻度固定 0–100%，只重绘变化行以减少闪烁。Logo 使用 true-color `#0CF5B8`，需要终端支持 24 位颜色。

```sh
# 交互菜单：状态、入站、客户端流量、监听端口和 Xray 错误
3x-ui-node menu

# 适合脚本或快速排查的单项查询
3x-ui-node menu status
3x-ui-node menu inbounds
3x-ui-node menu clients
3x-ui-node menu ports
3x-ui-node menu errors

```

安装在专用副节点时，若系统尚未存在完整面板的 `x-ui` 命令，安装器会额外创建兼容入口；可直接输入 `x-ui` 打开菜单。

### 主面板不变时的默认客户端兼容模式

如果主面板只创建空 VLESS 入站、未同步客户端，可在副节点保存一组默认客户端。随后副节点仅会为**客户端列表为空**的 VLESS 入站自动补入该客户端；已有客户端不会覆盖，非 VLESS 入站不会处理。

```sh
3x-ui-node default-client set UUID EMAIL
rc-service 3x-ui-node restart
3x-ui-node default-client show
```

清除该兼容配置：

```sh
3x-ui-node default-client clear
rc-service 3x-ui-node restart
```

```sh
sh install.sh upgrade ./3x-ui-node-0.1.24-linux-amd64.tar.gz TRUSTED_SHA256
```

升级停止服务后保留状态，切换 current 链接，启动失败回到原二进制。
`previous` 指向上一版本；数据额外留有 `state.pre-upgrade.json`。
成功升级后只保留当前与上一版本；更早的、路径和标识通过核验的回退目录会被删除。
不要通过主面板的完整面板更新按钮升级该程序。

## 计数与恢复边界

内存展示采用探针同口径：主指标使用 `/proc/meminfo` 的 `MemTotal - MemAvailable`，容器总占用使用 cgroup `memory.current / memory.max`，并列出文件缓存、匿名内存、Swap、PSI some avg10（10 秒平均内存等待占比）及历史/本页新增 OOM 杀进程数。工作集（总占用减非活跃文件页）仅作辅助参考，不是进程私有内存；指标缺失显示不可用，不凭总占用百分比判断 OOM 或泄漏。

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

第二项使用真实固定版本 Xray，验证普通 VLESS/TCP、REALITY/Vision、API 热增删、不重启和失败配置恢复。
测试仅放行自身临时回环 HTTP 目标，不改变生产配置。
资源结果及未完成项见 [资源与验收记录](VALIDATION.md)。

`run-lab.sh` 使用Docker开发测试环境；服务限制96MiB/1CPU/无Swap，客户端和目标位于预算之外。
脚本不接触VPS。其隔离11.233.0.0/24网段必须未被其他Docker网络占用；旧测试网络需先确认后清理。
产物目录含临时测试私钥和令牌，整个 `.cache` 不应发布；CI只上传指定的无凭据测量文件。
Xray源代码和许可证随其上游 [v26.7.28发布](https://github.com/XTLS/Xray-core/releases/tag/v26.7.28) 提供，安装包含 `XRAY-LICENSE`。
