# cpa-workbuddy 架构与技术边界

> 本文描述实现边界；产品要求以 [requirements.md](requirements.md) 为准，来源与未验证事实见 [compatibility.md](compatibility.md)，实施顺序见 [14 片任务](tasks/workbuddy-plugin.md)。目录中的未来职责不代表已实现，不预建空文件或占位能力。

## 运行形态与最小加载闭环

交付物是 CPA 加载的单个 CGO 动态库，插件 ID 为 `cpa-workbuddy`，provider key 为 `workbuddy`。Windows 使用 `.dll`，Linux 使用 `.so`，macOS 使用 `.dylib`。库没有独立服务进程，也没有 `start` / `stop` 命令；加载、停用、调度及卸载由 CPA 管理。

Slice 01 建立工程初始化、产品版本、真实 C ABI 身份与注册、错误信封、缓冲区释放和卸载闭环，并提供真实只读 `management_api`：`GET /v0/management/plugins/cpa-workbuddy/status` 返回身份、版本、ABI 与 schema，复用 CPA 现有管理鉴权，不交付独立页面。宿主 `internal/pluginhost/host.go:1025-1060` 要求 Name、Version、Author、GitHubRepository 非空且至少一项实际能力，不能用零 capabilities 注册。作者为 `sheepyu`，计划仓库为 `ygq-future/cpa-workbuddy`，远程尚未创建。AuthProvider、ModelProvider、Executor、格式支持与模型目录按后续切片真实落地后再声明；不注册虚构模型，不以“未实现”executor 充当能力。

原生 ABI 版本与 JSON schema 是两个独立协商维度：当前只读 CPA 源码分别为 ABI 1、schema 6。C ABI 的符号、结构体布局、长度与所有权以目标 CPA 源码为准；参考插件只提供桥接模式。原生测试宿主必须真正动态加载库并调用导出符号，不能把 Go 单元测试或直接调用业务函数等同于加载验证。测试宿主验证成功与实际 CPA 进程加载成功分开记录。

Slice 02 在此基础上声明 `auth_provider`，分派 `auth.identifier`、`auth.parse`、`auth.login.start` 与 `auth.login.poll`。`internal/workbuddy/` 实现双站点浏览器协议与带版本凭证；`cmd/cpa-workbuddy/` 按值保留宿主回调表，原生请求借用 Go 输入缓冲区，宿主响应复制后始终由宿主的 `free_buffer` 释放，包括 transport 错误。网络只使用 `host.http.do` 并透传当前 `host_callback_id`；CPA 保存成功后才对管理客户端显示登录完成。刷新实现由 Slice 06 承接。

插件持有的登录 state 与上游 state 分离，按会话串行轮询且不同会话并行，令牌在账号确认等待期间保留。会话最多五分钟、最多 128 个，过期定时清理；成功、终止失败、取消及关闭都会废弃会话，重启不恢复未完成登录。quiesce/shutdown 先关闭新任务入口、取消本地任务并等待在途同步宿主回调返回，再清理认证状态。CPA 的同步 HTTP 回调由其请求 context 控制实际网络取消；插件不会在回调返回前卸载，也不在本地取消后发布登录成功。

Slice 03 的 `credentialIdentity` 统一生成站点/uid/个人或企业作用域的身份摘要，展示名不参与摘要。`AuthData.RequestHeaders` 从受版本校验的 StorageJSON 派生用户、domain 和企业/租户头，并核对 AuthData.ID；仅携带 AuthID 的宿主请求允许不提供 FileName，提供 FileName 时必须与该身份摘要匹配。mutable metadata 不参与头生成。未绑定身份时的登录账号选择与 `/accounts` 发现保持无企业头，避免先选作用域再反推身份。

持久化明确写出 `enterprise_id`、`refresh_token`、`domain`、`token_type` 和 `refresh_expires_at` 的当前值，包括空字符串/null；这些是插件拥有的协议字段。CPA 重新登录时只补回缺失元数据，因此当前空值同样是认证 SSOT，防止旧认证值被补回。禁用、优先级、代理和模型排除等宿主管理字段由 CPA 合并；插件不在登录成功结果中设置这些字段的默认值。

Slice 04 声明 `model_provider`：`model.static` 返回空目录，`model.for_auth` 以当前凭证通过宿主 HTTP 读取固定站点 `/v3/config`，使用上游 CLI User-Agent。CLI 列表之后追加企业 `custom:`，保留完整 ID。成功结果通过 `AuthUpdate` 交给 CPA 的模型注册路径，由其更新 Manager 与 FileTokenStore；插件不直接访问凭证目录。

