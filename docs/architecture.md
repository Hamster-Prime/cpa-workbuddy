# cpa-workbuddy 架构与技术边界

> 本文描述实现边界；产品要求以 [requirements.md](requirements.md) 为准，来源与未验证事实见 [compatibility.md](compatibility.md)，实施顺序见 [14 片任务](tasks/workbuddy-plugin.md)。目录中的未来职责不代表已实现，不预建空文件或占位能力。

## 运行形态与宿主边界

交付物是由官方 CPA 加载的单个 CGO 动态库，插件 ID 为 `cpa-workbuddy`。Windows 使用 `.dll`，Linux 使用 `.so`，macOS 使用 `.dylib`；加载、停用、调度及卸载由 CPA 管理。

原生 ABI 为 1，JSON schema 为 10。注册时要求宿主具备 schema 10 的多 OAuth 入口能力，并返回本插件实际实现的 schema；产品兼容版本按官方源码与实际加载结果记录。插件 status 返回根 VERSION、ABI 和 schema，管理鉴权与路由注册复用官方宿主契约。

C ABI 入口按值保存宿主回调表，借用请求输入，复制宿主响应并由宿主的 `free_buffer` 配对释放。原生独立加载测试检验缓冲区、错误和关闭边界；完整 CPA 进程加载与协议验收单独记录。

### 认证与凭证生命周期

持久 provider 为 `workbuddy`。两个 OAuth 入口 `workbuddy`、`workbuddy-ai` 分别公开“WorkBuddy国内”“WorkBuddy国际”，由原版管理前端按入口元数据展示。入口确定站点，显式 metadata.site 必须与入口一致；凭证 provider 与登录入口分别保存。

浏览器登录通过宿主 HTTP 获取 state，用户完成网页登录，服务端轮询 token/account。state 按会话隔离，五分钟过期，最多 128 个；取消、失败、完成和关闭释放会话。个人、企业及站点身份由账号响应确认，凭证 ID/FileName 基于 `site + uid + enterprise/tenant scope` 的稳定摘要，昵称不参与唯一性。

`auth.refresh` 只返回轮换后的 AuthData；原版 Manager 负责保存、安装、凭证版本检查与迟到结果处理。执行端把 401 交回宿主，由原生流程读取最新凭证、刷新并最多重试一次。同账号同登录代次的 refresh token 在插件内单飞，成功结果保留 60 秒；缓存不跨站点、身份或登录代次采用。

自动续期通过 `refresh_interval_seconds=300` 使用原版周期调度。宿主保存失败时以 warning 告警并继续使用新内存凭证，重启有丢失风险；插件返回阶段不承诺得知后续落盘结果。永久刷新拒绝采用原版需要重新登录状态；尚未到期、未被实际拒绝的 access token 仍可推理。重启后由宿主重新验证并重建状态。

凭证文件位置、写盘和管理设置由 CPA 负责。插件的带版本 JSON 同时包含当前 token、domain、企业作用域、过期时间、登录代次及模型快照；空值明确表达当前认证数据。手动禁用、优先级、代理、排除和别名保持为宿主管理字段。插件不直接访问用户凭证目录。

### 模型与协议

账号模型发现读取本站点 `/v3/config`，解析 CLI 和企业 custom 模型，以身份、站点、采集时间保存成功快照。普通发现通过 AuthUpdate 更新宿主进程内状态；登录和原生刷新结果携带当前快照，由宿主随凭证持久化。重启使用最近已落盘快照，可能早于最后一次普通发现。临时故障可恢复本站点同身份的成功快照；首次失败不授予静态模型资格。

executor 接受和输出 `openai`、`openai-response`、`claude`、`gemini`。插件根据当前凭证站点和宿主已解析模型选择 WorkBuddy 原生协议：国内 Claude 使用 `/v1/messages`，其他模型使用对应站点 `/v2/chat/completions`。客户端协议不决定上游路由；`AuthData.Prefix` 留空，执行前校验并只剥离一层 `wb/` 或 `wb-ai/`。

协议转换直接使用官方公开 translator SDK，在插件内适配请求和每请求响应状态。Messages 保留 system、工具、thinking 和可表达的签名语义；Chat 负责 gzip、system/developer、工具 Schema、可逆工具名、账号会话及 compatibility 整形。图片与 effort 以当前账号快照验证，未知能力不扩大权限。转换后的 usage、停止原因、reasoning 和工具结果按客户端协议交付。

上游推理使用 SSE；同协议直接交给原生消费者，跨协议在完整事件边界转换。非流式聚合与流式共用校验逻辑，工具调用由客户端执行；缺少完成事件、未闭合工具参数、坏事件或中断均报告错误。

### 资源与重试

