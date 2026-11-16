# 质量契约

本项目的插件和工程命令使用 Go；原生 ABI 边界使用 CGO/C。根 `VERSION` 是产品版本唯一来源。工程入口位于 `cmd/project/`，仅依赖 Go 标准库。插件由 CPA 加载，没有独立的服务生命周期。

## 命令与安装

要求 Go **1.27.1**、Git 和可用 C 编译器；独立 C 探针通过 `gcc` 调用编译器，macOS 可使用同名的 Apple Clang 驱动。质量门禁验证当前主机原生平台，需要 CGO=1。

```console
go run ./cmd/project bootstrap
go run ./cmd/project quality
go run ./cmd/project quality --stage lsp --stage lint
go run ./cmd/project build
go run ./cmd/project test
go run ./cmd/project package
go run ./cmd/project --help
```

`bootstrap` 显式准备固定工具与打包素材：gopls **v0.23.0**、以 Go1.27.1 编译的 golangci-lint **2.12.2**，以及按 SHA256 固定的补充许可原文。优先使用 `out/tools/bin` 中匹配版本的工具，其次复用 PATH；需要安装时通过固定 `@version` 的 `go install` 写入项目工具目录。许可原文缓存于 `out/cache/licenses/`，首次准备需要网络；缓存损坏明确失败。质量检查不安装工具，不修改全局 Go/Git 配置。

编排器启动后的全部子进程使用项目局部 GOCACHE、GOMODCACHE、GOPATH、GOBIN、GOTMPDIR、GOENV、GOLANGCI_LINT_CACHE、GOPLSCACHE、XDG 与临时目录，统一位于 `out/`。关闭 Go telemetry、自动工具链下载和工作区自动发现，原生门禁清除继承的 GOOS/GOARCH。`WORKBUDDY_TOOLS_ROOT` 允许 index 隔离副本复用已安装的工具。

工程入口在 `out/go.mod` 建立产物模块边界，使根 `./...` 扫描排除依赖缓存及其他生成的 Go 源码；该文件属于忽略的本地产物，不是产品依赖模块。隔离测试同样通过工程入口创建此边界。

模块检查先将 go.mod/go.sum 复制到 `out/tmp`，通过 `go mod download -modfile=<临时清单> all` 准备完整固定模块图，再对根清单静默执行 `go mod tidy -diff`；临时清单及下载新增校验项在阶段结束后清理。单独原生编译使用 `go mod download` 准备已声明模块。下载与编译分别检查错误，编译阶段继续拒绝诊断输出。工程消费者测试复制实际 go.mod/go.sum，将测试进程 GOPROXY 指向本仓库已准备的模块归档缓存；隔离副本使用自己的 GOMODCACHE，仅预置已有的 sumdb 签名校验证明，保留 Go 的正常验证。从模块归档冷缓存实际运行 modules 与 package，断言源码模块文件字节不变，测试不依赖外网下载。

独立 `test` 入口在构建前执行模块检查与缓存准备；完整 `quality` 在前置 modules 阶段完成该步骤，测试阶段复用已有准备结果。

首次 `go run` 的编译发生在工程命令启动前。需将该次编译缓存也放到项目目录时，在当前终端先设置以下环境；CI 已在共享 toolchain action 中设置：

```powershell
New-Item -ItemType Directory -Force out/cache/go-build, out/cache/go-mod, out/tmp/go | Out-Null
$env:GOCACHE = "$PWD/out/cache/go-build"
$env:GOMODCACHE = "$PWD/out/cache/go-mod"
$env:GOTMPDIR = "$PWD/out/tmp/go"
```

```sh
mkdir -p out/cache/go-build out/cache/go-mod out/tmp/go
export GOCACHE="$PWD/out/cache/go-build"
export GOMODCACHE="$PWD/out/cache/go-mod"
export GOTMPDIR="$PWD/out/tmp/go"
```

这些是当前会话变量，工程命令不执行 `go env -w`。也可显式构建 `out/bin/project` 或 Windows 的 `out/bin/project.exe` 后直接运行。

## 门禁与诊断

默认 `quality` 顺序执行 **tools、version、format、modules、vet、lsp、lint、build、test** 共 9 个阶段。`--stage` 可重复，仍按默认顺序执行；定向阶段不能替代完整门禁。检查不格式化、不修复、不改依赖清单、不自动暂存。成功只显示阶段和摘要；失败输出子进程完整 stdout/stderr 并返回非零，`--verbose` 展示成功命令详情。

