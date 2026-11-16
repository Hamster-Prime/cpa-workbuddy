# WorkBuddy 插件部署与人工验收

本文对应**未修改的官方 CPA `v8.0.69`**（commit `f7ae8feeb3710319f524730849fbe32fde1751e4`）、**官方 CPAMC `v1.28.0`**（`management.html` SHA256 `035cdd5340a5b9a76cb43d11e45444be1990ae1ecff413d6a92b7e8f03bc6373`）、插件产品版本以根 [VERSION](../VERSION) 为准、C ABI `1`、JSON schema `10`。安装不要求宿主补丁，也不要求修改或扩展官方前端；管理接口以官方 `/v8/management` 为准，插件自有只读状态仍在 `/v0/management/plugins/cpa-workbuddy/status`。

官方发布包本身即以 `CGO_ENABLED=1` 构建并对插件加载提供支持（`go version -m` 读取 `out/verification/cpa-official-release-rs9gn_e4/windows/cli-proxy-api.exe` 与 `linux/cli-proxy-api` 可见构建设置，Windows 二进制的 ldflags 记录 `main.Version=8.0.69`、`main.Commit=f7ae8fee`）。因此部署不再需要自建宿主；自建仅在需要其它平台/架构时进行。

## 安装前确定证据边界

| 证据 | 能证明什么 | 不能据此声称什么 |
| --- | --- | --- |
| 插件公共协议测试、独立 C 原生探针 | 动态库 ABI、注册/status、回调、取消和卸载边界 | 官方 CPA 服务、管理前端、真实账号或容器已验收 |
| 隔离 fixture 上游 + 官方发布包/官方源码构建的可执行程序 | 指定二进制、配置、管理鉴权、插件加载路径与协议链路 | 真实 WorkBuddy 账号、真实网页交互或第三方网络可用 |
| 真实 Linux 运行记录（`out/logs/native-official-linux-v8.json`、`native-official-linux-final.json`） | 官方 linux_amd64 发布包与 `.so` 在 Linux 环境下的管理/登录/模型/协议/刷新/重启/隐私表现 | 其它架构、macOS、真实账号或用户浏览器步骤已通过 |
| 真实 Windows 运行记录（`native-official-windows-source.json`、`native-official-windows-v8.json`、`native-official-windows-final.json`、`native-official-windows.json`） | 官方源码构建与官方 Windows 发布包各自的加载与检查结果 | 未通过项不能当作通过；发布包在专用 CA 下的 protocol 套件未跑完 |
| 用户自行完成的真实账号清单 | 清单列出的账号、身份、模型和协议行为 | 未列出的权限、套餐、模型和平台可用 |

## 固定来源与校验

| 输入 | 固定值 |
| --- | --- |
| CPA linux amd64 发布包 | `CLIProxyAPI_8.0.69_linux_amd64.tar.gz`，SHA256 `4213723d57096bc97ad32d810b89653ba3edeb384087cecc90956c4c33e8af22`（25,724,234 bytes） |
| CPA windows amd64 发布包 | `CLIProxyAPI_8.0.69_windows_amd64.zip`，SHA256 `c81830b40c69d3d4d44b4fb9721827ffc8d3c686ebc69e16aea3af5636d54517`（26,002,138 bytes） |
| 官方 `checksums.txt` | SHA256 `a901d6edb4ad3570f03597f5e6831760637c8bed47dca7e37894e216bf774c5c`；内含上表两项及 darwin/arm64/freebsd 条目 |
| CPAMC v1.28.0 页面 | `management.html`，SHA256 `035cdd5340a5b9a76cb43d11e45444be1990ae1ecff413d6a92b7e8f03bc6373`，3,879,501 bytes |
| 官方源码树 | `out/verification/cpa-official-gVOq3z`；HEAD `1aaf56bc6a0fc6fce19049ba7ae8554c882304a6`，与 v8.0.69 `f7ae8fee` 的差异仅为 README 与 `assets/`（`git diff --stat`），因此下文行号对 v8.0.69 同样适用 |
| CPAMC 源码树 | `out/verification/cpamc-official-hdYiKj`，tag `v1.28.0`，commit `3d893ec88938c3ceef200a15be6af8cf972564b3` |

本地已下载的发布输入位于 `out/verification/cpa-official-release-rs9gn_e4/{CPA,CPAMC}`，校验值记录在 `out/logs/cpa-native-release-inputs.json`。仅使用官方 release 产物；不把 CI 构建物或本地重新编译二进制的摘要当作官方 provenance。自建宿主时记录 commit、Go 版本与文件摘要，`main.Version` 可能为 `dev`，此时以 commit 与摘要共同标识。

## 发布包内容与校验

插件包为 `out/dist/cpa-workbuddy-v<VERSION>-<goos>-<goarch>.zip`，同目录 `.zip.sha256` 记录完整 ZIP 摘要；先用 SHA256 工具核对 ZIP，再按包内 `checksums.sha256` 核对每个成员。

包内成员为：动态库、C 生成头、`native.h`、`VERSION`、根 MIT `LICENSE` 与 `README.md`、需求/架构/兼容/质量/部署/任务文档、`licenses/THIRD-PARTY.txt`（实际链接模块的完整许可汇总，打包时从 Go 模块缓存生成）、`licenses/Go-LICENSE`、`licenses/Go-PATENTS`。所需补充许可原文按固定 SHA256 从官方源下载并缓存于 `out/cache/licenses/`，不在仓库内保留许可副本。