凭证内 `model_snapshot` 保存身份摘要、站点、成功采集时间和完整能力/积分元数据，`model_discovery_failure` 保存结构或明确 token 拒绝状态。临时故障只恢复匹配身份且未被拒绝的快照；结构错误、缺 CLI 和带 `invalid_token` challenge 的 401 返回成功空列表，使宿主撤销旧资格，并保留最后成功快照作为历史。成功发现清除失败状态；重新登录明确写出 null 快照和空失败状态，覆盖 CPA 合并时保留的旧字段。静态目录不授予权限；图片和 reasoning 等执行能力完成后才对外声明。诊断只含身份摘要、站点、状态、原因、模型数量及恢复快照时间。

相同成功模型内容沿用快照采集时间，不产生 AuthUpdate；内容、作用域或失败状态变化时才更新成功快照。CPA watcher 会比较完整凭证元数据，此规则使发现与凭证文件更新收敛。认证请求头由 `AuthData.RequestHeaders` 统一维护，站点应用名和模型前缀由 `selectSite` 提供。

Slice 05 的 executor 以 OAuth 作用域接受和输出 `openai`。请求使用宿主解析的模型与凭证站点，固定发送到该站点 `/v2/chat/completions`；JSON gzip 且上游始终流式。`AuthData.RequestHeaders` 提供认证与应用归属，Chat 层补全上游 SDK 头。会话优先取 CPA canonical/execution/derived session 元数据，再按用户消息派生，账号摘要参与会话和轮次身份。

`host.http.do_stream` 创建宿主持有的上游 HTTP stream，插件读取并在结束、错误或取消时关闭。下游 streaming stream ID 由 CPA 创建；插件异步解析完整 SSE 事件，通过 `host.stream.emit` 推送，最终 `host.stream.close`。Service 记录在途执行，关闭时取消并等待异步 pump 与取消回调退出，原生 Runtime 再完成卸载。非流式调用使用同一个 SSE 解析器聚合；缺少完成事件、未结束 choice、坏事件或读取中断均明确失败。单事件上限 1 MiB，聚合上限 32 MiB，实际 HTTP 取消由宿主请求 context 与 stream close 共同驱动。

Chat 显式拒绝尚未实现的生成能力；国内 Claude 的 Messages 路由由 Slice 10 接入。`count_tokens` 与 `http_request` 返回明确不支持，不构造远端计数或任意 URL 请求。客户端协议互通的适配与证据由 Slice 11 维护。

正常流式完成按 emit → close 顺序交付；取消可并发关闭下游，以中断宿主队列中阻塞的 emit。插件在每个后续事件前检查取消，并等待 pump、取消回调和清理诊断全部退出后释放宿主回调。上游启动阶段失败由 RPC error 交回 CPA，CPA 在 `rpc_client_stream.go:24-27` 清理预先分配的下游流。清理失败走带回调上下文的 `host.log`；只有宿主日志通道自身失败时才使用进程 stderr 作为最后诊断出口。

Slice 06 分派 `auth.refresh`，与 Chat 和模型发现共用按稳定账号身份串行的 `renew`。每次消费前读取宿主列表及物理凭证，401 和轮换结果返回后再次确认代次；外部已保存的身份凭证优先，晚到结果不覆盖已观测的新代次。成功结果保留 60 秒；保存失败的结果持续保留到保存恢复，禁止再次消费已用 refresh token。请求恢复只重发一次，沿用已压缩的请求体和会话头；已输出 SSE 后不恢复。

宿主预刷计划为 access 过期前五分钟，请求 stale 窗口为一分钟，上游绝对时间按毫秒、相对时间按秒。`host.auth.save` 原子写入并安装新版身份，禁用/优先级/代理/排除仍由宿主合并。保存失败以 `credential_save_failed` 报告重启风险；后续执行和模型发现可使用内存代次。旧 access 尚有效时暂时刷新失败记录安全原因并继续原请求；明确永久拒绝由 Slice 07 的凭证状态契约处理。

当前 CPA 参考版本的保存、调度缺口通过授权的 `out/verification/cpa-compatible` 独立副本修复并导出补丁。参考目录保持只读；副本使用插件 RefreshEvaluator 绑定计划，Manager 的 pending/backoff 门控与插件成功计划分别处理。

