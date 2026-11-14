# WorkBuddy 插件部署与人工验收

本文针对 CPA commit `673131f57484517c3a1eae7e36c4cfa7b9bb4efc` 的独立兼容副本、插件 `0.1.0`、C ABI `1`、JSON schema `6`。产品版本以根 [VERSION](../VERSION) 为准。这里的安装步骤不表示发布包已公开，也不表示任意 CPA v7/v8 或同 schema 的宿主均兼容。协议、真实宿主和平台结果以 [兼容性记录](compatibility.md) 为准。

## 安装前确定证据边界

| 证据 | 能证明什么 | 不能据此声称什么 |
| --- | --- | --- |
| 插件公共协议测试、独立 C 原生探针 | 动态库 ABI、注册/status、回调、取消和卸载边界 | 完整 CPA server、管理前端、真实账号或容器已验收 |
| 隔离 CPA Host/Manager/FileTokenStore 测试 | 实际宿主组件加载真实库、合成凭证与协议上游的闭环 | 实际服务程序的 HTTP 路由、用户浏览器或真实账号可用 |
| 完整 CPA server 可执行程序与 HTTP 请求记录 | 指定二进制、配置、管理鉴权及请求路径 | 真实 WorkBuddy 账号或实际页面交互已通过 |
| Docker 中实际 CPA 程序加载 Linux 库、卷重启记录 | 指定镜像摘要、架构、libc 与挂载布局下的运行和恢复 | 其他镜像、arm64、macOS 或真实账号可用 |
| 用户自行完成的真实账号清单 | 清单列出的账号、身份、模型和协议行为 | 未列出的权限、套餐、模型和平台可用 |

部署使用仓库及交付包内的累计补丁 [docs/patches/cpa-v7.3.15.patch](patches/cpa-v7.3.15.patch)，SHA256 `b99eaf584200f7d1c23be728a11583fa0970f785b299afa10cc128a562c0bded`。它针对上述固定 CPA 基线，包含稳定身份保存、刷新计划、凭证代次/CAS、永久认证失效、协议转换、故障边界、模型发现结果原子保存、操作员字段写入和管理日志隐私契约。先在独立 CPA checkout 运行 `git apply --check <补丁绝对路径>`，确认通过后在该 checkout 应用并构建；原参考目录保持只读。
插件 ZIP 提供补丁源码及其 CPA MIT 版权许可，CPA 完整程序和 CPAMC 页面需按固定来源另行构建/取得。补丁并未提交 CPA 上游，也未发布为官方 CPA release。本地完整服务验收程序及日志保留在被忽略的 `out/`，与可从仓库或 ZIP 获取的安装输入分别记录。


原始 `../.cpa-upstream-src/` 和 `_ref/` 保持只读。复现仅使用新建的隔离工作目录和专用凭证目录，不指向已有用户 auth 目录。记录 CPA 基线 commit、累计补丁 SHA256、二进制/动态库 SHA256、插件版本、OS/arch、Go/C 编译器和 libc；自编译程序的版本头可能为 `dev`，此时以 commit、补丁和文件摘要共同标识，不能把 `dev` 当正式版本。

## 包校验与运行依赖

本地包为 `out/dist/cpa-workbuddy-v<VERSION>-<goos>-<goarch>.zip`，同目录的 `.zip.sha256` 记录完整 ZIP 的 SHA256。先用 SHA256 文件校验工具核对 ZIP，再解压并按 `checksums.sha256` 核对各成员；该内层清单覆盖除清单自身以外的全部文件。保留文档目录层级以便本地链接可用，确认 `VERSION` 与 `build-info.txt` 的产品版本一致，并核对实际 GOOS、GOARCH、Go 与 CGO 构建设置。

包内包含动态库、生成头、native.h、版本、许可、安装/契约文档和固定 CPA 兼容补丁。`licenses/Go-LICENSE`、`licenses/Go-PATENTS` 从实际 Go 工具链取得；CPA 及协议移植的版权见 [版权与许可](third-party-notices.md)。凭证和运行日志属于宿主管理的数据，不属于插件安装包。

本机 Windows amd64 库由 GCC14.2.0（MinGW-Builds，MCF/SEH/UCRT）构建，PE 实际导入 `KERNEL32.dll`、UCRT API DLL 和 `libmcfgthread-1.dll`。本插件包不分发该 MCF 运行库；部署时需从相同编译器发行方提供匹配的 amd64 运行库，使 CPA 进程能够通过其程序目录或进程 PATH 解析。CPA Windows loader 会先把插件阴影复制再 `LoadDLL`（`internal/pluginhost/loader_windows.go:73-78`）；按实际 CPA 进程验证依赖加载。采用其他 C 工具链重新构建时需重新核对导入，不能沿用本机这一依赖清单。

