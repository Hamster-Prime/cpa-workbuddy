# 版权与第三方许可

本项目以根 [LICENSE](../LICENSE) 的 MIT 条款分发。发布包保留该许可证、以下来源说明及所需完整许可文本；参考源码快照不属于运行时依赖，也不随包分发。

| 来源 | 固定依据与使用范围 | 许可与随包位置 |
| --- | --- | --- |
| magpie-community/plugins WorkBuddy | commit `0135b7b0ea979c94033a7712a0c6ca1be8dafb70`，`packages/workbuddy` 版本 `0.2.31`；双站点协议、Schema/请求整形、SSE、工具和兼容策略的 Go 移植。原始字节和哈希由仓库 `_ref/UPSTREAM.json` 固定 | MIT，Copyright (c) 2026 magpie-community contributors；完整声明即根 `LICENSE`，与固定上游根 LICENSE 字节一致 |
| router-for-me/CLIProxyAPI | tag `v7.3.15`，commit `673131f57484517c3a1eae7e36c4cfa7b9bb4efc`；原生 ABI/JSON 宿主契约及随包的 `docs/patches/cpa-v7.3.15.patch`。补丁由本项目维护；完整 CPA 源码树和服务程序需另行取得 | MIT；上游版权原文与完整许可见 [CLIProxyAPI-MIT.txt](licenses/CLIProxyAPI-MIT.txt) |
| Go runtime 与标准库 | CGO 动态库静态链接的 Go runtime/标准库；实际工具链由包内 `build-info.txt` 记录 | Go 的 BSD 许可与专利授权从构建工具链 GOROOT 原样收录为 `licenses/Go-LICENSE`、`licenses/Go-PATENTS` |

WorkBuddy 协议移植的对应函数、上游测试和适配位置见 [兼容性记录](compatibility.md)。根 VERSION 只标识本插件产品版本，与以上上游版本、CPA ABI/schema 和 Go 工具链版本分别维护。

C 系统库和平台 loader 的要求按对应平台依赖记录安装。当前包只收录白名单产物、文档、许可和兼容补丁；实际登录凭证、账号响应、原始参考快照、日志缓存及工程工具均不收录。需使用兼容 CPA 基线与补丁，安装前核对版本、SHA256 和平台运行记录，具体见 [部署说明](deployment.md)。