Slice 07 的 `credential_state` 是带版本的凭证状态对象：`version=1`、`status=reauth_required`、规范化 reason 及当前 access/refresh 二元 JSON 数组的 SHA256 摘要。只有字段有效且摘要匹配当前 token 时才生效；未知、格式错误或绑定其他代次的状态不授予永久失效判断。成功登录产生随机 `credential_generation` 并明确写出健康 null，刷新和解析沿用该代次；即使 token 字符串相同，新登录也可清除上一代的拒绝与缓存。失败登录不交付凭证。普通 401、网络、超时、额度、模型权限和配置失败不产生永久标记；明确 invalid_grant/撤销，或一次恢复后仍明确 invalid_token 才进入该状态。

插件先通过 `host.auth.list` 核对 `credential_state_version=1`，再发送带 `expected_credential_json` 的保存请求。独立 CPA 补丁比较稳定 ID/provider、当前 token、credential_generation 和当前 credential_state，在共享凭证文件锁内完成预期代次校验、原子写入及 Manager 安装；安装使用宿主已有的 skip-persist context，避免递归写盘。登录存储共用该文件锁，旧请求/刷新/缓存不能跨越新登录代次写回。已由其他请求保存的同代终态视为幂等成功，返回需要重新登录而非暂时保存失败。

宿主把有效状态映射到 `StatusError`、`Unavailable`、HTTP 401 `reauth_required` 与空刷新计划，并在加载、候选选择、刷新和迟到结果处理时维护该约束；手动 Disabled 保持独立。重新登录替换 token 后只清理认证错误及 401 模型状态，保留宿主管理设置、额度和其他模型限制。失效状态保存失败返回 `credential_state_save_failed` 并保留内存拒绝结果，避免重复消费被拒绝的 token；持久化未成功时重启风险明确可见。

Slice 08 在 Chat 请求整形边界处理工具 Schema 与名称。Schema 只遍历协议定义的子 Schema 位置，保留 examples、属性名称和合法约束；根对象、组合、引用、enum/type、tuple 与布尔 Schema 按固定参考适配，递归有界。合法工具名预先占位，非法原名排序后分配唯一安全名称；声明、明确选择与历史调用使用同一映射，SSE 与聚合结果恢复客户端原名。映射不跨请求或账号持久化。

同一 choice 的工具流按 tool index 独立记录 ID、类型和原名；参数碎片按到达顺序连接，完成时校验调用身份完整且不重用 ID。流式路径转发完整事件而不累计参数正文，非流式路径把参数计入既有聚合上限。客户端执行工具并提交结果，插件只适配后续历史，不执行客户端工具；工具循环沿用原用户轮次，新用户消息开启新轮次。

## 代码布局与版本来源

```text
cpa-workbuddy/
  VERSION                    # 产品版本唯一来源
  version.go                 # 根 Go 包读取并暴露 VERSION
  cmd/cpa-workbuddy/          # CGO main、C ABI 导出与最小库入口
  internal/plugin/           # JSON wire、注册、分派、错误与生命周期
  internal/workbuddy/        # 双站点浏览器认证、凭证；后续模型与执行协议
  cmd/project/              # Go 标准库工程命令与 CLI 行为测试
  testdata/                  # 按需加入脱敏协议样本
  docs/                      # 需求、架构、兼容证据、任务、ADR
  _ref/                      # 固定上游原始快照，只读，不打包
  out/                       # 全部构建物、缓存、临时文件、日志及探索材料
    scratch/                 # 本地探索材料与下载归档
```

`cmd/cpa-workbuddy` 负责原生 ABI 边界；`internal/plugin` 处理真实契约与插件生命周期；根 `version.go` 从 `VERSION` 读取产品版本。产品版本与 WorkBuddy 协议版本、CPA ABI/schema、参考包版本各自独立。平台产物从 `VERSION` 取版本。

业务层不依赖 CPA 私有 internal 包。认证、刷新、模型、Chat、Messages 与 SSE 模块在对应切片中按实际职责建立。

## 工程操作边界

统一从仓库根目录执行：

```text
go run ./cmd/project quality
go run ./cmd/project build
go run ./cmd/project test
go run ./cmd/project package
```

