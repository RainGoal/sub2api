# 请求记录、会话封禁与设备封禁

请求记录位于管理端「请求审计」（`/admin/request-audits`）。采集、人工会话封禁和设备封禁是三个独立开关，默认关闭；默认保留最近 3 天，可配置 1～30 天。

列表和详情优先显示用户名/邮箱、API Key 名称和分组名称。列表的所属分组使用彩色标签，仅显示平台图标与分组名称，不再添加平台文字前缀；长名称省略并提供完整悬停提示。详情保留独立的平台标签。`user_username`、`user_email`、`api_key_name`、`group_name`、`group_platform` 随列表和详情一次查询返回，旧 ID 字段保留；软删除关联仍可显示名称，关联缺失时回退到 ID。这些名称和平台是关联对象的当前值，多平台分组不代表该次请求最终路由的平台。

用户筛选复用已有管理员用户搜索，输入用户名或邮箱后选择匹配用户（含软删除用户），再按其 ID 精确查询。会话 ID 可单独筛选并返回跨 API Key 的匹配记录，填写 API Key ID 可以缩小范围。客户端提供的会话 ID 不保证跨 Key 全局唯一，因此会话封禁仍严格作用于「API Key + 会话 ID」。

所属分组可按名称搜索并选择，包含已停用分组，提交 `group_id` 精确筛选。「请求 ID」可粘贴使用记录中的系统请求 ID；普通 HTTP 的 `client:<ID>` 及裸入站关联 ID 兼容已有审计数据。`v0.1.54` 开始为有采集上下文的网关用量记录保存最终请求 ID 到审计入口的明确关联，Responses WebSocket 返回所属连接的握手记录，有会话 ID 时可再通过详情的「会话请求」查看该会话；不把连接内所有帧冒充某一用量轮次。视频轮询触发的用量结算关联到该次轮询 HTTP 请求。请求 ID 查询仅返回 HTTP 或 WebSocket 握手入口，且仍受时间、分组、用户、Key 和保留期限制。

旧 WebSocket 响应 ID、Live 等未传递采集上下文的通道、独立后台任务及审计已过期/丢弃的请求没有可靠关联，不按时间或会话猜配，也不回填历史。关联通过原有审计队列异步写入，可能短暂延迟或随队列故障丢失，不改变计费 ID、金额、重试及业务准入；关联记录随审计入口级联清理。

审计优先使用既有明确会话请求头（包括按目标平台识别的 Grok 会话头），再读取 `X-Codex-Turn-Metadata` JSON 中的 `session_id`。Responses 原始请求体还支持 `client_metadata.session_id` 和 `client_metadata.x-codex-turn-metadata` JSON 字符串中的 `session_id`；Messages 保留 Claude `metadata.user_id` 的会话解析。提取发生在入站协议/指纹改写之前，并用于 HTTP 与 WS 的会话封禁检查，不改变用量记录、调度或缓存规则。`thread_id`、`turn_id`、`window_id`、`prompt_cache_key` 和内容哈希不会被当作审计会话 ID。客户端没有提供有效的明确标识时仍显示「无会话 ID」。

`v0.1.53` 修复了 Codex 元数据里已有 `session_id`、审计却漏取的情况；无需数据库迁移，只影响升级后的新请求，旧记录不会自动回填。

打开详情后自动分块预览 Headers、请求体和 Trailers，每部分先读取 64 KiB，可独立翻段、重试和下载。预览及下载最多同时读取两部分；关闭或切换记录后忽略旧返回。Headers 和 Trailers 的完整 JSON 默认格式化；允许读取不完整前缀与显示敏感值仍需显式选择。

请求体默认按消息角色展示文本与内嵌图片，支持 OpenAI Chat/Responses、Anthropic Messages、Gemini Contents 常见结构，并保留「原文」切换。完整请求体不超过 4 MiB 时自动连续读取最多 64 段，先按字节拼接再解析 UTF-8/JSON，避免跨段字符或 Base64 图片被截断。单张图片最多 2 MiB，只预览通过 MIME、Base64、文件签名及尺寸检查的 PNG/JPEG/GIF/WebP；单边最多 8192 像素，单图最多 16 × 1024² 像素，整份请求体最多 32 × 1024² 像素，动画按帧累计画布占用。外部图片 URL 不会自动加载。消息/区块展示有数量上限，未支持的内容明确提示。超限、不完整、二进制、读取失败或无法识别的消息结构自动展示原文片段，仍可分页或下载完整原文。

## 数据与使用边界

