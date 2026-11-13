# cpa-workbuddy

面向 CLIProxyAPI（CPA）的 WorkBuddy 原生动态库插件，一个 provider `workbuddy` 承载国内 `copilot.tencent.com` 与国际 `www.workbuddy.ai` 的多个账号及个人/企业身份。

**已实现工程基线、Slice 01 原生接入及 Slice 02 双站点浏览器登录与凭证恢复。** 登录使用 CPA 认证接口与宿主 HTTP 回调，凭证路径与持久化由 CPA 管理；账号模型、聊天、刷新、工具、图片、reasoning、国内 Claude Messages 和客户端协议互通按后续切片交付。各项验证状态见 [任务表](docs/tasks/workbuddy-plugin.md) 与 [兼容性记录](docs/compatibility.md)。

插件 ID 为 `cpa-workbuddy`，作者 `sheepyu`。产品版本唯一来源是 [VERSION](VERSION)；计划仓库为 `ygq-future/cpa-workbuddy`，远程仓库尚未创建。提交、推送和发布需用户明确授权。

## 当前交付边界

CPA 宿主要求注册包含有效身份与至少一项真实能力。Slice 01 提供经 CPA 现有管理鉴权保护的只读状态接口：

```text
GET /v0/management/plugins/cpa-workbuddy/status
```

接口返回插件身份、产品版本、ABI 和 schema；不创建独立管理页。插件另声明已实现的 `auth_provider`，提供浏览器登录、轮询和自身带版本凭证解析；模型、执行和格式能力按后续切片声明。真实原生测试宿主、实际 CPA 宿主与真实账号的证据分别记录。

交付物为 CPA 加载的 CGO 动态库：Windows `.dll`、Linux `.so`、macOS `.dylib`。库没有独立 `start` / `stop`；加载、停用和卸载由 CPA 管理。首个验证平台为 Windows amd64，其他平台的构建和实际加载将在后续单独记录。

浏览器登录通过 CPA 现有管理鉴权访问以下入口，必须显式选择站点：

```text
GET /v0/management/workbuddy-auth-url?site=workbuddy
GET /v0/management/workbuddy-auth-url?site=workbuddy-ai
```

用户自行打开返回的 `url`，随后使用返回的 `state` 查询 `GET /v0/management/get-auth-status?state=<state>`；`wait` 表示等待浏览器完成，`ok` 表示 CPA 已成功保存凭证，可从 `GET /v0/management/auth-files` 查看。插件会话有效期最多五分钟，重启、过期、取消或已消费的状态需要重新发起登录；同一状态只交付一次凭证。登录 URL 与 state 属于敏感数据，不应粘贴到日志或问题报告。真实网页登录与企业微信步骤由用户完成，自动化验证使用隔离协议上游。

## 工程入口

在仓库根目录使用统一 Go 入口：

```text
go run ./cmd/project bootstrap
go run ./cmd/project quality
go run ./cmd/project build
go run ./cmd/project test
go run ./cmd/project package
```

代码入口为 `cmd/cpa-workbuddy/`，ABI/wire 与分派位于 `internal/plugin/`，根 `version.go` 读取 `VERSION`。所有产物、缓存、临时文件和日志统一位于 `out/`，本地探索材料保留在 `out/scratch/`。工具链及质量门禁见 [quality.md](docs/quality.md)，验证结果见兼容性记录。

## 首版目标

后续通过 WorkBuddy 浏览器登录接入 CPA 凭证保存、列表、刷新、禁用和删除。按账号 `/v3/config` 发现 CLI 与企业 `custom:` 对话模型；发现临时失败仅沿用同账号最近成功快照，无快照不获得静态模型路由资格。

| 站点 | 凭证 site | CPA 模型 ID | WorkBuddy 上游路由 |
|---|---|---|---|
| 国内 | `workbuddy` | `wb/<上游 ID>` | Claude 为 `/v1/messages`，其他为 `/v2/chat/completions` |
| 国际 | `workbuddy-ai` | `wb-ai/<上游 ID>` | 全部为 `/v2/chat/completions` |

`AuthData.Prefix` 留空，前缀由插件生成；只在支持模型的同站点凭证间调度。全局模型按 ID 合并不代表各账号权限、能力或配额相同，完整解释见需求第五项问答。国际目标是 WorkBuddy AI，独立 CodeBuddy 服务不在范围内；首版不读取桌面凭证、不导入 API key、不交付独立管理页，也不包含用量、积分、签到或其他非对话 API。

## 文档与来源

- [需求](docs/requirements.md)：唯一需求正文、完整首版范围、协议契约、验收矩阵与五项问答。
- [架构](docs/architecture.md)：代码布局、宿主责任、身份/站点/协议与资源边界。
- [兼容性](docs/compatibility.md)：事实、固定源码引用、上游函数/测试对应、风险、待核验项及实际运行证据。
- [工程质量](docs/quality.md)：固定工具安装、统一门禁、Git hooks、CI 与验证证据。
- [14 片任务](docs/tasks/workbuddy-plugin.md)：精确依赖、实施内容和可观察 AC；状态按验证证据更新。

WorkBuddy 行为参考固定 [Magpie WorkBuddy commit](https://github.com/magpie-community/plugins/tree/0135b7b0ea979c94033a7712a0c6ca1be8dafb70/packages/workbuddy)。原始源码、测试、样本和上游根许可证保存在 `_ref/`，来源与文件摘要见 [_ref/UPSTREAM.json](_ref/UPSTREAM.json)，不修改且不打包。CPA 契约以只读 `../.cpa-upstream-src/` 为准，`../cpa-secret-manager/` 只提供桥接模式参考。项目采用 MIT；移植代码保留必要版权及许可证声明。