- **包内不含任何 CPA 补丁**：安装不需要 `git apply`、不需要修补宿主源码或前端源码。
- `build-info.txt` 由实际库读取，记录 Go 工具链、模块与实际 GOOS/GOARCH/CGO 设置；打包入口按实际链接模块生成 `licenses/THIRD-PARTY.txt`，每个模块版本都必须有对应许可原文。
- 凭证、账号响应、登录 URL/state、运行日志与参考快照不属于包内容。
- 动态库文件名必须为 `cpa-workbuddy` + 平台扩展名（`.dll`、`.so`、`.dylib`）；`-v<版本>` 后缀会被宿主识别为插件版本，首次部署不建议使用。

## 运行依赖与平台

| 平台 | 已核对事实 | 未验证 |
| --- | --- | --- |
| Windows amd64 | 官方发布包与官方源码构建均能加载插件；DLL 由 MinGW GCC 14.2.0 构建，实际导入 `KERNEL32.dll`、UCRT API DLL 与 `libmcfgthread-1.dll`；插件包不分发该 MCF 运行库 | 需在目标进程环境提供匹配的 amd64 运行库；官方 Windows 发布包（Go1.26.10）在隔离设施中不读取专用 CA，该次运行（`out/logs/native-official-windows.json`）未跑完 protocol 套件；同会话后续记录见 `native-official-windows-v8.json`、`native-official-windows-final.json` |
| Linux amd64 | 官方发布包为动态链接 glibc 可执行程序；`.so` 由 Ubuntu GCC 13.3.0/glibc 2.39 构建，ELF `NEEDED` 仅 `libc.so.6`，最高所需符号 `GLIBC_2.34` | musl/Alpine、更旧 glibc、arm64 |
| Linux arm64 / macOS amd64 / macOS arm64 | 只有工具链与打包目标 | 无构建、无宿主加载记录 |
| 远程 CI | — | 无可用 remote，未运行 |

Linux 官方发布包已含 `-ldl` 链接的 cgo loader（`internal/pluginhost/loader_unix.go:1-7`）；非 cgo 构建会被 `loader_unsupported.go:1-10` 明确拒绝。自建 Linux 宿主（仅其它架构/发行版需要）必须以 `CGO_ENABLED=1` 构建。glibc 构建的 `.so` 不能直接假定可在 musl 中加载；应在目标运行环境构建/加载并记录 loader、依赖与 libc 版本。

## 安装与启动

1. 解压官方发布包到目标目录（Linux 为 tar.gz，Windows 为 zip），得到 `cli-proxy-api`/`cli-proxy-api.exe`、`config.example.yaml`、`README*`、`LICENSE`。包内 `config.example.yaml` 是版本对应的字段全集，本文只列部署所需键。
2. 把 `cpa-workbuddy.dll`/`.so` 放到专用 `plugins.dir` 根目录。宿主也检查 `<plugins.dir>/<goos>/<goarch>/`，且该子目录优先（`internal/pluginhost/platform.go:313-318`）；首次部署保持单一候选，避免版本选择混淆。
3. 写 `config.yaml`（v8 布局）。绝对路径由部署者选择，密钥必须替换成新生成的各自独立值：

```yaml
config-version: 8
server:
  host: ""              # 空为监听所有接口；本机验证可改 127.0.0.1
  port: 8317
  commercial-mode: true # 可选：关闭高开销请求日志中间件（见隐私一节）
management:
  allow-remote: true    # 仅本机访问时可保持 false
  secret-key: "<新生成的管理密钥>"   # 空则管理路由 404
  disable-control-panel: false
  disable-auto-update-panel: true
oauth:
  auth-dir: "/var/lib/cpa/auth"
access:
  api-keys:
    - "<另一个新生成的客户端密钥>"
observability:
  logs:
    debug: false
    logging-to-file: false
    request-log: false
requests:
  proxy-url: ""
plugins:
  enabled: true
  dir: "/opt/cpa/plugins"
  configs:
    cpa-workbuddy:
      enabled: true
```

`plugins.enabled` 与 `plugins.configs.cpa-workbuddy.enabled` 必须同时为 `true`；插件生效值还要求它已实际注册（`effective_enabled = plugins_enabled && enabled && registered`，`internal/api/handlers/management/plugins.go:151-157`）。管理请求统一使用 `Authorization: Bearer <管理密钥>` 或 `X-Management-Key: <管理密钥>`；`access.api-keys` 只用于模型 API，两者不能互换。`management.secret-key` 中的明文会在启动时被哈希，配置目录需要可写，或预置宿主认可的哈希。`MANAGEMENT_PASSWORD` 非空时同时覆盖管理密钥并放宽远程访问限制（`internal/api/handlers/management/handler.go:76-86,378-381`），不要把它当成只替换 secret-key 的变量。

4. 启动：`./cli-proxy-api -config <配置文件绝对路径>`。`/v0/management` 与 `/v8/management` 只有在配置了管理密钥后才会注册（`internal/api/server_management.go:14-27`）。

### 加载与版本确认

```http
GET /v8/management/plugins
Authorization: Bearer <管理密钥>

GET /v0/management/plugins/cpa-workbuddy/status
Authorization: Bearer <管理密钥>
```

`plugins` 响应含 `plugins_enabled`、`plugins_dir` 和 `plugins[]`；逐项核对 `id=cpa-workbuddy`、`path` 指向实际库、`registered=true`、`effective_enabled=true`、`supports_oauth=true`、`oauth_provider=workbuddy`、`oauth_providers=["workbuddy","workbuddy-ai"]`、`oauth_provider_entries` 中两个入口名称为 “WorkBuddy国内”“WorkBuddy国际”、`metadata.version` 等于 `VERSION`（`internal/api/handlers/management/plugins.go:27-41,130-175`）。文件存在或 `configured=true` 都不证明已加载。