本机 Linux amd64 库由 Ubuntu GCC13.3.0/glibc2.39 构建，实际 ELF `NEEDED` 仅 `libc.so.6`，最高所需 glibc 符号为 `GLIBC_2.34`。完整宿主加载在 Debian/glibc2.41 容器通过；这组证据不覆盖 musl、其他 glibc 环境、arm64 或 macOS。五平台各自的实际构建、宿主加载与未验证项见 [平台记录](compatibility.md#slice-14-多平台构建与本地交付)。

## 构建、安装与版本确认

1. 在独立 CPA checkout 固定到上述 commit，先对累计补丁作适用性检查，再应用到该 checkout；保留原始基线。构建入口是 `./cmd/server`，插件入口是本仓库 `go run ./cmd/project build`，输出为 `out/bin/cpa-workbuddy.dll`、`.so` 或 `.dylib`。工程工具链固定 Go `1.27.1` 和可用的目标 C 编译器；缓存和中间产物布局见 [质量契约](quality.md)。不要修改原始参考目录来复现。
2. Linux 宿主也必须以 `CGO_ENABLED=1` 编译：固定宿主 `internal/pluginhost/loader_unix.go:1-7` 的 loader 依赖 cgo/`libdl`；`loader_unsupported.go:1-10` 明确拒绝非 cgo Linux 加载。不能用一个 `CGO_ENABLED=0` 的常规 CPA Linux 二进制搭配插件后宣称支持。
3. 同时匹配 OS、CPU 架构及动态链接依赖。glibc 构建的 `.so` 不能直接假定可在 musl/Alpine 中加载；构建机 glibc 较新也不能假定旧运行环境满足符号版本。使用目标运行环境兼容的 C 编译器/sysroot 构建宿主和库，记录 loader、依赖与 libc 版本，并在最终运行环境实际加载。macOS/arm64 无对应运行记录时仍是未验证。
4. 将库放到专用 `plugins.dir` 的根目录，文件名使用 `cpa-workbuddy` 加平台扩展名。宿主也会检查 `<plugins.dir>/<goos>/<goarch>/`，但首次部署保持单一候选，避免不同版本库选择混淆。无需运行该库，也没有插件独立服务。
5. 启动 CPA 时显式指定 `-config <配置文件绝对路径>`。Linux 兼容宿主的编译形状为 `CGO_ENABLED=1 go build -trimpath -o <隔离输出目录>/cli-proxy-api ./cmd/server`，在已应用补丁的 CPA checkout 执行；这是构建操作说明，运行证据必须另附。具体 Docker 构建文件、工具链和镜像摘要只采用本次实际验收记录，不能套用未核实的官方镜像 tag。

下面是配置片段；绝对路径由部署者选择，密钥标记必须替换成新生成的各自独立密钥，不能直接运行示例值。本机首次验证可将 `host` 改为 `127.0.0.1`、`allow-remote` 改为 `false`。容器内 `host: ""` 允许容器接口接收请求；主机映射端口的地址范围另由 Docker/反向代理限制。

```yaml
host: ""
port: 8317
auth-dir: "/var/lib/cpa/auth"
remote-management:
  allow-remote: true
  secret-key: "<新生成的管理密钥>"
  disable-control-panel: false
  disable-auto-update-panel: true
plugins:
  enabled: true
  dir: "/opt/cpa/plugins"
  configs:
    cpa-workbuddy:
      enabled: true
api-keys:
  - "<另一个新生成的客户端密钥>"
debug: false
logging-to-file: false
request-log: false
proxy-url: ""
```

两个 `enabled` 都必须为 `true`。管理接口的 `Authorization: Bearer <管理密钥>` 或 `X-Management-Key: <管理密钥>` 复用 CPA 现有鉴权；客户端 `api-keys` 只用于模型 API，不能互换。`remote-management.secret-key` 为空且没有其他已核实的启用方式时管理路由不可用；配置中的明文管理密钥会在启动时被哈希，首次启动须允许配置写入，或预置目标宿主认可的哈希。修改配置的管理动作也需要配置所在位置可写。`MANAGEMENT_PASSWORD` 是固定宿主真实支持的环境变量，但非空时还会覆盖远程访问限制（`handler.go:73-83,318-320`）；不要把它误当只替代 secret-key 且不改变访问策略的变量。本文不定义虚构的插件 token、站点或 auth-dir 环境变量。

先发送以下请求并只保留脱敏摘要。下文所有 HTTP 示例相对同一个 CPA origin；管理请求均使用上面的管理头，带 JSON 的请求加 `Content-Type: application/json`。

```http
GET /v0/management/plugins
Authorization: Bearer <管理密钥>

GET /v0/management/plugins/cpa-workbuddy/status
Authorization: Bearer <管理密钥>
```

`plugins` 列表中核对 `id=cpa-workbuddy`、`registered=true`、`effective_enabled=true`、`supports_oauth=true`、`oauth_provider=workbuddy` 和实际库路径；`metadata.version` 应为 `0.1.0`。状态接口预期 JSON 为：

```json
{"name":"cpa-workbuddy","version":"0.1.0","abi_version":1,"schema_version":6}
```

同时记录管理响应头 `X-CPA-VERSION`、`X-CPA-COMMIT`、`X-CPA-BUILD-DATE`、`X-CPA-SUPPORT-PLUGIN`。`registered` 和状态请求证明库实际注册/响应；只看到文件存在或 configured/enabled 不足以证明加载。状态响应不能单独证明累计保存/转换契约全部可用，应继续运行对应 HTTP/重启验收。

## 固定管理前端与双站点扩展边界

2026-11-14 直接读取官方 release 页面、API 和源码后，本文选择 **CPAMC `v1.24.2` / commit `4530da271ba2e89810d4dccebc57f3091afa590a`** 作为固定 v7 管理前端来源。其 [README](https://github.com/router-for-me/Cli-Proxy-API-Management-Center/blob/4530da271ba2e89810d4dccebc57f3091afa590a/README.md) 和 [src/utils/constants.ts:17](https://github.com/router-for-me/Cli-Proxy-API-Management-Center/blob/4530da271ba2e89810d4dccebc57f3091afa590a/src/utils/constants.ts#L17) 使用 `/v0/management`；这是源码兼容依据，实际页面及全部管理行为仍须在目标服务核验。

- [官方 v1.24.2 release 页面](https://github.com/router-for-me/Cli-Proxy-API-Management-Center/releases/tag/v1.24.2) 与 [完整 release API](https://api.github.com/repos/router-for-me/Cli-Proxy-API-Management-Center/releases/tags/v1.24.2)：`management.html` 为 2,767,070 bytes，官方 API 摘要 `sha256:51b24db8170a5414875602c2f4e35017185b1c1dd7e9a6b1c0de69905a81e997`。
- [固定 HTML 下载地址](https://github.com/router-for-me/Cli-Proxy-API-Management-Center/releases/download/v1.24.2/management.html)。下载到隔离目录后，先核对大小和上述 SHA256，再安装为 `<静态资源目录>/management.html`；不把官方摘要记录本身称为已下载文件核验。
- 将环境变量 `MANAGEMENT_STATIC_PATH` 设为安装后的 **HTML 绝对路径**，例如 `/opt/cpa/static/management.html`；保留配置 `disable-auto-update-panel: true`，预先放好文件。`GET /management.html` 是服务端证明面：确认 HTTP 成功，并核对返回正文 SHA256 与固定文件相同；无需打开可见浏览器来完成静态服务验证。
- 关闭自动更新只阻止周期更新；文件缺失时首次 `GET /management.html` 仍可能同步下载。必须预装固定文件，不能让缺文件路径下载 latest 后继续把它标成 v1.24.2。暂不部署管理页面时可设 `disable-control-panel: true`，仍通过现有管理 API 操作。

固定宿主 updater 默认指向官方 `releases/latest`，并有 `https://cpamc.router-for.me/` 回退。2026-11-14 读取的官方 main 为 `3d893ec88938c3ceef200a15be6af8cf972564b3`，[该版本 README](https://github.com/router-for-me/Cli-Proxy-API-Management-Center/blob/3d893ec88938c3ceef200a15be6af8cf972564b3/README.md) 已要求 CPA ≥8，使用 `/v8/management` 且无 v0 fallback。因此不能将 latest/main 自动当作本文固定 v7 宿主的前端。

已核实的扩展点是 **CPA 管理插件列表 → CPAMC OAuth 页动态 provider**，不是插件独立页面：

| 层 | 核实结果 | 证据 |
| --- | --- | --- |
| CPA 管理列表 | 返回 `supports_oauth`、单个 `oauth_provider`、`effective_enabled`、metadata | 兼容副本 `internal/api/handlers/management/plugins.go:27-41,120-145` |
| CPA 动态登录路由 | `/<provider>-auth-url` 解析 provider，query 透传 metadata；经管理 middleware | `auth_files_oauth_callback.go:148-220`；`server_management.go:253-284` |
| 固定 CPAMC 动态入口 | `buildPluginOAuthProviderCards` 按有效插件的单个 oauthProvider 生成入口 | [OAuthPage.tsx:183-214](https://github.com/router-for-me/Cli-Proxy-API-Management-Center/blob/4530da271ba2e89810d4dccebc57f3091afa590a/src/pages/OAuthPage.tsx#L183-L214)，`:321-345` 加载插件列表 |
| 固定 CPAMC 发起登录 | `startAuth(provider, signal)` 只生成 provider 路径；没有 `site` 参数；同页 `:499` 调用该方法 | [oauth.ts:41-51](https://github.com/router-for-me/Cli-Proxy-API-Management-Center/blob/4530da271ba2e89810d4dccebc57f3091afa590a/src/services/api/oauth.ts#L41-L51) |
| 固定 CPAMC 轮询 | 使用 `/get-auth-status` 并传 state | 同文件 `:54-58` |

**固定前端尚不能据上述入口直接选择 WorkBuddy 国内/国际。** WorkBuddy 必须显式传 `site`，不能把一个未带站点的动态按钮当成双站点支持。外部改动范围已定位为 `router-for-me/Cli-Proxy-API-Management-Center` 的 OAuth 页和 API adapter：保留 provider `workbuddy`，增加国内/国际选择并把 `site=workbuddy` 或 `site=workbuddy-ai` 传给已有 auth-url；登录尝试状态需隔离站点，保留原有轮询和凭证列表。若希望宿主声明通用站点选项，则还涉及 CPA 的插件 metadata/管理列表契约，不能假定现有单 oauth_provider 已包含该字段。本文没有实现这些外部改动，也没有验证真实按钮、卡片颜色、登录页布局或 WeCom。下面的 API 操作提供可复现后端路径，不替代“现有管理前端完整双站点登录”的人工验收。
外部前端的具体接线为：`OAuthPage.tsx:479-520` 将 `startAuth(provider)` 扩展为携带本次站点，替换 `:499` 对 adapter 的调用；`oauth.ts:41-51` 在保留原有 provider/path 的前提下把站点加入 query。两个站点的尝试键不能仅以 `workbuddy` provider 为键。宿主列表的实际 JSON 字段仍为 `supports_oauth`、`oauth_provider`、`effective_enabled`、`metadata`，没有可凭空使用的 `site_options` 或独立 `workbuddy-ai` provider。已有动态入口没有传 site，因而不能完成当前要求显式站点的 WorkBuddy 登录。


## 双站点浏览器登录

国内和国际都使用同一个 provider `workbuddy`，只有 `site` 不同。国际路径不是 `workbuddy-ai-auth-url`：

```http
GET /v0/management/workbuddy-auth-url?site=workbuddy
Authorization: Bearer <管理密钥>

GET /v0/management/workbuddy-auth-url?site=workbuddy-ai
Authorization: Bearer <管理密钥>
```

成功返回 `{"status":"ok","url":"<本次敏感登录URL>","state":"<本次敏感state>"}`。每次登录分别保管其 state，不在两个站点间复用。用户在自己的浏览器中打开本次 `url`，完成 WorkBuddy 账号登录和网站实际要求的个人/企业身份选择、授权或 WeCom 步骤；自动化不操作可见桌面，也不代替用户扫码。不要把 CPA 管理密钥传给 WorkBuddy 登录页。

由有管理鉴权的客户端每隔约 1–3 秒查询，直到结束或最多五分钟会话窗口：

```http
GET /v0/management/get-auth-status?state=<URL编码后的本次state>
Authorization: Bearer <管理密钥>
```

- `status=wait`：仍等待浏览器/token/账号确认；HTTP 200 本身不是成功。
- `status=ok`：对这条有效登录 state，宿主已完成凭证保存；随后必须查看列表和模型。**state 不得省略**：固定处理器对空 state 也返回 ok，不能用空请求当登录证据。
- `status=error`：读取安全错误摘要；超时、重启、未知 state、取消或账号确认失败后重新发起登录。服务端会话与插件会话都不跨进程保留；不要继续重放已消费会话来创建重复凭证。

取消本次管理会话可发送 `DELETE /v0/management/oauth-session?state=<编码后的state>`；停止查询并在需要时发起新会话。取消会话不等于删除已有凭证。WorkBuddy 使用服务端轮询，容器无需浏览器、桌面、宿主机回调端口映射或导入桌面凭证；不要将其他 provider 的手动 callback 流程套在这里。

登录 URL、state、access/refresh token 和完整账号响应均属敏感材料。不要把浏览器地址栏、轮询 query、完整 refresh 响应或凭证 JSON 写入日志、截图和问题报告。

## 凭证列表、身份恢复与管理动作

```http
GET /v0/management/auth-files
GET /v0/management/auth-files?name=<URL编码后的列表name>
GET /v0/management/auth-files/models?name=<URL编码后的列表name>
```

这些仍须带管理鉴权。列表返回 `files`；记录对应的 `id`、`auth_index`、`name`、provider、`status`、`status_message`、`disabled`、`unavailable` 和可用的刷新时间，公开报告只用脱敏的身份标签/ID 摘要。不要从显示昵称猜文件名。单 provider 在列表都可能显示 workbuddy，站点以登录来源和模型前缀核对；卡片具体站点标签/企业细节是否显示属于前端未验证项目。

稳定身份为 `site + uid + enterprise/tenant scope`。昵称变化不创建新身份；同一身份重登应回到原 ID/FileName，不需要先删除失效记录。不同站点、个人/企业、两个企业分别保存。成功安装新代次后应清除旧认证失效和对应认证冷却、刷新计划和模型目录；保留手动禁用、priority、proxy_url、排除及别名等宿主管理设置。手动禁用记录重登后仍应禁用，额度或其他非认证限制不会因重登自动解除。取消、超时、账号确认/保存失败不得清理旧失效或声称登录成功。

手动禁用的账号重登后保持禁用，成功登录会清除旧模型快照；宿主只在显式启用后重新发现模型。验收时先确认重登没有解除禁用，再启用并等待账号快照和目录恢复，随后检查真实请求和重启。模型发现产生的 AuthUpdate 必须经兼容宿主的凭证 CAS 落盘，不能只检查内存 `/v1/models` 已有条目。

以下 `name` 均取自实际列表，示例中的文件名只是占位。每步检查 HTTP 状态、响应体、重新读取列表、实际调度及重启恢复，不仅看按钮或 HTTP 200：

```http
PATCH /v0/management/auth-files/status
Content-Type: application/json

{"name":"<列表中的name>","disabled":true}
```

恢复手动启用使用相同请求并将 `disabled` 改为 `false`。启用不应解除永久认证失效；这时需要同身份重新登录。

```http
POST /v0/management/auth-files/refresh
Content-Type: application/json

{"name":"<列表中的name>"}
```

刷新共用插件协调及 CPA 存储；成功后核对列表刷新时间和重启恢复。该宿主处理器成功时返回 `auth` 对象，应在受信任客户端内检查后丢弃，不将完整对象写进验收报告。`all=true` 会触及所有凭证，人工验收优先逐条刷新。

```http
DELETE /v0/management/auth-files?name=<URL编码后的列表name>
```

删除是删除凭证与路由资格，不是暂时停用；真实账号执行前先明确选择目标。删除后检查列表消失、该 auth 不再被命中及重启后不恢复。

模型规则使用**带站点前缀的源 ID**，`AuthData.Prefix` 保持空。下面的模型 ID 必须替换成对应账号实际目录中的 ID；不能因为示例写了模型就假定该账号有权限。

```http
PATCH /v0/management/auth-files/fields
Content-Type: application/json

{"name":"<列表中的name>","priority":7,"excluded_models":["wb/<国内上游ID>"],"model_aliases":[{"name":"wb/<另一个国内上游ID>","alias":"wb/manual-domestic","fork":true}]}
```

这是单凭证字段修改，`excluded_models`/`model_aliases` 为凭证 JSON 字段；不要用 `headers` 覆盖 token、用户、企业或 domain。随后重读该凭证的模型列表确认 watcher/注册更新。全局规则则在 provider `workbuddy` 下同时容纳两个站点：

```yaml
oauth-excluded-models:
  workbuddy:
    - "wb/<国内上游ID>"
    - "wb-ai/<国际上游ID>"
oauth-model-alias:
  workbuddy:
    - name: "wb/<国内上游ID>"
      alias: "wb/manual-domestic"
      fork: true
    - name: "wb-ai/<国际上游ID>"
      alias: "wb-ai/manual-international"
      fork: true
```

等价的 provider 级 API 分别为 `PATCH /v0/management/oauth-excluded-models`、body `{"provider":"workbuddy","models":["wb/<ID>"]}`，以及 `PATCH /v0/management/oauth-model-alias`、body `{"provider":"workbuddy","aliases":[{"name":"wb-ai/<ID>","alias":"wb-ai/manual-international","fork":true}]}`。这些 PATCH 替换该 provider 的列表，先读取对应 GET、保留已有规则；`PUT` 的范围更大，不用只含 WorkBuddy 的 body 无意覆盖其他 provider。

单凭证 aliases 优先于相同客户端别名的全局映射。排除先于别名；带前缀的源模型不存在于选中账号时，别名不能增加其权限。保留两个站点不同的客户端别名并核对解析后的执行模型；不要给两个站点设置同一个模糊别名来验收隔离。全局 `/v1/models` 合并不等于各 auth 的权限/图片/effort 相同，也不能让国内凭证获得 `wb-ai/*` 调度资格。

## 无桌面容器的路径、持久化与代理

采用已应用累计补丁且带 cgo loader 的 Linux CPA 二进制和同架构 Linux `.so`；Windows DLL 不能挂到 Linux 容器使用。容器的具体 image/digest、Go/C/libc 及实际执行结果应与兼容性记录逐项一致。本文的目录布局是可选部署约定，不是未经核实的官方镜像内置路径：

| 容器路径 | 挂载与权限 | 原因 |
| --- | --- | --- |
| `/opt/cpa/cli-proxy-api` | 已核验二进制，可只读 | 启动程序，显式 `-config /etc/cpa/config.yaml` |
| `/opt/cpa/plugins/cpa-workbuddy.so` | 已核验库，可只读 | `plugins.dir=/opt/cpa/plugins`，启动期间不覆盖正在加载的库 |
| `/etc/cpa/config.yaml` | 持久配置；需要管理修改时可写 | 启动密钥哈希、插件启停和全局规则保存需要写入 |
| `/var/lib/cpa/auth` | **专用持久卷，CPA 运行用户可读写并可原子替换** | token 轮换、稳定身份、失效状态、刷新时间和账号模型快照 |
| `/opt/cpa/static/management.html` | 固定 v1.24.2 文件，可只读 | `MANAGEMENT_STATIC_PATH` 显式指向文件，关闭自动更新 |

凭证目录需要目录级写权限和替换权限，仅给旧 JSON 文件写权限不够。不要将卷挂载为空目录遮蔽已准备的库或静态文件。凭证卷不需要和开发机相同绝对路径；凭证身份不依赖 Windows 盘符。原始证据/合成 fixture/日志均进入本仓库 `out/`，真实凭证仅在部署者指定的专用受保护卷中，不能放入镜像层、仓库或发布包。

[INFERENCE] 容器的普通 bridge 网络映射通常使来自主机的请求被 CPA 视为非 localhost；以实际 `403 remote management disabled` 和隔离网络记录判断。选择 loopback 发布端口、可信 TLS 反向代理或受控网络后再启用所需远程管理。`trusted-proxies` 只列真实可信转发代理，并按配置要求重启；不要依靠任意 X-Forwarded-For 绕过管理策略。用户浏览器访问 CPA 管理 origin 和 WorkBuddy 登录站点，CPA 容器访问固定上游 API；只需 CPA 服务端可达及正确 DNS/TLS/时钟，不需要桌面。

上游代理使用真实 CPA `proxy-url`；单凭证代理通过 `PATCH /auth-files/fields` 的 `proxy_url` 字段设置，例如 `{"name":"<name>","proxy_url":"http://<代理地址>:<端口>"}`。`http_bridge.go:224-235` 按请求覆盖 → auth.ProxyURL → cfg.ProxyURL 选择；登录尚无 auth 时应配置宿主全局代理，不能假定某个旧凭证的 proxy_url 影响新登录。`direct`/`none` 可显式绕过配置/环境代理，具体支持以目标宿主解析器为准。服务端代理不改变用户浏览器自己的网络设置。容器中的 `127.0.0.1` 指容器本身，代理地址必须从容器可达。

核验代理时用受控代理只记录目标 host/端口、连接次数和结果，分别覆盖登录 state/token/account、模型发现、刷新和推理；再故意拒绝代理确认请求确实失败。不要记录 CONNECT 后的解密正文、Authorization、X-Refresh-Token 或账号响应。已有宿主组件代理测试不替代最终容器的代理连通证据。

宿主 `MaskSensitiveHeaderValue` 已对含 authorization/api-key/token/secret 的头调用掩码，但这不是完整的隐私清理：用户/企业/租户标识头、管理密钥自定义头及 state query 必须单独核验。部署片段显式设 `debug:false`、`logging-to-file:false` 和真实配置键 `request-log:false`（`internal/config/sdk_config.go:45-46`），仍须检查反向代理、容器日志驱动、Gin access log 和错误日志路径；这些开关不证明 access log 的 state query 已脱敏。代理 access log 对管理登录/轮询路径不记录 query，移除敏感头，不捕获登录/refresh body；人工证据只保留安全 code/HTTP status、站点、模型、脱敏 auth ID、时间和结果。不能将一次 Authorization 掩码成功泛化为所有敏感头都安全。

Slice 13 兼容宿主还必须包含 WorkBuddy 隐私修正：`X-User-Id`、`X-Enterprise-Id`、`X-Tenant-Id` 的日志值完整替换为 `[REDACTED]`，`state` query 在实际 Gin access log 路径也完整替换，涵盖大小写、编码键及 `[]` 形式；以累计补丁和实际中间件回归/服务证据确认，而非只调用一个工具函数。原始固定 CPA 基线的通用掩码不满足这项要求。该修正仍不等于任意 JSON 正文自动脱敏，凭证下载、refresh 的 auth 响应、登录 URL 和账号正文继续禁止捕获。隐私验收的通过状态以兼容性记录中对应运行证据为准，此处仅规定所需契约。

重启验收保留同一个 auth 卷和持久配置，停止旧容器后以相同已核验 image/digest 和库重新创建。比较凭证条数/稳定 ID、disabled、proxy/priority/规则、永久失效状态、刷新时间和账号快照，并实际请求确认健康账号恢复、失效和手动禁用账号不命中。重新登录恢复后再重启一次，验证新代次保存；只读文件存在不能证明恢复成功。

### 本地隔离复现

以下命令使用本次交接保留的 `out/verification/cpa-live/main.go`、兼容 CPA 二进制及已校验的固定 HTML，全部为本地产物，不是首次克隆即有的公开工具。先按上文构建对应宿主和插件；每次指定一个尚无凭证的新 proof 目录。设施使用合成上游与专用 CA，不能把其结果当成真实账号验收。

Windows PowerShell 在项目根目录执行：

```powershell
go build -o out/verification/cpa-live.exe out/verification/cpa-live/main.go
$env:MANAGEMENT_STATIC_PATH = "$PWD/out/verification/cpa-management-static/management.html"
$env:GODEBUG = "x509sslcertoverrideplatform=1"
./out/verification/cpa-live.exe --server "$PWD/out/verification/cpa-server.exe" --plugin-dir "$PWD/out/bin" --workdir "$PWD/out/verification/local-proof-windows" --management-sha256 51b24db8170a5414875602c2f4e35017185b1c1dd7e9a6b1c0de69905a81e997
```

该 `GODEBUG` 仅让 Go1.27 的隔离进程读取 proof 专用 CA，不修改系统信任库。生产部署按真实证书与宿主正常信任策略运行，不使用 proof CA。Linux 在 Docker daemon 所在环境执行，Go/C 工具链及宿主/库均为 Linux amd64：

```sh
go build -o out/verification/cpa-live-linux out/verification/cpa-live/main.go
MANAGEMENT_STATIC_PATH="$PWD/out/verification/cpa-management-static/management.html" \
  ./out/verification/cpa-live-linux \
  --server "$PWD/out/verification/cpa-server-linux" \
  --plugin-dir "$PWD/out/bin" \
  --workdir "$PWD/out/verification/local-proof-container" \
  --proxy-listen 127.0.0.1:18889 --port 18890 \
  --docker-image sha256:c038a8ef24ab88bc8edacfc93704c9bf301647b9253a59077f6f5342f39727ae \
  --management-sha256 51b24db8170a5414875602c2f4e35017185b1c1dd7e9a6b1c0de69905a81e997
```

镜像必须已在本地，runner 使用 `--pull=never`，不自动获取新 tag。`--network=host` 使隔离代理与容器通信；这是本地验收网络，不是公开服务的推荐网络布局。内部程序、配置、凭证与页面使用 `/opt/cpa`、`/proof` 路径，退出后容器移除，专用 proof 目录保留供检查。期望 JSON 为 `checks_passed=62`、`checks_failed=0`，同时核对四实际 auth ID、24 个跨协议场景及 12 个工具结果闭环计数；任何非零 exit 或失败项都不是通过。

## 故障排查

| 观察/安全错误 | 检查与处理 |
| --- | --- |
| 管理 404 | 检查实际 origin、`/v0/management`、管理 key 是否启用、Home 模式边界及插件有效启用；`/management.html` 404 还检查 disable-control-panel、固定静态路径和文件权限 |
| 管理 401/403 | 使用管理 key；检查 allow-remote、MANAGEMENT_PASSWORD 的覆盖、可信代理与源 IP；反复错误会触发临时 IP 禁用，停止重复尝试 |
| configured/enabled 但未 registered | 检查实际库路径、平台/架构、依赖和 loader 错误；Linux 确认 CPA 以 cgo 构建；不要只检查插件自己的 CGO |
| `incompatible_schema` / `unsupported_host_contract` | 核对 ABI/schema 和固定 CPA 基线+完整累计补丁；后一错误表示缺少 generation-safe credential-state 契约，不应靠重登反复消费 refresh token |
| auth-url 失败、状态一直 wait | 显式选 site，确认 state/token/account 的宿主出网、代理、时钟和证书；浏览器完成指定个人/企业身份；五分钟后重开会话，不转抄 token |
| 轮询 ok 但无可用模型 | 核对非空的本次 state、列表实际保存、disabled/失效和单 auth 模型目录。首次发现失败不授予静态模型权限；修复上游后由重新登录、文件 watcher auth update 或配置重注册重新发现 |
| `credential_read_failed`、持久化/保存错误 | 检查专用 auth 卷权限、空间、原子替换、配置路径和 Manager 安装结果；轮换后的内存凭证可能暂时可用，未确认落盘前不能承诺重启恢复，不再次消费旧 refresh token |
| `reauth_required` / 明确 invalid_grant/revoked | 保留原记录，同 site/uid/企业身份重登；检查原 ID、认证错误清理、管理设置、模型和重启。普通单次 401、网络故障不直接当永久失效 |
| `unsupported_model` / `unsupported_format` | 从所选 auth 的目录选带正确站点前缀的模型；核对累计宿主的 NativeFormat/客户端翻译，不改投另一站点或手工删除前缀 |
| `account_restricted` / 11140 | 账号风控，按站点要求处理；不把它当 token 过期 |
| `credit_package_exhausted` / 14018 | 账号额度/套餐不足；更换请求范围或处理套餐，重登不保证解决 |
| `model_permission_denied` / HTTP 403 | 核对该账号实际模型权限；全局模型显示不能证明所选账号有权限 |
| `client_version_rejected` / 10085 | 当前 WorkBuddy 协议/客户端版本被拒绝，保留安全错误和固定插件构建信息；更新需重新核验协议，不靠任意改 UA 掩盖 |
| 11128 / 11153 / 11159 | 对应 `channel_rejected`、`tool_continuation_rejected`、`thinking_signature_rejected`；内部有界纠正耗尽后明确失败，保留脱敏请求形状，禁止添加无限重试 |
| `context_length_exceeded` | 缩短上下文/输出预算并按当前账号限额重新请求 |
| `rate_limit_exceeded`、`upstream_unavailable`、`upstream_timeout` | 429/5xx/超时交给宿主策略和合法 Retry-After；插件不叠加状态码循环。客户端已收到回答后不重放整轮 |
| 截断、取消或超时后的流失败 | 结果为失败/中断，不能把部分内容当完整成功；检查上游关闭、客户端取消和宿主超时策略 |

## 真实账号人工验收单

以下项目均需部署者实际填写“通过 / 失败 / 未验证及具体原因”，不能由合成协议或静态页面源码代勾。每条记录含日期、CPA 基线/补丁/二进制摘要、插件版本/库摘要、前端 tag/commit/HTML 摘要、平台/镜像摘要/libc、脱敏配置、客户端协议/流模式、模型 ID、脱敏实际命中 auth ID、结果及证据位置。账号、凭证 JSON、完整 URL/state 和 header 不放报告。

- [ ] 国内至少两条、国际至少两条真实凭证登录；记录每条 site、个人/企业作用域和稳定 ID。用户完成浏览器/WeCom，现有管理前端明确选站点、保存和列表；若前端外部扩展未实现，明确记录该项未通过，API 成功不能代替 UI AC。
- [ ] 同 uid 的个人与企业、两个企业、双站点分别独立；昵称变化后同身份重登仍同一条。失效重登不先删记录，认证状态恢复且 disabled、priority、代理、排除和别名保留。取消/超时/账号确认失败/保存失败不清除旧状态。
- [ ] 对每账号记录成功模型快照时间与实际 CLI/custom 模型；按权限制造账号间模型/图片/effort 差异。首次失败无静态资格；临时失败仅沿用该账号旧快照，明确认证失效则停止候选。
- [ ] 每站点各两条凭证分别实际命中 auth ID；可依次手动禁用其他候选确定目标，再恢复配置。不同站点同名模型绝不混用；全局合并不增加任一账号权限。单凭证与全局排除/别名分别验证，源 ID 带 wb/ 或 wb-ai/。
- [ ] OpenAI Chat `POST /v1/chat/completions`、Responses `POST /v1/responses`、Claude Messages `POST /v1/messages`、Gemini `POST /v1beta/models/<模型>:generateContent`/`:streamGenerateContent` 均走 CPA 客户端鉴权，使用实际目录 ID、各协议合法 payload。Gemini 模型路径须按客户端规则编码含 `/` 的模型 ID。每种协议记录代表性的流式、非流式、文本、多轮、工具调用→客户端执行→工具结果返回、usage 和停止原因。
- [ ] 国内 Claude 无论客户端格式都命中本站点 `/v1/messages`；国内其他模型及国际全部模型命中对应站点 `/v2/chat/completions`。验证宿主翻译的双向工具/reasoning 语义，不只记录端点 HTTP 200。
- [ ] 对明确支持的账号/模型测图片输入及 reasoning/effort；国际 GLM 图片不支持应明确拒绝。不能用图片生成、音视频、embeddings 或计数/任意 HTTP 转发替代当前范围；缺模型权限/资源时逐项写出账号、协议、模型及待执行步骤。
- [ ] 单条手动刷新、计划刷新、首次 401 一次恢复、明确永久失效分别检查；过期但可刷新与网络/额度/权限失败不误标永久失效。失效仍在列表、停止调度且不用旧模型快照恢复资格。只记录安全分类，不为真实账号主动泄露/伪造 token。
- [ ] 容器无桌面，由远端用户浏览器登录；相同持久卷重启后条数、ID、刷新时间、失效状态、手动禁用和快照恢复。恢复登录后再次重启仍健康，禁用/删除仍有效。
- [ ] 代理分别覆盖登录、目录、刷新、Chat/Messages；拒绝代理时请求失败。检查 Authorization、X-Refresh-Token、管理密钥、X-User-Id、X-Enterprise-Id、X-Tenant-Id、state query、正文和错误日志无原值。
- [ ] 客户端取消、超时、插件停用/卸载会关闭在途上游和流；停止后不再发新请求。重新启用/重启后只恢复已持久化的合法状态。停用插件用 `PATCH /v0/management/plugins/cpa-workbuddy/enabled`、body `{"enabled":false}`，重新启用反向操作；卸载/删除库另行记录，不能用删凭证代替插件生命周期。

## 源码证据索引

宿主位置均相对于 `out/verification/cpa-compatible/`，固定官方基线可从 [CPA commit 页面](https://github.com/router-for-me/CLIProxyAPI/tree/673131f57484517c3a1eae7e36c4cfa7b9bb4efc) 和 [该版本 config.example.yaml](https://github.com/router-for-me/CLIProxyAPI/blob/673131f57484517c3a1eae7e36c4cfa7b9bb4efc/config.example.yaml) 获取；兼容补丁改动以本地累计 patch 为准。官方 README 的管理文档链接为 [Management API](https://help.router-for.me/management/api)，在线文档可能随最新版变化，本文精确字段以固定源码为准。以上官方前端 release、README、源码和 API 均于 **2026-11-14** 读取正文，未以搜索摘要替代依据。

| 本文事实 | 文件与行号 |
| --- | --- |
| bind、管理 key/远程限制、静态资源配置、auth-dir | `config.example.yaml:1-44` |
| 双 enabled、插件目录、CGO 与候选发现 | `config.example.yaml:107-136`；`internal/pluginhost/platform.go:62-95,119-145,308-313` |
| 启动配置参数 | `cmd/server/main.go:152` |
| 管理头与版本头、环境覆盖 | `internal/api/handlers/management/handler.go:73-83,263-320` |
| 原生注册版本/status | 本项目 `internal/plugin/runtime.go:17-22,197-209` |
| 管理插件实际注册/能力状态 | `internal/api/handlers/management/plugins.go:27-41,120-158` |
| 动态 auth-url 路径/metadata/结果及鉴权 | `auth_files_oauth_callback.go:148-220`；`internal/api/server_management.go:253-284` |
| 空 state 陷阱、wait/error/成功保存 | `auth_files_provider_oauth.go:929-1003` |
| 列表、单 auth 模型、状态和刷新时间 | `auth_files.go:117-173,333-377,655-665,722-723` |
| 管理路由/刷新/禁用/字段 patch | `internal/api/server_management.go:179-199`；`auth_files_refresh.go:12-75`；`auth_files_fields.go:27-146,258-415` |
| 全局排除/别名 API；单 auth 别名约定 | `config_lists.go:1211-1272,1296-1366`；`config.example.yaml:837-879` |
| 排除先于别名，prefix 最后应用 | `sdk/cliproxy/service_executors.go:549-565` |
| updater latest/回退、关闭更新和静态路径 | `internal/managementasset/updater.go:28-34,107-119,143-186`；`internal/api/server_management.go:309-336` |
| 宿主网络代理优先级 | `internal/pluginhost/http_bridge.go:224-235,550-570` |
| 敏感头/query 掩码边界 | `internal/util/provider.go:227-239,280-292`；`internal/runtime/executor/helps/logging_helpers.go:635-656` |
| 安全错误分类与 Retry-After | 本项目 `internal/workbuddy/chat_errors.go:82-139` |

表中未带目录的 `auth_files*.go`、`config_lists.go` 都位于 `internal/api/handlers/management/`。源码可以证明契约存在，最终服务、前端、容器和真实账号的验收仍须有各自独立运行记录。
