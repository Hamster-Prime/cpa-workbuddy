# Overview & Context

为 CPA 实现 `cpa-workbuddy` 原生动态库插件，一个 provider `workbuddy` 承载国内和国际多个账号及个人/企业身份。唯一需求正文是 [requirements.md](../requirements.md)，架构见 [architecture.md](../architecture.md)，固定上游源码、测试映射与实际宿主边界见 [compatibility.md](../compatibility.md)。工程初始化交付框架与质量门禁，原生接入、登录与聊天由对应切片交付。

每片依据可复现验证证据逐项勾选 AC，全部满足后将状态更新为 `completed`；其余保持 `pending`。依赖按编号表示。缺少实际宿主、账号或平台证据的验收明确记录为未验证。

统一入口：`go run ./cmd/project quality/build/test/package`（斜线表示四个独立子命令）。库由 CPA 加载，没有独立 start/stop。全部产物、缓存、临时文件和日志写入 `out/`。`_ref/` 原始文件与 `UPSTREAM.json` 不修改，原探索材料保留于 `out/scratch/`，不作为实现或协议证据。

所有切片的完成契约：在公共边界验证消费者可见行为；记录固定上游函数/行号、对应测试、CPA 适配位置与本地结果；并行修改结束后统一执行适用构建、测试、静态/LSP、真实 smoke、XERJ 索引同步和 `post-impl`，涉及公开契约或生命周期时先完成 code-review，再更新状态。默认保留未提交工作树；commit、push、发布须用户明确授权。验证不启动 GUI，真实浏览器登录由用户完成。测试针对公开行为，已声明能力必须有完整实现。

## Slice 01: 原生插件加载与只读状态接口

Status: completed

Blocked by: None

What to build:

依托 Go/CGO 工程与 Go 标准库工程入口，使用 `cmd/cpa-workbuddy/`、`internal/plugin/` 和根 `version.go` + `VERSION` 实现真实原生身份、注册、只读管理状态、错误边界、释放和卸载。核对 CPA ABI/schema 与宿主可达边界，形成后续业务可依赖的最小真实插件基础。

Acceptance criteria:

- [x] 以目标 CPA 源码核对原生 ABI 1 / JSON schema 6、导出符号、C 结构布局和调用所有权，兼容记录列明源码位置、当前平台/架构及工具链，不以参考插件替代目标契约。
- [x] `VERSION` 为产品版本 `0.1.0` 的唯一来源，根 `version.go` 和注册身份读取同一版本；代码入口位于 `cmd/cpa-workbuddy/`，wire 与分派位于 `internal/plugin/`。
- [x] 真实测试宿主动态加载实际构建库、查询 ABI/身份、调用注册并校验插件 ID/版本/schema；释放每次返回的成功及错误缓冲区，完成 shutdown/卸载，加载闭环可重复且无占用遗留。
- [x] 原生 smoke 覆盖不兼容 ABI/结构、无效或缺失参数、坏 JSON、错误 schema、未知方法等实际契约错误边界；返回可解析错误或 ABI 约定错误结果，进程不崩溃且缓冲区仍可释放。
- [x] 以宿主要求的 Name/Version/Author/GitHubRepository 非空身份注册，作者 `sheepyu`、计划仓库 `ygq-future/cpa-workbuddy`（尚未创建远程）；声明真实只读 `management_api`，`GET /v0/management/plugins/cpa-workbuddy/status` 返回身份/版本/ABI/schema，复用 CPA 管理鉴权且无独立页面；不声明未实现 AuthProvider、ModelProvider、Executor、协议格式、模型或其他占位能力，原生 smoke 验证状态路由及方法边界。
- [x] `go run ./cmd/project quality`、`build`、`test`、`package` 是统一入口且帮助准确，库无 start/stop；构建物、缓存、临时文件、日志及探索材料统一位于 `out/`，探索材料原样保留。
- [x] 统一完成真实动态加载 smoke、适用 Go 行为测试、静态检查、LSP 与 CGO 构建，记录命令/退出结果和 `out/` 证据；工具缺失或检查失败明确处理，不静默跳过。
- [x] `compatibility.md` 分开记录实际 CPA 版本/二进制/架构/加载证据与原生测试宿主结果；未运行实际 CPA、前端扩展、状态通道或账号场景明确标未验证及后续验收位置。
- [x] `requirements.md` 为唯一需求正文，`architecture.md` 记录技术边界，`compatibility.md` 记录事实/来源/风险/待核验及上游函数+测试映射；五项问答完整保留，含全局模型按 ID 合并与每凭证路由资格差异；README 中文准确标示初始化与业务状态，文档链接有效。
- [x] 14 片任务的标题、精确依赖、构建内容和可观察 AC 完整，未做业务切片保持 pending；不改 `_ref/` 与 `UPSTREAM.json`，不预建空业务模块。
- [x] 最终 XERJ 索引同步覆盖本片变更，完成适用审查与 `post-impl` 并记录结论，所有可核验 AC 有证据后勾选和更新状态。

