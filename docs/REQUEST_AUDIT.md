# 请求记录与会话封禁

请求记录位于管理端「请求审计」（`/admin/request-audits`）。采集和人工会话封禁是两个独立开关，默认关闭；默认保留最近 3 天，可配置 1～30 天。

列表和详情优先显示用户名/邮箱、API Key 名称、分组名称及平台。列表的所属分组使用一个彩色标签，例如 `OpenAI-分组名称`，长名称省略并提供完整悬停提示。`user_username`、`user_email`、`api_key_name`、`group_name`、`group_platform` 随列表和详情一次查询返回，旧 ID 字段保留；软删除关联仍可显示名称，关联缺失时回退到 ID。这些名称和平台是关联对象的当前值；多平台分组显示「多平台」，不代表该次请求最终路由的平台。

用户筛选复用已有管理员用户搜索，输入用户名或邮箱后选择匹配用户（含软删除用户），再按其 ID 精确查询。会话 ID 可单独筛选并返回跨 API Key 的匹配记录，填写 API Key ID 可以缩小范围。客户端提供的会话 ID 不保证跨 Key 全局唯一，因此会话封禁仍严格作用于「API Key + 会话 ID」。

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
| `GET /request-audits` | `from/to`（RFC3339）、`user_id`、`api_key_id`、`session_id`、`kind`、`capture_state`、`http_status`、`cursor`、`limit`；返回 `items/next_cursor/has_more`，单页最多 100 条；会话 ID 可独立筛选，API Key ID 可选 |
| `GET /request-audits/:id` | UUID；返回入口摘要、身份、结果、终结清单与完整性状态，不返回原文 |
| `GET /request-audits/:id/payload` | `part=headers/body/trailers`；`mode=preview/raw`；`offset/limit`；不完整内容需 `allow_partial=true` |
| `GET /request-audits/config` | 返回开关、保留天数、版本、更新时间和管理员 ID |
| `PUT /request-audits/config` | `enabled`、`session_ban_enabled`、`retention_days`、`expected_version`；返回新配置 |
| `GET /request-audits/status` | 返回当前实例配置状态、队列、失败/丢弃和清理指标 |
| `GET /session-bans` | `api_key_id/session_id/source_audit_id/state/cursor/limit`；返回游标列表 |
| `POST /session-bans` | `source_audit_id/public_reason` 必填，`internal_note/expires_at` 可选；成功返回 201 和封禁规则 |
| `GET /session-bans/:id` | 查询规则及当前 active/expired/revoked 状态 |
| `POST /session-bans/:id/revoke` | 幂等解封，返回规则 |

预览每次最多 256 KiB，返回 `content/encoding/offset/returned_bytes/part_bytes/has_more/capture_state/partial/redacted`；headers 预览可用 `reveal_sensitive=true` 显示凭据。原文下载支持单个 HTTP `Range`，失败范围返回 416；响应提供 `Content-Length` 和完整性标头。下载中途读库失败会中断连接，不能当作完整成功文件。并发原文读取最多 2 个，读取上下文与下载写入限时 30 秒，过长文件可分段下载；此限时仅作用于审计下载，不改变网关 SSE/WS 超时。

常见错误：401 未登录、403 非管理员、400 参数错误、404 不存在或已到期、409 配置版本冲突/身份不可用/原文不完整、416 无效范围、429 原文读取繁忙、503 存储不可用或封禁结果待确认。客户端应使用 `reason` 和当前状态查询判断后续操作，不解析英文报错文本。

## 部署与回退

1. 发布前完成后端全量、unit、lint、integration，前端 typecheck、test 和 build；真实 PostgreSQL/Redis 集成验证需 Docker。自动跳过不代表验证通过。
2. 部署镜像包含迁移 `243_request_audit.sql`、`244_request_audit_cleanup_index.sql`、`245_request_audit_global_session_index.sql` 和新的内嵌管理端。244 为清理中的记录补充部分索引；清理分别查询删除中记录与新过期记录，避免每条记录重复全表扫描排序。245 为独立会话查询增加有序部分索引；索引构建期间会阻塞审计表写入，建议在低峰更新并观察写入失败/丢弃计数，大表需预留构建时间。沿用现有迁移机制执行，不修改历史业务数据；不要删除数据卷或直接覆盖线上 compose。
3. 确认 `/admin/request-audits` 页面、`/api/v1/admin/request-audits/*`、`/api/v1/admin/session-bans/*` 经 Nginx 正常转发，非管理员被拒绝。此次不新增 OAuth 或支付回调。
4. 首次部署保持两个开关关闭，先验证健康接口与既有 HTTP/SSE/WS 行为。再启用采集，以真实 JSON、压缩体、multipart、大请求与 WS 消息核对原始字节和 headers；启用后重连已有 WS。
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