- 保存模型网关收到的原始 body 字节、多值 headers、Host、原始 URL、协议和 trailers；HTTP 与 WebSocket 消息分别记录。入站内容在解压、协议转换和上游凭据改写之前采集。
- header 值、Host 或 URL 含有非 UTF-8 字节时，该单值使用 `{encoding:"base64",content:"..."}` 保真表示，正常文本仍是字符串，多值顺序不变。
- `User-Agent` 的原始多值与完整长度在全局会话绑定归一化前保留，业务侧仍使用原有归一化值。包含 NUL 或无效 UTF-8 的摘要值不写入数据库摘要字段，原文 `BYTEA` 不变；明确的数据错误只隔离对应请求，不拖累同批其他请求。
- 请求记录与用量、操作审计分开存储。原文分为 headers、body、trailers 三部分，每块最多 256 KiB，以 PostgreSQL `BYTEA` **明文**保存，无额外应用层加密。数据库默认 TOAST 行为不等同于加密。
- 管理员可以预览、分段查看和下载原始字节。二进制预览采用 base64；下载不把正文重新序列化。headers 默认遮盖常见凭据、请求 URL 的用户名/密码及敏感查询参数（包括 `key`、`api_key`）；无法安全解析的 URL 整体遮盖。管理员可显式显示或下载完整内容，无二次验证要求，原始存储不变。
- 复用管理员鉴权与操作审计；读取原文、修改配置、封禁、解封都会留操作记录。非管理员不能调用这些接口，响应不允许缓存。
- `complete` 表示终结清单与数据库实际分块数、顺序和字节数一致；`partial` 表示正文未读完、读取错误、预算耗尽或缺块；`capturing` 表示未收到完整终结信息。详情顶部显示整条记录状态，各部分预览依据读取接口的 `partial` 独立判定完整性，其他部分不完整不会阻止完整请求体的消息视图。HTTP 成功不能替代完整性验证。
- 原文采集不会预读请求，不等待数据库，也不会为审计继续读取已被拒绝的 body。鉴权失败、断连或超限时，只能保存入口信息及业务实际读取到的前缀。
- 队列拥塞、数据库故障或进程崩溃时优先继续业务，审计可能丢失。运行状态展示已知丢弃、写入失败、封禁查询失败等实例级指标，不能将此功能视为零丢失存档。
- 不记录响应正文。代理/CDN 在进入应用前拒绝的请求、HTTP/TLS 帧，以及直接发往上游的 WebRTC 媒体不在采集范围内。

## 会话封禁

在请求详情选择「封禁会话」，填写向客户端返回的原因，可选内部备注和到期时间。服务端从所选请求记录提取 `API Key ID + 入站原始会话 ID`，不接受前端任意指定身份；缺少可用身份时拒绝创建封禁。

封禁在调用上游之前检查，命中后按对应协议返回 HTTP `403` 与公开原因，OpenAI 兼容错误码为 `session_banned`。WebSocket 在后续请求或透明控制通道消息转发前检查。关闭采集和日志到期都不会删除封禁；封禁支持到期失效和手动解除。

Claude 会话提取兼容业务已支持的 UTF-8 BOM 和宽松 JSON，并使用相同的规范化大小上限；超限仍由业务 reader 返回 `413`。Composite WebSocket 在首帧确定目标平台后补齐 Grok 会话身份，再记录并检查封禁，拒绝的帧不会转发上游。

HTTP 和 Responses 中已经准入的请求不会因重复检查而取消；实时语音/Live 透明通道可能由音频触发服务端自动生成，因此在下一条客户端消息命中封禁时关闭连接，可能影响该连接仍在输出的内容。

客户端可以更换会话 ID。需要阻止用户持续滥用时，配合现有 API Key 或用户禁用功能。封禁查询故障时继续放行并累计失败指标。多实例的开关配置周期刷新，不应将修改响应理解为全部实例瞬时切换。

封禁操作遇到网络或提交结果不确定时，先按返回的封禁 ID 或来源请求查询状态，再决定是否重试。明确查询到不存在或没有生效规则后，可保留草稿重试；查询仍失败时继续保持待确认状态。不要把超时直接理解为操作未生效。

## 设备封禁

在请求详情查看设备类型、原始标识、来源与解析状态，选择「封禁设备」，填写公开原因，可选内部备注和到期时间。「设备封禁」页签可查询及解封；「同设备请求」按类型和设备 ID 查询跨用户、Key 和会话的记录，仍受当前保留期限制。没有有效设备标识的历史记录显示缺失，不从正文回填或用会话/缓存标识代替。

规则从来源审计记录读取 `device_type + device_id`，不接受前端自填身份。类型为 `claude` 或 `codex`，两者分别匹配；规则不限定用户、分组或 API Key。设备 ID 保留原始内容和大小写，不使用任何上游账号的 `openai_device_id`、收敛指纹或重新派生值。有效 ID 必须是非空、最多 255 UTF-8 字节、没有首尾空白和控制字符的字符串。