`host.http.do_stream` 创建宿主持有的上游流，插件读取并关闭；下游通过 `host.stream.emit` 和 `host.stream.close` 交付。Service 统一控制认证、模型和执行准入，关闭时停止新任务、取消并等待同步回调和异步 pump 退出，再清理缓存和回调资源。

插件请求体和非流式聚合上限为 32 MiB，RPC/C 缓冲区上限为 64 MiB，错误体最多读取 64 KiB，单个 SSE 事件上限为 1 MiB。宿主网络读取与日志资源策略由实际官方版本及配置决定，插件不声明控制宿主内部缓冲区。

Chat 的逗号、system 和工具后续纠正分别限次，单次 executor 调用最多四个上游请求；Messages thinking 签名最多纠正一次。原生 401 恢复最多重新调用 executor 一次，组合上限分别为八个和四个请求。已输出回答后不重放；429、5xx 和超时交给宿主，Retry-After 使用官方 `retry_after_seconds` 错误字段。

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
  docs/                      # 需求、架构、兼容证据与任务
  _ref/                      # 本地忽略的只读上游参考，不参与构建/测试/CI/打包
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
| 凭证 | 管理鉴权、文件位置与保存、版本检查、安装、列表、禁用/删除、调度、刷新驱动及失效状态 | 浏览器协议、带版本存储、稳定身份、token 轮换和安全错误分类 |
| 网络 | HTTP transport policy、代理、取消与宿主日志 | 只调用宿主 HTTP/流回调；固定站点目标、请求头、gzip 和协议解析 |
| 模型 | 每凭证注册、全局 ID 汇总、别名/排除、候选调度 | 账号配置发现与成功快照，生成站点前缀，执行前再次检查站点 |
| 格式 | 按 executor 声明传递客户端格式，维护 HTTP handler 的流记录契约 | 按账号模型选择上游协议，使用公开 translator SDK 转换四种客户端格式，保留可表达语义 |
| 生命周期 | 加载、回调入口、调度停用和卸载 | ABI 缓冲区与 stream 所有权、有界资源、取消、停止新任务并等待在途任务 |

CPA 通过公开 OAuth 入口标识选择站点，插件返回单一持久 provider 的凭证。管理前端与宿主能力分别按实际官方版本验证。公开接口不能满足的业务行为必须先汇报并由用户决定，不修改 CPA 或官方管理前端。

## 身份、模型与路由边界

持久化身份按 `site + uid + enterprise/tenant scope` 生成摘要和稳定记录；展示名不参与唯一性。token、登录 state 和完整账号响应不得进入日志、测试快照或发布包。凭证代次约束在途刷新与重新登录的写回，防止旧状态覆盖新登录；用户管理设置与认证失效状态分别处理。

模型 ID 由插件生成 `wb/<上游 ID>` 或 `wb-ai/<上游 ID>`，`AuthData.Prefix` 留空。宿主已解析模型决定执行目标，不能把原始客户端别名当作执行模型；只剥离一层前缀并检查凭证站点。账号权限来自本账号成功快照，全局按 ID 合并的公开元数据不等同各账号权限或能力相同。

国内 `claude-*` 使用 WorkBuddy `/v1/messages`；国内其他模型和国际所有模型使用本站点 `/v2/chat/completions`。客户端协议不决定上游路由。翻译归属必须在实现前写入兼容表并通过双向转换场景证明，声明 format 字符串本身不构成转换实现。

## 并发、资源与恢复边界

同账号同登录代次的刷新结果短时共享，保存与安装由原生 Manager 完成。永久刷新拒绝、实际访问拒绝、临时故障、额度、模型权限和手动禁用分别处理；恢复策略遵循需求中已确定的原生生命周期。

HTTP 回调 ID、C buffer、宿主上游 stream ID 与下游 stream ID 分别记录创建者、消费者及释放者；请求取消关闭上游读取，shutdown 停止接收并取消、等待在途任务，释放后不得回调宿主。请求体、错误体、SSE 单事件、聚合响应及队列必须有界，具体限制与目标宿主配置一起记录。

纠正重试沿用同轮会话身份，限定每条规则与组合总次数。输出回答事件后不得重放整次请求；流内错误按客户端协议终止。不把协议失败改投其他站点、模型或协议。

## 文档与完成契约

需求只在 `requirements.md` 维护，技术边界在本文维护，源码证据和兼容状态在 `compatibility.md` 维护，执行依赖与可观察 AC 在 `tasks/workbuddy-plugin.md` 维护。

每片必须保留上游函数/行号、上游测试、CPA 适配位置与本地验证结果的对应记录。所有并行修改结束后统一完成所需构建、行为测试、真实原生 smoke、静态/LSP 检查、XERJ 索引同步及 `post-impl`，再按证据更新任务状态。仅通过构建不能勾选 CPA 加载或真实账号 AC。验证不启动可见窗口，不操作用户桌面；真实网页登录由用户完成。所有变更保持未提交，commit、push、发布需用户明确指示。
