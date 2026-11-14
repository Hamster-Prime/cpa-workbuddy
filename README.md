# cpa-workbuddy

面向**未修改的官方 CLIProxyAPI（CPA）**的 WorkBuddy 原生动态库插件：一个 provider `workbuddy` 承载国内 `copilot.tencent.com` 与国际 `www.workbuddy.ai` 的多个账号及个人/企业身份。

## 当前契约

| 项目 | 固定值 |
| --- | --- |
| 宿主 | 官方 CPA `v8.0.69`，commit `f7ae8feeb3710319f524730849fbe32fde1751e4`；不改源码、不打补丁 |
| 管理前端 | 官方 CPAMC `v1.28.0`，`management.html` SHA256 `035cdd5340a5b9a76cb43d11e45444be1990ae1ecff413d6a92b7e8f03bc6373`（3,879,501 bytes） |
| 插件 | 产品版本见 [VERSION](VERSION)；C ABI `1`；JSON schema `10` |
| 配置 | 官方 v8 布局（`config-version: 8`），字段以宿主自带 `config.example.yaml` 为准 |

插件只用官方公开接口：schema 10 的多 OAuth 入口、认证 provider、模型 provider、四格式 executor，以及插件自有只读管理路由。安装不要求宿主补丁、不要求前端分支或外部页面扩展；管理操作全部走官方 `/v8/management`，插件自有状态仍在 `GET /v0/management/plugins/cpa-workbuddy/status`。宿主与前端均保持原版字节，升级宿主时只需重新核对本文列出的公开契约。

## 已实现能力

原生接入、双站点浏览器登录与恢复、个人/企业身份管理、账号模型发现、Chat 文本/工具/图片/reasoning、国内 Claude Messages、CPA 客户端协议互通、凭证刷新与永久认证失效恢复、有界兼容纠正及资源生命周期。同身份重新登录更新原记录并保留宿主管理设置；模型使用 `wb/`、`wb-ai/` 前缀。

OpenAI Chat、Responses、Claude Messages 与 Gemini 客户端按已选站点和已解析模型进入原生路由，结果再按客户端协议返回，支持流式、非流式及客户端工具循环；四种格式的请求/响应转换直接使用官方公开 translator SDK（`sdk/translator`、`sdk/translator/builtin`）。图片和 effort 依据当前账号成功模型快照核验，国际 GLM 保持文本输入。Chat 兼容纠正与认证恢复、Messages 签名纠正与认证恢复都有明确合计上限；成功策略按站点、账号、模型及请求形状隔离，输出后不重放。安全过滤保留停止语义，请求体、错误体、SSE 单事件、聚合响应与原生缓冲区均有上限；停用/卸载取消并等待在途工作。

刷新与 401 由官方 Manager 接管：插件 `auth.refresh` 只返回轮换后的凭证，宿主负责保存、安装、代次校验与重试；自动续期使用宿主读到的 `refresh_interval_seconds=300`。插件内同账号同代次 refresh 单飞、成功结果保留 60 秒。刷新凭证被明确拒绝与“access token 仍有效”分别处理：未到期且未被上游拒绝的 access token 仍可推理，重启后由宿主重新加载并重建状态。

## 当前验证状态

官方 Linux amd64 发布包与同一插件 SO 在 WSL 及 Debian 容器分别通过62/62项检查；各包含四身份、24个跨协议流模式场景及12个工具结果闭环。Windows官方源码构建通过61/62项，唯一目录迟留已接受：删除立即移除凭证并阻断调用，独占模型可能暂留在全局目录（实测请求HTTP503）。官方Windows发布包加载与管理页验证通过，专用测试CA条件下的完整协议未验。

原版CPAMC v1.28.0已在隔离headless Chromium中实际点击国内/国际两入口，完成合成上游登录、轮询、保存及双凭证列表，控制台错误为0。证据集中于 [兼容性记录](docs/compatibility.md) 和 `out/logs/native-official-summary.json`、`native-official-headless-ui.json`；真实WorkBuddy网页、WeCom及账号模型能力仍未验证。

