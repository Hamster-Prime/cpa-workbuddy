<div align="center">

# CPA WorkBuddy (`cpa-workbuddy`)

[![CI](https://github.com/ygq-future/cpa-workbuddy/actions/workflows/ci.yml/badge.svg)](https://github.com/ygq-future/cpa-workbuddy/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/ygq-future/cpa-workbuddy)](https://github.com/ygq-future/cpa-workbuddy/releases)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

**CLIProxyAPI 的 WorkBuddy 国内 / 国际双站点插件**

</div>

通过 CPA 管理页面登录 WorkBuddy，将账号可用模型接入 OpenAI、Responses、Claude 与 Gemini 客户端。插件 ID、动态库基础名和 CPA 配置键均为 `cpa-workbuddy`。

## 导航

- [功能概览](#功能概览)
- [安装与配置](#安装与配置)
- [登录与使用](#登录与使用)
- [Docker 部署](#docker-部署)
- [常见问题](#常见问题)
- [本地构建](#本地构建)
- [许可证](#许可证)

## 功能概览

- **国内与国际登录**：管理页面提供“WorkBuddy国内”和“WorkBuddy国际”两个浏览器登录入口。
- **多账号与企业身份**：国内、国际、个人及企业身份独立保存；同一身份重新登录更新原记录，保留宿主管理设置。
- **动态模型发现**：按账号获取可用模型，国内使用 `wb/` 前缀，国际使用 `wb-ai/` 前缀。
- **四种客户端协议**：支持 OpenAI Chat Completions、OpenAI Responses、Claude Messages 和 Gemini，提供流式与非流式响应。
- **工具、图片与推理**：支持工具调用闭环、图片输入和 reasoning / effort；具体能力取决于所选模型和账号权限。
- **凭证自动刷新**：接入 CPA 的保存、刷新和请求恢复流程；账号卡片与模型弹窗显示可读名称。

## 安装与配置

使用支持原生插件及 JSON schema 10 的官方 [CLIProxyAPI](https://github.com/router-for-me/CLIProxyAPI)；推荐 CPA `v8.0.69` 或更新版本。管理页面使用官方 CPAMC。

1. 从 [Releases](https://github.com/ygq-future/cpa-workbuddy/releases) 下载与 **CPA 运行平台和架构**一致的 ZIP 及其 `.sha256` 文件。
2. 核对 ZIP 的 SHA256 后解压，将动态库放入 CPA 的插件目录，保留文件名。

| CPA 运行环境 | 发布包后缀 | 动态库安装路径示例 |
| --- | --- | --- |
| Windows x64 | `windows-amd64.zip` | `plugins/windows/amd64/cpa-workbuddy.dll` |
| Linux x64 | `linux-amd64.zip` | `plugins/linux/amd64/cpa-workbuddy.so` |
| Linux ARM64 | `linux-arm64.zip` | `plugins/linux/arm64/cpa-workbuddy.so` |
| macOS Intel | `darwin-amd64.zip` | `plugins/darwin/amd64/cpa-workbuddy.dylib` |
| macOS Apple Silicon | `darwin-arm64.zip` | `plugins/darwin/arm64/cpa-workbuddy.dylib` |

3. 在 CPA 的配置文件中启用插件：

```yaml
plugins:
  enabled: true
  dir: "plugins"
  configs:
    cpa-workbuddy:
      enabled: true
```

4. 重启 CPA，在管理页面的插件列表确认 `cpa-workbuddy` 已加载并启用。

插件复用 CPA 的网络代理、凭证目录及管理鉴权。管理密钥与客户端 API key 分别由 CPA 配置。

## 登录与使用

1. 打开 CPA 管理页面，进入 OAuth 登录。
2. 选择 **WorkBuddy国内** 或 **WorkBuddy国际**，打开生成的登录链接。
3. 在浏览器完成登录、扫码及所需的身份授权，等待 CPA 提示成功。
4. 在认证文件列表确认账号，打开模型列表查看可用模型。
5. 将客户端的 API 地址和 API key 指向 CPA，选择带对应站点前缀的模型。

例如，通过 OpenAI Chat Completions 调用国际站模型：

```sh
curl http://127.0.0.1:8317/v1/chat/completions \
  -H "Authorization: Bearer $CPA_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{"model":"wb-ai/hy4flash-free","messages":[{"role":"user","content":"你好"}],"stream":false}'
```

请从账号实际模型列表选择模型。国内模型形如 `wb/<模型 ID>`，国际模型形如 `wb-ai/<模型 ID>`。登录会话有效期为五分钟，超时后重新发起即可。

## Docker 部署

将插件目录挂载到容器，并确保配置中的 `plugins.dir` 指向容器内目录。例如，在现有 Compose 服务的 `volumes` 中加入：

```yaml
volumes:
  - ./plugins:/CLIProxyAPI/plugins:ro
```

Linux amd64 容器对应的宿主文件为 `./plugins/linux/amd64/cpa-workbuddy.so`。在 CPA 配置中填写 `dir: "/CLIProxyAPI/plugins"`，并启用上述两个开关；保留 CPA 的配置和凭证目录持久卷。

容器内运行 CPA，浏览器登录在你自己的电脑上完成。选择与容器架构匹配的 Linux 包；Linux 发布包使用 glibc，Alpine / musl 环境需使用匹配环境构建的产物。

## 常见问题

**插件未加载？** 检查动态库的平台、架构、文件名和目录，确认 `plugins.enabled` 与 `plugins.configs.cpa-workbuddy.enabled` 均为 `true`，再查看 CPA 启动日志。Windows 如提示缺少 DLL，检查构建工具链所需的运行库。

**看不到登录入口？** 确认插件已启用，更新官方 CPA 和管理页面后刷新。

**模型未出现或请求被拒绝？** 查看该账号的模型列表、禁用状态及模型排除规则，使用包含 `wb/` 或 `wb-ai/` 的完整 ID。模型和图片、工具、推理能力由上游账号权限决定。

**需要重新登录？** 在同一站点重新发起登录，同一身份会更新已有凭证。网络故障先检查 CPA 代理配置；不要在问题报告中附上 token、登录链接或完整凭证文件。

## 本地构建

准备 Go **1.27.1**、Git 和 C 编译器，在仓库根目录执行：

```sh
go run ./cmd/project bootstrap
go run ./cmd/project quality
go run ./cmd/project build
```

动态库位于 `out/bin/`；`go run ./cmd/project package` 生成当前平台 ZIP 和 SHA256。开发约定与发布流程见 [开发指南](docs/development.md)，版本变更见 [Release notes](docs/release-notes.md)。

## 许可证

本项目采用 [MIT License](LICENSE)。二进制包中的 `licenses/` 目录包含实际链接依赖的许可声明。