插件自有状态接口预期返回：

```json
{"name":"cpa-workbuddy","version":"0.1.0","abi_version":1,"schema_version":10}
```

该路由由插件在 `management.register` 声明的 `GET /v0/management/plugins/cpa-workbuddy/status` 提供（本项目 `internal/plugin/runtime.go:22,192-209`），不是独立页面。若宿主返回 `incompatible_schema`（`schema_version must be at least 10`），说明宿主不支持 schema 10 的多 OAuth 入口，应升级到官方 v8.0.69，而不是改插件或宿主源码。

同时记录每个管理响应的 `X-CPA-VERSION`、`X-CPA-COMMIT`、`X-CPA-BUILD-DATE`、`X-CPA-SUPPORT-PLUGIN`（`internal/api/handlers/management/handler.go:323-330`），用于确认实际运行的二进制。

## 官方管理前端（CPAMC v1.28.0）

CPAMC v1.28.0 的管理前缀为 `/v8/management`（`src/utils/constants.ts:17`）。宿主在 `GET /management.html` 提供页面，文件路径解析顺序为：`MANAGEMENT_STATIC_PATH` 指定的文件/目录 → 用户可写目录下的 `static` → 配置文件同级的 `static`（`internal/managementasset/updater.go:141-186`）。

1. 下载官方 v1.28.0 `management.html`，核对 3,879,501 bytes 与 SHA256 `035cdd5340a5b9a76cb43d11e45444be1990ae1ecff413d6a92b7e8f03bc6373`。
2. 安装为 `<静态目录>/management.html`，把 `MANAGEMENT_STATIC_PATH` 设为该 **HTML 绝对路径**，例如 `/opt/cpa/static/management.html`；保留 `management.disable-auto-update-panel: true`。
3. 验证面是服务端行为：`GET /management.html` 返回 HTTP 200，且正文 SHA256 与固定文件一致，无需打开可见浏览器。关闭自动更新只阻止周期更新；文件缺失时首次访问仍会尝试下载，因此必须预装固定文件，不能让缺文件路径下载 latest 后仍标成 v1.28.0。暂不部署页面时可设 `management.disable-control-panel: true`，继续用管理 API 操作。

schema 10 允许一个插件声明多个 OAuth 入口，管理列表返回 `oauth_providers` 与 `oauth_provider_entries`；CPAMC v1.28.0 的 `buildPluginOAuthProviderCards` 为每个有效入口生成一张卡片，标题取入口名称（`src/features/oauth/providers.ts:158-186`），按入口 ID 调用 `/oauth/auth-url?provider=<入口ID>`（`src/services/api/oauth.ts:61-80`）。隔离 headless Chromium 已通过页面实际点击“WorkBuddy国内”“WorkBuddy国际”，两次对话框均成功，凭证列表显示两条记录，落盘 site 与 oauth_provider 分别对应两个入口，控制台错误为 0（`out/logs/native-official-headless-ui.json`）。该结果使用合成上游；真实 WorkBuddy 网页、WeCom、账号权限及主观界面体验仍待用户验收。

## 双站点浏览器登录

两个站点共用持久 provider `workbuddy`，入口 ID 决定站点：

| 入口 | 站点 | 凭证 site |
| --- | --- | --- |
| `workbuddy` | 国内 | `workbuddy` |
| `workbuddy-ai` | 国际 | `workbuddy-ai` |

```http
GET /v8/management/oauth/auth-url?provider=workbuddy
Authorization: Bearer <管理密钥>

GET /v8/management/oauth/auth-url?provider=workbuddy-ai
Authorization: Bearer <管理密钥>
```

成功返回 `{"status":"ok","url":"<本次敏感登录URL>","state":"<本次敏感state>"}`。处理过程：`StartOAuthV8` 读取 `provider`，其余 query 作为插件登录 metadata 透传，并把回调路径设为 `/v8/management/oauth/callback`（`internal/api/handlers/management/auth_files_v8.go:135-147`；`auth_files_oauth_callback.go:198-221`）。插件只接受 `site`、`is_webui` 两个 metadata 键；若显式传 `site`，必须与入口一致，否则返回 `invalid_metadata`（本项目 `internal/workbuddy/service.go:226-236`）。CPAMC 只传 `provider`，因此无需额外参数。不要把 `provider` 写成 `workbuddy-ai-auth-url` 之类的路径。

`proxy_url` 只允许放在 POST body（GET query 中的 `proxy_url`/`proxy-url` 会被拒绝）。登录阶段尚无凭证时使用全局 `requests.proxy-url`。

用户在**自己的浏览器**打开 `url`，完成 WorkBuddy 账号登录和网站实际要求的个人/企业身份选择、授权或 WeCom 步骤。自动化不操作可见桌面，也不代替用户扫码；不要把管理密钥传给 WorkBuddy 页面。

轮询（建议 1–3 秒一次，最多五分钟）：

```http
GET /v8/management/oauth/status?state=<URL编码后的本次state>
Authorization: Bearer <管理密钥>
```

- `status=wait`：仍在等待浏览器/token/账号确认；HTTP 200 本身不是成功。
- `status=ok`：宿主已完成该 state 的凭证保存；随后必须查看凭证列表和模型。
- `status=error`：读取安全错误摘要；超时、重启、未知 state、取消或账号确认失败后重新发起登录。
- **state 不能省略**：`GetAuthStatus` 对空 state 直接返回 `{"status":"ok"}`（`auth_files_provider_oauth.go:1319-1323`），空请求不能作为登录证据。
- 未注册/过期的 state 返回 `unknown or expired state`（`:1330-1333`）。

