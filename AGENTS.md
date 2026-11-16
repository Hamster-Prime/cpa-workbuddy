# AGENTS.md

cpa-workbuddy 是 Go/CGO 实现的 CLIProxyAPI WorkBuddy 动态库插件。应用版本以根 `VERSION` 为唯一来源；原生 ABI 与 JSON schema 是独立版本域。

## 项目入口

- 构建：`go run ./cmd/project build`
- 测试：`go run ./cmd/project test`
- 完整质量门禁：`go run ./cmd/project quality`
- 本机交付包：`go run ./cmd/project package`
- 工具安装、单阶段检查和 Git hooks 的准确用法见 `docs/quality.md`。质量门禁只检查，工具安装单独执行。
- 插件由 CPA 加载，没有独立的服务进程生命周期。

## 范围与依据

- 需求唯一正文：`docs/requirements.md`；任务与状态：`docs/tasks/workbuddy-plugin.md`。
- 架构边界：`docs/architecture.md`；宿主兼容及移植证据：`docs/compatibility.md`。
- 任务文档仅维护状态、依赖、实施内容和验收项；验证证据归质量与兼容性文档，七维审查报告在交付回复中提供。
- WorkBuddy 协议参考固定 commit 的原始源码与测试；`_ref/` 为本地忽略的只读参考副本，不参与构建、测试、CI 或打包。
- 每项功能记录上游函数/测试、CPA 适配位置和验证证据。推论标记 `[INFERENCE]`；实际账号、宿主和平台未验证时如实记录。
- 单 provider `workbuddy`，两站点模型分别为 `wb/*` 与 `wb-ai/*`；`AuthData.Prefix` 留空。本站点、用户及企业作用域贯穿认证、缓存和调度。
- 只实现当前任务批准的切片；认证、模型及执行能力完成前不得声明对应 capability。
- 项目初始化只交付工程框架、统一工具和质量门禁，并形成独立 init 提交；插件能力与业务实现按切片单独实施和提交。提交拆分须保证每个提交可验证、门禁通过。

## 目录与工程约束

- 根 Markdown 仅 `README.md` 与 `AGENTS.md`；其他文档统一 `docs/`，源码快照内上游文档保留原位。
- 所有构建、工具安装、缓存、测试临时文件、覆盖率、日志及探索材料统一进入被忽略的 `out/`。不要在根或子包生成 `dist/`、`build/`、`target/`、`bin/`。
- `cmd/cpa-workbuddy/` 为原生入口，业务与协议边界位于 `internal/`；`cmd/project/` 用 Go 标准库统一编排构建、质量检查和 Git 事件。
- 不直接访问用户 auth 目录；网络与凭证保存使用 CPA 宿主契约。所有 C 缓冲区、回调、流及关闭动作必须有明确所有者。
- 不吞错误、不伪造成功、不增加静态检查抑制、不使用 `--no-verify`。新增测试应检验消费者可见行为及真实不确定边界。
- 工程代码、构建工具和自动化优先使用项目当前语言 Go；新增运行时或包管理器须有实际必要性并经用户明确确认。
- 验证仅采用命令行或隔离 headless；不操作前台桌面、不打开可见浏览器。真实登录交由用户完成。

## 检索与索引

XERJ prefix: "cpawb"

- 优先 LSP 查询定义、引用和重构；无可用 LSP 时使用 XERJ。已知文件使用定点读取。
- 索引排除规则由 `.xerjignore` 与 `.gitignore` 管理；`out/` 和本地参考目录排除，自有源码与文档纳入索引。
- 未启用 watch。代码或文档修改后、最终审查前运行：
  `xerj autoindex . --prefix cpawb --no-graph --code-analyzer code --yes`
- 同步须 exit 0 或 3 且 `code_files_indexed > 0`。索引错误先按项目要求 remove 同前缀并完整重建，不能跳过。

## 完成门禁

- 迭代使用覆盖变更的最窄检查；最终工作树运行一次完整 `go run ./cmd/project quality`。失败先修复对应阶段，再运行完整门禁。
- CLI LSP 的 warning/info/hint 与编译、静态检查错误同样阻断完成；本地与 CI 平台结果分别记录。
- 非平凡代码变更在质量门禁及索引同步通过后，显式读取并执行 `post-impl` 七维审查；涉及公开协议或生命周期时先执行 `code-review`。有修复则重跑受影响检查。
- 按实际证据勾选任务 Acceptance Criteria；没有运行的宿主或账号验收不能勾选为通过。
- 所有变更留在工作树。只有用户明确要求时才 commit、push 或发布。