- 尚未验证：真实账号与 WeCom 网页登录、Linux arm64、macOS amd64/arm64、远程 CI。

不把构建成功当宿主加载成功，也不把隔离上游结果当真实账号结果；逐项状态与未验证范围以 [兼容性记录](docs/compatibility.md) 和运行记录为准。

## 交付边界

| 能力 | 说明 |
| --- | --- |
| 管理路由 | `GET /v0/management/plugins/cpa-workbuddy/status`，返回 `name`、`version`、`abi_version`、`schema_version` |
| OAuth 入口 | `workbuddy`（WorkBuddy国内）与 `workbuddy-ai`（WorkBuddy国际）；持久 provider 只有 `workbuddy` |
| 模型 | `wb/<上游 ID>`、`wb-ai/<上游 ID>`；`AuthData.Prefix` 留空，执行前校验并只剥离一层 |
| 输入/输出格式 | `openai`、`openai-response`、`claude`、`gemini` |
| 上游路由 | 国内 `claude-*` 走 `/v1/messages`；国内其他模型与国际全部模型走对应站点 `/v2/chat/completions` |
| 计数/任意转发 | `count_tokens` 与 `http_request` 返回明确不支持，不伪造上游计数 |

浏览器登录：调用 `GET /v8/management/oauth/auth-url?provider=workbuddy`（或 `provider=workbuddy-ai`）取得 `url` 与 `state`，用户自行在浏览器完成登录，再轮询 `GET /v8/management/oauth/status?state=<state>`。`wait` 表示等待，`ok` 表示宿主已完成凭证保存，随后从 `GET /v8/management/credentials` 查看。会话最多五分钟；重启、过期、取消或已消费的状态需要重新发起。登录 URL 与 state 属于敏感数据，不得粘贴到日志或问题报告。真实网页登录与企业微信步骤由用户完成。

模型发现读取账号 `/v3/config` 的 CLI 与企业 `custom:` 模型。普通发现只更新宿主进程内状态，不承诺每次立即落盘；登录与原生刷新结果携带当前快照，由宿主随凭证持久化，因此重启后沿用的快照可能早于最后一次普通发现。首次发现失败且无快照的账号不获得静态模型路由资格。

## 安装与文档索引

安装输入是官方 CPA v8.0.69 发布包与官方 CPAMC v1.28.0 页面；插件交付包包含动态库、生成头、版本、契约文档、CPA MIT、实际链接 Go 依赖完整许可及打包工具链 Go LICENSE/PATENTS。`build-info.txt` 记录实际模块版本，`checksums.sha256` 覆盖其余成员。库放入 CPA 的 `plugins.dir`，两个 `enabled` 由宿主管理，启动与凭证操作由宿主负责。完整步骤见 [部署说明](docs/deployment.md)。

| 文档 | 内容 |
| --- | --- |
| [需求](docs/requirements.md) | 功能、站点与验收范围 |
| [架构](docs/architecture.md) | 宿主/插件边界、身份与资源所有权 |
| [兼容性](docs/compatibility.md) | 固定来源、逐平台实际构建/加载证据及未验证限制 |
| [部署](docs/deployment.md) | 官方包校验、v8 配置、双入口登录、管理、容器、故障与真人验收 |
| [质量契约](docs/quality.md) | 统一工程命令、工具、CI、原生测试与包白名单 |
| [任务表](docs/tasks/workbuddy-plugin.md) | 14 片状态、依赖及实际勾选的 AC |
| [版权与许可](docs/third-party-notices.md) | 协议移植、CPA SDK 与 Go runtime 的完整许可位置 |

## 工程入口

在仓库根目录使用统一 Go 入口：

```text
go run ./cmd/project bootstrap
go run ./cmd/project quality
go run ./cmd/project build
go run ./cmd/project test
go run ./cmd/project package
```