取消会话：

```http
DELETE /v8/management/oauth/session?state=<编码后的state>
Authorization: Bearer <管理密钥>
```

取消不等于删除已有凭证。WorkBuddy 使用服务端轮询，容器无需浏览器、桌面、宿主机回调端口或导入桌面凭证；其他 provider 的手动 callback 流程不适用。

登录 URL、state、access/refresh token 和完整账号响应都是敏感材料：不要写入日志、截图、问题报告或浏览器地址栏转述。

## 凭证列表与管理动作

```http
GET /v8/management/credentials
GET /v8/management/credentials?name=<URL编码后的列表name>
GET /v8/management/credentials/models?name=<URL编码后的列表name>
```

列表响应为 `{"observed_at":..., "files":[...]}`（`internal/api/handlers/management/auth_files.go:106-176`），每条含 `id`、`auth_index`、`name`、`type`/`provider`、`label`、`status`、`status_message`、`disabled`、`unavailable`、`source`、`path`、`last_refresh`、`updated_at` 等。公开报告只使用脱敏的身份标签与 ID 摘要，不从昵称猜文件名。

稳定身份为 `site + uid + enterprise/tenant scope`。昵称变化不创建新身份；同一身份重登回到原 ID/FileName，不需要先删除失效记录。不同站点、个人/企业、两个企业分别保存。成功安装新代次后清除旧认证失效与认证冷却、刷新计划和模型目录；保留手动禁用、`priority`、`proxy_url`、排除与别名等宿主管理设置。取消、超时、账号确认或保存失败不得清理旧状态或声称登录成功。

以下 `name` 取自实际列表，示例文件名只是占位。每步都要检查 HTTP 状态、响应体、重新读取列表、实际调度与重启恢复，不能只看按钮或 200。

```http
PATCH /v8/management/credentials/status
Content-Type: application/json

{"name":"<列表中的name>","disabled":true}
```

恢复启用把 `disabled` 改为 `false`。启用不应解除永久认证失效；后者需要同身份重新登录。

```http
POST /v8/management/credentials/refresh
Content-Type: application/json

{"name":"<列表中的name>"}
```

刷新共用宿主 Manager 与插件协调；成功时响应包含刷新结果对象，人工验收只检查列表刷新时间与重启恢复，不把完整对象写进报告。`{"all":true}` 会触及所有凭证，真实账号优先逐条刷新。

```http
POST /v8/management/credentials/verify
Content-Type: application/json

{"name":"<列表中的name>","model":"<带站点前缀的模型ID>"}
```

可选主动校验；`name`/`auth_index` 至少给一个（`auth_files_v8.go:544-604`）。校验失败不等于凭证失效，需按错误分类判断。

```http
DELETE /v8/management/credentials?name=<URL编码后的列表name>
```

删除立即移除凭证记录并阻断该凭证的实际调用。Windows 官方源码构建实测存在目录迟留：删除后等待 25 秒，独占模型仍在 `/v1/models`，实际请求已返回 HTTP 503；Linux 官方发布包目录清理通过（`out/logs/native-official-windows-final.json`、`native-official-linux-final.json`、`native-official-deletion-boundary.json`）。用户已接受此官方边界；需要目录立即清理时，可先禁用再删除，或重启宿主，并按实际结果核对。列表消失与目录可见不能替代真实调度检查。

模型规则使用**带站点前缀的源 ID**，`AuthData.Prefix` 保持空。模型 ID 必须来自该账号实际目录；不能因为示例写了模型就假定有权限。

```http
PATCH /v8/management/credentials/fields
Content-Type: application/json

{"name":"<列表中的name>","priority":7,"excluded_models":["wb/<国内上游ID>"],"model_aliases":[{"name":"wb/<另一个国内上游ID>","alias":"wb/manual-domestic","fork":true}],"proxy_url":"http://<代理地址>:<端口>"}
```

这是单凭证字段修改（`auth_files_fields.go:263-360`），`name` 必填，其余为凭证元数据键；不要用 `headers` 覆盖 token、用户、企业或 domain。随后重读该凭证的模型列表确认 watcher/注册更新。单凭证别名优先于同客户端别名的全局映射；排除先于别名。

全局规则在 v8 布局下属于 `oauth` 子树，写操作需要 `If-Match`（`internal/api/handlers/management/config_v8.go:27-57`）：

```http
GET /v8/management/config/oauth/excluded-models
Authorization: Bearer <管理密钥>
```

记录响应头 `ETag`，然后：

```http
PATCH /v8/management/config/oauth/excluded-models
If-Match: <上一步的 ETag>
Content-Type: application/json

{"workbuddy":["wb/<国内上游ID>","wb-ai/<国际上游ID>"]}
```

别名同理使用 `GET/PATCH /v8/management/config/oauth/model-alias`，body 为 `{"workbuddy":[{"name":"wb-ai/<ID>","alias":"wb-ai/manual-international","fork":true}]}`（键映射见 `internal/config/config_v8.go:90`）。缺少或过期的 `If-Match` 返回 `412 {"error":"precondition_failed"}`。旧的 `/v0/management/oauth-excluded-models`、`/v0/management/oauth-model-alias` 仍可用，但会写旧布局；固定 v8 宿主时统一走 v8 路径，避免两套写法混用。

全局 `/v1/models` 合并不等于各 auth 的权限/图片/effort 相同，也不能让国内凭证获得 `wb-ai/*` 调度资格。两个站点请使用不同的客户端别名，并在验收时核对解析后的执行模型。