## Slice 02: 双站点浏览器登录与凭证恢复

Status: completed

Blocked by: 01

What to build:

在 CPA 认证接口中接入明确 `site` 的两站点浏览器 state/token/account 流程与带版本凭证解析、宿主保存及重启恢复。最初可完成登录闭环，个人/企业稳定身份细化由 03 承接。

Acceptance criteria:

- [x] CPA `workbuddy-auth-url` 的 metadata 明确选择 `workbuddy` / `workbuddy-ai`；不猜站点、不接收 API key、不读取桌面凭证，目标站点只能来自常量。
- [x] 协议上游完成 state → token → account → `AuthData` → CPA 保存链路；URL 使用 HTTPS、经核实的登录目标域、正确 version 与随机 `loginSessionId`，不将 state/token 写入日志。
- [x] `11217`、`12151` 表现为等待；取消、终止错误、参考五分钟窗口/实际宿主期限及超时清理可观察，不将任意失败当作继续等待。
- [x] 并发双站点登录各自完成；未知 state、过期、重复轮询和插件重启返回明确状态，不串用登录结果，不在重启后伪造登录成功。
- [x] 仅解析自身 provider、类型及版本可识别的浏览器凭证；其他 provider 的 `access_token` 文件不被接管；必要字段、过期时间与站点恢复后可由宿主识别。
- [x] 宿主负责保存路径与原子持久化；保存成功后凭证列表可查询，宿主/容器重启能恢复记录；不直接写用户 auth 目录或持久化本机绝对路径依赖。
- [x] 参考 `browserSignIn/poll/success/merge` 与企业登录/WeCom 样本有迁移对应及隔离协议测试；真实网页步骤交用户，未登录账号如实标未验证。

## Slice 03: 个人与企业身份管理

Status: completed

Blocked by: 02

What to build:

确认登录所选个人/企业身份，稳定生成跨重启的身份 ID、文件名和展示标签，保留宿主管理元数据并隔离企业请求头。

Acceptance criteria:

- [x] `/login/account` 与必要 `/accounts` 合并只确认登录页实际所选身份；个人账号不误选列表中的企业，缺企业名/ID时按参考可证明规则确定或明确失败。
- [x] `site + uid + enterprise/tenant scope` 决定稳定身份和摘要文件名；不同站点、用户、个人/企业、不同企业各自保存，昵称变化不会新增同身份记录。
- [x] `AuthData.ID`、`FileName` 与刷新保存指向同一逻辑记录，文件名不含 token 或直接暴露个人信息，Label 能区分站点与企业。
- [x] 同身份正常重新登录更新原文件与列表记录，用户无需先删除；管理元数据中的手动禁用、优先级、代理、模型排除设置保持原值。
- [x] 企业请求携带与当前凭证作用域绑定的 `X-Enterprise-Id`/`X-Tenant-Id`，个人请求无企业头，客户端或旧凭证数据不能切换目标企业。
- [x] 带版本持久化覆盖 provider/site/uid/作用域/展示名/token/过期时间及后续快照；敏感 token、state、完整账号响应不进入日志、样本、发布物，同时记录宿主敏感头处理核验。
- [x] 迁移 `enterpriseOf/enterpriseHeaders/accountName/scopeOf`，个人/企业/同 uid 跨站点/重复登录及保存失败场景可由列表与文件数观测。

## Slice 04: 账号模型列表与站点隔离

Status: completed

Blocked by: 02

What to build:

读取各账号 `/v3/config`，解析 CLI 与企业 custom 对话模型、可表达能力和价格来源；保存成功快照并按站点注册、路由和恢复。

Acceptance criteria:

- [x] 各站点使用当前浏览器凭证读取配置，CLI 模型后补 `custom:` 企业模型，保留 `:`/`|` 等完整 ID；按 ID 去重，同展示名不同 ID 仍保留。
- [x] 解析上下文、最大输出、图片限制、reasoning levels/default effort，国内 Messages 的 max 与路由能力一致；仅声明 CPA ModelInfo 可表达字段，不将积分倍率伪装货币价格。
- [x] 仅成功且结构有效的响应更新该账号作用域与时间戳快照；有效空列表、缺 CLI、格式损坏、临时网络失败和明确认证失效有不同结果及诊断。
- [x] 临时失败只恢复同账号最近成功快照并报告时间/原因；首次失败无快照不注册可路由模型，不从静态目录、其他账号/企业/站点补权限。
- [x] `model.for_auth` 只返回本站点成功列表的 `wb/*` 或 `wb-ai/*`，`AuthData.Prefix` 为空；静态元数据不授予无发现成功账号路由资格，实际宿主影响单独验证。
- [x] 站点与账号作用域覆盖模型快照和缓存；重新登录、身份变化、删除、插件/宿主重启时更新、隔离、清理、恢复可观察，首次失败后模型发现的宿主触发机制有证据。
- [x] 两个国内与两个国际凭证的 CPA 集成中，排除模型和别名源使用带前缀 ID，模型只派发支持它的同站点凭证；无前缀、未知前缀、跨站点模型不会进入错误账号。
- [x] 全局模型表按 ID 合并的测试验证同站点能力差异不扩大单账号权限，国内/国际同名上游 ID 在全局仍是两个 ID；迁移源码与 custom/efforts/images/price 样本对应齐全。

## Slice 05: 双站点 Chat 文本闭环

Status: completed

Blocked by: 04

What to build:

实现两站点 Chat 文本的宿主 HTTP/流桥接、真实请求头、gzip、会话与轮次、SSE、非流式聚合、模型回写及多轮执行。

Acceptance criteria:

- [x] 两站点 Chat 模型通过本站点 `/v2/chat/completions` 完成文本流式、非流式与多轮；上游 `stream:true`、JSON gzip，经宿主 transport policy，代理/取消/日志链路可见。
- [x] Authorization、用户、domain、企业/租户与 WorkBuddy UA/IDE/Agent/Requested-With/Stainless 头按参考来源集中维护；10085 可诊断，不读取任意 endpoint。
- [x] 执行使用 CPA 已解析模型，检查站点并只剥离一层前缀；合法别名不因原始 payload 字面不同被拒绝，响应 model 写回站点 ID。
- [x] 默认 system、developer、max_completion_tokens 和文本历史整形遵循参考，国内 Claude 不被冒充 Chat 已支持，工具/图片/reasoning 尚未完成时不虚报能力。
- [x] 会话/轮次 ID 包含站点账号作用域；同轮工具循环与有界重试沿用 request ID，下一用户轮次变化；优先宿主 session，消息派生按参考行为。
- [x] SSE 不依赖网络 chunk，拆分 UTF-8、多行事件、空 delta、usage、结束事件、异常 EOF 均保持语义与顺序；非流式聚合 content/finish_reason/usage，上游中断不返回完整伪成功。
- [x] 请求取消/超时关闭上游读取与回调资源；缓冲区与上下游 stream ID 所有权明确，测试观察到流关闭及资源释放。
- [x] 记录 request/sent/conversation 样本与适配位置，使用真实原生边界及协议上游完成闭环，真实账号 smoke 缺证据时单列未验证。

## Slice 06: 刷新轮换与请求恢复

Status: completed

Blocked by: 02

What to build:

实现定时刷新与执行恢复共用的凭证代次协调、轮换持久化、短时结果缓存与一次认证恢复。

Acceptance criteria:

- [x] refresh 请求携带正确 `X-Refresh-Token`、`X-Auth-Refresh-Source: plugin`、本站点 domain 与接口所需企业头，新 token/过期时间正确映射宿主字段。
- [x] 宿主提前五分钟刷新计划、上游秒/毫秒单位和 `NextRefreshAfter` 可观察；参考请求前 stale 窗口与宿主预刷窗口明确区分。
- [x] 定时、手动与请求刷新共用入口，同账号同代 refresh token 并发只消费一次，成功结果保留 60 秒；不同站点/身份互不复用。
- [x] 401 先读取已更新凭证，必要时刷新并重发一次；新 token 再被拒绝即终止，与其他兼容纠正组合也不能再次刷新或无限循环。
- [x] 新凭证通过 `host.auth.save` 保存并安装，同一代状态供执行与模型发现使用；保存失败明确报告并保留可用内存结果，不再次消费旧 refresh token，记录重启风险。
- [x] 短时网络/502/超时、refresh 到期和明确认证拒绝分别报告；旧 access token 尚有效时暂时失败不标永久失效。
- [x] 并发刷新、其他请求已保存新 token、轮换后保存失败、重启与旧结果晚到场景有行为证据，并对应 refresh/unauthorized 上游测试。