- Claude Messages：从原始 body 的 `metadata.user_id` JSON 字符串读取 `device_id`；兼容旧版 `user_<64hex>_account_<uuid>_session_<uuid>`。JSON 格式即使没有会话 ID 仍能提取设备。Messages 不使用其他协议的 Codex 请求头覆盖 Claude 设备。
- Codex：优先读 `X-Codex-Installation-Id`，其次读 `X-Codex-Turn-Metadata` JSON 中的 `installation_id`；没有有效头标识时，Responses 从 `client_metadata["x-codex-installation-id"]` 或 `client_metadata["x-codex-turn-metadata"]` JSON 字符串内的 `installation_id` 补齐。WS `response.create` 支持 `response` 包裹的请求体。同一解析层（请求头或请求体）内多个有效来源不一致时保留优先来源，并记录 `conflict`。

设备识别发生在入站协议/指纹改写之前，HTTP 每个已解析设备只做一次准入检查；命中后返回 `403` 与公开原因，OpenAI 兼容错误码为 `device_banned`。WS 在首个及后续生成请求转发前检查，发送 `device_banned` 错误并以 1008 关闭连接；已准入 Responses 的取消等控制消息仍可通过。透明实时通道沿用逐条客户端消息检查机制。连接保留第一个有效设备标识，后续请求体或出站改写不替换它。

`device_ban_enabled` 默认关闭，独立于采集和会话封禁；关闭采集、审计到期或清理均不删除设备规则。采集和设备封禁都关闭时，不执行新增的设备提取或设备规则查询，现有会话流程保持不变。规则支持到期失效、幂等解封和提交结果不确定时查询恢复。缺失或无效设备标识不触发设备封禁；查询故障沿用可用性优先策略放行，并累计 `ban_check_failures`。客户端可修改或省略这些标识，这属于安装标识匹配，不是不可绕过的硬件识别。

WS 连接在采集和设备封禁都关闭时建立，不保留设备标识。之后开启设备封禁，Responses 只能从后续带有效设备标识的生成请求补齐；仅在握手或首帧发送安装标识的客户端需要重连。已经绑定的有效设备标识不会被后续消息替换。

## 保留和资源配置

查询按请求接收时间计算滚动 `N × 24 小时` 窗口；后台每 5 分钟分批清理。清理中的记录不会因延长保留期而重新显示。封禁规则不随请求记录级联删除。PostgreSQL 删除释放的空间通常用于内部复用，文件不一定立即缩小；备份和 WAL 的保留周期独立管理。

配置键 `request_audit` 复用现有服务配置体系：

```yaml
request_audit:
  buffer_bytes: 67108864
  max_events: 8192
  write_timeout_ms: 1000
  db_max_connections: 2
  policy_max_connections: 2
  ban_lookup_timeout_ms: 20
```

运行开关、保留天数及配置版本保存在 `settings.request_audit_config`。管理端更新使用 `expected_version` 检测并发修改。独立连接池将审计写入与封禁查询隔离；内存预算包含排队和写入中的事件，不能当作进程 RSS 增量的绝对上限。

WebSocket 消息保留握手入口快照。启用采集后，旧连接需要重新连接才能取得完整入口信息。实时语音等透明通道可能频繁检查封禁，应按真实消息频率验证数据库容量。

## 管理 API

全部路由在 `/api/v1/admin` 下，要求现有管理员身份，沿用 `{code,message,data}` 响应；业务错误附带 `reason`，原文下载返回二进制。

| 方法 / 路径 | 输入和输出 |
| --- | --- |
| `GET /request-audits` | `from/to`（RFC3339）、`user_id`、`api_key_id`、`group_id`、`request_id`、`session_id`、`device_type`、`device_id`、`kind`、`capture_state`、`http_status`、`cursor`、`limit`；返回 `items/next_cursor/has_more`，单页最多 100 条；会话/请求/设备 ID 可独立筛选，API Key ID 可选；请求 ID 最多 256 UTF-8 字节，拒绝控制字符，按精确值查询 |
| `GET /request-audits/:id` | UUID；返回入口摘要、身份、结果、终结清单与完整性状态，不返回原文 |
| `GET /request-audits/:id/payload` | `part=headers/body/trailers`；`mode=preview/raw`；`offset/limit`；不完整内容需 `allow_partial=true` |
| `GET /request-audits/config` | 返回开关、保留天数、版本、更新时间和管理员 ID |
| `PUT /request-audits/config` | `enabled`、`session_ban_enabled`、`device_ban_enabled`、`retention_days`、`expected_version`；返回新配置；旧客户端省略 `device_ban_enabled` 时保留当前值，版本冲突保护不变 |
| `GET /request-audits/status` | 返回当前实例配置状态、队列、失败/丢弃和清理指标 |
| `GET /session-bans` | `api_key_id/session_id/source_audit_id/state/cursor/limit`；返回游标列表 |
| `POST /session-bans` | `source_audit_id/public_reason` 必填，`internal_note/expires_at` 可选；成功返回 201 和封禁规则 |
| `GET /session-bans/:id` | 查询规则及当前 active/expired/revoked 状态 |
| `POST /session-bans/:id/revoke` | 幂等解封，返回规则 |
| `GET /device-bans` | `device_type/device_id/source_audit_id/state/cursor/limit`；返回 `items/next_cursor/has_more`，每页最多 100 条 |
| `POST /device-bans` | `source_audit_id/public_reason` 必填，`internal_note/expires_at` 可选；设备身份只从来源记录取得；成功返回 201 和规则 |
| `GET /device-bans/:id` | 查询设备规则及当前 active/expired/revoked 状态 |
| `POST /device-bans/:id/revoke` | 幂等解封设备，返回规则 |