## 插件启停

```http
PATCH /v0/management/plugins/cpa-workbuddy/enabled
Content-Type: application/json

{"enabled":false}
```

v8 路由组的插件相关路由只有列表 `GET /v8/management/plugins`、`DELETE /v8/management/plugins/:id`、store 安装与 quota 组，没有启停开关；启停仍在 v0 组（`internal/api/server_management.go:35-46`）。停用/删除库与删除凭证是两件事，不能用删凭证代替插件生命周期验收。也可以用 `plugins.configs.cpa-workbuddy.enabled` 在配置层停用，但需要宿主重载配置。

## 刷新、401、失效与重启

官方 Manager 接管刷新与 401：

- 自动续期读取凭证 metadata 中的 `refresh_interval_seconds`（`sdk/cliproxy/auth/conductor_refresh.go:214-221`）。插件写入 `300`（本项目 `internal/workbuddy/credentials.go:255`，常量 `refresh.go:11`），因此是五分钟周期调度，而不是“仅在过期前五分钟刷新一次”。
- 执行收到 401 时，宿主按当前凭证刷新并重试一次（`conductor_refresh.go:1148-1196`）；插件不自行循环重试，刷新后再次被拒即终止。
- `auth.refresh` 只返回轮换后的 AuthData；保存、安装、代次校验与迟到结果处理都由宿主完成。宿主保存失败时以 warning 显式告警并继续使用新内存凭证，此时重启可能丢失新 token。
- 刷新凭证被明确拒绝（`invalid_grant`、撤销）时进入宿主的需要重新登录状态；**尚未到期且未被上游拒绝的 access token 仍可推理**（`conductor_refresh.go:1364` 起的分支）。单次普通 401、超时或网络错误不构成永久失效。
- 重启后由宿主重新加载凭证文件并重建认证与调度状态；插件内存中的快照、单飞结果与拒绝记忆不跨进程保留。文件存在本身不构成真实账号能力证据。

验收时分别记录：单条手动刷新、计划刷新、首次 401 恢复、明确永久失效、临时网络错误；只记录安全分类，不为真实账号主动泄露或伪造 token。

## 无桌面容器的路径、持久化与代理

使用官方 linux amd64 发布包与同架构 `.so`；Windows DLL 不能挂到 Linux 容器。官方仓库提供 Dockerfile 与 compose 示例，但本文不声明任何官方镜像 tag 或摘要；自建镜像时以官方发布包或官方源码 Dockerfile 为基，并记录实际 image digest。

| 容器路径 | 挂载与权限 | 原因 |
| --- | --- | --- |
| `/opt/cpa/cli-proxy-api` | 已核验二进制，可只读 | 启动程序，显式 `-config /etc/cpa/config.yaml` |
| `/opt/cpa/plugins/cpa-workbuddy.so` | 已核验库，可只读 | `plugins.dir=/opt/cpa/plugins`；启动期间不覆盖正在加载的库 |
| `/etc/cpa/config.yaml` | 持久配置；需要管理修改时可写 | 启动密钥哈希、插件启停和全局规则保存需要写入 |
| `/var/lib/cpa/auth` | **专用持久卷，运行用户可读写并能原子替换** | token 轮换、稳定身份、失效状态、刷新时间与账号模型快照 |
| `/opt/cpa/static/management.html` | 固定 v1.28.0 文件，可只读 | `MANAGEMENT_STATIC_PATH` 显式指向文件，关闭自动更新 |

凭证目录需要目录级写权限与替换权限，仅给旧 JSON 文件写权限不够。不要把卷挂载成空目录去遮蔽已准备的库或静态文件。凭证卷不需要与开发机同路径，身份不依赖盘符。原始证据、合成 fixture 与日志写入本仓库 `out/`；真实凭证只放在部署者指定的受保护卷中，不能进入镜像层、仓库或发布包。

网络：[INFERENCE] 容器默认 bridge 网络下，来自主机的请求通常被 CPA 视为非 localhost；以实际 `403` 与隔离网络记录判断。选择 loopback 发布端口、可信 TLS 反代或受控网络后再启用远程管理。`server.trusted-proxies` 只列真实可信转发代理，并按配置要求重启；不要依靠任意 `X-Forwarded-For` 绕过管理策略。

代理：全局用 `requests.proxy-url`；单凭证用 `PATCH /v8/management/credentials/fields` 的 `proxy_url`。宿主按“请求覆盖 → auth.ProxyURL → 全局 proxy-url”选择；`direct`/`none` 可显式绕过配置/环境代理。登录尚无 auth 时应配置全局代理，不能假定旧凭证的 `proxy_url` 影响新登录。容器中的 `127.0.0.1` 指容器自身，代理地址必须从容器可达。核验时用受控代理只记录目标 host/端口、连接次数与结果，分别覆盖登录 state/token/account、模型发现、刷新和推理；不要记录 CONNECT 后的正文、Authorization、`X-Refresh-Token` 或账号响应。

重启验收保留同一 auth 卷与持久配置，停止旧容器后以相同 image digest 与库重建。比较凭证条数/稳定 ID、disabled、proxy/priority/规则、永久失效状态、刷新时间与账号快照，并实际请求确认健康账号恢复、失效与手动禁用账号不命中。重新登录恢复后再重启一次，验证新代次已落盘。

### 本地隔离复现

