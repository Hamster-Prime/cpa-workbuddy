# cpa-workbuddy 兼容性、来源与核验记录

> 证据记录，不是功能完成声明。产品范围见 [requirements.md](requirements.md)，技术边界见 [architecture.md](architecture.md)，验收状态见 [14 片任务](tasks/workbuddy-plugin.md)。所有未运行项目写“未验证”，不以源码推断替代实际宿主结果。

## 当前证据等级与宿主边界

| 项目 | 已知事实或证据 | 状态与限制 |
|---|---|---|
| 产品版本 | 根 `VERSION` 为唯一来源，当前 `0.1.0` | 已通过独立原生探针核对实际注册/status 返回版本与清单 |
| 原生 ABI | `../.cpa-upstream-src/sdk/pluginabi/types.go:7` 为 `ABIVersion = 1` | 本次已读源码；不代表已运行 CPA |
| JSON schema | 同文件 `:20` 为 `SchemaVersion = 6` | 本次已读源码；schema 与 C ABI 不能混称版本 |
| CPA 能力契约 | `sdk/pluginapi/types.go:72-100` 的 capabilities 为可选接口；`:274-279`、`:394-396`、`:594-607` 分别规定认证、模型与执行/宿主网络接口 | 本次已读源码；Slice 01 只声明真实实现能力 |
| 最小注册约束 | `../.cpa-upstream-src/internal/pluginhost/host.go:1025-1060` 要求 Name/Version/Author/GitHubRepository 非空且至少一项能力 | 已核实；Slice 01 声明真实只读 `management_api` |
| 管理状态能力 | `GET /v0/management/plugins/cpa-workbuddy/status` 返回身份/版本/ABI/schema，接入 CPA 管理路由 | 原生 RPC smoke 通过；目标 CPA 的 HTTP 请求及鉴权联调未验证，无独立页面 |
| 作者与仓库 | 用户已批准作者 `sheepyu`、计划仓库 `ygq-future/cpa-workbuddy` | 远程尚未创建；注册所需仓库元数据不表示已创建、提交或发布 |
| 固定 WorkBuddy 基线 | `_ref/UPSTREAM.json:2-7`：commit `0135b7b0ea979c94033a7712a0c6ca1be8dafb70`、包 `0.2.31`、根 MIT 许可证 | 读取来源记录；本次文档迁移不重新下载或改写参考文件 |
| 协议常量 | `_ref/index.mjs:68-114`：国内/国际 endpoint、platform、app `5.9.3`、CLI `2.165.0`；国际 `glm-*` 图片限制 | 本次已读源码；是快照记录，不保证在线版本永久有效 |
| 浏览器协议 | `_ref/index.mjs:1824-1895`：state → token → account、11217/12151、企业身份与 token 字段 | 已迁移并通过隔离协议测试；2026-11-13 在线 state 返回两站点可信登录域，真实账号登录未验证 |
| 模型解析 | `_ref/index.mjs:3667-3733`：CLI、`custom:`、原始 ID、同名展示区分 | 本次已读源码；真实账号模型权限未验证 |
| 全局模型合并 | `internal/registry/model_registry.go:670-675`、`:1183-1204`、`:1347-1364` | 本次已读源码；每凭证支持列表与全局公开 Info 是两层 |
| 永久失效与恢复 | `sdk/cliproxy/auth/conductor_refresh.go:374-414`、`:552-586` | 本次已读源码；仅刷新返回错误不能保证撤销凭证立即停调度 |
| 原生测试宿主加载 | Windows amd64；Go1.27.1；MinGW GCC14.2.0；独立 C 进程加载实际 DLL | 通过；另验证 9 次回调与 9 次配对释放、transport/坏响应/取消边界、同步回调在途时 shutdown 等待和卸载 |
| 实际 CPA 宿主加载 | 只读源码 commit `673131f57484517c3a1eae7e36c4cfa7b9bb4efc`；Windows amd64 隔离 Go 测试进程调用实际 host/management/FileTokenStore，加载真实 DLL | 通过双站点协议模拟登录、管理鉴权、保存/列表和子进程重启恢复；完整 CPA server 可执行程序尚未验收 |
| 管理前端与账号 | 双站点入口、卡片状态、重新登录恢复、真实请求 | 后端管理处理器已验证；前端、真实网页登录、WeCom 与账号请求未验证 |
| 发布平台 | Windows amd64 DLL 构建、原生探针和隔离 CPA 宿主加载通过；Linux amd64/arm64、macOS amd64/arm64 未验证 | 完整服务、容器和其他平台的实际加载仍由后续切片独立验收 |

