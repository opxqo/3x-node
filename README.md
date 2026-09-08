<p align="center">
  <img alt="3X-NODE" src="./media/3x-node-logo.png" width="760">
</p>

<h1 align="center">3x-ui · 主面板与精简副节点</h1>

<p align="center">在主面板集中管理入站与客户端，在副节点运行代理服务。</p>

<p align="center">
  <a href="https://github.com/opxqo/3x-ui/releases"><img src="https://img.shields.io/badge/发布版本-opxqo%2F3x--ui-blue" alt="发布版本"></a>
  <a href="https://github.com/opxqo/3x-ui/actions/workflows/ci.yml"><img src="https://github.com/opxqo/3x-ui/actions/workflows/ci.yml/badge.svg" alt="持续集成"></a>
  <a href="./LICENSE"><img src="https://img.shields.io/badge/许可证-GPL--3.0-blue" alt="许可证"></a>
</p>

本仓库基于 [MHSanaei/3x-ui](https://github.com/MHSanaei/3x-ui) 持续开发，提供完整网页管理面板，以及面向低内存 Alpine 容器的独立精简副节点。两者使用不同的安装入口，请根据机器用途选择。

## 选择版本

| 对比项 | 完整主面板 | 精简副节点 |
| --- | --- | --- |
| 用途 | 网页管理、订阅与多节点管理 | 接收主面板下发并运行代理服务 |
| 管理方式 | 网页、API、`x-ui` 菜单 | API、命令与菜单，无网页 |
| 协议范围 | VLESS、VMess、Trojan、Shadowsocks 等多协议 | 仅 VLESS + TCP/RAW |
| 安全层 | 按具体协议和传输配置 | 无加密或 REALITY，可选 Vision |
| 数据存储 | SQLite 或 PostgreSQL | 本地配置与状态文件 |
| 安装环境 | 多种 Linux 发行版，另有 Docker 部署 | Alpine + OpenRC，amd64 / arm64 |
| 安装脚本 | `install.sh` | `install-node.sh` |

精简节点当前安装版本为 **0.1.20-node**，Xray 固定为 **26.7.28**。节点版仍属实验性实现，支持范围见[节点使用说明](docs/node/README.md)。

## 快速安装

### 完整主面板

在具备 Bash 和 curl 的 Linux 服务器上，以 root 身份执行：

```bash
bash <(curl -Ls https://raw.githubusercontent.com/opxqo/3x-ui/main/install.sh)
```

安装完成后运行 `x-ui`，查看面板状态、管理登录信息、证书和服务。安装器会生成随机登录信息及访问路径，请保存安装结果。

需要验证主分支开发构建时，可以指定 `dev-latest`：

```bash
bash <(curl -Ls https://raw.githubusercontent.com/opxqo/3x-ui/main/install.sh) dev-latest
```

开发构建不代表稳定版本。其他指定版本以[本仓库发布页](https://github.com/opxqo/3x-ui/releases)实际提供的标签和安装包为准。

### 精简副节点

在 Alpine/OpenRC 服务器上，以 root 身份执行：

```sh
apk add --no-cache ca-certificates curl && curl -fLSs https://raw.githubusercontent.com/opxqo/3x-ui/main/install-node.sh -o /root/install-node.sh && sh /root/install-node.sh
```

脚本自动识别架构，下载固定节点版本，验证内置 SHA256 摘要，然后安装并启动服务。使用 Alpine 自带的 `sh`，无需 Bash；安装包暂存在磁盘目录，结束后自动清理。

安装完成后查看接入信息和运行状态：

```sh
3x-ui-node credentials
3x-ui-node status
3x-ui-node menu
```

在没有已有 `x-ui` 命令的专用副节点上，安装器还会创建 `x-ui` 菜单入口。

## 将副节点接入主面板

1. 在主面板添加节点，选择 **HTTPS**。
2. 填写副节点的公网地址和管理端口，默认容器内端口为 **2053**。
3. 填写 `credentials` 输出的**副节点 API Token**。
4. 选择证书指纹固定，填写 **TLS SHA256** 指纹。
5. 从主面板创建或下发受支持的 VLESS 入站与客户端。

**副节点不需要主面板 API 令牌。** 从 `0.1.14-node` 起，已移除副节点定时拉取主面板的配置、后台任务、`sync` 命令及菜单 15–19，统一由主面板主动下发。

如果服务器使用 NAT，主面板应填写管理端口的**公网映射端口**。VLESS 业务端口需要另行映射，分享链接也应使用公网地址与业务映射端口。**62789 是仅供本机使用的 Xray API 端口，不要映射到公网。**

## 已有功能

### 完整面板

- **入站与客户端管理**：多协议入站、客户端增删改、流量配额、到期时间、在线状态和分享链接。
- **多节点管理**：集中配置节点、克隆入站、同步客户端及查看节点状态。
- **流量统计**：按入站、客户端和出站统计，支持清零。
- **订阅与分享**：订阅服务、二维码，以及原始、JSON 和 Clash 等输出。
- **传输与路由**：TCP/RAW、WebSocket、gRPC、HTTPUpgrade、XHTTP 等传输，以及出站和路由规则。
- **访问限制与自动化**：IP 限制、设备限制、续期周期、Telegram 管理和 API 令牌。
- **数据库与部署**：SQLite、PostgreSQL、Docker 和无人值守安装。

具体协议和功能以面板实现及所使用的 Xray 版本为准；完整面板功能不能直接视为精简节点已支持的功能。

### 精简副节点

- 支持 VLESS + TCP/RAW，无加密或 REALITY，可选 Vision，使用直连出口。
- 保留入站与客户端增删改、分享元数据、流量统计、配额、固定到期、首次使用计时和清零。
- 客户端修改复用本机 gRPC；监听结构变化时串行重启 Xray。
- 接收完整面板客户端数据时，忽略其他协议的 `password`、`auth`、`secret` 字段，避免 VLESS 同步被无关字段拒绝。
- 对实际未实现的访问限制仍明确拒绝，不会静默假装支持。
- 优化配置复制过程，减少临时分配；安装时避免重复解压大体积二进制，日志轮转总量约 4MiB。

精简节点**不支持**其他协议、订阅服务、自动续期、IP/设备限制、嗅探、区域规则、通知及完整面板更新。主面板失联时继续使用最后生效的配置。

## 节点升级与运行边界

已有精简节点时，重新下载入口，并显式执行升级：

```sh
apk add --no-cache ca-certificates curl && curl -fLSs https://raw.githubusercontent.com/opxqo/3x-ui/main/install-node.sh -o /root/install-node.sh && sh /root/install-node.sh upgrade
```

已安装目标版本时直接返回；实际升级保留配置与状态，保留上一版本用于回退。不要使用主面板的完整面板更新按钮升级精简节点。

| 项目 | 精简节点说明 |
| --- | --- |
| 默认管理端口 | HTTPS 2053 |
| 配置 | `/etc/3x-ui-node/config.json` |
| 状态与日志目录 | `/var/lib/3x-ui-node` |
| 程序目录 | `/usr/local/lib/3x-ui-node` |
| 重启服务 | `rc-service 3x-ui-node restart` |
| 安装资源检查 | 有效内存至少 96MiB，剩余磁盘至少 300MiB |

安装检查阈值不等于长期运行容量保证。实际占用取决于架构、连接数和供应商限制，应结合[资源与验收记录](docs/node/VALIDATION.md)及目标机器实测判断。

流量默认每 5 秒采样，发生变化时保存；异常退出可能损失最近尚未保存的计数。跨节点总量依赖主面板回传，失联后不能保证跨节点实时额度一致。

## Docker 与数据库

完整面板可从本仓库构建并运行：

```bash
git clone https://github.com/opxqo/3x-ui.git
cd 3x-ui
docker compose up -d --build
```

默认使用 SQLite，Compose 将数据库、证书及证书续期状态保存在宿主机目录。业务入站需在 `docker-compose.yml` 中补充相应端口映射。

使用 PostgreSQL 时，先配置 Compose 文件中的 `XUI_DB_TYPE`、`XUI_DB_DSN` 及数据库凭据，再启动：

```bash
docker compose --profile postgres up -d --build
```

Linux 脚本安装默认 SQLite 数据库路径为 `/etc/x-ui/x-ui.db`；PostgreSQL 连接通过 `XUI_DB_TYPE=postgres` 和 `XUI_DB_DSN` 配置。精简节点不使用这套数据库配置。

## 文档与开发

| 文档 | 内容 |
| --- | --- |
| [精简节点说明](docs/node/README.md) | 构建、安装、功能范围、NAT、命令与升级 |
| [节点资源与验收记录](docs/node/VALIDATION.md) | 资源测量、验证方法及已知限制 |
| [自动化部署](deploy/README.md) | 无人值守安装与云端部署 |
| [API 读取工具](tools/panel-api-reader/README.md) | 只读检查面板 API 数据与客户端字段 |
| [贡献指南](CONTRIBUTING.md) | 开发与贡献约定 |

节点开发常用命令：

```sh
sh scripts/node/build.sh
go test -race ./internal/node ./cmd/3x-ui-node
go test -race -tags mastercontract ./internal/node
```

构建要求以仓库 `go.mod` 和构建脚本为准。问题反馈请附版本、架构、复现步骤和脱敏日志，通过[本仓库问题页](https://github.com/opxqo/3x-ui/issues)提交。

## 面板预览

<details>
<summary>展开查看面板截图</summary>

截图用于展示完整面板界面，精简节点不提供网页。

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="./media/01-overview-dark.png">
  <img alt="面板概览" src="./media/01-overview-light.png">
</picture>

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="./media/02-add-inbound-dark.png">
  <img alt="添加入站" src="./media/02-add-inbound-light.png">
</picture>

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="./media/03-add-client-dark.png">
  <img alt="添加客户端" src="./media/03-add-client-light.png">
</picture>

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="./media/05-add-nodes-dark.png">
  <img alt="节点配置" src="./media/05-add-nodes-light.png">
</picture>

</details>

## 开源与致谢

本仓库是 3x-ui 的衍生开发版本，遵循 [GPL-3.0 许可证](LICENSE)。感谢 [MHSanaei/3x-ui](https://github.com/MHSanaei/3x-ui)、[alireza0](https://github.com/alireza0) 与 [Xray-core](https://github.com/XTLS/Xray-core) 的开发者和贡献者。

完整面板所使用的规则数据包括 [Iran v2ray rules](https://github.com/chocolate4u/Iran-v2ray-rules) 和 [Russia v2ray rules](https://github.com/runetfreedom/russia-v2ray-rules-dat)，均遵循各自的 GPL-3.0 许可；精简节点包不附带 GeoIP/GeoSite 数据。第三方组件许可证随相应源码或发布包保留。