| 能力 | 提供者与范围 | 阻断规则 |
| --- | --- | --- |
| 工具与配置 | 固定 Go SDK、gopls、golangci-lint；golangci config verify | 缺失、版本漂移、无效配置失败 |
| 格式 | gofmt；全部自有 Go 源与测试，包含工程命令 | `gofmt -l` 非空也失败 |
| 语法与类型 | Go compiler、go vet；根模块 `./...` | 编译或类型错误失败 |
| LSP | gopls 默认分析器，全部自有 Go 源与测试 | `check -severity=hint` 的任何输出阻断，即使子工具 exit 0 |
| 静态分析与弃用 | golangci staticcheck(all)、unused、errcheck、ineffassign、bodyclose、durationcheck、nilerr、makezero | 任一诊断阻断，不用 suppression |
| C 警告 | 自有 CGO 包与独立 C probe | `-Wall -Wextra -Werror`；不向 Go runtime/cgo 传播私有 warning 策略 |
| 模块一致性 | `go mod tidy -diff`、`go mod verify`，拒绝未声明嵌套 Go module | 不一致或异常失败，不改清单 |
| 行为测试 | `go test -json -count=1 ./...`，包含工程 CLI 和独立 C 加载 | 测试失败、未执行根模块 `TestNativeABI` 均失败 |
| 原生产物 | `go build -trimpath -buildmode=c-shared` | 库为空或生成头缺失/缺导出符号均失败 |
| 产品版本 | 根 VERSION、Go embed、原生注册/status 消费者 | SemVer 不合法、显式 tag/RELEASE_TAG 不等于 v+VERSION 失败 |
| Git 消息 | 实际消息文件、历史范围与 GitHub event | 非法消息、空范围、缺失历史失败 |
| 本地 hooks | 真实 index 副本与消息文件 | 保护部分暂存，保留已有 hook manager |
| CI / 交付流水线 | 五条原生 OS/arch runner，同一 Go 根入口 | 聚合拒绝失败、取消及意外 skipped；tag 必须匹配版本 |

分析只覆盖自有源码；`out/` 是产物目录。gopls 默认关闭的可选分析器不在覆盖声明内。目前没有 clangd、clang-tidy 或 clang-format，C 覆盖由严格编译及实际加载测试承担，不声称覆盖 clang 专属检查或自动格式检查。