CPA `v7.3.15` 参考版本和 `v7.2.155` schema 起点属于此前调研记录。本次未重新联网核对，不据此设定正式支持下限；正式版本选择须通过 Web Search 并打开官方来源验证，再附实际加载证据。

## 来源与历史源码定位

### 参考来源

| 来源 | 用途与边界 |
|---|---|
| `../.cpa-upstream-src/` | CPA ABI、宿主回调、认证、模型注册与执行契约；只读参考 |
| `../cpa-secret-manager/` | 现有 CGO 插件桥接样例；具体接口仍以目标 CPA 源码为准 |
| `https://github.com/magpie-community/plugins/tree/0135b7b0ea979c94033a7712a0c6ca1be8dafb70/packages/workbuddy` | 固定 commit 的 WorkBuddy 协议、行为与测试基线 |
| `_ref/` | 完整 WorkBuddy 包源码、33 个测试文件及 5 个 testdata 样本；包版本 `0.2.31`；附上游根许可证，不进入插件发布包 |
| `_ref/UPSTREAM.json` | 来源仓库、commit、文件清单、Git blob SHA-1、SHA-256 与大小；下载后逐文件核验 |
| `out/scratch/workbuddy-go-spike/` | 本地 Go 探索材料，不属于 Magpie 源码，不作为协议依据或已完成实现 |

`_ref/` 的上游文件保持原文并纳入项目版本控制，修改业务行为不能直接改写参考源码来使其“匹配”。当前固定 commit 为 `0135b7b0ea979c94033a7712a0c6ca1be8dafb70`，文件清单及校验记录见 `UPSTREAM.json`。上游更新时显式调整基线并重新检查行为差异，不静默追踪 main。

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

- 目标插件协议为 schema 6；此前版本调研定位到 CPA `v7.2.155` 开始提供该协议，参考源码版本为 `v7.3.15`。正式兼容声明必须附实际加载验收的 CPA 版本，不能仅凭版本号推断全部行为相同。
- 业务实施前核对目标 CPA 的版本、ABI 头文件、Go/CGO 工具链及平台架构。schema 6 是当前最低能力要求；旧协议适配属于单独范围。
- Windows amd64 作为首个加载验证平台；发布构建覆盖 Linux amd64/arm64、macOS amd64/arm64。构建成功与实际宿主加载成功分别记录。
- 容器内运行不需要桌面环境。用户在自己的浏览器中完成登录，容器通过服务端轮询获取结果；验证容器重启后凭证、刷新时间及模型快照可恢复。
- 后端已支持 `GET /v0/management/workbuddy-auth-url` 及登录状态轮询。现有管理前端是否支持动态显示插件登录入口、选择站点，必须在目标版本核验；后端接口存在不等于界面自动出现按钮。
- 若目标管理前端缺少入口，先确认其现有插件认证扩展点。需要修改 CPA/管理前端时，应列明外部仓库改动并确认范围，不能以独立插件页面代替已确定的集成目标。

## 认证失效与重新登录的宿主边界

已核实的宿主行为与接入限制：

- CPA 列表返回 `status`、`status_message`、`disabled`、`unavailable`：`../.cpa-upstream-src/internal/api/handlers/management/auth_files.go:654-665`；对持续认证失败保留错误状态的逻辑位于同文件 `:487-503`、`:544-549`。
- 当前参考源码的刷新失败分支，在 access token 的时间仍有效时会保留原可用状态；无有效 token 时才按错误类别更新错误状态和刷新时间：`../.cpa-upstream-src/sdk/cliproxy/auth/conductor_refresh.go:552-586`。因此仅向宿主返回刷新错误，不足以保证所有永久失效场景都正确展示与停用。
- `AuthData` 提供 Disabled 等字段，但没有直接设置 Status/StatusMessage/Unavailable 的字段：`../.cpa-upstream-src/sdk/pluginapi/types.go:224-246`。`host.auth.save` 是凭证文件保存接口，不能直接视为运行状态更新接口：`../.cpa-upstream-src/internal/pluginhost/auth_callbacks.go:116-132`。
- Slice 01 记录目标宿主已有的认证失败上报、调度阻断及持久化边界，Slice 07 完成实际接入验收。若无法满足需求契约，列为宿主兼容性缺口并明确所需外部改动。