预览每次最多 256 KiB，返回 `content/encoding/offset/returned_bytes/part_bytes/has_more/capture_state/partial/redacted`；headers 预览可用 `reveal_sensitive=true` 显示凭据。原文下载支持单个 HTTP `Range`，失败范围返回 416；响应提供 `Content-Length` 和完整性标头。下载中途读库失败会中断连接，不能当作完整成功文件。并发原文读取最多 2 个，读取上下文与下载写入限时 30 秒，过长文件可分段下载；此限时仅作用于审计下载，不改变网关 SSE/WS 超时。

常见错误：401 未登录、403 非管理员、400 参数错误、404 不存在或已到期、409 配置版本冲突/身份不可用/原文不完整、416 无效范围、429 原文读取繁忙、503 存储不可用或封禁结果待确认。客户端应使用 `reason` 和当前状态查询判断后续操作，不解析英文报错文本。

## 部署与回退

设备封禁新增迁移 `247_request_audit_device_bans.sql`，增加审计设备摘要列、设备查询索引及独立 `device_bans` 表。升级时应用迁移并发布管理端资源；新增索引可能阻塞审计写入，建议低峰部署。设备开关默认关闭，先核对 Claude/Codex 的原始设备摘要，再使用隔离测试 Key 验证跨 Key/会话拦截、公开原因、到期及解封；HTTP/SSE/WS 行为应一并回归。升级前已建立的 WS 连接应重连以保证能识别原始设备。回退前先关闭设备封禁，保留新增列、表和规则，不删除数据卷。

1. 发布前完成后端全量、unit、lint、integration，前端 typecheck、test 和 build；真实 PostgreSQL/Redis 集成验证需 Docker。自动跳过不代表验证通过。
2. 部署镜像包含迁移 `243_request_audit.sql`、`244_request_audit_cleanup_index.sql`、`245_request_audit_global_session_index.sql`、`246_request_audit_filter_indexes.sql`、`247_request_audit_device_bans.sql` 和新的内嵌管理端。244 为清理中的记录补充部分索引；清理分别查询删除中记录与新过期记录，避免每条记录重复全表扫描排序。245 为独立会话查询增加有序部分索引；246 增加分组/入站请求 ID 查询索引与独立用量 ID 关联表，关联表带精确查询和级联删除索引；247 增加设备摘要、查询索引及设备规则表。索引构建期间会阻塞审计表写入，建议在低峰更新并观察写入失败/丢弃计数，大表需预留构建时间。沿用现有迁移机制执行，不修改历史业务数据；不要删除数据卷或直接覆盖线上 compose。
3. 确认 `/admin/request-audits` 页面、`/api/v1/admin/request-audits/*`、`/api/v1/admin/session-bans/*`、`/api/v1/admin/device-bans/*` 经 Nginx 正常转发，非管理员被拒绝。此次不新增 OAuth 或支付回调。
4. 首次部署保持采集、会话封禁和设备封禁三个开关关闭，先验证健康接口与既有 HTTP/SSE/WS 行为。再启用采集，以真实 JSON、压缩体、multipart、大请求与 WS 消息核对原始字节和 headers；启用后重连已有 WS。
5. 用隔离测试 Key 验证封禁原因、后续 HTTP/WS 请求拦截和解封；不要用正在服务的用户会话做破坏性验证。监测网关 p95/p99、吞吐、内存、数据库 I/O、WAL、丢弃和清理延迟，按实际流量确定资源预算。
6. 回退可先关闭采集；人工封禁开关独立处理。应用回退时保留新增表与规则，后续确认不再需要后再制定独立清理计划。

本地测试通过不能代替生产容量压测。运行指标是当前实例自启动以来的计数，重启会归零；负载均衡场景需要逐实例查看或接入现有监控。

## 初版验证记录（2026-10-14）