以下使用交接保留的隔离 runner `out/verification/cpa-native-live-JijbBZ/main.go`（构建为 `out/verification/cpa-native-live.exe`）与官方发布包。它生成 `config-version: 8` 配置（含 `server.commercial-mode: true` 与 `observability.logs.request-log: false`）、专用 CA 与 fixture CONNECT 代理，并把 `SSL_CERT_FILE`/`SSL_CERT_DIR` 指向 proof 目录。全部为本地设施，不能当作真实账号验收。

Windows PowerShell 在项目根目录执行（官方源码构建的宿主）：

```powershell
go build -o out/verification/cpa-native-live.exe out/verification/cpa-native-live-JijbBZ/main.go
$env:MANAGEMENT_STATIC_PATH = "$PWD/out/verification/cpa-official-release-rs9gn_e4/CPAMC/management.html"
$env:GODEBUG = "x509sslcertoverrideplatform=1"
./out/verification/cpa-native-live.exe --server "$PWD/out/verification/cpa-official-source.exe" --plugin-dir "$PWD/out/bin" --workdir "$PWD/out/verification/local-native-windows-proof" --management-sha256 035cdd5340a5b9a76cb43d11e45444be1990ae1ecff413d6a92b7e8f03bc6373
```

Linux 环境（本仓库记录的实际运行环境为 WSL，路径形如 `/mnt/d/...`）：

```sh
go build -o out/verification/cpa-native-live-linux out/verification/cpa-native-live-JijbBZ/main.go
MANAGEMENT_STATIC_PATH="$PWD/out/verification/cpa-official-release-rs9gn_e4/CPAMC/management.html" ./out/verification/cpa-native-live-linux \
  --server "$PWD/out/verification/cpa-official-release-rs9gn_e4/linux/cli-proxy-api" \
  --plugin-dir "$PWD/out/bin" \
  --workdir "$PWD/out/verification/native-official-linux-v8" \
  --proxy-listen 127.0.0.1:18889 --port 18890 \
  --management-sha256 035cdd5340a5b9a76cb43d11e45444be1990ae1ecff413d6a92b7e8f03bc6373
```

可选容器模式：给同一 runner 传 `--docker-image sha256:<已在本地加载的 image>` 与 `--config-root=/proof`（必须运行在 Linux 侧，使用 `--pull=never` 与 `--network=host`）。这是本地验收网络，不是公开服务推荐布局。

每次复现使用尚未包含凭证的新 proof 目录。当前 Linux 官方发布包与同包的 Debian 容器均62/62项通过；Windows官方源码构建61/62项，唯一目录迟留按已接受边界记录，不能重写成全部通过。重启检查只把实际推理命中视为禁用账号调度，模型验证/刷新与推理分开；轮换token不要求重启前后字节不变，必须检查有效期、代次、快照和管理字段。完整证据见 `out/logs/native-official-summary.json`。

### 账号名称与凭证文件

插件返回独立的AuthData.ID、FileName和Label：完整ID标识站点/用户/企业作用域，FileName用于官方列表与模型弹窗标题，Label保存展示名。首次生成的可读文件名带短身份区分符；规范file_name随凭证保存，源文件解析、刷新及同身份重登沿用。昵称变化更新Label，已有文件的名称通过管理接口明确迁移。

既有摘要文件名可在同一官方宿主上迁移：

1. 经认证的 `GET /v0/management/auth-files` 与download接口读取目标记录和当前JSON，记录完整ID、登录代次、Label及宿主管理设置；凭证JSON按敏感资料处理。
2. 选取可读、唯一且安全的.json文件名，将JSON中的file_name设为该值，通过 `POST /v0/management/auth-files` 的file表单上传。保留站点、用户、企业、登录代次、token和管理字段。
3. 重新读取列表，确认完整ID与代次不变、name为目标名称；通过新name下载并核对JSON，确认模型列表和手动刷新可用。
4. 确认运行记录的name/path均指向新文件，且旧文件名不等于当前ID或name，再经 `DELETE /v0/management/auth-files?name=<旧名称>` 清理旧文件。删除失败须处理，不能把重复文件留作迁移成功。
5. 保持镜像和持久卷重启，检查单条记录、名称、作用域、管理状态和实际请求；使用未修改的官方页面检查账号卡片与模型弹窗。整个流程不直接操作用户auth目录。

## 日志与隐私

部署片段显式设置 `observability.logs.debug: false`、`logging-to-file: false`、`request-log: false`，并可设 `server.commercial-mode: true`。需要知道这两层的实际边界：

- `request-log: false` 关闭常规请求日志，但宿主仍会为上游错误写错误请求日志文件，`observability.logs.error-logs-max-files` 控制其保留数量（`config.example.yaml:1290-1297`；evidence：`out/verification/native-official-windows-source-proof/logs/error-*.log`）。
- `server.commercial-mode: true` 使宿主完全不构造请求 logger（连错误请求日志一起跳过），并把 SDK 请求日志标志强制为关闭，但 Gin 访问日志中间件仍然启用（`internal/api/server.go:183-195`；`internal/api/server_options.go:50-52`）。它不是“关闭所有日志”的开关；`native-official-linux-v8-Jx6ehn/logs/` 为空正对应这一模式。
- 宿主对含 authorization/api-key/token/secret 的头有掩码，但不等于完整隐私清理：用户/企业/租户标识头、管理密钥自定义头、`state` query 与 JSON 正文必须单独核验。
- Linux原生、Debian容器与Windows官方源码构建的完整隐私扫描均在request-log关闭、commercial-mode启用的配置下通过（`out/logs/native-official-linux-final.json`、`native-official-container-final.json`、`native-official-windows-final.json`）。此前仅关闭request-log的失败记录保留为配置边界证据，原始敏感输出不公开。
- 仍需检查反向代理、容器日志驱动与错误日志路径；代理 access log 对管理登录/轮询路径不记录 query，移除敏感头，不捕获登录/refresh body；人工证据只保留安全 code/HTTP status、站点、模型、脱敏 auth ID、时间和结果。