## Slice 07: 永久失效与重新登录恢复

Status: completed

Blocked by: 03,05,06

What to build:

把确定必须重新登录的认证失效接入 CPA 列表、持久化与调度，重新登录同身份恢复原记录，并用凭证代次隔离旧请求/刷新写回。

Acceptance criteria:

- [x] 明确撤销/invalid_grant 或按协议恢复后仍明确 invalid_token 才进入永久失效；单次普通 401、刷新超时、网络、额度、模型权限和配置获取失败不误判。
- [x] token 时间仍未到期但已被撤销的场景中，原列表记录保留并可观察错误/不可用，停止正常调度，不重复消费被拒绝 refresh token；现有模型快照不能掩盖失效。
- [x] 聊天、定时刷新、手动刷新及账号模型发现统一进入失败处理；宿主/容器重启后状态和调度资格一致，不因文件存在恢复可用。
- [x] 使用经核实的宿主状态上报/持久化/候选阻断通道，不冒充手动 disabled、不伪造过期时间、不删除记录；若需外部 CPA 改动，明确记录并完成授权范围内必要集成，不假称任意刷新错误足够。
- [x] 未删除旧失效记录时，同身份重新登录更新稳定 ID/FileName，文件与列表各保留一条；新凭证安装后清除旧认证错误与相关模型认证冷却，重新获取模型并完成实际调度。
- [x] 原记录手动禁用时重新登录仍禁用，并保留优先级/代理/排除；额度或其他非认证限制不被清除。
- [x] 登录取消/超时/账号确认失败/保存失败不删除旧记录、不清旧失效、不显示成功；旧在途请求、刷新和缓存不能覆盖新凭证或给新代次重新标旧错误。
- [x] token/过期字段按宿主可识别契约映射，覆盖宿主凭证变化检测与可能继承的 ModelStates；恢复后重启仍保持列表、注册和实际调度一致。
- [x] 个人/企业/不同企业/双站点身份与昵称变化联合验收，结果与兼容表中真实宿主版本、状态字段和外部缺口相对应。

## Slice 08: Chat 工具调用闭环

Status: pending

Blocked by: 05

What to build:

实现 Chat 工具 Schema、tool_choice、名称可逆映射、流参数碎片与聚合响应及后续工具历史循环。

Acceptance criteria:

- [ ] 工具 JSON Schema 根对象、组合、$ref、enum/type、tuple、布尔 schema、空/非法字段及参考转义按上游样本迁移，有界递归且不破坏合法 schema。
- [ ] 不合法工具名安全化后碰撞可区分，tools、tool_choice、历史调用同步映射，客户端流式与非流式响应恢复各自原名，不将不同工具合并。
- [ ] tool_choice 的 none/字符串/对象形式遵循参考可表达语义，不静默改变调用方明确选择。
- [ ] SSE 工具参数跨 UTF-8/网络碎片、多个 tool index/id、空 delta 和完成事件可重组，聚合包含 tool_calls、finish_reason、usage。
- [ ] 完整工具调用 → 客户端结果 → 后续轮请求可成功，保持同用户轮次归属；下一用户消息生成新轮次，多账号不会共享会话 ID。
- [ ] tools 上游测试与公共协议边界场景有对应，既验证碰撞和历史可逆，也验证干净工具请求不被无端改写。

## Slice 09: Chat 图片与reasoning能力

Status: pending

Blocked by: 05

What to build:

补齐账号模型可声明的图片输入、reasoning 历史/输出、默认与显式 effort、低预算例外及 SSE/非流式语义。

Acceptance criteria:

- [ ] 上游支持的图片输入按 Chat 协议传递；国际 GLM 遵循固定参考的图片限制，不宣称上游会丢弃的输入可用，不把本范围扩成图片生成/视频/语音/embeddings。
- [ ] 模型能力取自账号配置与经过核实的站点限制，输入上下文与最大输出不混淆；同站点不同账号能力差异不因全局 Info 合并而获得额外权限。
- [ ] 历史 reasoning_content/reasoning_details 按参考整形，流式与非流式 reasoning 输出、content、usage 同时保留可表达语义。
- [ ] 调用方显式 effort 优先；模型默认 effort 只在适用条件填充，低 token budget 的标题/分类请求不被默认 reasoning 耗尽预算。
- [ ] none/canDisableThinking/onlyReasoning 与参考 OFF_TAKEN 例外逐项核验；国内 Claude max 留给 Messages 能力，Chat 不伪造支持。
- [ ] images/reasoning/default-effort/efforts 样本与边界测试对应，实际账号图片识别和 reasoning smoke 缺证据时明确未验证。

## Slice 10: 国内Claude Messages闭环

Status: pending

Blocked by: 07,08,09

What to build:

国内 `claude-*` 使用同一 WorkBuddy 浏览器凭证的原生 `/v1/messages`，补齐 system/content blocks、thinking/签名、effort、工具及两种输出模式。

Acceptance criteria:

- [ ] 国内 Claude 按已解析模型路由 Messages，国内其他模型和国际所有模型仍 Chat；不因客户端协议、错误或模型名称猜测切换站点，不在失败后自动改投协议。
- [ ] 保留原生 Messages system、content blocks、tool_use/tool_result、thinking 与合法签名，不套用 Chat 专属整形；企业头及账号会话归属正确。
- [ ] Messages effort 字段与支持级别真实映射，max 只对支持的模型声明；流式和非流式回答、工具循环、usage、停止原因与模型前缀回写正确。
- [ ] 无签名 thinking 的参考处理与 11159/包装错误纠正有样本，合法签名保留，纠正次数有界且只在回答尚未输出前发生。
- [ ] 401 与 07 认证状态路径一致，同账号新凭证恢复不重刷或重复执行；永久失效后 Messages 不继续调度。
- [ ] `/v1/messages/count_tokens` 的历史 404 与实际宿主估算边界核实；无可用契约时返回明确不支持，不伪造上游计数；`executor.http_request` 支持范围同样明确。
- [ ] native 上游路由/请求/签名/工具样本与本地真实协议边界对应；本片不以格式字符串声明冒充跨协议转换完成。

## Slice 11: CPA客户端协议互通

Status: pending

Blocked by: 10

What to build:

核实并实现 CPA translator 与插件之间的格式责任，贯通 OpenAI Chat、Responses、Claude Messages 及 CPA 支持转换的 Gemini 客户端。

Acceptance criteria:

- [ ] 兼容表记录目标 CPA 实际输入/输出格式、每个转换方向的归属与适配位置；capabilities 只声明真实原生接受和输出格式。
- [ ] OpenAI Chat 客户端调用国内 Claude，实际使用本站点 Messages 并返回 Chat 语义；Responses 的代表性请求同样贯通。
- [ ] Claude Messages 客户端调用国内非 Claude 与国际模型，实际使用 Chat 并返回原客户端格式，站点和凭证不改变。
- [ ] CPA 可转换 Gemini 请求完成代表性双向路径；不支持的格式返回明确错误，不静默吞字段或只宣称两个 format 字符串。
- [ ] 流式/非流式转换保留文本、可支持图片、工具调用/结果、usage、停止原因与 reasoning 可表达语义；thinking/签名无法表达部分遵循已核实契约而非猜测。
- [ ] 模型别名执行和站点模型回写在转换后正确；转换失败不触发跨站点重试或重复工具执行，取消和中途失败按客户端协议终止。
- [ ] 各协议方向均有公共宿主边界测试与实际 CPA 请求证据，动态模型按实际路由和能力验收，不把未实现协议的模型标已支持。

## Slice 12: 兼容纠正与故障边界

Status: pending

Blocked by: 10

What to build:

实现参考兼容纠正与有界重试状态机，统一错误映射、取消、资源、读取限制和 shutdown 生命周期。

Acceptance criteria:

- [ ] 11128 按参考顺序逗号纠正、必要时移动 system；11153 只对工具结果后接 user 的适用形状插入空 assistant；11159 签名处理和 401 恢复不相互形成循环。
- [ ] 兼容成功策略按站点/账号/模型及必要请求形状隔离；DeepSeek Harness 与国际 `extra_vars.growthEvent` 按参考生成并保留调用方兼容字段，不扩展隐藏拒绝文本触发换号重试。
- [ ] 每条纠正次数和组合总上限有独立断言，同轮会话 ID 保持；已发送回答事件后不重放，流内错误明确终止而非返回伪成功。
- [ ] 11140 风控、14018 额度、10085 版本、上下文超限、模型无权限与认证失效分开映射，保留上游错误；429/5xx/超时/Retry-After 遵循宿主重试责任，不叠加无界重试。
- [ ] 内容安全过滤保留明确停止语义；错误体读取有界、可诊断且不泄露 token/state/完整账号数据。
- [ ] 大于 1 MiB 的真实请求正确 gzip，SSE 单事件、请求/错误体、聚合与队列均有结合宿主配置的明确上限，超限不会无限缓存或崩溃。
- [ ] 客户端取消、超时、上游断流和异常 EOF 关闭上下游 stream、释放 callback/C buffer；并发模型发现和执行不重复消费 refresh token。
- [ ] quiesce/shutdown 停止新任务，取消并等待在途任务，释放缓存及回调；卸载后没有宿主调用、悬空 stream 或资源，真实动态卸载 smoke 可证明。
- [ ] comma/systemasuser/aftertools/hidedsh/deepseek-flash/growth/filtered/native 等上游场景有对应，故障注入从公共协议与生命周期观察结果。

## Slice 13: CPA管理与容器验收

Status: pending

Blocked by: 11,12

What to build:

在隔离实际 CPA 与容器中贯通现有管理接口、双站点登录、凭证生命周期、模型调度及代理，形成真实账号人工验收记录和可复现操作说明。

Acceptance criteria:

- [ ] 记录实际 CPA/管理前端版本、插件版本/构建、平台、配置、客户端协议、模型与结果；原生测试宿主 smoke 与实际 CPA 加载证据分列。
- [ ] CPA 现有管理流程明确选择国内/国际并完成浏览器登录、保存和列表；界面缺动态入口时按已核实扩展点列明外部修改，不用独立插件页面替代。
- [ ] 凭证列表的刷新、禁用、删除及模型排除生效；永久失效显示与停调度、同身份重新登录原记录恢复和管理设置保留在真实宿主可观察。
- [ ] 至少两张国内与两张国际凭证记录实际命中 auth ID，按各账号模型资格调度；别名正确，跨站点永不混用，全局模型合并不扩大权限。
- [ ] Chat/Messages/各客户端协议的代表性流式、非流式、工具、图片/reasoning 能力用真实账号验证；缺账号权限或资源的项写明具体未验证人工步骤。
- [ ] 容器无需桌面，用户浏览器完成远端登录，服务端轮询；持久卷重启恢复凭证、刷新时间、认证失效状态和账号模型快照，不依赖开发机路径。
- [ ] 宿主代理配置可观察生效，敏感头日志策略核验；取消/超时/停用/卸载和认证恢复后重启保持一致。
- [ ] 安装、双站点登录、管理、容器持久化、故障排查与真实账号人工清单可复现，事实与未验证限制同步 compatibility；没有可见 GUI 自动化。

## Slice 14: 多平台构建与交付包

Status: pending

Blocked by: 13

What to build:

形成 Windows amd64、Linux amd64/arm64、macOS amd64/arm64 的 CGO 构建和可审查交付包，完成校验和、许可证、文档索引及平台验证记录。

Acceptance criteria:

- [ ] 统一入口 build/package 与 CI 为目标平台/架构产出正确扩展名的 `cpa-workbuddy` 动态库，产品版本均来自 VERSION，不以交叉编译成功宣称运行兼容。
- [ ] 每平台分别记录构建状态、实际宿主加载状态、工具链、动态链接依赖与 Linux 容器 libc 要求；未加载平台明确未验证。
- [ ] 发布包包含所需 MIT 及移植代码版权/许可证声明、版本与 checksums，校验和可用于安装核对。
- [ ] 打包排除凭证/token/state/日志缓存、`_ref/` 原始快照、`out/scratch/` 探索材料和临时工具，产物仅写入 out。
- [ ] README 安装/登录/容器/故障说明、实际兼容版本与文档契约索引对应最终实现；14 片 AC 证据齐备且真实账号未验证项未被勾选。
- [ ] 最终质量入口、适用静态/LSP、行为测试、真实加载、XERJ 索引同步与 post-impl 完成，失败先修复再交付；无未实现 capabilities、空壳或兼容 shim。
- [ ] 交付只生成本地可审查包；commit、push、发布和自动发布启用均等待用户明确授权，不把 CI 构建配置等同发布授权。