宿主提供的基础与仍需验证的边界：

- 登录保存前按文件名/ID读取旧记录并合并管理元数据：`../.cpa-upstream-src/internal/api/handlers/management/auth_files_fields.go:904-937`；保存入口在同文件 `:941-1002`。凭证文件写入使用原子替换：`../.cpa-upstream-src/internal/pluginhost/auth_provider.go:443-457`。
- 宿主更新检测到凭证变化时会清理 unauthorized 相关状态，但也可能继承已有 ModelStates 并保留配额冷却：`../.cpa-upstream-src/sdk/cliproxy/auth/conductor_lifecycle.go:218-243`；凭证变化比较规则位于 `../.cpa-upstream-src/sdk/cliproxy/auth/conductor_refresh.go:374-414`。
- 因此稳定文件名只解决更新目标，不等于卡片状态和所有调度状态已经恢复；必须以完整重新登录后的列表、模型注册、实际调度及重启恢复验收。

Slice 01 记录当前可确认的宿主边界；认证恢复行为由 Slice 07 完整验收。发现外部宿主缺口必须明确列出，并在业务实施前确认外部改动范围。

## 五项问答的源码对应

五项完整问答在 [需求附录](requirements.md#附录-acpa-凭证与模型机制问答) 维护。以下为按问答顺序记录的源码定位；除上表特别标明本次已读的行段外，后续适配时逐项复核。

- 保存登录记录：`../.cpa-upstream-src/internal/api/handlers/management/auth_files_provider_oauth.go:1025`。
- 构造凭证列表项：`../.cpa-upstream-src/internal/api/handlers/management/auth_files.go:637`，字段从 `:655` 起。
- 模型获取入口：`../.cpa-upstream-src/sdk/cliproxy/service_models.go:67`、`service_executors.go:498`。
- 以 auth.ID 注册：`../.cpa-upstream-src/sdk/cliproxy/service_executors.go:428`、`:454`。
- 支持性检查：`../.cpa-upstream-src/sdk/cliproxy/auth/conductor_selection.go:985`，通过 `ClientSupportsModel(auth.ID, routeModel)` 判断。
- AuthData 转换优先使用 FileName，再使用 ID：`../.cpa-upstream-src/internal/pluginhost/auth_provider.go:565`。
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
| 06 刷新与一次恢复 | `stale:245`、`refreshable:256`、`renew:262`、`merged:287`、`fresh:298`、`refreshed:320`、`reissued:2115` | `refresh.test.mjs:43`、`:63`、`:86`、`:96`、`:106`；`unauthorized.test.mjs:80`、`:121`、`:154`、`:165` | `refresh.go` 与宿主兼容补丁；账号代次协调、原子保存、手动/自动刷新与一次恢复通过，真实账号未验证 |
| 07 失效/重新登录 | `refusedText:2161`、`signedOut:2187`、`reissued:2115`；Magpie 标记语义须适配 CPA，不能直接照搬 | `unauthorized.test.mjs:97`、`:113`、`:131`；重新登录/重启是新增 CPA 验收 | `invalidation.go`、credential_state/generation 与宿主兼容补丁；实际 Manager/FileTokenStore/DLL 验证状态恢复、调度阻断、CAS 与原身份重登，真实账号未验证 |
| 08 工具调用 | `toolChoiceOn:1126`、`safeName:1173`、`fitSchema:1190`、`fitShape:1291`、`objectRoot:1362`、`typeUnions:1416`、`fitTools:1448`、`namedBack:1649`、`renamed:1666` | `tools.test.mjs:13`、`:28`、`:41`、`:73`、`:94`、`:181` | `chat_tools.go`、`tool_schema.go` 与 SSE 工具聚合；公共边界及真实 CPA 双站点流式/非流式工具循环通过，真实账号未验证 |
| 09 图片/reasoning | `textOnly:150`、`reasoningOf:917`、`asReasoning:926`、`withDefaultEffort:3652`、`namesEffort:3663` | `images.test.mjs`、`reasoning.test.mjs`、`default-effort.test.mjs`、`efforts.test.mjs` | `chat_capabilities.go`、`chat_reasoning_response.go` 与 ModelInfo 声明；公共边界及真实 CPA 账号能力隔离、图片/effort/reasoning 通过，真实账号未验证 |
| 10 国内 Messages | `nativeOf:182`、`isMessages:652`、`withoutUnsigned:703`、`unsignedOn:709`、`withoutSignedThinking:728`、`effortsOf:3587`、`makePlugin:3885` | `native.test.mjs:39`、`:46`、`:110`、`:210`、`:283`、`:320`、`:334` | 国内 Claude 路由、Messages 原生执行与签名恢复；待实现/未验证 |
| 11 CPA 协议转换 | `nativeOf:182`、`isMessages:652`、`modelOf:3483`；参考包不是 CPA 翻译器 | `native.test.mjs`；新增 Chat/Responses/Claude/Gemini 双向场景 | executor formats 与宿主 translator 分工先核对再实现；待实现/未验证 |
| 12 兼容纠正与故障 | `unflag:872`、`systemAsUserOn:1016`、`commaPausedOn:1080`、`withGrowth:1531`、`replyAfterToolsOn:3761`、`hideDsh:3823`、`explained:4459` | `unflag.test.mjs`、`comma.test.mjs`、`systemasuser.test.mjs`、`aftertools.test.mjs`、`hidedsh.test.mjs`、`deepseek-flash.test.mjs`、`growth.test.mjs`、`filtered.test.mjs`、`native.test.mjs` | 有界重试/错误映射/取消/shutdown；待实现/未验证 |
| 13 管理与容器 | CPA 管理认证保存、列表、调度、持久化代码；没有可替代实际宿主验收的 JS 函数 | 需求 R4 验收矩阵，真实账号人工清单 | 实际 CPA/管理前端/容器版本与结果待实现后记录 |
| 14 构建交付 | 以目标 CPA C ABI、平台 CGO 与动态链接依赖为准 | 各平台构建和实际加载分别留证 | `go run ./cmd/project build/package`；多平台未验证 |

## 项目策略与上游行为差异

- 站点前缀、稳定 CPA 文件名、账号成功快照和首次失败无路由资格是本项目约定，不能描述成 Magpie 现有策略。参考包保留桌面/API key/用量/签到等功能不等于迁移它们。
- 参考 `_ref/index.mjs:20-22` 区分执行前 1 分钟 stale、宿主预刷提前 5 分钟和刷新结果缓存 60 秒；需求的提前 5 分钟计划须映射 CPA 调度，不能把参考两个窗口误写为相同逻辑。
- 参考浏览器 URL 只检查 HTTPS；本项目同时校验登录目标域。2026-11-13 对固定双站点 state 接口的实际 POST 均返回 code 0，登录目标分别为 `https://www.workbuddy.cn/login` 与 `https://www.workbuddy.ai/login`，只记录 scheme/host/path。国内还接受固定 `enterprise.test.mjs:55` 已证明的 `www.codebuddy.cn` 历史登录页；该登录域许可不改变 API 目标或引入 CodeBuddy 服务。
- 参考 JS 可直接 fetch，CPA 插件必须通过宿主 transport policy。非流式聚合、CPA 多客户端协议、认证失效卡片及调度恢复均需宿主适配和独立验证。
- 参考中图片支持、Claude Messages、count_tokens 404 等实测注释属于固定快照的历史记录；不能标为本仓库真实账号验证。

## 风险与待核验项

| 风险/待核验事实 | 处理与验收要求 |
|---|---|
| 目标 CPA 版本与管理前端扩展点未现场核验 | 宿主边界核验 先验证 ABI、双站点登录入口及格式转换能力；外部仓库变更单独说明 |
| 模型首次发现失败时宿主如何恢复注册 | 明确宿主重试/刷新触发，不依赖“请求自然会恢复”的假设 |
| 同站点不同账号同模型的全局元数据可能覆盖 | 路由以每凭证模型列表为准；测试账号间能力差异，不能把全局元数据等同每账号权限 |
| refresh token 轮换与持久化失败 | 单飞、短时结果缓存、明确保存失败、重启风险报告；不重复消费旧 token |
| 终止认证失败不能直接通过 AuthData 设置完整运行状态 | 宿主边界核验 核实宿主状态上报及持久化机制；覆盖 token 尚未到期即撤销，不把返回刷新错误等同完整失效处理 |
| 重新登录覆盖文件但继承旧错误/冷却或被在途旧结果覆盖 | 同身份稳定记录、凭证代次隔离、管理设置保留及状态恢复联合验收 |
| WorkBuddy UA/协议版本变化 | 常量集中、10085 可诊断、协议样本可更新；不宣称静态版本永久有效 |
| Messages 与 Chat 的语义转换损失 | 宿主边界核验 明确翻译归属，Slice 10–11 覆盖工具、reasoning、签名和 usage |
| 宿主与插件重试相叠加 | 明确责任和总上限，流开始后不重放，保留上游错误及 Retry-After |
| 容器原生库及 CGO 依赖 | 分平台/架构构建，记录 libc 与运行依赖，实际加载验证独立列出 |
| 真实账号与平台资源不齐 | 自动化完成可达验证，其余保留具体人工清单，不标为通过 |

后续可单独规划 WorkBuddy 用量/套餐/积分查询、国内签到及免费模型策略。它们不是首版的依赖，也不预建空接口或管理页面。

## 本地验证与后续验收

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

## Slice 06 前置宿主契约核验

只读 overlay 命令 `go -C ../.cpa-upstream-src test -overlay=<out/verification/cpa-host-probe/overlay.json 的绝对路径> -timeout=30s -count=1 -run '^TestWorkBuddy(SaveContractProbe|RefreshScheduleProbe)$' -v ./internal/pluginhost ./sdk/cliproxy/auth` exit 0。探针使用真实插件解析人工凭证、实际 FileTokenStore/Manager 与宿主回调，不修改参考源码；输出见 `out/logs/slice06-host-contract-probe.log`。这些是兼容缺口测量，尚非 Slice 06 通过证据。

- `host.auth.save` 的 `auth_callbacks.go:295-304` 调用 `buildAuthFromFileData`，`:332-345` 使用路径派生 ID，`:515-535` 返回相对文件名。探针中一条已禁用的插件凭证保存后，Manager 出现两条记录，其中一条启用；保存路径没有调用插件 parser，未保留插件 ID 与 Disabled。刷新要求同一记录并保留宿主管理状态，需要宿主保存路径按插件认证契约解析、安装及持久化。
- 当前 `workbuddy` 的 `ProviderRefreshLead` 为 nil。`conductor_refresh.go:122-124` 只把 NextRefreshAfter 当作最早允许时间，`:154-156` 在未知 provider 无 lead 时停止；探针中已到的 NextRefreshAfter 仍得到 `shouldRefresh=false`。成功刷新路径 `:608-609` 还会清空返回的 NextRefreshAfter。提前五分钟刷新需要宿主按插件返回的时间计划调度。
- 用户已授权在 `out/verification/cpa-compatible` 独立 CPA 副本修复两项契约；原参考目录保持只读。补丁 `out/verification/cpa-refresh-contract.patch` 包含原子保存、插件 parser/stable ID、宿主管理状态合并、插件刷新计划 evaluator、实际调度器到期处理及成功计划保留；副本及补丁均未提交 CPA 仓库。

## Slice 06 协议映射与本机证据

| 固定上游 | CPA 插件适配 | 行为证据 |
|---|---|---|
| `_ref/index.mjs:232-246` merge/stale | `credentials.go` 秒/毫秒时间；`refresh.go` mergeCredentialTokens/freshAuth | 两站点头、相对过期时间、五分钟计划及一分钟请求窗口 |
| `_ref/index.mjs:249-334` renew/refreshed | `refresh.go` renew/auth.refresh；`runtime.go` 分派 | `TestRefreshManualRequestAndDiscoveryShareConsumption`、`TestRefreshResultWindowAndIdentityIsolation`，成功缓存 60 秒、站点/企业隔离 |
| `_ref/refresh.test.mjs:43-123` | `host.auth.save` 与内存旋转结果 | `TestRefreshSaveFailureKeepsMemoryAndCanPersistWithoutConsumption`，保存失败、安全错误、恢复保存与重启读取新代次 |
| `_ref/unauthorized.test.mjs:80-193` | `chat.go` 首次 401 恢复，`models.go` 同代请求 | `TestChatUnauthorizedRecoveryIsBoundedAndKeepsRequest`、`TestRefreshAdoptsHostGenerationBeforeAndAfterNetwork`，外部更新优先及晚到成功/拒绝 |

- `go test -timeout=90s ./internal/workbuddy ./internal/plugin`、`go test -race -timeout=120s ./internal/workbuddy ./internal/plugin` exit 0；`go run ./cmd/project quality` exit 0。日志 `out/logs/slice06-plugin-tests.log`、`slice06-race.log`、`slice06-quality.log`。
- `go -C out/verification/cpa-compatible test -timeout=180s ./internal/pluginhost ./sdk/cliproxy/auth` exit 0，覆盖既有宿主测试与新增保存、计划、禁用、失败退避和内置 provider 回归。日志 `out/logs/slice06-host-tests.log`。
- `go -C out/verification/cpa-smoke test -timeout=30s -count=1 -run '^TestCPANativeCredentialRotationAndAutoRefresh$' -v .` exit 0。实际 CPA Manager、FileTokenStore、native DLL、宿主 HTTP 回调完成禁用身份手动刷新、首次 401 两次请求及真实自动调度；三条稳定记录，无重复身份，新的五分钟计划保留。日志 `out/logs/slice06-native-refresh.log`。`TestCPAChatExecution` 在兼容宿主仍通过。
- 原只读 CPA 版本仍有上述缺口；兼容结论只适用于独立补丁副本。真实账号、完整 CPA server/管理前端、容器及其他平台未验证。宿主文件保存与 Manager 安装是两个操作，后者失败会报告错误；已轮换的内存结果用于恢复，不保证失败后重启仍能取得未保存 token。
- 插件增量 LSP：`gopls check -severity=hint internal/plugin/runtime.go internal/workbuddy/service.go internal/workbuddy/credentials.go internal/workbuddy/chat.go internal/workbuddy/models.go internal/workbuddy/models_test.go internal/workbuddy/refresh.go internal/workbuddy/refresh_test.go` exit 0，无诊断。CPA 全模块 CLI LSP 首次因额外依赖加载超时；改用实际 gopls stdio LSP，两处包工作区设置 `expandWorkspaceToModule=false`，逐文件 `textDocument/diagnostic` 覆盖全部八个宿主改动文件，10 项 warning/info/hint 均已修复，最终 0 项；日志 `out/logs/slice06-host-scoped-diagnostics.log`。宿主包测试与真实 DLL 闭环随后重新通过。
- `git -C ../.cpa-upstream-src apply --check <out/verification/cpa-refresh-contract.patch 的绝对路径>` exit 0，仅检查适用性，不改参考目录。原生 smoke 需设置 `WORKBUDDY_SMOKE_PLUGIN_DIR=<out/bin 绝对路径>` 与 `WORKBUDDY_SMOKE_TMP=<out/tmp 绝对路径>`；验证不依赖会话临时环境继承。
- 正确性审查修复：非 2xx 上游关闭失败单独记录诊断，保留原 HTTP 状态及首次 401 的一次恢复；缺少有效过期时间的刷新响应返回 `invalid_refresh_response`，不安装已经到期的预刷计划。`TestChatHTTPStatusAndRecoverySurviveCleanupFailure`、`TestRefreshMissingExpiryIsRejectedBeforeInstallation` 通过；新增绝对毫秒时间、请求预刷窗口及取消等待者回归亦通过。

## Slice 07 永久认证失效与重新登录恢复

宿主基础契约仍为 ABI 1 / schema 6；独立 CPA 补丁扩展现有保存请求的可选 `ExpectedCredentialJSON` 字段，并在 `host.auth.list` 回调声明 `credential_state_version=1`。新状态属于带版本的 credential metadata 契约，不把手动 Disabled 或过期时间用作失效标记。原只读参考宿主未声明该支持，插件在消费 refresh token 或保存状态前返回 `unsupported_host_contract`。

| 固定上游来源 | 本项目适配 | 验证 |
|---|---|---|
| `_ref/index.mjs:2086-2093` invalidToken/refreshRefused，`unauthorized.test.mjs:38-44` 实测响应 | `invalidation.go` authenticationReason/recoverAuthentication；`service.go` 保留规范化拒绝原因 | 普通 401、权限、额度、网络及超时不永久失效；结构化 invalid_grant/revoked、HTTP401+12153 和恢复后的 invalid_token 进入终态 |
| `_ref/index.mjs:2115-2148` reissued，`unauthorized.test.mjs:154-173` 外部更新优先 | `refresh.go` 代次协调，`invalidation.go` invalidate/persistRotation | 旧成功/拒绝在网络边界和保存边界遇到新登录时采用最新代次；CAS 同时保护 token 与状态 |
| `_ref/index.mjs:2187-2196` signedOut，`signin.test.mjs:23-43` | `credentials.go` 版本化状态和健康 null；CPA `credential_state.go` 投影、恢复 | 当前代次摘要绑定、失效后保留原文件/列表、登录恢复及非认证限制保留 |

- `go test -timeout=90s ./internal/workbuddy ./internal/plugin`、`go test -race -timeout=120s ./internal/workbuddy ./internal/plugin` exit 0。公共 Service 测试覆盖明确/普通拒绝、请求恢复、终态保存失败、重启、非法/旧代次标记、外部登录、在途成功/失败、保存 CAS、取消/过期登录、健康登录 null、手动禁用、宿主契约协商。见 `out/logs/slice07-plugin-tests.log`、`slice07-race.log`。
- `go -C out/verification/cpa-compatible test -timeout=180s ./internal/pluginhost ./sdk/cliproxy/auth ./sdk/pluginapi` exit 0。宿主测试覆盖文件 CAS、状态 CAS、并发写入、操作员元数据、注册/加载、调度、手动/定时刷新、迟到结果、冷却恢复、配额重置和重新登录。见 `out/logs/slice07-host-tests.log`。
- 设置 `WORKBUDDY_SMOKE_PLUGIN_DIR=<out/bin 绝对路径>` 和 `WORKBUDDY_SMOKE_TMP=<out/tmp 绝对路径>` 后，`go -C out/verification/cpa-smoke test -timeout=60s -count=1 -run '^TestCPAPermanentAuthAndRelogin$' -v .` exit 0。实际管理登录、FileTokenStore、CPA Manager 和 native DLL 验证国内/国际各个人、企业一、企业二共六条身份；分别通过 Chat、账号模型发现、手动和自动刷新进入永久失效。原文件和列表保留，未伪造 Disabled 或过期时间，模型快照不授予失效资格。见 `out/logs/slice07-native-state.log`。
- 同一 smoke 创建独立子进程重新加载六条失效凭证，验证全部阻断且上游调用为 0；保存失败和账号确认失败的重登保持旧文件及失效。昵称变化后真实重新登录回到原六个 ID/FileName，重新发现模型并调度，手动禁用身份保持禁用，非认证模型冷却保留。恢复后再次启动独立子进程，六条身份中五条启用身份请求成功，禁用身份未请求上游。此前 `TestCPAChatExecution` 和 `TestCPANativeCredentialRotationAndAutoRefresh` 在累计补丁上亦通过。
- `go run ./cmd/project quality` exit 0。插件逐文件 `gopls check -severity=hint` 与宿主包工作区实际 LSP `textDocument/diagnostic` 覆盖改动文件；宿主 18 项 warning/info/hint 已修复，16 个改动文件最终 0 项。去掉无效果的 omitempty struct 标签保留既有 JSON 输出。日志 `out/logs/slice07-quality.log`、`slice07-plugin-diagnostics.log`、`slice07-host-diagnostics.log`。
- 累计补丁仍为 `out/verification/cpa-refresh-contract.patch`，独立副本及补丁没有提交 CPA 仓库，原参考目录未修改。真实账号、完整 CPA server/管理前端、容器和其他平台尚未验证；独立进程证明仅覆盖本机 Windows amd64 的宿主加载与文件恢复。
- 最终契约审查补齐同 token 重新登录与失效保存确认丢失的边界：成功登录生成独立 `credential_generation`，刷新/解析保持，旋转缓存按 refresh token 和代次共同索引，宿主 CAS 和 CredentialsChanged 同时识别代次。已有终态的幂等保存返回 `reauth_required`。`TestSameTokenLoginClearsCachedRejection`、`TestLostTerminalSaveAcknowledgmentAdoptsPersistedState`、`TestCredentialGenerationRecognizesSameTokenLoginAndLegacyRecords`、`TestHostCredentialCASRejectsSameTokenPreviousLoginGeneration` 均通过；代次为空的旧凭证与其他 provider 行为保持。
- 最终收敛同时验证“新登录代次已失效”的采用路径：`TestAdoptedRejectedGenerationRemainsBlocked` 证明不发送任何该代次上游请求。插件竞态、宿主三包、完整 quality 与宿主 16 文件 LSP 随后重新通过；正确性与架构审查确认无剩余阻断项。

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