## 故障排查

| 观察/安全错误 | 检查与处理 |
| --- | --- |
| 管理 404 | 检查 origin、`/v8/management`、`management.secret-key` 是否为空、`disable-control-panel`、固定静态路径与文件权限 |
| 管理 401/403 | 使用管理密钥；检查 `management.allow-remote`、`MANAGEMENT_PASSWORD` 的覆盖、可信代理与源 IP；反复错误会触发临时 IP 禁用 |
| configured/enabled 但未 registered | 检查库路径、文件名、平台/架构、依赖与 loader 错误；Linux 确认宿主是 cgo 构建（官方发布包已是） |
| `incompatible_schema` | 宿主 schema 低于 10；升级到官方 v8.0.69，勿改宿主源码 |
| `invalid_metadata` / `invalid_provider` | 入口必须是 `workbuddy` 或 `workbuddy-ai`；显式 `site` 必须与入口一致 |
| auth-url 失败、状态一直 wait | 确认宿主出网、代理、时钟与证书；浏览器完成指定个人/企业身份；五分钟后重开会话，不转抄 token |
| 轮询 ok 但无可用模型 | 核对非空 state、列表实际保存、disabled/失效与单 auth 模型目录。首次发现失败不授予静态权限；修复上游后重新登录或由 watcher/配置重注册触发重新发现 |
| `412 precondition_failed` | v8 配置写缺少或过期 `If-Match`；重新 GET 取 `ETag` |
| 凭证保存/持久化错误 | 检查 auth 卷权限、空间、原子替换与配置路径；内存新凭证可能暂时可用，未确认落盘前不承诺重启恢复，不再消费旧 refresh token |
| 需要重新登录 / `invalid_grant`、撤销 | 保留原记录，同 site/uid/企业身份重登；检查原 ID、认证错误清理、管理设置、模型与重启。普通单次 401 不当永久失效 |
| `unsupported_model` / `unsupported_format` | 从所选 auth 目录选带正确站点前缀的模型；不要改投另一站点或手工删除前缀 |
| `account_restricted` / 11140 | 账号风控，按站点要求处理；不是 token 过期 |
| `credit_package_exhausted` / 14018 | 额度/套餐不足；重登不保证解决 |
| `model_permission_denied` / HTTP 403 | 核对该账号实际模型权限；全局模型显示不能证明所选账号有权限 |
| `client_version_rejected` / 10085 | 当前协议/客户端版本被拒；保留安全错误与构建信息，更新需重新核验协议 |
| 11128 / 11153 / 11159 | `channel_rejected`、`tool_continuation_rejected`、`thinking_signature_rejected`；有界纠正耗尽后明确失败，禁止无限重试 |
| `context_length_exceeded` | 缩短上下文/输出预算并按账号限额重试 |
| `rate_limit_exceeded`、`upstream_unavailable`、`upstream_timeout` | 429/5xx/超时交给宿主策略与合法 Retry-After；插件不叠加状态码循环，已输出回答后不重放 |
| 流截断、取消或超时 | 结果为失败/中断，不能把部分内容当完整成功；检查上游关闭、客户端取消与宿主超时策略 |
| 隔离设施 TLS 握手失败（Windows 官方发布包） | Go1.26.10构建在本次条件下未采用proof CA；该包完整协议保持未验。隔离复现使用Go1.27.1从官方源码构建，并仅给proof进程设置专用CA环境；生产按正常证书信任策略运行 |

## 真实账号人工验收单

以下项目需部署者实际填写“通过 / 失败 / 未验证及具体原因”，不能由合成协议或静态页面源码代勾。每条记录含日期、CPA 版本/commit/二进制摘要、插件版本/库摘要、CPAMC tag/HTML 摘要、平台/镜像摘要/libc、脱敏配置、客户端协议/流模式、模型 ID、脱敏实际命中 auth ID、结果与证据位置。账号、凭证 JSON、完整 URL/state 和 header 不写入报告。