[gopls v0.23.0 的 filecache 实现](https://go.googlesource.com/tools/+/refs/tags/gopls/v0.23.0/gopls/internal/filecache/filecache.go) 优先读取 GOPLSCACHE，再追加 `gopls/`；本项目指向 `out/cache`，Windows 持久索引因此也位于 `out/cache/gopls/`。

## Git 事件输入与安全暂存

```console
go run ./cmd/project install-hooks
go run ./cmd/project quality --staged
go run ./cmd/project commit-msg --message-file out/COMMIT_EDITMSG
go run ./cmd/project commit-msg --commit-range BASE..HEAD
go run ./cmd/project quality --github-event
```

`install-hooks` 只配置仓库 local `core.hooksPath` 和实际 Go 可执行文件路径 `workbuddy.go`，设置 hook 可执行位。遇到其他 hook manager 或活动原生 hooks 明确失败，保留现有设置。hook 以保存的 Go 路径执行根入口，支持路径包含空格；克隆后须显式安装。

pre-commit 枚举 `git ls-files --stage -z` 并按对象 ID 读取原始 blob，在 `out/tmp/index-*` 建立精确暂存副本，运行副本自己的完整 Go `quality`。不 stash、reset 或自动暂存，不用未暂存工作树替换即将提交内容。冲突、symlink、submodule 和越界路径明确失败；退出清理临时副本，工具复用原仓库 `out/tools`。

commit-msg 读取真实消息文件，约定 `<type>[(scope)][!]: <subject>`，type 为 feat/fix/docs/style/refactor/perf/test/build/ci/chore/revert，标题不超过 100 个字符，正文空行分隔。允许 Git 的 Merge branch/remote-tracking branch/pull request/tag 与 `Revert "..."` 标题。

历史范围逐条检查完整消息；初始提交可用 `--commit-range HEAD` 遍历可达历史。CI 使用 fetch-depth:0，PR 检查 base..head，push 检查 before..after，零 before 的首次 push 检查 after 的全部可达历史。缺失基点应失败，不能缩窄范围。

## 产物与产品版本

`build` 在当前主机 Go/C 工具链上构建 `out/bin/cpa-workbuddy.dll`、`.so` 或 `.dylib`，支持的原生目标为 Windows amd64、Linux amd64/arm64、macOS amd64/arm64；检查生成头导出。`test` 构建该库并以绝对路径 WORKBUDDY_TEST_LIBRARY 交给真实原生 smoke；完整质量门禁复用 build 阶段产物。控制器清除继承的 GOOS/GOARCH，跨平台验证需在目标原生 runner 或对应实际环境执行。

`package` 重新构建当前源码，生成 `out/dist/cpa-workbuddy-v<VERSION>-<goos>-<goarch>.zip` 和外部 `.zip.sha256`。严格白名单为库、Go 生成头、native.h、VERSION、根 MIT LICENSE/README、需求/架构/兼容/质量/部署/任务文档、`licenses/THIRD-PARTY.txt` 以及构建工具链的 `licenses/Go-LICENSE`、`licenses/Go-PATENTS`；文档保留相对路径。`build-info.txt` 从实际库读取 Go 版本、模块与 GOOS/GOARCH/CGO 构建设置，不记录本机产物路径。打包入口按实际链接模块从 Go 模块缓存生成 `licenses/THIRD-PARTY.txt`，补充许可原文读取 `bootstrap` 准备的 SHA256 缓存；缺失或校验不符即失败。包内 `checksums.sha256` 覆盖其他所有成员，外部摘要在压缩写入时计算。白名单以外的参考快照、凭证、账号响应、缓存、日志和探索工具均不收录。产品版本与 ABI/schema/上游包/工具版本各自独立。

CI 与 tag 的 release-artifacts 工作流采用五条原生矩阵：`windows-2025`、`ubuntu-24.04`、`ubuntu-24.04-arm`、`macos-15-intel`、`macos-15`，对应上述五个 OS/arch，每项先核对 `go env GOOS/GOARCH`。两工作流都执行完整原生质量门禁；tag 工作流另核对 VERSION、打包并上传 workflow artifact，权限仅 `contents: read`，不创建 GitHub Release。官方 runner 标签依据及实际平台运行情况在兼容性记录中单列；配置存在不等于远程 CI 已运行。

## 固定工具依据

工具基线保持下表版本。工程命令使用 Go 标准库；插件的客户端协议转换使用公开 CPA SDK，实际链接模块、版本与完整许可在打包时生成到 `licenses/THIRD-PARTY.txt`，补充原文按固定 SHA256 缓存于 `out/cache/licenses/`。

| 工具 | 官方依据 | 本项目选择 |
| --- | --- | --- |
| Go1.27.1 / gopls v0.23.0 | [gopls 官方 release](https://go.dev/gopls/release/v0.23.0) 的 Go1.27 支持 | 保留现有固定版本，以本机实际检查验证组合 |
| golangci-lint 2.12.2 | [官方 release](https://github.com/golangci/golangci-lint/releases/tag/v2.12.2) | 保留以 Go1.27.1 构建的既有版本 |
| Actions 固定 SHA | [checkout v7.0.1](https://github.com/actions/checkout/releases/tag/v7.0.1)、[setup-go v7.0.0](https://github.com/actions/setup-go/releases/tag/v7.0.0)、[upload-artifact v7.0.2](https://github.com/actions/upload-artifact/releases/tag/v7.0.2) | 沿用固定官方 commit SHA；托管 action 的内部运行时由 runner 提供 |

## 验证状态与债务

Windows amd64 工具基线为 Go1.27.1、MinGW GCC14.2.0、gopls0.23.0、golangci-lint2.12.2。完整 `quality` 包含工程 CLI 行为测试、原生/生命周期测试和独立 C 动态加载。当前实现的完整运行结果与官方宿主加载证据以 [兼容性记录](compatibility.md) 为准。

完整九阶段 `go run ./cmd/project quality` 在阻断外网代理、根目录不存在参考副本的环境下通过，记录 `out/logs/publication-cleanup-quality.log`；5个变更Go文件的 `gopls check -severity=hint` 无warning/info/hint，记录 `publication-cleanup-lsp.log`。模型配置样本位于测试自有 `testdata/`；包消费者验证自动生成的许可原文、完整成员与摘要、文档链接，以及缺失或损坏许可缓存时的失败行为，记录 `publication-cleanup-package-test.log`。

`go run ./cmd/project --help`、`bootstrap`、`install-hooks`、`package` 均 exit 0。真实 commit-msg hook 的合法消息 exit 0、非法消息 exit 1；部分暂存测试确认工作树与 index 原始字节不变、隔离副本退出清理。仓库 local hook 使用实际 Go 路径。

隔离 Go 工程副本分别注入格式、类型、LSP 语义、弃用 API SA1019 和失败测试，全部使同一根入口阻断；源码字节保持不变，副本已清理。额外验证了项目本地已无已迁移的脚本、配置、解释器或相关工具缓存。证据见 `out/logs/go-gate-probes.json`。

包消费者测试实际调用 `package`，核对完整白名单、构建信息、外层与逐成员 SHA256、依赖许可生成结果与固定来源摘要、私有材料排除和文档链接。七维审查在交付回复中提供，XERJ 前缀为 `cpawb`。质量命令不执行 Git 提交、推送或发布；这些动作由用户授权后单独执行。

规格/行为轴由主代理核对 CLI 分派、诊断传播、来源清单、实际测试事件、ZIP 成员和精确 index 快照，并结合上述运行证据完成审查；未发现待修缺陷。独立架构审查收敛为 0 项硬性违规、0 项设计待修项。测试环境的 GOPLSCACHE 与正式入口一致，使用 gopls 自带的子目录规则。

本机验证不代表远程 CI、实际 CPA、真实账号、容器或其他目标平台通过；这些仍需对应切片独立验收。

不接受默认历史债务 baseline；新诊断必须修复。确认已有债务需保留可见失败并记录具体规则、文件/符号、原因和移除条件，得到明确接受后才可建立窄例外。实际 CPA、真实账号、容器、远程 CI 和其他目标平台未验证时分别记录。