后端命令在 `backend/` 执行，前端命令在仓库根目录执行。Windows 验证时临时将 Git 的 `bin` 目录加入当前进程 `PATH`，供现有 PgDumper 测试调用 `sh`；未修改全局环境或项目依赖。

| 命令 / 检查 | 实际结果 |
| --- | --- |
| `go test -p 2 ./...` | 全包通过 |
| `go test -p 2 -tags=unit ./...` | 全包通过，包含 API 契约测试 |
| `golangci-lint run --concurrency 2 --timeout=30m ./...` | 通过，0 issues；使用临时目录中的官方 2.14.0 工具，已核对官方 SHA256，未替换全局工具 |
| `go test -race ./internal/requestaudit` | 通过 |
| `go test -p 2 ./internal/service -run '^TestRequestAuditWebSocket' -count=1` | 最后一次类型断言修正后的 WebSocket 回归通过 |
| `go test -p 2 -tags embed ./internal/web` | 通过 |
| `pnpm --dir frontend run typecheck` | 通过 |
| `pnpm --dir frontend run test:run` | 369 个测试文件、2902 个测试通过 |
| `pnpm --dir frontend run build` | 通过 |
| 管理接口、HTTP/WS、HTTP/1.1 与 TLS HTTP/2 下载截断回归 | 通过；中途失败时客户端读取报错，没有拼接 JSON 响应 |
| 本地模拟 API 的桌面、窄屏和交互检查 | 通过；预览服务已停止，未操作生产数据 |
| 改动 Go 文件的 `gofmt -l` | 无输出 |
| `git diff --check` / `git status --short` | 空白检查通过；仅本功能相关改动，未暂存构建产物、配置或数据文件 |
| `go test -tags=integration ./internal/requestaudit -run TestRequestAuditPostgresIntegration -count=1` | 容器启动失败，本机无可用 Docker；PostgreSQL 集成用例未执行，不计为通过 |

尚未完成完整 `go test -tags=integration ./...`、远端完整 CI 和生产容量验证。发布前必须在具备 Docker 的环境补齐集成验证，并根据真实 HTTP/WS 流量完成容量评估。本次仅修改本地 `feat/my-sub` 工作区，未提交、推送或部署。

## 审查复核与修复（2026-10-14）

审查会话 `01a13a62-7eb3-7a82-b9b6-364c5486c71b` 的七项问题均复核成立，修复如下：

| 确认的问题 | 修复及回归范围 |
| --- | --- |
| 含 NUL 的 model 导致同批审计回滚 | 拒绝无效摘要、保留原文；明确的行数据错误只剔除对应请求。验证健康事件落库、预算释放，以及连接/配置/提交错误仍走整体故障处理 |
| 清理条件导致反复全表扫描排序 | 拆分 deleting 与 active-expired 查询，新增迁移 244 的部分索引；覆盖删除继续执行、保留期延长不复活与执行计划 |
| 全局中间件提前截短/合并 User-Agent | 在改写前保留原始值，采集时消费并释放；验证长值、多值、缺失值及业务归一化行为不变 |
| BOM 请求绕过 Claude 会话封禁 | 与业务使用相同的宽松解析及大小上限；覆盖 BOM、原始控制字节、原文不变、超限 413 与提高上限后命中 403 |
| Composite→Grok WebSocket 会话未绑定 | 首帧路由确定后补绑、记录并准入；覆盖首轮/后续轮次封禁、透明转发、记录身份与原始字节 |
| SSE 输出后的普通 panic 不记录日志 | 先记录异常与堆栈，再判断响应已输出；保留 ErrAbortHandler 特例，不拼接 JSON 或记录请求凭据头 |
| 明确查无封禁后无法原地重试 | 保留草稿并恢复提交；未知结果继续禁用；成功确认后刷新列表，新增八个前端回归分支 |

额外复核了兼容解析的内存上界：不采用输入长度六倍的解析上限，改为传递业务配置 `gateway.max_body_size`，避免审计在业务拒绝前先生成过大副本。

本轮修复后的实际验证结果：