- [ ] 国内至少两条、国际至少两条真实凭证登录；记录每条 site、个人/企业作用域与稳定 ID。用户完成浏览器/WeCom，CPAMC v1.28.0 显示两张入口卡并能保存、进入列表；API 成功不能代替 UI AC。
- [ ] 同 uid 的个人与企业、两个企业、双站点分别独立；昵称变化后同身份重登仍是同一条。失效重登不先删记录，认证状态恢复且 disabled、priority、代理、排除与别名保留。取消/超时/账号确认失败/保存失败不清除旧状态。
- [ ] 对每账号记录成功模型快照时间与实际 CLI/custom 模型；按权限制造账号间模型/图片/effort 差异。首次失败无静态资格；临时失败仅沿用该账号旧快照；明确认证失效则停止候选。
- [ ] 每站点各两条凭证分别实际命中 auth ID；可依次手动禁用其他候选确定目标，再恢复配置。不同站点同名模型绝不混用；全局合并不增加任一账号权限。单凭证与全局排除/别名分别验证，源 ID 带 `wb/` 或 `wb-ai/`。
- [ ] OpenAI Chat `POST /v1/chat/completions`、Responses `POST /v1/responses`、Claude Messages `POST /v1/messages`、Gemini `POST /v1beta/models/<模型>:generateContent`/`:streamGenerateContent` 均走 CPA 客户端鉴权，使用实际目录 ID 与各协议合法 payload。Gemini 模型路径按客户端规则编码含 `/` 的 ID。每种协议记录代表性的流式、非流式、文本、多轮、工具调用→客户端执行→工具结果返回、usage 与停止原因。
- [ ] 国内 Claude 无论客户端格式都命中本站点 `/v1/messages`；国内其他模型及国际全部模型命中对应站点 `/v2/chat/completions`。验证宿主翻译的双向工具/reasoning 语义，不只记录端点 HTTP 200。
- [ ] 对明确支持的账号/模型测图片输入及 reasoning/effort；国际 GLM 图片不支持应明确拒绝。不能用图片生成、音视频、embeddings 或计数/任意 HTTP 转发替代当前范围；缺权限时逐项写出账号、协议、模型与待执行步骤。
- [ ] 单条手动刷新、计划刷新（300 秒周期）、首次 401 一次恢复、明确永久失效分别检查；过期但可刷新与网络/额度/权限失败不误标永久失效。失效仍在列表、停止调度且不用旧模型快照恢复资格。只记录安全分类，不主动泄露/伪造 token。
- [ ] 容器无桌面，由远端用户浏览器登录；相同持久卷重启后条数、ID、刷新时间、失效状态、手动禁用与快照恢复。恢复登录后再次重启仍健康，禁用/删除仍有效。
- [ ] 代理分别覆盖登录、目录、刷新、Chat/Messages；拒绝代理时请求失败。检查 Authorization、`X-Refresh-Token`、管理密钥、`X-User-Id`、`X-Enterprise-Id`、`X-Tenant-Id`、state query、正文与错误日志无原值。
- [ ] 客户端取消、超时、插件停用/卸载会关闭在途上游与流；停止后不再发新请求。重新启用/重启后只恢复已持久化的合法状态。停用插件用 `PATCH /v0/management/plugins/cpa-workbuddy/enabled`、body `{"enabled":false}`；卸载/删除库另行记录，不能用删凭证代替插件生命周期。

## 源码与日志索引

源码位置均相对于 `out/verification/cpa-official-gVOq3z`（v8.0.69）与 `out/verification/cpamc-official-hdYiKj`（v1.28.0）；运行记录位于 `out/logs/`。

| 本文事实 | 来源 |
| --- | --- |
| 配置字段全集（server/management/access/oauth/observability/plugins） | `config.example.yaml:24-75,109-131,134-138,272,1031-1033,1280-1297,1322-1375` |
| 管理路由注册、v8 与 v0 分组、插件启停 | `internal/api/server_management.go:14-46`；同目录 `server_management_v8.go:11-80` |
| 管理鉴权、版本头、`MANAGEMENT_PASSWORD` | `internal/api/handlers/management/handler.go:76-86,323-330,378-381` |
| v8 登录入口与 provider/body 处理 | `internal/api/handlers/management/auth_files_v8.go:135-147` |
| query→插件 metadata、回调路径 | `internal/api/handlers/management/auth_files_oauth_callback.go:198-221` |
| 登录状态、空 state、取消会话 | `internal/api/handlers/management/auth_files_provider_oauth.go:1305-1348` |
| 凭证列表与条目字段 | `internal/api/handlers/management/auth_files.go:106-176,655-740` |
| 状态/刷新/字段/删除/校验 | `internal/api/handlers/management/auth_files_fields.go:30-146,263-360`；`auth_files_refresh.go:15-75`；`auth_files_v8.go:544-604`；`oauth_siwc_mutations.go:23` |
| v8 配置读写与 `If-Match` | `internal/api/handlers/management/config_v8.go:27-57`；`internal/config/config_v8.go:90` |
| 插件列表字段与 `effective_enabled` | `internal/api/handlers/management/plugins.go:27-41,130-175` |
| 静态页面路径与自动更新 | `internal/managementasset/updater.go:141-186`；`internal/api/server_management.go:309-336` |
| 请求日志与商业模式边界 | `internal/api/server.go:183-195`；`internal/api/server_options.go:50-52`；`internal/logging/request_logger.go:164-178` |
| 刷新周期与 401 恢复 | `sdk/cliproxy/auth/conductor_refresh.go:214-221,1148-1196,1364` |
| 插件 cgo loader 与发现目录 | `internal/pluginhost/loader_unix.go:1-7`；`loader_unsupported.go:1-10`；`platform.go:297-318` |
| 本插件双入口与 status | 本项目 `internal/workbuddy/service.go:187-200`；`internal/plugin/runtime.go:17-22,192-209` |
| CPAMC 管理前缀与插件卡片 | `src/utils/constants.ts:17`；`src/features/oauth/providers.ts:158-190`；`src/services/api/oauth.ts:61-84` |
| Linux 运行记录 | `out/logs/native-official-linux-v8.json`；`out/logs/native-official-linux-final.json`；`out/verification/native-official-linux-v8-Jx6ehn/config.yaml` |
| Windows 源码构建记录 | `out/logs/native-official-windows-source.json`、`native-official-windows-v8.json`、`native-official-windows-final.json` |
| Windows 官方发布包记录 | `out/logs/native-official-windows.json`；其余Windows协议记录来自官方源码构建 |
| 官方发布包摘要 | `out/logs/cpa-native-release-inputs.json`；`out/verification/cpa-official-release-rs9gn_e4/CPA/checksums.txt` |

源码可以证明公开契约存在；当前实际程序、前端DOM与容器结果统一见 [兼容性记录](compatibility.md) 首表及 `out/logs/native-official-summary.json`。历史切片记录保留各自当时版本与配置，不作为当前官方版本的通过证据。
