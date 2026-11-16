# cpa-workbuddy 兼容性、来源与核验记录

> 证据记录，不是功能完成声明。产品范围见 [requirements.md](requirements.md)，技术边界见 [architecture.md](architecture.md)，验收状态见 [14 片任务](tasks/workbuddy-plugin.md)。所有未运行项目写“未验证”，不以源码推断替代实际宿主结果。

## 当前证据等级与宿主边界

当前实施基线为未修改的官方 CPA `v8.0.69`（tag commit `f7ae8feeb3710319f524730849fbe32fde1751e4`）与 CPAMC `v1.28.0`，ABI 1/schema 10。官方 SDK 直接作为 Go 模块依赖，客户端翻译在插件内完成。下载来源、文件 SHA256、源码树与 tag 的关系见 [部署说明](deployment.md#固定来源与校验)；当前安装不使用宿主补丁。

| 验证面 | 实际结果 | 证据与限制 |
| --- | --- | --- |
| Linux amd64 官方发布包 | 62/62 项通过；四身份、24 个跨协议流模式场景、12 个工具结果闭环、刷新、重新登录、禁用/删除、失效及健康重启 | `out/logs/native-official-linux-final.json`；真实远端为合成 HTTPS 协议设施 |
| Linux amd64 Debian容器 | 同一官方发布包实际加载SO，62/62项通过，保留专用持久目录完成失效/健康两阶段重建 | `out/logs/native-official-container-final.json`；固定Debian镜像及glibc记录见历史平台表，真实账号未验 |
| Windows amd64 官方源码构建 | 61/62 项通过；唯一失败为删除凭证后的全局模型目录迟留 | `out/logs/native-official-windows-final.json`；源码树 `1aaf56bc6a0fc6fce19049ba7ae8554c882304a6` 与 tag 仅 README/assets 差异；此自建二进制不冒充官方发布物 |
| Windows 官方发布包 | 插件实际加载、管理鉴权及官方 HTML SHA256 核对通过；专用测试 CA 校验失败，完整协议未运行 | `out/logs/native-official-windows.json`；`go version -m` 为 Go1.26.10；未修改系统证书库 |
| 官方前端实际 DOM | headless Chromium 点击国内/国际两按钮、成功轮询、保存两站点凭证并打开实际列表；控制台错误 0 | `out/logs/native-official-headless-ui.json`；真实 WorkBuddy 网页、WeCom 与主观界面体验未验证 |
| Windows 删除边界 | 删除后列表移除，调用 HTTP503；独占模型在 `/v1/models` 残留至少 25 秒 | 用户接受目录迟留；`out/logs/native-official-deletion-boundary.json`；需要清理时先禁用再删除或重启，并核对结果 |
| 账号模型快照 | 登录和原生刷新结果携带可恢复快照；普通发现更新内存，持久化不保证逐次即时完成 | `internal/workbuddy/lifecycle_snapshot_test.go`、上述完整宿主记录；首次失败不授予静态资格 |
| 刷新/失效 | Manager 接管保存、安装和一次401恢复；300秒周期、60秒单飞结果；刷新拒绝与access仍有效分别处理，重启重新验证 | `internal/workbuddy/refresh.go`、`credentials.go`，官方 `sdk/cliproxy/auth/conductor_refresh.go`；保存失败可告警后继续内存凭证，存在重启风险 |
| 工程质量 | 完整quality九阶段通过；5个变更Go文件gopls hint级0诊断；模型样本由测试自有testdata提供，包消费者验证许可生成、原始字节、摘要和缓存错误传播 | `out/logs/publication-cleanup-quality.log`、`publication-cleanup-lsp.log`、`publication-cleanup-package-test.log`；参考目录缺席、缓存已准备时测试完全离线 |
| 平台与账号 | Windows/Linux amd64 已构建与加载；国际个人账号在本地 CPA v8.0.56 完成真实登录；hy4flash-free 四协议文本、多轮、工具、图片和reasoning，以及手动/周期刷新和刷新后重启恢复已实测 | 本节下表分列流模式、场景和证据；其他账号/站点、永久失效重登、三个原生平台与远程CI仍待验 |

运行使用 `server.commercial-mode:true`、`observability.logs.request-log:false` 和 debug 关闭，完整输出经 fixture token/state/用户与企业标识扫描。单独关闭 request-log 仍会生成错误请求日志，已保留失败证据 `native-official-windows-source.json`；官方 `internal/api/server.go:185-196` 的 commercial-mode 禁用 request logger，Gin 日志仍启用。生产代理与容器日志同样不得捕获敏感正文。

## 本地 WSL 真实账号验证与认证信息

2026-11-16，用户在现有 Debian 容器 CPA `v8.0.56`（commit `585f97d`）的管理页面完成国际站个人账号登录。经宿主管理接口确认凭证已保存且 `label` 与 `display_name` 对应；用户截图证明管理列表已显示记录。该证据只覆盖一个国际个人账号，不能替代国内登录、WeCom、企业或四账号调度验收。

原失败请求 `error-v1-chat-completions-2026-11-16T133907-7069cc04.log` 在插件解析阶段报 `WorkBuddy returned an invalid chat stream`；紧随的请求因冷却被拒绝。经 CPA `api-call` 重放其上游请求，实际返回 HTTP200 SSE：未完成分片使用 `finish_reason:""`，结束分片还包含 `function_call:{"name":"","arguments":""}`。解析器把空结束原因视为未完成，并接受无内容的旧式函数占位；非空旧式函数调用、错误类型、未知函数字段和缺少完成事件仍拒绝。对应消费者回归为 `TestExecutorResponseAcceptsWorkBuddyPendingAndTerminalPlaceholders` 与 `TestExecutorResponseRejectsIncompleteMalformedAndUnsupportedStreams`。

修复后的 Linux SO 在该实际容器加载后，`wb-ai/hy4flash-free` 的 OpenAI Chat、Responses、Claude Messages 和 Gemini 客户端各完成流式与非流式文本调用，均 HTTP200 且有正文。Chat 非流式具有 usage/stop，流式具有 stop/[DONE]；Responses 非流式状态 completed、error 为 null。重启前后凭证 ID 相同，Chat 验证后状态 active、无错误提示且未冷却。原始账号与正文不纳入公开证据；文本记录为 `out/logs/real-account-diagnosis.json`、`real-account-chat-proof.json` 与 `real-account-protocol-proof.json`。能力与刷新补验见下表。

### 官方管理前端的账号标题

CPA管理列表的name取AuthData.FileName，模型弹窗复用该name；Label与稳定ID分别保留。本项目通过插件输出可读FileName适配官方页面，文件名含账号名称与16位身份区分符，完整身份摘要仍为AuthData.ID。规范file_name写入凭证JSON；重登通过只读host.auth.list取得当前记录名称，元数据不可用或目标名称已属于其他凭证时明确失败。源文件解析支持安全相对路径与Windows大小写语义，刷新和模型快照写回沿用规范名称。

本地CPA保持原镜像，官方CPAMC v1.28.0 HTML的SHA256为 `035cdd5340a5b9a76cb43d11e45444be1990ae1ecff413d6a92b7e8f03bc6373`，已通过实际HTTP摘要核对。管理接口先保存并核验可读文件，再清理同身份的旧文件，完整ID、登录代次、Label、作用域和手动禁用状态保持；模型列表仍有23项，文件下载和手动刷新成功，重启后名称保留，Chat流式/非流式均HTTP200。

隔离headless浏览器在未修改的官方页面真实登录后，账号卡片与模型弹窗标题包含账号名称、不含完整认证ID，弹窗可访问名称关联标题，实际23行模型加载完成，页面错误0。安全记录为 `out/logs/official-panel-restoration.json` 与 `readable-credential-acceptance.json`。公开Service回归覆盖同名账号、站点/企业作用域、持久化/重登、大小写扩展名与跨平台名称安全。


### 真实能力与刷新补验

同一国际个人账号、同一 `wb-ai/hy4flash-free` 免费模型共补验26项，均通过；模型免费标记经当前凭证快照确认。以下每项均走实际CPA客户端入口及真实WorkBuddy上游，工具结果由客户端计算并回传，图片为隔离生成的64×64四色PNG。`out/logs/real-account-capabilities.json` 包含22项，`real-account-stream-tools.json` 包含4项；证据只记录安全断言，不保存账号标识、凭证、完整正文或reasoning内容。

| 场景 | 客户端/流模式 | 项数 | 通过依据 |
| --- | --- | --- | --- |
| 多轮上下文 | 四协议，非流式 | 4 | 首轮记录随机code，第二轮正确返回该code |
| 工具闭环 | 四协议，非流式首轮与续轮 | 4 | echo_probe名称/参数正确，续轮回答使用客户端随机结果 |
| 流式工具闭环 | 四协议，流式首轮、非流式续轮 | 4 | 从实际delta/结构化事件取得参数，正确回传工具结果并用于回答 |
| 图片输入 | 四协议，非流式 | 4 | 正确按指定顺序识别四个色块；图片SHA256为5defbf005193f316590586b5a8f327e87ab6e4516c4140729e669bfc171a3151 |
| high effort reasoning | 四协议，非流式 | 4 | 算式结果2562，协议对应reasoning字段非空 |
| high effort reasoning | 四协议，流式 | 4 | reasoning事件/片段非空，最终文本包含正确算式结果 |
| 手动刷新 | CPA Manager单凭证刷新 | 1 | access/refresh及有效期变化，ID/label/site/企业作用域保持，刷新后Chat成功 |
| 刷新后重启恢复 | 现有持久卷与实际容器 | 1 | 登录代次、ID/label、模型快照保持；启动后宿主再次刷新成功，真实Chat HTTP200 |

`out/logs/real-account-refresh-schedule.json` 记录当前账号的官方 `refreshed workbuddy, …, <nil>` 成功事件，连续五个自动刷新间隔为305、306、304、305、306秒。手动刷新使用 `POST /v0/management/auth-files/refresh`，仅指定当前凭证，未操作其他账号。

目录内容未变时，`internal/workbuddy/models.go:102-106` 保留既有快照及captured_at；因此本次以快照身份、作用域与模型内容有效作为刷新检查。宿主重启后按原生生命周期再次轮换token，验收依据为稳定身份、有效期、模型资格和实际请求，不要求刷新后的token字节在重启前后相同。

当前记录仅覆盖一个国际个人账号及一个模型；国内登录/WeCom、国内Claude原生Messages端点、多账号/企业权限隔离、首次401恢复、永久撤销及真实浏览器重登，仍需对应证据。其他原生平台和远程CI状态不因本次账号验证改变。


认证信息只分析，不在本次增加解析或展示：实际凭证没有独立的 `info` 键，相关字段位于顶层及 `model_snapshot`。以下为可选优化候选。

| 已有字段 | 可用信息与展示边界 |
| --- | --- |
| `display_name` / CPA `label` / `file_name` | Label保存账号展示名，规范file_name用于可读文件与官方标题，完整稳定ID负责认证隔离；刷新和同身份重登保留既有文件名 |
| `site`、`enterprise_id` | 区分国内/国际与个人/企业作用域；公开展示名称或作用域，不暴露内部标识 |
| `expires_at`、`refresh_expires_at`、`refresh_interval_seconds` | 解释令牌有效期和宿主刷新偏好；不能据到期时间承诺账号、配额或调用持续可用 |
| `model_snapshot.captured_at`、`model_discovery_failure` | 展示模型权限快照的新鲜度和发现状态；空错误仅代表当次发现未记录错误 |
| 模型 `free`、`credit_rate` | 免费标记及配置给出的计费倍率；不是余额、剩余次数、货币价格或实时额度 |
| 模型 `context_length`、`max_output_tokens`、`images`、`efforts`、`default_effort` | 已解析的模型能力声明；当前账号23个模型、1个free标记、21个images声明；hy4flash-free的图片及high effort已实测，不能推广为其他模型均通过 |

### 模型倍率与客户端展示边界

固定参考插件的 `modelOf`（`_ref/index.mjs:3483-3510`）直接给Magpie返回 `free`、`rate` 和 `rateWas`，分别表示免费、当前积分倍率与折前倍率；这些是Magpie自有模型属性。WorkBuddy `/v3/config` 的credits及促销由本项目 `internal/workbuddy/model_catalog.go:155-175` 解析，并作为 `credit_rate`、`credit_rate_was`、`free` 保存在账号模型快照中，未转换成货币token单价。

本项目 `internal/workbuddy/models.go:121-134` 只向CPA输出其模型协议可表达的字段。固定SDK `sdk/pluginapi/types.go` 的ModelInfo没有free/rate/rateWas，官方注册表的 `convertModelToMap` 也没有这些输出键；在插件JSON中增加同名字段不会贯通宿主。实际本地CPA `v8.0.56` 的 `GET /v1/models` 返回23个wb-ai模型，每项只有id/object/owned_by，当前名称、倍率与免费标记均未进入该公共响应。同期账号快照的23个模型均有倍率，hy4std为0.29、hy4flash为0.03、hy4flash-free为0且free=true；安全证据为 `out/logs/model-pricing-boundary.json`。

CPA宿主与官方管理前端保持原始发行物，本项目在插件公开契约内完成适配。积分倍率保留在账号模型快照中，当前公共模型列表不提供结构化倍率徽标；不将倍率拼进模型ID或转换成token货币价格。



## 来源与历史源码定位

### 参考来源

| 来源 | 用途与边界 |
|---|---|
| `../.cpa-upstream-src/` | CPA `v8.0.69`（commit `f7ae8feeb3710319f524730849fbe32fde1751e4`）的本地只读参考副本；CPA ABI、宿主回调、认证、模型注册与执行契约 |
| `../cpa-secret-manager/` | 现有 CGO 插件桥接样例；具体接口仍以目标 CPA 源码为准 |
| `https://github.com/magpie-community/plugins/tree/0135b7b0ea979c94033a7712a0c6ca1be8dafb70/packages/workbuddy` | 固定 commit 的 WorkBuddy 协议、行为与测试基线 |
| `_ref/` | 按固定 Magpie commit 取得的本地忽略只读副本：完整 WorkBuddy 包源码、33 个测试文件及 5 个 testdata 样本；包版本 `0.2.31`；附上游根许可证，不随仓库分发，也不进入插件发布包 |
| `_ref/UPSTREAM.json` | 本地副本的来源仓库、commit、文件清单、Git blob SHA-1、SHA-256 与大小；下载后逐文件核验 |
| `out/scratch/workbuddy-go-spike/` | 本地 Go 探索材料，不属于 Magpie 源码，不作为协议依据或已完成实现 |

`_ref/` 与 `../.cpa-upstream-src/` 是本地只读参考副本，不是构建、测试、CI 或打包输入；两者都按上表固定 commit 从上游公开仓库取得，可按需重新下载。`_ref/` 的上游文件保持原文，修改业务行为不能直接改写参考源码来使其“匹配”。当前固定 commit 为 `0135b7b0ea979c94033a7712a0c6ca1be8dafb70`，本地文件清单及校验记录见 `_ref/UPSTREAM.json`。上游更新时显式调整基线并重新检查行为差异，不静默追踪 main。

行号均指当前固定参考快照，源码更新后应同时更新证据。参考实现中的实测结论是其快照记录，不代表本项目已经对真实账号完成实测。

### 按参考源码迁移的约束

- WorkBuddy 协议与业务逻辑以 `_ref/index.mjs` 及其测试为直接依据，按功能迁移到 Go；CPA ABI、存储及格式适配以 CPA 源码为依据。
- 每个功能的迁移验收记录对应的上游函数/行号、相关上游测试、CPA 适配位置和本地验证结果。测试与 testdata 保留为行为依据，不能只根据 README 或模型记忆补写协议。
- 浏览器登录、账号/企业头、刷新、模型配置解析、请求整形、工具映射、会话标识、Chat/Messages、SSE 及兼容重试均需要逐项对照；参考实现未证明的请求字段、错误语义或模型能力先核验，再实现。
- 按需求已确认的范围移植浏览器登录和聊天相关逻辑；桌面登录、API key、签到及用量等参考能力仍完整保存在 `_ref/` 供查证，不因此自动扩展首版交付范围。
- 站点模型前缀、CPA 状态集成、账号模型列表缓存等本项目约定与上游适配分别记录。行为差异遵循需求，不能把项目自行增加的策略描述成 Magpie 原有逻辑。
- 遇到参考代码、测试及本地运行证据冲突时，说明具体差异并定位原因；架构或产品行为需要改变时重新确认，不自行猜测上游协议。

### 接口与行为定位

| 事实 | 证据位置 |
|---|---|
| 当前参考 JSON 契约的 `SchemaVersion` 为 6 | `../.cpa-upstream-src/sdk/pluginabi/types.go:20` |
| 插件能力含单个 AuthProvider、ModelProvider、Executor 及格式声明 | `../.cpa-upstream-src/sdk/pluginapi/types.go:72-100` |
| 认证包含解析、登录启动、轮询和刷新 | `../.cpa-upstream-src/sdk/pluginapi/types.go:274-279` |
| 轮询成功可返回一个或多个 AuthData | `../.cpa-upstream-src/sdk/pluginapi/types.go:336-345` |
| 静态模型与账号模型是独立接口 | `../.cpa-upstream-src/sdk/pluginapi/types.go:394-396` |
| executor 还包含 CountTokens 和 HttpRequest | `../.cpa-upstream-src/sdk/pluginapi/types.go:594-599` |
| 上游 HTTP 必须经过宿主 transport policy | `../.cpa-upstream-src/sdk/pluginapi/types.go:602-607` |
| 登录 URL query 会转交插件 metadata | `../.cpa-upstream-src/internal/api/handlers/management/auth_files_oauth_callback.go:194-195` |
| 账号模型先排除/别名处理，后由宿主加 AuthData.Prefix | `../.cpa-upstream-src/sdk/cliproxy/service_executors.go:562-565` |
| 非强制前缀模式会同时保留原模型 ID | `../.cpa-upstream-src/sdk/cliproxy/service_models.go:622-665` |
| 参考实现对国内 Claude 选择 WorkBuddy Messages 路由 | `_ref/index.mjs:168-183` |
| CLI 模型及企业 custom 模型的配置解析 | `_ref/index.mjs:3667-3733` |

### 版本与部署核验

- 目标契约为 ABI 1/schema 10；当前验收版本固定为官方 CPA v8.0.69 与 CPAMC v1.28.0，不据此声称所有 v8 版本均兼容。
- Windows amd64、Linux amd64 的真实加载与协议证据见本文首表；Linux arm64、macOS amd64/arm64 及远程 CI 保持未验证。
- 容器无需桌面；用户浏览器完成真实登录，服务端轮询。隔离协议验收与真人验收分别记录。
- 公开 OAuth 入口 workbuddy/workbuddy-ai 分别选择站点，持久 provider 都是 workbuddy。官方前端直接展示两个入口，不要求外部前端改动。
- CPA宿主与管理前端使用未修改的官方实现，项目不交付或维护补丁。当前已接受Windows删除后目录迟留边界，实际调用必须立即阻断。

## 认证失效与重新登录的宿主边界

原生Manager负责刷新驱动、凭证保存与安装、当前凭证版本检查及401后的单次恢复。插件只返回当前代次的轮换AuthData；不会通过host.auth.save自行安装结果，不要求定制credential-state声明。成功登录生成credential_generation，完整ID包含站点、用户和企业/租户作用域；规范file_name独立保留。

官方 v8 凭证列表暴露 `requires_sign_in`、`renewal_state`、`recovery_stage`、`refresh_error` 与 `oauth_provider`（`internal/api/handlers/management/auth_files.go:794-853`）。明确刷新拒绝采用原生状态；未到期、未实际被拒绝的 access token 仍可推理。单次401、临时网络故障或模型配置失败不能直接宣告永久失效。重启后允许官方宿主重新验证并重建状态。

重新登录必须联合核对稳定记录、认证错误清理、管理设置、模型与实际调用；四身份及禁用身份重新登录、健康/失效两阶段重启已由当前 Linux/Windows完整程序记录验证。保存失败时原生宿主可 warning 后继续使用新内存凭证，不能承诺未落盘凭证可跨重启恢复。Windows 删除目录的已接受限制见本文首表。

## 五项问答的源码对应

五项完整问答在 [需求附录](requirements.md#附录-acpa-凭证与模型机制问答) 维护。以下为按问答顺序记录的源码定位；除上表特别标明本次已读的行段外，后续适配时逐项复核。

- 保存登录记录：`../.cpa-upstream-src/internal/api/handlers/management/auth_files_provider_oauth.go:1025`。
- 构造凭证列表项：`../.cpa-upstream-src/internal/api/handlers/management/auth_files.go:637`，字段从 `:655` 起。
- 模型获取入口：`../.cpa-upstream-src/sdk/cliproxy/service_models.go:67`、`service_executors.go:498`。
- 以 auth.ID 注册：`../.cpa-upstream-src/sdk/cliproxy/service_executors.go:428`、`:454`。
- 支持性检查：`../.cpa-upstream-src/sdk/cliproxy/auth/conductor_selection.go:985`，通过 `ClientSupportsModel(auth.ID, routeModel)` 判断。
- 官方AuthData转换保留显式ID与独立FileName；管理列表name优先FileName，模型弹窗读取name。固定源码位置为 `internal/pluginhost/auth_provider.go:1307-1324` 与 `internal/api/handlers/management/auth_files.go:667-678`。
- 文件路径解析：`../.cpa-upstream-src/sdk/auth/filestore.go:411-440`。
- 同名模型再次注册时更新计数与公开 Info：`../.cpa-upstream-src/internal/registry/model_registry.go:670-675`。

第五项的关键含义：`RegisterClient(auth.ID, "workbuddy", models)` 的每凭证列表决定候选资格；全局按 modelID 合并计数、provider 和一份公开 Info。同站点同 ID 的公开元数据可能被后注册者更新，不证明所有账号能力、配额相同。`wb/glm-5.3` 与 `wb-ai/glm-5.3` 是不同模型 ID。

## 上游函数、测试与 CPA 适配对应表

函数与行号基于固定 `_ref/index.mjs`；上游测试作为行为来源保留，不代表 Go 迁移已通过。CPA/Go 位置写“待实现”的条目必须在对应切片完成时替换为真实位置及验证结果，不创建占位代码。

| 切片/行为 | 上游函数与定位 | 对应上游测试/样本 | CPA / Go 适配与本地结果 |
|---|---|---|---|
| 01 原生加载/管理状态 | WorkBuddy 参考包不提供 CPA C ABI；以 CPA `sdk/pluginabi/types.go:5-20`、`sdk/pluginapi/types.go:72-100`、`internal/pluginhost/host.go:1025-1060` 及桥接样例为依据 | `native_integration_test.go` + 独立 `testdata/abi/probe.c`；`internal/plugin/runtime_test.go` | `cmd/cpa-workbuddy/`、`internal/plugin/`、根 `version.go`；完整质量门禁及 Windows 原生 smoke 通过，实际 CPA 未验证 |
| 02 浏览器登录/凭证恢复 | `poll:1824`、`browserSignIn:1837`、`success:1882`、`merge:232` | `enterprise.test.mjs:66`、`:95`、`:116`；`wecom.test.mjs` | `internal/workbuddy/service.go:177`、`:273`、`:420`；`credentials.go:171`、`:228`、`:262`；`cmd/cpa-workbuddy/main.go:102`；`service_test.go`、原生 smoke、实际 CPA 管理处理器与子进程恢复通过；真实账号未验证 |
| 03 个人/企业身份 | `enterpriseHeaders:440`、`enterpriseOf:457`、`accountName:490`、`scopeOf:823` | `enterprise.test.mjs:20`、`:29`、`:37`、`:77`、`:107` | `credentials.go:93`、`:171`、`:215`、`:318`；`identity_test.go` 验证请求头来源与身份约束；实际 CPA 管理/文件存储验证五种身份、重复登录、设置保留和保存失败，真实账号未验证 |
| 04 模型与快照 | `textOnly:150`、`configModels:3469`、`modelOf:3483`、`priceOf:3560`、`effortsOf:3587`、`defaultEffortOf:3630`、`liveModels:3667` | `custom.test.mjs:73`、`:81`；`efforts.test.mjs`、`declared-images.test.mjs`、`price.test.mjs`；`testdata/config-561.json`、`config-efforts.json` | `model_catalog.go` 的 `ParseModelConfig`、`models.go` 的 `models` 和凭证 `model_snapshot`；公共协议测试、真实 CPA Host/Manager/FileTokenStore 与 service 注册/SelectAuth 通过，真实账号未验证 |
| 05 Chat 文本/会话/SSE | `sign:349`、`cliAgent:376`、`chatHeaders:394`、`signatureParts:553`、`turnParts:612`、`conversationParts:633`、`attendChat:789`、`systemOn:952`、`gzipped:1606`、`prepared:1620`、`makePlugin:3885` | `request.test.mjs`、`sent.test.mjs`、`conversation.test.mjs:51`、`:128`、`:153`、`:229`；`testdata/sent-0.1.43.json`、`sent.mjs` | `chat_request.go` 准备与作用域身份、`chat.go` 宿主 HTTP/stream 所有权、`chat_response.go` SSE/聚合、`runtime.go` executor 声明与分派；公共测试及实际 CPA 双站点文本/多轮/流式/取消/代理/日志验证通过，真实账号未验证 |
| 06 刷新与一次恢复 | `stale:245`、`refreshable:256`、`renew:262`、`merged:287`、`fresh:298`、`refreshed:320`、`reissued:2115` | `refresh.test.mjs`、`unauthorized.test.mjs` | `refresh.go` 同代单飞与公开 auth.refresh；原版 Manager 保存/安装/401恢复，完整宿主记录通过 |
| 07 失效/重新登录 | `refusedText:2161`、`signedOut:2187`、`reissued:2115`；状态适配 CPA 原生生命周期 | `unauthorized.test.mjs`；新增重登/重启验收 | `refreshFailure` 错误分类、`credentials.go` 代次与入口；原生列表、重登、禁用与重启通过，真实账号未验证 |
| 08 工具调用 | `toolChoiceOn:1126`、`safeName:1173`、`fitSchema:1190`、`fitShape:1291`、`objectRoot:1362`、`typeUnions:1416`、`fitTools:1448`、`namedBack:1649`、`renamed:1666` | `tools.test.mjs:13`、`:28`、`:41`、`:73`、`:94`、`:181` | `chat_tools.go`、`tool_schema.go` 与 SSE 工具聚合；公共边界及真实 CPA 双站点流式/非流式工具循环通过，真实账号未验证 |
| 09 图片/reasoning | `textOnly:150`、`reasoningOf:917`、`asReasoning:926`、`withDefaultEffort:3652`、`namesEffort:3663` | `images.test.mjs`、`reasoning.test.mjs`、`default-effort.test.mjs`、`efforts.test.mjs` | `chat_capabilities.go`、`chat_reasoning_response.go` 与 ModelInfo 声明；公共边界及真实 CPA 账号能力隔离、图片/effort/reasoning 通过，真实账号未验证 |
| 10 国内 Messages | `nativeOf:182`、`isMessages:652`、`withoutUnsigned:703`、`unsignedOn:709`、`withoutSignedThinking:728`、`effortsOf:3587`、`makePlugin:3885` | `native.test.mjs:39`、`:46`、`:110`、`:210`、`:283`、`:320`、`:334` | `messages_request.go`、`messages_response.go`、共享执行/认证恢复；原生公共边界与 CPA DLL 验证，真实账号未验证 |
| 11 CPA 协议转换 | `nativeOf:182`、`isMessages:652`、`modelOf:3483`；参考包不是 CPA 翻译器 | `native.test.mjs`；Chat/Responses/Claude/Gemini 双向场景 | `protocol_translation.go` 使用公开 translator SDK，完整官方宿主24场景与12工具闭环通过 |
| 12 兼容纠正与故障 | `unflag`、`systemAsUserOn`、`commaPausedOn`、`replyAfterToolsOn`、`hideDsh` | comma/systemasuser/aftertools/hidedsh/deepseek-flash/growth/filtered/native 测试 | `chat_compatibility.go`、`chat_errors.go`、`chat.go`；插件纠正有界，401交宿主，输出后不重放 |
| 13 管理与容器 | 官方管理登录、列表、调度和文件机制 | 需求 R4；真实账号人工清单 | 当前官方完整程序及前端证据见首表；真实账号/WeCom仍待验 |
| 14 构建交付 | CPA C ABI、平台 CGO 与动态链接依赖 | 原生加载及公开 CLI 包契约 | `cmd/project` 白名单、实际构建信息与完整依赖许可；三个未运行平台和远程CI待验 |

## 项目策略与上游行为差异

- 站点前缀、稳定 CPA 文件名、账号成功快照和首次失败无路由资格是本项目约定，不能描述成 Magpie 现有策略。参考包保留桌面/API key/用量/签到等功能不等于迁移它们。
- 参考实现区分执行前1分钟 stale、宿主预刷提前5分钟和60秒刷新缓存；当前产品选择官方300秒周期调度，单飞成功缓存保留60秒，具体边界见需求 R3.2。
- 参考浏览器 URL 只检查 HTTPS；本项目同时校验登录目标域。2026-11-13 对固定双站点 state 接口的实际 POST 均返回 code 0，登录目标分别为 `https://www.workbuddy.cn/login` 与 `https://www.workbuddy.ai/login`，只记录 scheme/host/path。国内还接受固定 `enterprise.test.mjs:55` 已证明的 `www.codebuddy.cn` 历史登录页；该登录域许可不改变 API 目标或引入 CodeBuddy 服务。
- 参考 JS 可直接 fetch，CPA 插件必须通过宿主 transport policy。非流式聚合、CPA 多客户端协议、认证失效卡片及调度恢复均需宿主适配和独立验证。
- 参考中图片支持、Claude Messages、count_tokens 404 等实测注释属于固定快照的历史记录；不能标为本仓库真实账号验证。

## 风险与待核验项

| 风险/待核验事实 | 处理与验收要求 |
|---|---|
| 官方 Windows 删除后的模型目录可能迟留 | 用户已接受；凭证列表移除且实际请求必须失败，残留模型不能证明可调度 |
| 模型首次发现失败时宿主如何恢复注册 | 明确宿主重试/刷新触发，不依赖“请求自然会恢复”的假设 |
| 同站点不同账号同模型的全局元数据可能覆盖 | 路由以每凭证模型列表为准；测试账号间能力差异，不能把全局元数据等同每账号权限 |
| refresh token 轮换与持久化失败 | 单飞、短时结果缓存、明确保存失败、重启风险报告；不重复消费旧 token |
| 终止刷新拒绝与有效 access token 分离 | 使用官方 requires_sign_in 与调度状态，重启后重新验证；真实账号仍待验 |
| 重新登录覆盖文件但继承旧错误/冷却或被在途旧结果覆盖 | 同身份稳定记录、凭证代次隔离、管理设置保留及状态恢复联合验收 |
| WorkBuddy UA/协议版本变化 | 常量集中、10085 可诊断、协议样本可更新；不宣称静态版本永久有效 |
| Messages 与 Chat 的语义转换损失 | 宿主边界核验 明确翻译归属，Slice 10–11 覆盖工具、reasoning、签名和 usage |
| 宿主与插件重试相叠加 | 明确责任和总上限，流开始后不重放，保留上游错误及 Retry-After |
| 容器原生库及 CGO 依赖 | 分平台/架构构建，记录 libc 与运行依赖，实际加载验证独立列出 |
| 真实账号与平台资源不齐 | 自动化完成可达验证，其余保留具体人工清单，不标为通过 |

后续可单独规划 WorkBuddy 用量/套餐/积分查询、国内签到及免费模型策略。它们不是首版的依赖，也不预建空接口或管理页面。

## 本地验证与后续验收

本节及下方 Slice 02–14 记录是此前切片的历史运行证据，包含独立 v7 兼容副本、当时测试名称和当时包内容。它们不描述当前安装输入，也不能替代本文首表的官方 v8 验收；当前行为与部署以需求、架构和部署说明为准。

- 工程命令使用 Go 标准库实现，入口为 `go run ./cmd/project`。完整 10 阶段质量门禁、CLI 行为测试、原生 smoke、hook 与打包的本机运行结果见 [质量契约](quality.md#验证状态与债务)。
- 格式、类型、LSP、弃用与失败测试须在同一根入口阻断；检查过程保持源码不变。
- 本机交付包位于 `out/dist/`，使用严格白名单并生成 SHA256；原生构建与实际 CPA 兼容声明分别记录。
- 文档检查通过：14 片精确依赖、五项原问答、文档本地链接及根 Markdown 目录规则；原始快照 45 文件通过大小/Git blob SHA1/SHA256 校验。

Slice 02 已通过隔离实际 CPA 宿主/管理处理器的 DLL 加载、双站点协议模拟登录、管理鉴权、凭证文件保存/列表及新子进程重启恢复。完整 CPA server 可执行程序、管理前端、认证失效状态通道、容器与真实账号仍未验证，按 Slice 03–14 的对应 AC 留证。原生探针、隔离实际宿主和真实账号分别记录，不相互替代。XERJ 验证记录见质量契约，最终审查结论在交付回复中提供。

### Slice 02 本地验证（2026-11-13）

- `go run ./cmd/project quality --stage lsp --stage lint --stage test` exit 0；包括浏览器协议、原生回调释放/取消/卸载、CLI 产物隔离与实际测试发现契约。`go test -race ./internal/workbuddy ./internal/plugin` exit 0。
- 隔离模块命令 `go -C out/verification/cpa-smoke test -count=1 -v .` exit 0，以实际 CPA commit `673131f57484517c3a1eae7e36c4cfa7b9bb4efc` 的 `internal/pluginhost`、management handler、FileTokenStore 加载当前 DLL。HTTP RoundTripper 只替换协议上游；实际原生调用、管理鉴权、metadata 透传、11217/12151、保存/列表和子进程恢复均运行宿主原代码。父子进程各自卸载插件，临时凭证目录退出清理。摘要见 `out/logs/slice02-cpa-smoke.json`。
- CPA 插件必须同时启用全局 `plugins.enabled` 和 `plugins.configs.cpa-workbuddy.enabled`，实际源码 `internal/pluginhost/config.go:13`、`:54-78` 默认禁用实例。浏览器凭证使用 `provider=workbuddy`、`type=workbuddy`、`credential_type=browser`、独立 `version=1`；宿主 `auth_provider.go:483-500` 的 type 归一化不会破坏解析。
- 完整 server 进程、容器、实际浏览器账号与 WeCom 未验证；人工登录须用户自行完成。插件同步 HTTP 回调实际网络取消受宿主请求 context 控制；本地关闭会拒绝新任务、丢弃迟到结果并等待同步回调结束，之后才允许卸载。

### Slice 03 本地验证（2026-11-13）

- `go test ./internal/workbuddy` exit 0；`go run ./cmd/project build` exit 0；`go run ./cmd/project quality --stage lsp --stage lint` exit 0。身份摘要算法保持不变；新增公共头生成边界验证个人/两个企业/双站点、mutable metadata 覆盖尝试、存储身份与已选 ID 不一致、调用方修改返回头等行为。
- `go -C out/verification/cpa-smoke test -count=1 -v .` exit 0，实际 CPA host/management/FileTokenStore 维护五个独立身份文件及列表记录：同 uid 的国内/国际个人、国内两个企业及另一个 uid。改昵称重新登录保持同 ID/FileName、替换 token 并保持单条记录；手动禁用、priority=7、代理与 excluded_models 保留。旧认证可选字段被当前空值清除；保存失败和企业选择不明确时原文件字节、文件数量与管理列表保持。用到的凭证均为合成数据，临时目录退出清理。
- 宿主敏感头核验：`internal/runtime/executor/helps/logging_helpers.go:635-656` 调用 `internal/util/provider.go:227-239`，Authorization 与含 token/secret 的头被掩码；隔离测试验证 `Authorization` 与 `X-Refresh-Token` 不保留完整值。目标源码 `RecordAPIRequest:65-72` 在无 Gin context 时退出，管理登录的 `PopulateAuthContext` 只从 background context 附加 RequestInfo（`auth_files_oauth_callback.go:187`、`auth_files_provider_oauth.go:957`、`:1053-1060`）。完整 server、其他请求路径的日志及真实账号仍未验证。

## Slice 04 本机验证记录

- `go test ./internal/workbuddy ./internal/plugin` 通过；完整 `go run ./cmd/project quality` 十阶段 exit 0，日志 `out/logs/slice04-quality.log`。LSP 使用 `gopls check -severity=hint`，覆盖全部修改 Go 文件且诊断为 0；工程 smoke 隔离副本复制实际生产源码，并验证缺少 ABI 测试时阻断。
- `go -C out/verification/cpa-smoke test -count=1 -run '^TestCPAAccountModelDiscovery$' -v .` exit 0；真实 CPA 加载当前 DLL，经四次管理登录创建两个国内/两个国际个人与企业凭证。验证 CLI UA/身份头、CLI/custom 完整 ID、128000/256000 不同账号上下文、带时间快照、文件内容替换、首次失败、有效空目录、缺 CLI、损坏配置、裸 401 与明确 token 拒绝、插件和子进程重启、同身份重登清理快照。日志 `out/logs/slice04-cpa-models.log`。运行证据核对保存内容与重启恢复，宿主原子写来源为 `internal/pluginhost/auth_provider.go:443-457`、`:511-540`。
- `go -C ../.cpa-upstream-src test -overlay=<out/verification/cpa-service-overlay/overlay.json 的绝对路径> -count=1 -run '^TestWorkBuddyRealServiceModelRegistration$' -v ./sdk/cliproxy` exit 0；overlay 仅增加隔离测试，未修改只读 CPA 源码。实际 `applyCoreAuthAddOrUpdate` 安装宿主原生兼容 executor，`completeModelRegistrationForAuth`、`handleAuthUpdate`、`Manager.SelectAuth`、`ResolveExecutionModel` 验证带前缀源模型别名与排除、未加前缀规则不误匹配、跨站点/未知前缀拒绝、账号私有模型与全局同 ID 合并隔离、删除撤销资格。未执行推理请求；聊天执行由 Slice 05 验证。日志 `out/logs/slice04-cpa-registration.log`。
- 首次发现失败后的实际恢复入口：宿主 `sdk/cliproxy/service_executors.go:498-568` 将成功空模型列表视为 handled 并撤销注册；重新登录、文件 watcher 的 auth update 和配置重注册会再次发现。隔离 service 测试实际执行 update 恢复，并证明结构/明确拒绝结果不能用 RPC error 代替，因为该路径 `:512-513` 遇错会直接返回而保留旧资格。宿主 `Manager.Update` 的 `conductor_lifecycle.go:273-275` 不传播普通凭证持久化错误，当前已验证成功落盘与子进程恢复；保存失败恢复属于 Slice 06。
- 模型发现仅通过成功列表授予权限，静态列表为空；token limits 映射 CPA ModelInfo。图片、efforts/default effort、积分倍率保留于账号快照，后续对应执行能力完成才对外声明；CPA 没有相应积分货币字段，不填写货币价格。真实账号、完整 server/管理前端、容器与其他平台仍未验证。
- 相同成功内容保留原快照采集时间且不返回 AuthUpdate，内容、作用域或失败状态变化时才写回。公共边界测试与真实 Host/Manager 文件字节验证覆盖重复发现；宿主 `internal/watcher/dispatcher.go:348-363` 比较含模型快照的元数据，`clients.go:208-226` 按文件字节 hash 去重，此收敛规则确保 watcher 重新注册后停止重复写入。请求身份、CLI UA 与 attribution headers 统一由 `AuthData.RequestHeaders` 生成，站点 appName/模型前缀由 `selectSite` 定义。

## Slice 05 本机验证记录

- `go test ./internal/workbuddy ./internal/plugin` 与 `go test -race ./internal/workbuddy ./internal/plugin` 各 216 项通过；`go run ./cmd/project quality` 十阶段 exit 0，包含 LSP hint 级诊断、lint、CGO 动态库构建和独立 C 探针。日志 `out/logs/slice05-quality.log`。
- `go -C out/verification/cpa-smoke test -timeout=30s -count=1 -run '^TestCPAChatExecution$' -v .` exit 0。真实 CPA Manager 选择人工合成的企业凭证，经 executorAdapter/native DLL 与宿主 HTTP 流，完成国内/国际各一次聚合、流式及下一用户轮次。验证固定站点路径、gzip、stream=true、别名 payload 与已解析模型分离、Unicode 文本、usage、停止原因、会话隔离和稳定轮次；八次实际响应 Body.Close 与请求配对，截断返回失败，取消关闭上下游。另以本地 HTTP 代理观测 `CONNECT www.workbuddy.ai:443`，确认宿主代理策略被使用，代理拒绝返回失败。日志 `out/logs/slice05-cpa-chat.log`。
- 实际 CPA 的 Gin request context 中 `API_REQUEST` 包含上游 URL 且 Authorization 原值被掩码；HTTP transport、宿主日志与 native 回调都运行实际代码，仅协议上游及拒绝连接的本地代理为隔离测试设施。`host.http.do_stream` 的回调上下文来自 `rpc_client_stream.go:16-42`，读/关桥接见 `host_callbacks.go:167-228`；客户端取消与关闭由 `http_stream_bridge.go:42-82` 传递。
- 请求源码迁移：`request.test.mjs:9-27` 的 developer/输出上限、`conversation.test.mjs:51-169` 的轮次/会话/应用头映射到 `chat_request_test.go`。`sent.mjs:39-57` 与 `sent-0.1.43.json` 中的文本、字节输入、历史身份关系以公开执行边界验证；完整样本内的工具安全映射、reasoning 生成与兼容纠正分别由 Slice 08/09/12 验收，不宣称全部样本的字节摘要一致。JSON 整形以语义为契约。
- `canonical_session_id`、`execution_session_id`、`derived_session_id` 来源于 CPA `sdk/cliproxy/executor/types.go:45-57`，routing-only LCP 身份不进入上游会话。`X-Conversation-Request-ID` 在同用户轮次与工具结果延续间稳定，`X-Request-ID` 与 `X-Conversation-Message-ID` 是每次 HTTP 请求的新消息 ID，遵循 `_ref/index.mjs:802-818`。
- 单 SSE 事件上限 1 MiB，非流式聚合上限 32 MiB，请求 payload 上限 32 MiB，客户端超时依据源 SDK 1200 秒；实际宿主请求的更短期限同样生效。公共 Service/runtime 测试覆盖分片 UTF-8、LF/CRLF/CR、多行 data、usage-only、所有 choice、终止缺失、取消/关闭、host 回调失败与一次资源释放。计数及任意 HTTP 请求返回明确不支持；真实账号、完整 CPA server/管理前端、其他平台与容器仍未验证。

## Slice 06 协议映射与宿主刷新

固定上游 `_ref/index.mjs:232-334` 的token合并、过期时间和刷新请求由 `credentials.go`、`refresh.go` 适配。插件返回本站点、用户及企业作用域的轮换结果，CPA官方Manager负责计划、保存和运行时安装。

`TestRefreshReturnsScopedTokensWithoutHostMutation` 验证两站点刷新及返回资格，模型发现失败保留已成功轮换的凭证。公共Service与原生ABI测试覆盖取消、并发及宿主错误边界；当前国际个人账号的实际手动/周期刷新和重启恢复证据见文首真实账号记录。

## Slice 07 认证失效与重新登录恢复

`invalidation.go` 识别上游认证原因，`refresh.go` 与执行器传播安全错误；CPA官方Manager负责错误状态、一次401恢复及凭证安装。插件不把额度、权限、网络或普通401归类为永久撤销，不把手动Disabled作为认证失效标记。

成功登录生成 `credential_generation`，解析和刷新保留该代次；稳定认证身份由站点、用户与企业作用域确定。当前真实证据覆盖国际个人账号的健康登录、刷新和重启，永久撤销、首次401恢复与浏览器重新登录仍按任务保持待验。

## Slice 08 Chat 工具调用映射

| 固定上游来源 | 本项目适配 | 公共边界证据 |
|---|---|---|
| `_ref/index.mjs` fitSchema/fitShape/objectRoot/typeUnions；`tools.test.mjs:13-62,128-211` | `tool_schema.go` normalizeToolSchema；`chat_tools.go` 请求接入 | `tool_schema_test.go` 根对象、组合/引用、enum/type、tuple、布尔 Schema、转义、数据字段保护、精确数字及递归上限 |
| `_ref/index.mjs:1169-1176,1448-1490` fitTools；`tools.test.mjs:73-91` | `chat_tools.go` 请求级双向映射、声明/选择/历史同步 | `TestChatToolsNamesChoicesAndHistoryStayReversible`、`TestChatToolsDefinitionOrderKeepsHistoricalNames`，合法名称占位、碰撞区分与声明顺序稳定 |
| `_ref/index.mjs:1126-1156` toolChoiceOn | `chat_tools.go` none/auto/required/具体函数转换 | `TestChatRequestToolDisableFormsProduceTextRequests`、`TestChatToolsCleanDefinitionsAndExplicitSelection`、`TestChatToolsRejectAmbiguousDefinitionsAndReservedForcedNames` |
| `_ref/index.mjs:1649-1663` namedBack；`tools.test.mjs:94-118` | `chat_response.go` SSE 事件；`chat_tools_response.go` 每 choice/tool index 聚合及原名回写 | `TestChatToolsInterleavedFragmentsRestoreNamesAndAggregate`、`TestChatToolsMalformedOrIncompleteCallsFail`、`TestChatToolsArgumentsRespectAggregateLimit` |

- `go test -timeout=120s ./internal/workbuddy ./internal/plugin` 和 `go test -race -timeout=120s ./internal/workbuddy ./internal/plugin` exit 0，日志 `out/logs/slice08-plugin-tests.log`、`slice08-race.log`。Schema 深度超过 64 明确报错，examples 等数据位置不参与 Schema 修复；数字使用 `json.Number`，不通过浮点转换破坏大整数。单事件仍为 1 MiB，非流式工具参数计入 32 MiB 聚合上限。
- `go run ./cmd/project quality` 十阶段 exit 0，日志 `out/logs/slice08-quality.log`；逐文件 LSP hint 与 lint 均通过。Schema 引用遍历采用 SplitSeq、切片查找及 map 复制使用标准库；工具名字符规则由同一函数维护。
- 设置既有 `WORKBUDDY_SMOKE_PLUGIN_DIR` 与 `WORKBUDDY_SMOKE_TMP` 后，`go -C out/verification/cpa-smoke test -timeout=60s -count=1 -run '^TestCPAChatTools$' -v .` exit 0。真实 CPA Manager、native DLL 和宿主 HTTP 回调完成双站点各两种输出模式的三工具调用、客户端结果回传及下一用户轮次。验证碰撞原名恢复、工具 ID/参数、usage/停止原因、17 字节网络读取、同轮请求 ID 稳定、新轮次变化与跨站点隔离。日志 `out/logs/slice08-native-tools.log`。
- 本片无需扩展宿主契约；真实工具执行由客户端负责，协议模拟上游不代替 CPA/插件逻辑。真实账号工具 smoke、完整 server/管理前端、容器和其他平台仍未验证。
- 契约审查补齐参考允许的缺省 function 类型、强制 `NONE` 名称冲突，以及名称分片和空/null 流元数据。`TestChatToolsAcceptFunctionShapeAndRejectForcedNoneCase`、`TestChatToolsSplitNamesAndEmptyMetadata` 从公开执行边界验证；完成时仍拒绝缺失或重复调用 ID。映射名称在完成事件发布，参数保持流式顺序。

## Slice 09 Chat 图片与 reasoning 映射

| 固定来源 | 本项目适配 | 公共边界证据 |
|---|---|---|
| `_ref/index.mjs:129-152` OFF_TAKEN/textOnly；`declared-images.test.mjs:10-29` | `model_catalog.go` catalogImages/catalogEfforts；`chat_capabilities.go` 当前账号能力查询；`models.go` 模态/Thinking 输出 | `TestChatCapabilityImagesFollowAccountAndSite`、`TestChatCapabilityModelPublicationMatchesExecutableRoutes`，国际 GLM 拒绝、个人/企业快照隔离及 Messages 能力留待原生路由 |
| `_ref/index.mjs:3620-3665` defaultEffortOf/withDefaultEffort；`default-effort.test.mjs:65-105,153-188` | `chat_capabilities.go` applyChatEffort；`chat_request.go` max_tokens 归一化 | `TestChatCapabilityDefaultAndExplicitEffort`，显式值保留、1024/1025 窗口、两个 OFF_TAKEN 例外、onlyReasoning 与禁用限制 |
| `_ref/reasoning.test.mjs:39-66` 历史字段整形；上游响应透传边界 | `chat_request.go` normalizeChatReasoningHistory；`chat_response.go`、`chat_reasoning_response.go` SSE 与聚合 | `TestExecutorResponseAggregatesReasoningWithoutAliasingOrVisibleLeakage`、`TestExecutorResponsePreservesReasoningStreamEventsAndUsageOrder`，结构化元数据/Unicode/工具/usage 与可见文本同时保留 |
| `_ref/images.test.mjs:59-89` 仅 Chat gzip；`efforts.test.mjs:21-35` 级别来源 | 固定 Chat 端点和既有 gzip；账号快照级别 | `TestChatCapabilityRejectsMalformedImagesAndForeignSnapshots`、`TestExecutorResponseSharesReasoningTextAndToolAggregateBudget`，不提供图片生成/编辑或其他 API |

- `go test -timeout=180s ./internal/workbuddy ./internal/plugin` 和 `go test -race -timeout=180s ./internal/workbuddy ./internal/plugin` exit 0，日志 `out/logs/slice09-plugin-tests.log`、`slice09-race.log`。ModelInfo 扩展前通过真实 `gopls references internal/workbuddy/models.go:14:6` 核对消费者，结果位于 `out/logs/slice09-modelinfo-references.log`；实际 CPA 字段以 `sdk/pluginapi/types.go:145-199` 为准。
- 真实账号图片识别/reasoning、完整 server/管理前端、容器与其他平台尚未验证；隔离协议证明不替代真实账号结果。
- `go run ./cmd/project quality` 十阶段 exit 0，日志 `out/logs/slice09-quality.log`。结构化 metadata 判定使用统一身份分类，lint 和 hint 级 LSP 无诊断。
- 设置既有原生 smoke 环境变量后，`go -C out/verification/cpa-smoke test -timeout=90s -count=1 -run '^TestCPA(ChatImageAndReasoning|AccountModelDiscovery|ChatTools|ChatExecution)$' -v .` exit 0，日志 `out/logs/slice09-native-capabilities.log`。真实 CPA Manager/native DLL/宿主 HTTP 验证双站点的流式和非流式图片、reasoning/工具/usage、低预算与显式 effort，以及各站点两账号的不同能力。全局 Info 即使含图片和完整级别，也不能给不支持的已选账号开启对应请求；无快照和国际 GLM 在上游请求前拒绝。
- 原生证明经实际模型发现生成并安装 AuthUpdate 快照；执行期间没有额外配置读取。两个 OFF_TAKEN ID 均接受 none，后续工具历史将 reasoning_content/reasoning_details 整形为参考 reasoning。账号模型发现、既有 Chat 与工具闭环在本片 DLL 上亦通过。模型诊断验收保留身份/状态/原因/快照时间，并直接断言实际凭证令牌不进入日志。
- 独立审查后的公共边界回归 `TestChatImageOnlyTurnsHaveStableScopedIdentity` 验证纯图片重试、工具延续、换图、新用户轮次和跨账号身份；`TestChatEmptyReasoningControlsPreserveUnadvertisedTextRequests` 验证可选空值透传，默认 effort 继续按键存在性与低预算规则处理。流式结构化 reasoning 仅保留身份/元数据映射，聚合顺序列表只在非流式路径分配。

## Slice 10 国内 Claude Messages 映射

| 固定来源 | 本项目适配 | 公开边界验证 |
|---|---|---|
| `_ref/index.mjs:168-183,652-728`；`native.test.mjs:39-110,407-430` | 已解析站点模型选择 `/v1/messages`；`messages_request.go` 保留原生 system/工具/content，处理无签名 thinking | `messages_request_test.go` 原始字段、合法签名、工具轮次/账号归属、图片与 effort；`TestCPANativeMessages` 原生模型发现与调用 |
| `native.test.mjs:365-403,435-499` 11159、包装错误及权限错误 | `chat.go` 单次签名纠正与单次 401 恢复分别计数 | `TestMessagesSignatureAndAuthenticationRecoveryAreJointlyBounded` 两种恢复顺序最多三次，持续拒绝不循环；CPA 原生证明验证两种输出模式 |
| 参考原生 SSE；CPA executor payload/stream 契约 | `event_stream.go` 有界共享解码；`messages_response.go` 原生事件状态、工具 JSON、thinking 签名、usage 与聚合 | `messages_response_test.go` 碎片 UTF-8、多行/CRLF、工具/签名、停止原因、未知可表达字段、坏流、取消与大流 |

- `go test -timeout=180s ./internal/workbuddy ./internal/plugin`、`go test -race -timeout=180s ./internal/workbuddy ./internal/plugin` exit 0；完整 `go run ./cmd/project quality` 十阶段 exit 0，包含 hint 级 LSP、lint、DLL 构建与原生 C 验证。日志 `out/logs/slice10-plugin-tests.log`、`slice10-race.log`、`slice10-quality.log`。
- 计数边界按目标 CPA 实际源码核实：`sdk/api/handlers/claude/code_handlers.go:126-130` 调用 ExecuteCountWithAuthManager；`handlers_execution.go:168-173` 将 Manager 错误直接交回；`internal/pluginhost/adapters_executors.go:966-970` 直接传播插件 CountTokens 错误，没有估算回退。`_ref/native.test.mjs:501-504` 及固定源码内 count_tokens 404 是历史来源，本片没有真实账号复测该远端路径。插件返回 501 `unsupported_operation`，任意 HTTP 转发同样明确不支持。
- 原生格式声明为 `openai`、`claude`，按已选模型要求实际匹配；跨客户端协议转换仍由 Slice 11 验收。国内其他模型和国际所有模型保持本站点 Chat；不根据错误切协议或换站点。真实账号、完整 server/管理前端、容器及其他平台仍未验证。
- 设置既有原生 smoke 环境变量后，`go -C out/verification/cpa-smoke test -timeout=90s -count=1 -run '^TestCPANativeMessages$' -v .` exit 0，日志 `out/logs/slice10-native-messages.log`。真实 CPA Manager/FileTokenStore/native DLL/HTTP callbacks 验证国内原生流式与聚合工具闭环、system/cache 控制、合法签名与无签名历史、max 能力、两种签名拒绝及持续拒绝、权限/404 不切协议、输出后流内错误不重放、一次 401 恢复和永久拒绝阻断。国内非 Claude 与国际 Claude 实际保持 Chat，计数和任意 HTTP 转发 501 且上游调用为 0。仅上游服务为协议设施，真实账号结果未验证。
- 独立架构审查依据 `_ref/index.mjs:4117` 补齐 Messages 共用的 agent/SDK attribution headers；gzip 仅在 Chat 调用处设置，Messages 保持原生 JSON。原生响应中未知 block/message 字段在可表达位置保留；没有规范聚合位置的事件级元数据在流式原样转发、非流式明确返回 `unsupported_upstream_response`，不丢弃后伪造完整聚合。

## Slice 11 客户端格式与SDK翻译边界

插件通过固定官方SDK在 `protocol_translation.go` 完成客户端格式与WorkBuddy模型原生格式之间的转换，本站点路由在 `chat_request.go` 决定。CPA宿主与管理前端保持官方实现。

| 客户端格式 | 国内Claude原生 | 国内其他/国际模型原生 | 转换依据 |
| --- | --- | --- | --- |
| OpenAI Chat `openai` | Messages `claude` | Chat `openai` | 官方SDK请求、聚合及流式转换 |
| Responses `openai-response` | Messages `claude` | Chat `openai` | 官方SDK请求、聚合及流式转换 |
| Claude Messages `claude` | Messages `claude` | Chat `openai` | 原生Messages或官方SDK转换 |
| Gemini `gemini` | Messages `claude` | Chat `openai` | 官方SDK请求、聚合及流式转换 |

聚合与流式输出按客户端HTTP入口消费，保留文本、reasoning、工具参数、usage和停止语义；不伪造上游签名或按文本长度估算reasoning token。官方宿主隔离验收及国际真实账号四协议能力的边界和结果见文首记录。

## Slice 12 兼容纠正、故障与资源边界

- 规则来源：`_ref/index.mjs:842-900` FLAGGED/unflag、`:1016-1109` systemAsUserOn/commaPausedOn、`:1531-1588` withGrowth/inputLengthOf、`:3761-3859` replyAfterToolsOn/hideDsh、`:4406-4504` 错误分类；对应 comma/systemasuser/aftertools/hidedsh/deepseek-flash/growth/native 测试。适配位于 `chat_compatibility.go`、`chat_compatibility_state.go`、`chat_request.go` 与 `chat.go`。内容安全过滤只保留明确停止语义及正文，不采用参考 filtered 测试中删除拒绝文本触发另一个账号的行为。
- `chat_recovery_test.go` 从 Service.Handle 观察三种纠正各一次、与 401 的全部十二种合法顺序、五次组合上限、失败不记忆、站点/账号/模型/system 形状隔离及同轮 ID。Messages 的签名/认证组合边界继续由既有公共回归验证。`chat_faults_test.go` 验证风控、额度、版本、上下文、权限、429/5xx/超时分类、Retry-After 安全值、64 KiB 错误体、超过 1 MiB 的 gzip 请求和 content_filter 停止。
- `service_lifecycle_test.go` 通过阻塞实际宿主协议回调验证同步登录/模型/刷新全部受 Close 等待保护，并覆盖 caller deadline、并发 Close 与准入竞争。Service 等待所有任务后释放刷新及成功策略缓存。既有流式取消、异常 EOF、队列背压、消费 refresh token 串行和 C 缓冲区证明保留。
- 请求/整形结果及聚合为 32 MiB，SSE 事件为 1 MiB，错误体为 64 KiB，原生 RPC/C 消息为 64 MiB；宿主 `HostConfigSummary` 没有对应大小配置字段。原生 `host.http.do` 回调改为限量聚合流式读取，超限取消上游；32 KiB 读取分片最多有 64 KiB 超额预读，聚合不保留超限片段。流式请求体同样在 transport 前检查。下游 stream bridge 队列为 16，达到上限后背压。宿主 HTTP transport 仍使用既有代理与拨号契约。
- ABI 1/schema 6 保持；错误信封新增可选 `upstream_code`/`retry_after`，兼容宿主将有效秒数/HTTP 日期暴露为现有 `RetryAfter()` 和 `Headers()` 接口。只公开分类、HTTP 状态及结构化上游代码，原始 token/state/账号响应不进入错误正文。普通状态码交由宿主重试。
- `go run ./cmd/project quality` 十阶段 exit 0，见 `out/logs/slice12-quality.log`；兼容宿主 `go test -race -timeout=180s ./internal/pluginhost ./sdk/pluginabi` exit 0，见 `slice12-host-race.log`。公共 Host 原生协议测试验证精确大小成功、超限预读上界与关闭、零 transport 的超限请求、流式增量、合法/非法 Retry-After 和元数据所有权。
- 设置既有 DLL/temp 环境后，`go -C out/verification/cpa-smoke test -timeout=90s -count=1 -run '^TestCPA(BoundedRecoveryAndDynamicUnload|NativeClientProtocols|ClientProtocolHTTPHandlers|PermanentAuthAndRelogin|NativeCredentialRotationAndAutoRefresh|ChatExecution|ChatTools|ChatImageAndReasoning|NativeMessages)$' -v .` exit 0，见 `slice12-native-boundaries.log`。真实 DLL/CPA Manager 验证逗号后 system 纠正、成功策略复用、growth、原生 Retry-After，以及在上游仍在输出时直接卸载：上下游终止、body 全部关闭、旧 executor 拒绝新工作且无新增上游请求；累计认证、刷新与四协议证明继续通过。上游仍为隔离协议设施，真实账号、完整 server/管理前端、容器及其他平台验收分列为后续项。
- `go test -race -timeout=240s ./internal/workbuddy ./internal/plugin` exit 0，日志 `slice12-plugin-race.log`；较短的 90 秒总时限曾在既有 32 MiB 工具聚合边界回归执行中到期，同一完整范围以明确 240 秒时限通过。修改的 14 个插件 Go 文件经 `gopls check -severity=hint` 无输出，宿主 stdio `textDocument/diagnostic` 总诊断 0。XERJ 同步 exit 0，`code_files_indexed=88`。
- 累计 CPA 补丁为 `out/verification/cpa-boundary-contract.patch`，SHA256 `2d4bf1290d887281dca212f2c6082744123b690549f2369928f3c6e87dcb9fc7`；对只读 CPA 参考目录执行 `git apply --check` exit 0。原生 HTTP 限制施加于 RPC 回调边界，通用 transport 文件与原始参考一致；测试专用 envelope decoder 移入测试文件，无实际消费者的私有 HTTP/decoder helper 经 LSP references 核验后清理。
- 规格审查补齐 HTTP 400 的结构化 11128 触发：本地化错误文案仍按逗号 → system 顺序纠正。公共回归分别使用数字 `internal_code` 与字符串 `code`，断言三次请求的完整 payload 顺序；原有 429/5xx 与非适用形状不重放回归保留。

## Slice 13 实际管理服务与容器验收

### 固定来源与 Windows 服务

- 只读 CPA 基线为 tag `v7.3.15` / commit `673131f57484517c3a1eae7e36c4cfa7b9bb4efc`，`git show -s` 记录日期为 2026-09-23；完整程序在独立兼容副本以 Go1.27.1 构建，标识 `v7.3.15-compatible`。原参考目录不应用补丁。插件版本 `0.1.0`、ABI 1/schema 6 沿用根 VERSION 与注册/status。
- `out/verification/cpa-live/main.go` 是标准库 headless 验收程序，启动真实 CPA `cmd/server`，经实际 HTTP 管理鉴权、FileTokenStore、watcher、Manager 和原生 DLL 执行；仅远端 WorkBuddy HTTPS 由受控 CONNECT 设施替换。设施只接受固定两 API host，独立 CA/密钥、合成凭证及产物全部在 `out/`，不安装系统证书、不访问用户凭证目录。Go1.27.1 构建的宿主因其模块兼容默认值仍含 `x509sslcertoverrideplatform=0`；Windows 隔离证明显式设置文档支持的 `GODEBUG=x509sslcertoverrideplatform=1` 与专用 `SSL_CERT_FILE`，见 `go doc crypto/x509.SystemCertPool` 与 `go version -m` 输出。这是证明环境配置，不是产品信任绕过。
- `out/logs/slice13-live-windows.json`：完整 Windows amd64 服务验收 exit 0，62 项通过、0 失败。四条记录分别为两张国内与两张国际，真实上游观察命中四个独立稳定 auth ID；全局合并、账号模型资格、两级别名/排除、禁用/启用、删除、手动刷新及代理生效均经公开 HTTP 入口验证。
- 四客户端 Chat/Responses/Messages/Gemini × 国内 Claude Messages、国内 GLM Chat、国际 Claude Chat × 两种输出模式，共 24 项全部通过；另有 12 个客户端工具调用 → 工具结果 → 原生后续请求闭环、账号二请求与国际 GLM 图片明确拒绝。断言文本、reasoning、工具名称/参数/结果归属、站点与原生路由；不以 HTTP 200 代替语义证明。此处合成上游结果不代表真实账号权限和远端模型能力。
- 实际进程先在永久失效及手动禁用状态下重启，比较 token/登录代次/绑定摘要/刷新期限/账号模型快照及管理字段，失效身份无新增上游请求；同身份重登保持原 ID/FileName 与禁用，显式启用后重新发现并请求，再次独立进程重启恢复健康状态。取消关闭实际上游，管理停用/卸载撤销路由。当前 Windows 运行的 7 个生成日志文件及 52,799 字节进程输出经精确 fixture token/state/管理密钥/用户和企业标识检查，无原值泄漏。

### Linux amd64 容器

- 容器运行时为 WSL Ubuntu 上的 Docker Engine 28.3.3，固定本地 Debian 镜像 `sha256:c038a8ef24ab88bc8edacfc93704c9bf301647b9253a59077f6f5342f39727ae`，分发摘要 `debian@sha256:f324c7ff54321e8d9c588493a20244965938ce0aa50bbd1022d38010e9ffc4b1`，实际 `getconf GNU_LIBC_VERSION` 为 glibc 2.41。CPA 与 `.so` 均由项目局部 Go1.27.1/linux-amd64、Ubuntu GCC13.3.0 构建；插件 ELF 最高所需 glibc 符号为 `GLIBC_2.34`，不把此结果外推到 musl 或更旧 glibc。
- `out/logs/slice13-live-container.json` exit 0，62 项通过、0 失败，四实际 auth ID、24 个跨协议场景、12 个工具结果闭环及失效/健康两阶段重建均通过。每次使用新的命名容器，`--pull=never` 固定镜像，持久目录映射 `/proof`、CPA 程序映射 `/opt/cpa/cli-proxy-api`、静态页映射 `/opt/cpa/static/management.html`。容器配置仅使用这些内部 Linux 路径；凭证及模型快照不依赖开发机盘符。无桌面、无可见浏览器。
- 登录 state/token/account、模型、刷新和 Chat/Messages 全部由配置的宿主 CONNECT 代理观察；拒绝其他目标，57 个实际推理请求的模型和账号资格均匹配。7 个日志文件及 46,166 字节容器进程输出经过同样的精确敏感值检查。账号失效和禁用时重建不触及该身份上游，显式启用后的健康状态再次重建仍可请求。
- Linux 验收补齐了由 Windows 文件事件掩盖的宿主缺口：管理修改直接复用文件加载的模型规则投影，清理旧排除/别名属性并合并全局规则；凭证删除同步撤销模型注册；AuthProvider 与原生 executor 两条刷新入口都保留宿主物理凭证路径。公开 HTTP/Manager 回归在没有文件 watcher 的条件下断言这些行为，不能依赖稍后的文件事件修正管理状态。

### 管理前端与人工边界

- 官方 [CPAMC v1.24.2 release API](https://api.github.com/repos/router-for-me/Cli-Proxy-API-Management-Center/releases/tags/v1.24.2) 正文核实于 2026-11-14；下载 `management.html` 为 2,767,070 字节，SHA256 `51b24db8170a5414875602c2f4e35017185b1c1dd7e9a6b1c0de69905a81e997`。实际 CPA 的 `GET /management.html` 返回相同摘要，未打开可见浏览器。预装固定 HTML，`MANAGEMENT_STATIC_PATH` 配合 `disable-auto-update-panel:true`，避免缺文件后取 latest。
- 固定前端使用 `/v0/management` 且支持插件动态 OAuth provider，但 adapter 未传必需的 `site`；国内/国际选择需要在既有 OAuth 页和 API adapter 扩展，详见 [部署说明](deployment.md) 的固定源码链接。当前官方 main 已为 CPA v8 的 `/v8/management`，不能直接替换该固定 v7 页面。页面交互、卡片展示和真实浏览器登录不列为通过。
- 真实账号资源未提供，Chat/Messages/Responses/Gemini 的真实推理、工具、图片/reasoning、WeCom 与远端容器网页登录均未验证；[真实账号人工清单](deployment.md#真实账号人工验收单) 分别列出四账号、权限、协议、模型、流模式、持久化和失败场景及必填证据，不以协议设施代勾。

### 宿主修复与复现入口

- `internal/pluginhost/model_auth_update.go` 与 `sdk/cliproxy/service_executors.go`：网络发现的 AuthUpdate 复用物理凭证 CAS 和原子保存，不继承 watcher 输入的 skip-persist；保存失败不授予模型资格。公开 Host 回归覆盖落盘、并发新登录、文件已删除及取消，实际 server 证明快照、刷新期限和能力重启恢复。
- `internal/pluginhost/auth_provider.go`：凭证替换继承磁盘操作员设置；同代管理保存接受当前记录的显式启用/禁用及字段移除。实际 FileTokenStore 回归复现原手动禁用未落盘，修复后禁用/启用、排除清理及同 token 新登录保持禁用均通过。
- `internal/util/provider.go` 与实际 Gin access log 回归：用户/企业/租户头完整脱敏，OAuth `state` 包括大小写、编码键与数组形式完整脱敏；正文捕获仍需关闭。SDK web-search 测试按目录 SSOT 验证探测仅增加能力，不再把目录中已有能力的模型误断言为不支持。
- 历史累计补丁保留于忽略目录 `out/verification/cpa-management-contract.patch`，共48个宿主文件；其当时 SHA256 为 `b99eaf584200f7d1c23be728a11583fa0970f785b299afa10cc128a562c0bded`。当前官方安装与包不分发该补丁；原始参考目录和独立历史副本均不作为当前修改目标。
- `go run ./cmd/project quality` 全部十阶段 exit 0；宿主受影响的 pluginhost、management、synthesizer 与 SDK 包完整 race 测试 exit 0，隐私 util/logging 包完整测试 exit 0；所有累计改动宿主文件经实际 gopls stdio `textDocument/diagnostic` 最终 0 条。日志 `slice13-quality.log`、`slice13-host-race.log`、`slice13-host-privacy-suites.log`、`slice13-host-diagnostics.log`。此前原生 Manager/DLL 的认证、刷新、模型及客户端协议回归 exit 0，另记 `slice13-native-regressions.log`，不与完整程序的证据混同。



## Slice 14 多平台构建与本地交付

### 平台记录

| 目标 | 本地构建与包契约 | 实际 CPA 宿主加载 | 工具链与动态依赖 | 原生 CI runner |
| --- | --- | --- | --- | --- |
| Windows amd64 | DLL构建与隔离CLI包契约通过 | 官方源码构建61/62项，目录迟留边界已接受；官方发布物受CA限制未跑完全部协议 | Go1.27.1；MinGW-Builds GCC14.2.0，MCF/SEH/UCRT；导入KERNEL32、UCRT API DLL与libmcfgthread-1.dll | windows-2025，远程未运行 |
| Linux amd64 | SO构建、Linux原生测试与隔离CLI包契约通过 | 官方Linux发布包及Debian容器62/62项；实际CPA v8.0.56通过国际账号能力验证 | Go1.27.1；SO仅依赖libc.so.6，最高GLIBC_2.34；当前用户容器glibc2.36 | ubuntu-24.04，远程未运行 |
| Linux arm64 | 未实际构建，已配置原生 CI | 未验证 | CI 固定 Go1.27.1；C 编译器、库依赖与 libc 未产生实际运行记录 | ubuntu-24.04-arm，远程未运行 |
| macOS amd64 | 未实际构建，已配置原生 CI | 未验证 | CI 固定 Go1.27.1；Apple C 工具链、部署版本和 dylib 依赖未产生实际记录 | macos-15-intel，远程未运行 |
| macOS arm64 | 未实际构建，已配置原生 CI | 未验证 | CI 固定 Go1.27.1；Apple C 工具链、部署版本和 dylib 依赖未产生实际记录 | macos-15，远程未运行 |

- 2026-11-14 打开 [GitHub 官方 runner-images Available Images](https://github.com/actions/runner-images#available-images) 核对五个标签及架构，采用明确版本标签；标准 macos-15 为 arm64、macos-15-intel 为 x64。两工作流使用 include 矩阵并比较 go env 的实际目标；配置解析验证五个目标无缺漏，权限均为 contents:read。远程仓库尚未建立，未执行 CI、上传或发布。
- Windows PE 依赖由本机 objdump -p 读取，详见 `out/logs/slice14-windows-dependencies.log`；Linux ELF 由 readelf -d/-V 读取，记录 `slice14-linux-dependencies.log`。动态运行库安装与 Windows 阴影加载要求见部署说明，C 依赖未作为插件文件分发。

### 本地验证与交付契约

- `TestPackageContentsAndChecksums` 通过真实CLI编译、打包生产源码，验证17项白名单、相对文档路径、payload与ZIP摘要、CGO/OS/arch构建信息，以及实际依赖的许可原文。隔离副本中的认证、日志、下载及参考哨兵不进入包；校验数据库缓存状态完整复制，保留正常Go签名校验。
- Linux 在独立 ext4 源码副本通过统一入口 `go run ./cmd/project test` exit 0，完整测试包含实际 SO 的 C ABI 动态加载及包契约。副本只复制当前源码输入，工具链与缓存复用项目 out，生成物在副本 out；日志 `out/logs/slice14-linux-native-ext4-tests.log`。失败路径的同一 hooks 测试在该环境重复十次 exit 0，见 `slice14-linux-native-cleanup.log`。
- Windows 挂载目录的 WSL/DrvFS 验收曾在 hooks 临时空目录清理发生 permission denied，同一场景十次中六次失败，目录可在进程退出后删除。源码与业务断言未改；转到 Linux ext4 的完整场景及十次相同用例均通过。[INFERENCE] 差异来自共享文件系统的目录句柄/删除语义；未验证具体持有者。保留 `slice14-linux-native-tests.log` 和 `slice14-linux-hook-cleanup.log`，不将该共享文件系统运行标成通过，不添加重试或测试豁免。
- package只构建当前原生目标并从VERSION命名，包含库、头、项目契约文档和生成的依赖许可；build-info.txt由实际库读取，checksums.sha256覆盖除自身外的成员，ZIP摘要在写入时计算。应用版本、ABI与JSON schema分别维护。
- 本地交付位于out/dist；CPA与CPAMC使用官方发行物。没有执行push、创建GitHub Release或启用自动发布；真实账号与原生平台缺口按当前任务逐项保留。

- `go run ./cmd/project quality` 十阶段 exit 0（`out/logs/slice14-quality.log`）；`gopls check -severity=hint cmd/project/main.go cmd/project/package_test.go` 实际增量扫描 0 条 warning/info/hint。已修复 Go1.27 废弃的 runtime.GOROOT 查询，测试使用实际 go env GOROOT；错误文本符合 staticcheck。XERJ Post-Edit Sync exit 0、code_files_indexed=89，七维审查执行后保留实际平台及账号验收缺口。

- ZIP 文档导航回归先在真实包中复现 README 指向未收录源码文件的缺链，`slice14-package-navigation-repro.log` 为预期失败；修复后发布说明链接到随包许可文档。消费者测试遍历包内 Markdown 的本地链接并要求目标成员存在，源码仓库路径保留为来源说明，参考快照继续排除。