| 命令 / 检查 | 结果 |
| --- | --- |
| `go test -p 2 ./...` | 最终全包通过 |
| `go test -p 2 -tags=unit ./...` | 最终全包通过，包含 API 契约测试 |
| `golangci-lint run --concurrency 2 --timeout=30m ./...` | 官方 2.14.0，最终全量 0 issues |
| `go test -race ./internal/requestaudit` | 通过，含坏事件隔离和首条 WS 消息身份回归 |
| `go test -p 2 -tags=unit ./internal/server/middleware -run 'TestRequestAudit\|TestRecovery\|TestSessionBindingContext' -count=1` | 通过 |
| `go test -p 2 ./internal/service ./internal/server/middleware -run '^TestRequestAuditClaude' -count=1` | 大小上限修正后通过 |
| `go test -p 1 ./internal/handler -run 'Test(RequestAudit\|OpenAIResponsesWebSocket_Composite)' -count=1` | 最终首帧修改后通过 |
| 临时 Go overlay 验证采集后的 headers 原文 | 长 UA、多值 UA、缺失 UA 与原始请求一致 |
| `go test -p 2 -tags embed ./internal/web` | 通过 |
| `pnpm --dir frontend run typecheck` | 通过 |
| `pnpm --dir frontend run test:run` | 369 个文件、2910 个测试通过 |
| `pnpm --dir frontend run build` | 通过；构建产物未暂存 |
| PGlite 的 SQL / 执行计划复核 | 原问题复现；244 可重复执行，两条新清理查询均有无排序的索引扫描路径；不能替代真实 PostgreSQL 集成或容量验证 |
| `go test -p 2 -tags=integration ./internal/requestaudit -run '^TestRequestAuditPostgresIntegration$' -count=1` | 再次在容器启动时失败：`rootless Docker is not supported on Windows`；数据库用例未执行 |
| `gofmt -l`、`git diff --check`、`git status --short` | 格式与空白检查通过，无暂存文件；保留本地功能改动，未提交、推送或部署 |

发布前仍需在可用 Docker 环境完成全量 integration 和远端 CI，并验证启用采集、封禁与过期清理时的网关延迟及数据库负载。此次保持两个运行开关默认关闭。

## 审计可读性改造验证（2026-10-15）

本轮在 `feat/my-sub` 完成名称/分组展示、用户名或邮箱选择、独立会话查询、自动原文预览及消息/内嵌图片视图，纳入个人版本 `v0.1.52`。发布流程为先推分支，确认该提交的 CI（含真实数据库集成）及 Security Scan 全部通过，再单独推送 annotated Tag；不直接部署生产。

| 命令 / 检查 | 实际结果 |
| --- | --- |
| `go test -p 2 ./...` | 全包通过 |
| `go test -p 2 -tags=unit ./...` | 全包通过，含 API 契约 |
| `golangci-lint run --concurrency 2 --timeout=30m ./...` | 官方 2.14.0，全量 0 issues |
| `go test -p 2 -tags embed ./internal/web` | 新管理端构建后通过 |
| `pnpm --dir frontend run typecheck` / `run lint:check` / `run build` | 通过 |
| `pnpm --dir frontend run test:run --maxWorkers=2 --minWorkers=1` | 发布前审查修复后，全量 373 个文件、3004 项通过；含请求审计 6 个文件、117 项 |
| `pnpm --dir frontend exec vitest run src/features/request-audit/__tests__ --maxWorkers=2 --minWorkers=1` | 读取段数边界补丁后，6 个文件、97 项通过；后续图片与完整性回归已纳入上述最终全量验证 |
| 本地模拟 API 浏览器检查 | 1600px / 390px 布局、用户名搜索、无 Key 会话查询、消息内嵌图片、原文切换通过；未操作生产数据 |
| `gofmt -l` / `git diff --check` / `git status --short` | Go 格式与空白检查通过，仅本次相关源码、测试和文档；构建产物/截图未纳入提交 |
| `go test -tags=integration ./internal/requestaudit -run '^$'` | 仅编译通过；本机无可用 Docker，数据库用例尚未实际执行 |

初次前端全量测试遇到 Vitest worker 的调用栈溢出，限制并发后完整通过；一次并行 lint 遇到已被 Vite 删除的临时配置文件，重新执行通过。上述环境失败没有计为通过。真实 PostgreSQL 的 metadata JOIN、跨 Key 会话查询和迁移 245 索引计划仍需在 CI 执行，不能以编译检查替代。新索引在大表上的构建时间与写入等待需按部署章节处理。

发布前交叉审查确认并修复了以下问题：

- 高压缩图片可能以很小的文件产生超大解码画布：在设置图片地址前检查容器尺寸、动画帧与累计像素预算，覆盖四种支持的格式及 APNG 声明帧数一致性。
- 绝对请求 URI 的用户名/密码未被默认遮盖：与查询参数一并脱敏，显式显示和原文下载保持原始字节。
- 其他部分不完整导致完整 Body 无法展示：依据当前部分的 `partial` 判断；缺少明确完整性结果时仍回退原文，整体状态保留在详情顶部。

专项回归已覆盖普通/渐进 JPEG、PNG、GIF、WebP 和动画图片、超大画布/帧预算、URI 脱敏与原文保真，以及整体部分状态下的完整 Body 跨段读取。审查未发现剩余阻断问题；本机不具备 Docker，发布前仍必须确认待发布提交的远端 CI 集成步骤实际通过。

## Codex 会话识别与分组标签验证（2026-10-15）

