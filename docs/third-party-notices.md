# 版权与第三方许可

本项目以根 [LICENSE](../LICENSE) 的 MIT 条款分发。发布包保留该许可证、以下来源说明及所需完整许可文本。参考源码快照用于来源核对，不随包分发。

| 来源 | 固定依据与实际用途 | 许可与随包位置 |
| --- | --- | --- |
| magpie-community/plugins WorkBuddy | commit `0135b7b0ea979c94033a7712a0c6ca1be8dafb70`，`packages/workbuddy` 版本 `0.2.31`；双站点协议、Schema/请求整形、SSE、工具和兼容策略的 Go 移植。原始字节和哈希由仓库 `_ref/UPSTREAM.json` 固定 | MIT，Copyright (c) 2026 magpie-community contributors；完整声明即根 `LICENSE`，与固定上游根 LICENSE 字节一致 |
| router-for-me/CLIProxyAPI 公开 Go SDK | Go 模块 `github.com/router-for-me/CLIProxyAPI/v8@v8.0.69`，官方 tag 对应 commit `f7ae8feeb3710319f524730849fbe32fde1751e4`；插件实际链接 `sdk/translator` 与 `sdk/translator/builtin`，用于 OpenAI Chat、Responses、Claude、Gemini 请求及响应转换 | MIT；上游完整许可原样保留于 [CLIProxyAPI-MIT.txt](licenses/CLIProxyAPI-MIT.txt)，亦纳入 [Go 依赖许可汇总](licenses/Go-DEPENDENCIES.txt) |
| Go runtime 与标准库 | CGO 动态库静态链接的 Go runtime/标准库；实际工具链由包内 `build-info.txt` 记录 | Go BSD 许可与专利授权由打包时的 GOROOT 原样收录为 ZIP 根下 `licenses/Go-LICENSE`、`licenses/Go-PATENTS` |

CPA 的 SDK 许可原文取自上述版本的 Go 模块下载缓存 `LICENSE`，SHA256 为 `879792e89cf1bdd6a8d446033ec87e30496f97dcafc4656dc53f641509b346a6`。原版 CPA 服务与 CPAMC 前端独立安装，具体基线与接入方式见 [部署说明](deployment.md)。

下表列出 `cmd/cpa-workbuddy` 实际导入的全部外部 Go 模块。来源依据为标准 Go 命令 `go list -deps -json ./cmd/cpa-workbuddy`，使用 `CGO_ENABLED=1` 与默认 build tags；已查询 Windows amd64、Linux amd64/arm64、macOS amd64/arm64，五个目标的模块集合相同。该集合与 `go.mod` 中保留的全部间接依赖不同。发布时以生成动态库的 `build-info.txt` 为最终链接版本记录，打包入口要求其中每个模块版本均有许可汇总条目。

| 实际链接模块 | 固定版本 | 许可及原文来源文件 |
| --- | --- | --- |
| `github.com/cespare/xxhash/v2` | `v2.3.0` | MIT，`LICENSE.txt` |
| `github.com/gabriel-vasile/mimetype` | `v1.4.3` | MIT，`LICENSE`；内嵌 Go JSON 代码的 BSD 完整声明亦保留 |
| `github.com/gin-contrib/sse` | `v0.1.0` | MIT，`LICENSE` |
| `github.com/gin-gonic/gin` | `v1.10.1` | MIT，`LICENSE`；派生 httprouter 的 BSD-3-Clause 声明另见下文 |
| `github.com/go-playground/locales` | `v0.14.1` | MIT，`LICENSE` |
| `github.com/go-playground/universal-translator` | `v0.18.1` | MIT，`LICENSE` |
| `github.com/go-playground/validator/v10` | `v10.20.0` | MIT，`LICENSE` |
| `github.com/google/uuid` | `v1.6.0` | BSD-3-Clause，`LICENSE` |
| `github.com/leodido/go-urn` | `v1.4.0` | MIT，`LICENSE` |
| `github.com/mattn/go-isatty` | `v0.0.20` | MIT，`LICENSE` |
| `github.com/pelletier/go-toml/v2` | `v2.4.3-0.20260624044523-35f78d5908f1` | MIT，`LICENSE` |
| `github.com/redis/go-redis/v9` | `v9.19.0` | BSD-2-Clause，`LICENSE`；内嵌 Camlistore Apache-2.0、Harald Rudell ISC、Georges Menie CRC16 BSD-3-Clause 声明亦保留 |
| `github.com/router-for-me/CLIProxyAPI/v8` | `v8.0.69` | MIT，`LICENSE` |
| `github.com/router-for-me/moego` | `v0.1.2` | MIT，`LICENSE`；由 `github.com/MoeForge/moego@v0.1.0-alpha.1` 的 Go `replace` 选中，许可按实际替换来源保留 |
| `github.com/sirupsen/logrus` | `v1.9.3` | MIT，`LICENSE`；内嵌 `tebeka/atexit` 的 MIT 声明亦保留 |
| `github.com/ugorji/go/codec` | `v1.2.12` | MIT，`LICENSE` |
| `go.etcd.io/bbolt` | `v1.5.0` | MIT，`LICENSE` |
| `go.uber.org/atomic` | `v1.11.0` | MIT，`LICENSE.txt` |
| `golang.org/x/crypto` | `v0.57.0` | BSD-3-Clause 与专利授权，`LICENSE`、`PATENTS` |
| `golang.org/x/net` | `v0.60.0` | BSD-3-Clause 与专利授权，`LICENSE`、`PATENTS` |
| `golang.org/x/sys` | `v0.48.0` | BSD-3-Clause 与专利授权，`LICENSE`、`PATENTS` |
| `golang.org/x/text` | `v0.42.0` | BSD-3-Clause 与专利授权，`LICENSE`、`PATENTS` |
| `google.golang.org/protobuf` | `v1.34.1` | BSD-3-Clause 与专利授权，`LICENSE`、`PATENTS` |
| `gopkg.in/yaml.v3` | `v3.0.1` | MIT 与 Apache-2.0，`LICENSE`、`NOTICE`；同时保留 libyaml 的版权声明 |

