# cpa-workbuddy 架构与技术边界

> 本文描述实现边界；产品要求以 [requirements.md](requirements.md) 为准，来源与未验证事实见 [compatibility.md](compatibility.md)，实施顺序见 [14 片任务](tasks/workbuddy-plugin.md)。目录中的未来职责不代表已实现，不预建空文件或占位能力。

## 运行形态与最小加载闭环

交付物是 CPA 加载的单个 CGO 动态库，插件 ID 为 `cpa-workbuddy`，provider key 为 `workbuddy`。Windows 使用 `.dll`，Linux 使用 `.so`，macOS 使用 `.dylib`。库没有独立服务进程，也没有 `start` / `stop` 命令；加载、停用、调度及卸载由 CPA 管理。

Slice 01 建立工程初始化、产品版本、真实 C ABI 身份与注册、错误信封、缓冲区释放和卸载闭环，并提供真实只读 `management_api`：`GET /v0/management/plugins/cpa-workbuddy/status` 返回身份、版本、ABI 与 schema，复用 CPA 现有管理鉴权，不交付独立页面。宿主 `internal/pluginhost/host.go:1025-1060` 要求 Name、Version、Author、GitHubRepository 非空且至少一项实际能力，不能用零 capabilities 注册。作者为 `sheepyu`，计划仓库为 `ygq-future/cpa-workbuddy`，远程尚未创建。AuthProvider、ModelProvider、Executor、格式支持与模型目录按后续切片真实落地后再声明；不注册虚构模型，不以“未实现”executor 充当能力。

原生 ABI 版本与 JSON schema 是两个独立协商维度：当前只读 CPA 源码分别为 ABI 1、schema 6。C ABI 的符号、结构体布局、长度与所有权以目标 CPA 源码为准；参考插件只提供桥接模式。原生测试宿主必须真正动态加载库并调用导出符号，不能把 Go 单元测试或直接调用业务函数等同于加载验证。测试宿主验证成功与实际 CPA 进程加载成功分开记录。

## 代码布局与版本来源

```text
cpa-workbuddy/
  VERSION                    # 产品版本唯一来源
  version.go                 # 根 Go 包读取并暴露 VERSION
  cmd/cpa-workbuddy/          # CGO main、C ABI 导出与最小库入口
  internal/plugin/           # JSON wire、注册、分派、错误与生命周期
  internal/host/             # 后续：HTTP、流、凭证保存、日志回调
  internal/workbuddy/        # 后续：站点、认证、刷新、模型、协议和执行
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