`v0.1.53` 补齐 Codex 明确会话来源，并将列表分组合并为单个「平台-分组名称」彩色标签。独立审查发现并修复了两个边界：Composite Responses WebSocket 握手必须等首帧确定平台后再绑定低优先级元数据；从元数据 Header 提取的短 ID 必须复制，避免长连接保留整段 Header。最终审查未发现剩余阻断问题。

| 命令 / 检查 | 实际结果 |
| --- | --- |
| `go test -p 2 ./...` / `go test -p 2 -tags=unit ./...` | 全包通过，含 API 契约 |
| `golangci-lint run --concurrency 2 --timeout=30m ./...` | 官方 2.14.0，全量 0 issues；已补齐新测试的返回值检查 |
| service、middleware、handler 的 RequestAudit 与既有会话专项回归 | 通过；含真实 Composite WS 中间件链、普通头/Grok 优先级、压缩体、原文保真、非法输入与长度限制 |
| `go test -p 2 -tags embed ./internal/web` | 新前端构建后通过 |
| `pnpm --dir frontend run typecheck` / `run build`、改动文件 ESLint | 通过 |
| `pnpm --dir frontend run test:run --maxWorkers=2 --minWorkers=1` | 373 个文件、3004 项通过 |
| 本地模拟 API 浏览器检查 | 1600px / 390px 单标签、长名称完整提示、多平台与缺失关联回退通过，无页面水平溢出和控制台错误 |
| `gofmt -l` / `git diff --check` / `git status --short` | 格式、空白检查通过；仅本轮源码、测试和文档，截图/构建产物未纳入提交 |

本机无可用 Docker，本轮真实 PostgreSQL/Redis 集成测试由远端完整 CI 执行；必须等同一待发布提交的 CI 与 Security Scan 全部通过后再推 Tag。无需新增配置或迁移，部署后核对新请求的会话 ID 与分组标签，已有 WebSocket 连接需重连；历史记录不回填。

## 分组与请求 ID 筛选验证（2026-10-15）

`v0.1.54` 增加分组和使用记录请求 ID 查询，调整筛选区间距，并将类型、完整性、会话封禁状态统一为项目公共 Select。后端显式关联最终用量 ID 与采集入口，异步任务仅复制轻量关联标识，不保留原请求正文或上下文。独立审查覆盖关联队列、计费调用链、索引、分页、筛选重置及公共 Select 语义，未发现剩余阻断问题。

| 命令 / 检查 | 实际结果 |
| --- | --- |
| `go test -p 2 ./...` / `go test -p 2 -tags=unit ./...` | 全包通过，含 API 契约 |
| `golangci-lint run --concurrency 2 --timeout=30m ./...` | 官方 2.14.0，全量 0 issues |
| `go test -race ./internal/requestaudit` | 通过 |
| 审计筛选、关联队列、实际 usage 上下文与用量写入挂点专项测试 | 通过；覆盖原 Key 校验、Finish/取消后关联、队列故障不影响用量保存、分页与精确查询 |
| `pnpm --dir frontend run typecheck` / `run build`、改动文件 ESLint | 通过；构建产物未纳入提交 |
| `pnpm --dir frontend run test:run --maxWorkers=2 --minWorkers=1` | 最终统一下拉框后，全量 374 个文件、3010 项通过 |
| `go test -p 2 -tags embed ./internal/web` | 最终前端构建后通过 |
| 本地模拟 API 浏览器检查 | 1600px / 390px 布局、分组搜索、请求 ID 参数、重置及三个下拉菜单通过；方向键/Enter/Escape 正常，无页面水平溢出和控制台错误 |
| `go test -tags=integration ./internal/requestaudit -run '^$'` | 仅编译通过；本机无可用 Docker，真实数据库用例需由远端 CI 执行 |
| `gofmt -l` / `git diff --check` / `git status --short` | 格式和空白检查通过；只提交本轮源码、测试、迁移及文档，排除截图、构建产物和本地配置 |

发布前须确认同一提交的 CI（含真实 PostgreSQL/Redis 集成）与 Security Scan 全部通过，再单独推送 annotated Tag。部署需执行迁移 246，并核对管理端分组选择、普通 HTTP 请求 ID 与新 Responses WebSocket 用量 ID 查询。WebSocket 查询定位所属连接；旧响应 ID、未传递采集上下文的通道及已过期/丢弃的审计不回填，详见本文开头的查询边界。运行开关及现有管理路由不变。

## 设备封禁验证（2026-10-15）

本轮增加原始设备摘要、跨用户和 Key 的设备精确封禁、独立开关、管理 API 和管理页面。回归覆盖 Claude 无会话设备、Codex Header/Body/WS 来源优先级、原始字节保真、采集关闭后拦截、过期与解封、并发去重及不确定写入恢复。额外修复两处边界：Messages 不让 Codex Header 覆盖 Claude 设备；WS 后续轮次的本地设备拦截不计入上游账号调度失败，真实上游 1008 认证错误仍会报告。