`quality` 汇总仓库要求的静态检查与质量门，`build` 生成原生动态库，`test` 运行适用的行为和真实动态加载 smoke，`package` 输出可审查交付包。具体开关以入口帮助及工程质量规范为准，文档不并列维护另一套 task/just/npm 命令体系。所有产物、工具缓存、临时文件和日志统一写入 `out/`；CPA 管理的用户凭证属于宿主持久化数据，不能挪入构建目录。

## CPA 与插件责任划分

| 边界 | CPA 责任 | 插件责任 |
|---|---|---|
| 凭证 | 管理接口鉴权、文件位置、保存、列表、禁用/删除、调度与刷新驱动 | 浏览器协议、带版本存储、稳定身份、token 轮换及状态映射 |
| 网络 | HTTP transport policy、代理、取消与宿主日志 | 只调用宿主 HTTP/流回调；固定站点目标、请求头、gzip 和协议解析 |
| 模型 | 每凭证注册、全局 ID 汇总、别名/排除、候选调度 | 账号配置发现与成功快照，生成站点前缀，执行前再次检查站点 |
| 格式 | 根据已声明格式提供的请求/响应翻译 | 明确原生 Chat/Messages 的接受与输出范围，保留可表达语义 |
| 生命周期 | 加载、回调入口、调度停用和卸载 | ABI 缓冲区与 stream 所有权、有界资源、取消、停止新任务并等待在途任务 |

CPA 登录接口通过 metadata 传 `site`。插件不直接写 auth 目录，不读取桌面登录文件，不启动独立管理页。管理前端是否能显示动态插件入口、选择站点及如何反映永久失效，必须以目标 CPA 版本实际能力验证。若需要外部 CPA/前端改动，列明兼容缺口及外部修改范围，不隐式扩大本仓库交付。

## 身份、模型与路由边界

持久化身份按 `site + uid + enterprise/tenant scope` 生成摘要和稳定记录；展示名不参与唯一性。token、登录 state 和完整账号响应不得进入日志、测试快照或发布包。凭证代次约束在途刷新与重新登录的写回，防止旧状态覆盖新登录；用户管理设置与认证失效状态分别处理。

模型 ID 由插件生成 `wb/<上游 ID>` 或 `wb-ai/<上游 ID>`，`AuthData.Prefix` 留空。宿主已解析模型决定执行目标，不能把原始客户端别名当作执行模型；只剥离一层前缀并检查凭证站点。账号权限来自本账号成功快照，全局按 ID 合并的公开元数据不等同各账号权限或能力相同。

国内 `claude-*` 使用 WorkBuddy `/v1/messages`；国内其他模型和国际所有模型使用本站点 `/v2/chat/completions`。客户端协议不决定上游路由。翻译归属必须在实现前写入兼容表并通过双向转换场景证明，声明 format 字符串本身不构成转换实现。

## 并发、资源与恢复边界

同账号同代 refresh token 由定时刷新和请求恢复共用单飞入口；成功结果短时保留，保存失败明确报告且不得重复消费旧 token。认证永久失效、暂时网络错误、额度、模型无权限和用户禁用分别处理；模型缓存不恢复已失效的调度资格。

HTTP 回调 ID、C buffer、宿主上游 stream ID 与下游 stream ID 分别记录创建者、消费者及释放者；请求取消关闭上游读取，shutdown 停止接收并取消、等待在途任务，释放后不得回调宿主。请求体、错误体、SSE 单事件、聚合响应及队列必须有界，具体限制与目标宿主配置一起记录。

纠正重试沿用同轮会话身份，限定每条规则与组合总次数。输出回答事件后不得重放整次请求；流内错误按客户端协议终止。不把协议失败改投其他站点、模型或协议。

## 文档与完成契约

需求只在 `requirements.md` 维护，技术边界在本文维护，源码证据和兼容状态在 `compatibility.md` 维护，执行依赖与可观察 AC 在 `tasks/workbuddy-plugin.md` 维护。`docs/adr/` 用于后续真实决策；初始化只保留目录，不创建虚构 ADR。

每片必须保留上游函数/行号、上游测试、CPA 适配位置与本地验证结果的对应记录。所有并行修改结束后统一完成所需构建、行为测试、真实原生 smoke、静态/LSP 检查、XERJ 索引同步及 `post-impl`，再按证据更新任务状态。仅通过构建不能勾选 CPA 加载或真实账号 AC。验证不启动可见窗口，不操作用户桌面；真实网页登录由用户完成。所有变更保持未提交，commit、push、发布需用户明确指示。