以上各模块的完整许可、NOTICE、PATENTS 及实际导入源文件中的版权声明，集中保存在同一文档目录内的 [Go-DEPENDENCIES.txt](licenses/Go-DEPENDENCIES.txt)。汇总按实际来源模块路径与版本、文件和声明排序，记录 Go 模块 `h1` 校验和、Go 模块代理来源及各许可原文的 SHA256；`BEGIN`/`END` 之间保留来源字节。消费者测试按真实 `go.mod`、`go.sum` 构建生产代码，将动态库模块信息与汇总逐项对应，并与 Go 下载缓存的原始许可字节比较。

汇总同时保留以下嵌入代码的独立归属；它们属于表中模块的源文件，不另计为链接 Go 模块：

- Gin 的 `path.go`、`tree.go` 来源于 Julien Schmidt 的 httprouter；完整 BSD-3-Clause 许可取自 Go 模块代理的 `github.com/julienschmidt/httprouter@v1.3.0/LICENSE`，模块校验和与来源记录在汇总内。
- Logrus 的 `alt_exit.go` 内嵌 `tebeka/atexit` MIT 版权与完整条款；mimetype 的 `internal/json/json.go` 内嵌 Go Authors BSD 版权与完整条款，均从实际依赖源文件保留。
- Redis 的 `internal/once.go` 内嵌 Camlistore Apache-2.0 版权声明；`internal/util/atomic_max.go`、`atomic_min.go` 保留 Harald Rudell 的 ISC 版权及修改说明；完整 ISC 条款取自 [原作者 parl LICENSE](https://raw.githubusercontent.com/haraldrudell/parl/main/LICENSE)，以汇总内 SHA256 固定本次原始文本。
- Redis 的 `internal/hashtag/hashtag.go` 保留 Georges Menie 与 Go Authors 的 CRC16 版权；完整 BSD-3-Clause 条款取自 [原作者 CRC16 源码](https://www.menie.org/georges/embedded/crc16.c) 的许可头，以汇总内 SHA256 固定本次原始文本。

YAML 与 Redis 引用的 Apache License 2.0 完整条款另存为 [Apache-2.0.txt](licenses/Apache-2.0.txt)，原文来自 [Apache Software Foundation](https://www.apache.org/licenses/LICENSE-2.0.txt)，SHA256 为 `cfc7749b96f63bd31c3c42b5c471bf756814053e847c10f3eb003417bc523d30`。Go 依赖汇总、CPA MIT 与 Apache 完整文本均按白名单纳入 ZIP 及 `checksums.sha256`。

WorkBuddy 协议移植的对应函数、上游测试和适配位置见 [兼容性记录](compatibility.md)。根 VERSION 只标识本插件产品版本，与以上上游版本、CPA ABI/schema 和 Go 工具链版本分别维护。

C 系统库和平台 loader 的要求按对应平台依赖记录安装。当前包只收录白名单产物、文档和许可；实际登录凭证、账号响应、原始参考快照、日志缓存及工程工具均不收录。安装前核对版本、SHA256 和平台运行记录，具体见 [部署说明](deployment.md)。