| 命令 / 检查 | 实际结果 |
| --- | --- |
| `go test -p 2 ./...` / `go test -p 2 -tags=unit ./...` | WS 错误归因修正后最终全包通过，包含 API 契约 |
| `golangci-lint run --concurrency 2 --timeout=30m ./...` | 官方 2.14.0，最终全量 0 issues |
| service、middleware、handler、admin 的设备专项回归 | 通过，包含真实 Composite WS 中间件链与设备错误归因 |
| `go test -race ./internal/requestaudit` | 通过 |
| `pnpm --dir frontend run typecheck` / `run lint:check` / `run build` | 最终页面修正后通过；构建产物未纳入提交 |
| `pnpm --dir frontend run test:run --maxWorkers=2 --minWorkers=1` | 最终全量 374 个文件、3021 项通过 |
| `go test -p 2 -tags embed ./internal/web` | 最终管理端构建后通过 |
| 本地模拟 API 浏览器检查 | 1440px / 390px 的设备详情、创建、解封、列表状态和作用范围提示通过，无页面水平溢出；浏览器与预览服务已关闭，未修改生产数据 |
| 改动 Go 文件 `gofmt -l` / `git diff --check` / `git status --short` | 格式和空白检查通过；仅本次源码、测试、迁移和文档，未暂存构建产物、截图、配置或数据文件 |
| `go test -tags=integration ./internal/requestaudit -run '^$'` | 仅编译通过 |
| `go test -tags=integration ./internal/requestaudit -run TestRequestAuditPostgresIntegration -count=1` | 容器启动失败：`rootless Docker is not supported on Windows`；数据库断言未执行，不计为通过 |

新增迁移 247 的真实数据库持久化、并发封禁、过期续封及来源清理用例须在可用 Docker 环境执行，发布前仍需完整 `go test -tags=integration ./...` 和同一提交的远端 CI。部署先应用迁移 247、更新内嵌管理端和核对 `/api/v1/admin/device-bans/*` 路由，再在请求审计设置启用默认关闭的设备封禁；已有 WS 连接需重连。本轮仅保留本地 `feat/my-sub` 工作区改动，未提交、推送、创建版本或部署。

## v0.1.55 发布复核（2026-10-15）

设备封禁及分组标签简化纳入 `v0.1.55`。本节记录上述初轮验证之后的最终复核：

- 采集和设备封禁都关闭时跳过新增设备解析和规则查询，保留原有会话检查；覆盖 HTTP/SSE 原始请求体及真实 WS 两轮正常转发。
- 封禁列表分页使用已应用的筛选快照，修改筛选草稿或新查询失败均不会将不同条件的结果混入旧列表；会话和设备两种列表均有回归。
- 请求列表所属分组只显示平台图标和分组名称，详情保留独立平台标签。
- 交叉复核管理权限、操作审计、旧配置兼容、规则持久化及 WS 拒绝归因，未发现剩余发布阻断。

| 命令 / 检查 | 最终本地结果 |
| --- | --- |
| `go test -p 2 ./...` | 全包通过 |
| `go test -p 2 -tags=unit ./...` | 全包通过，包含 API 契约 |
| `golangci-lint run --concurrency 2 --timeout=30m ./...` | 官方 2.14.0，全量 0 issues |
| `go test -race ./internal/requestaudit` | 通过 |
| `pnpm --dir frontend run typecheck` / `run lint:check` / `run build` | 通过 |
| `pnpm --dir frontend run test:run --maxWorkers=2 --minWorkers=1` | 374 个文件、3023 项通过 |
| `go test -p 2 -tags embed ./internal/web` | 最终管理端构建后通过 |
| 改动 Go 文件 `gofmt -l` / `git diff --check` | 格式和空白检查通过 |

本机真实数据库集成仍受 Windows Docker 环境限制，不计为通过。发布顺序为推送 `feat/my-sub`，核对同一提交完整 CI 的 Integration tests 实际成功及 Security Scan 全部通过，再单独推送 annotated Tag；不直接部署生产。提交排除构建产物、截图、本地配置和数据文件。

部署需执行迁移 247 并更新内嵌前端，设备封禁默认关闭。先验证健康接口及普通 HTTP/SSE/WS 请求，再以隔离测试 Key 验证 `/api/v1/admin/device-bans/*` 的创建、查询、解封和到期。新增索引可能阻塞审计写入，应在低峰更新并观察丢弃计数。启用后已有 WS 连接应重连；只在握手或首帧发送设备标识的旧连接无法从后续无标识消息补齐设备身份。