代码入口为 `cmd/cpa-workbuddy/`，ABI/wire 与分派位于 `internal/plugin/`，业务协议位于 `internal/workbuddy/`，根 `version.go` 读取 `VERSION`。所有产物、缓存、临时文件和日志统一位于 `out/`，本地探索材料保留在 `out/scratch/`。工具链及质量门禁见 [quality.md](docs/quality.md)。

`build`、`test`、`package` 使用当前主机的 Go/C 原生工具链（Baseline 为 Go1.27.1 与可用 C 编译器）。CI 与本地包以 Windows amd64、Linux amd64/arm64、macOS amd64/arm64 为目标；每个 CI job 核对实际 OS/arch。当前实际构建与宿主加载证据只覆盖 Windows amd64 与 Linux amd64，其余三项及远程 CI 未验证。

`package` 输出 `out/dist/cpa-workbuddy-v<VERSION>-<goos>-<goarch>.zip` 与 `.zip.sha256`。库的文件名固定为 `cpa-workbuddy` + 平台扩展名（`.dll`/`.so`/`.dylib`）。

## 首版目标

通过 WorkBuddy 浏览器登录接入 CPA 凭证保存、列表、刷新、禁用和删除。按账号 `/v3/config` 发现 CLI 与企业 `custom:` 对话模型；发现临时失败仅沿用同账号最近成功快照，无快照不获得静态模型路由资格。

| 站点 | 凭证 site | CPA 模型 ID | WorkBuddy 上游路由 |
| --- | --- | --- | --- |
| 国内 | `workbuddy` | `wb/<上游 ID>` | Claude 为 `/v1/messages`，其他为 `/v2/chat/completions` |
| 国际 | `workbuddy-ai` | `wb-ai/<上游 ID>` | 全部为 `/v2/chat/completions` |

`AuthData.Prefix` 留空，前缀由插件生成；只在支持模型的同站点凭证间调度。全局模型按 ID 合并不代表各账号权限、能力或配额相同，完整解释见需求第五项问答。国际目标是 WorkBuddy AI，独立 CodeBuddy 服务不在范围内；首版不读取桌面凭证、不导入 API key、不交付独立管理页，也不包含用量、积分、签到或其他非对话 API。

## 文档与来源

- [需求](docs/requirements.md)：唯一需求正文、完整首版范围、协议契约、验收矩阵与五项问答。
- [架构](docs/architecture.md)：代码布局、宿主责任、身份/站点/协议与资源边界。
- [兼容性](docs/compatibility.md)：事实、固定源码引用、上游函数/测试对应、风险、待核验项及实际运行证据。
- [部署与人工验收](docs/deployment.md)：官方包安装、CPAMC v1.28.0、双入口登录、v8 管理接口、容器卷、代理和真实账号清单。
- [工程质量](docs/quality.md)：固定工具安装、统一门禁、Git hooks、CI 与验证证据。
- [14 片任务](docs/tasks/workbuddy-plugin.md)：精确依赖、实施内容和可观察 AC；状态按验证证据更新。

WorkBuddy 行为参考固定 [Magpie WorkBuddy commit](https://github.com/magpie-community/plugins/tree/0135b7b0ea979c94033a7712a0c6ca1be8dafb70/packages/workbuddy)。源码仓库的 `_ref/` 保留原始源码、测试、样本和上游根许可证，文件来源与摘要由 `_ref/UPSTREAM.json` 固定。官方契约以 `out/verification/cpa-official-gVOq3z`（v8.0.69）与 `out/verification/cpamc-official-hdYiKj`（v1.28.0）的源码为准。项目采用 MIT，移植代码保留必要版权及许可证声明，完整来源见 [版权与许可](docs/third-party-notices.md)。

提交、推送与发布需用户明确授权；作者 `sheepyu`，计划仓库 `ygq-future/cpa-workbuddy`（远程尚未创建）。
