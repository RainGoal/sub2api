# Video Tasks JSON v1 运维与验收

本轮交付通用视频协议、33 个 BBLabu 能力模板、独立售采单位、任务恢复和管理端复核。
内部平台仍是 `seedance`，已有 `bblabu_v1` / `fflink_v1`、历史任务和图片流程继续兼容。
模板和本地测试不代表已经完成供应商真实生成或账单验收。
当前版本默认启用 Video Tasks JSON v1，无需额外配置
`GATEWAY_VIDEO_TASKS_JSON_V1_ENABLED=true`；已有显式 `false` 配置仍优先于默认值。

## 升级顺序

1. 备份线上 compose 和数据库；先在具备 Docker 的环境通过完整 integration 测试。
2. 使用明确提交号或版本号构建应用镜像，保留原配置与数据卷。
3. 多实例滚动升级时，先显式设置 `gateway.video_tasks_json_v1_enabled: false`，
   再升级所有应用和恢复 worker，避免旧实例接收到新任务。
4. 确认新增迁移 `248z`、`248za`、`248zb` 全部成功，全部实例均支持新任务。
5. 检查健康接口、日志、管理端静态资源及下列网关路由；再启用选定模型。
6. 未配置开关的部署在新版重启后默认启用；若升级期间显式关闭，移除 `false`
   配置或改为 `true`，重启相应实例使配置生效。

开关默认启用；包括旧协议渠道在内的新视频任务使用 `vid_` 公开身份与冻结快照。
关闭开关会停止新版创建，已有任务仍需继续查询、结算、下载和人工处理。
旧程序不能处理新任务：回滚时先关闭创建并排空新版任务及预占。
还须保留支持 v1 的实例承接查询和下载，直到最后一笔成功结算后的 7 天取回窗口结束，
不能在任务刚结算时直接切回旧镜像。
不要删除迁移或数据，不要执行 `docker compose down -v`。

## 账号与定价

在现有账号页面选择 Video Tasks JSON v1，填写供应商名称、HTTPS API 基址和 Key。
裸域名会补 `/v1`，自定义版本前缀原样保留；基址不包含 `/models` 或 `/videos`。
同步只发现型号，管理员必须明确选择允许的型号；未知型号还需配置有效能力。
内置模板不带价格，切换协议不会自动上架，也不会自动填写供应商地址。

在账号配置显式采购价，在渠道配置显式视频售价，并分别选择按秒或按条。
金额单位是 USD；供应商平台点数或“刀”需要先按实际兑换规则换算。
按秒须选择请求秒数或可信的实际输出/参考计量，并有可验证的预占上限。
已知请求时长超过销售或采购秒数上限时，在提交生成前拒绝。
未知输出时长的 Omni 模板默认按条；不可信计量不能用于实际秒数结算。
MM3 参考视频 schema 未确认，保持禁用；不能因其他模式通过而启用此组合。

模型必须同时具备当前 Key/分组授权、可调度账号、有效能力、销售与采购价，
才会进入 `GET /v1/media/models` 的可用目录。目录返回完整能力变体与有效销售单位，
不返回账号、供应商地址、采购价格或 Key；提交时仍会重新校验。

### 账号模型映射

Seedance 账号使用 `credentials.model_mapping` 配置“客户请求模型 → 上游模型”。
例如客户统一请求 `seedance-2.0`，BBLabu 账号配置
`seedance-2.0 → Seedance-2.0`，志奇账号配置
`seedance-2.0 → seedance-2.0`。上游名称保留原始大小写；同步型号不会覆盖已有映射。

- 渠道售价、分组模型授权、公开模型列表和任务响应使用客户请求名称。
- 账号采购价使用最终实际发送的上游名称，同一公开型号在不同账号可以有不同采购价。
- 能力配置仍按各账号的协议、时长、分辨率和素材限制校验；模型映射不会放宽能力限制。
- 任务分配时冻结实际上游名称与售采价，之后修改账号映射或定价不会改变在途任务。

升级前检查已有非同名映射：如果采购价此前配置在公开名称下，需要在账号中改为真实
上游名称；渠道售价保持原公开名称。缺少上游采购价时不会使用公开型号的采购价替代。
不自动迁移或猜测价格，也不重新计算历史账单。已有旧格式价格继续按原规范兼容，
需要精确区分大小写的采购价使用显式 v1 视频定价。

旧 `bblabu_v1` / `fflink_v1` 未配置映射及已有同名白名单继续使用原驱动的型号转换，
包括 BBLabu 的 `Seedance-2.0` 和 FFLink 的 `bytedance/seedance-2.5` 兼容别名；
非同名映射发送配置的目标原文，目标仍须为对应旧协议支持的型号。
本功能不新增路由、数据库迁移或独立开关。部署后核对管理端映射保存与重新打开、
`GET /v1/models`、`GET /v1/media/models`、创建/查询响应中的公开名称，以及账号采购统计。
任意公开别名、显式 v1 售价和完整执行快照依赖现有
`gateway.video_tasks_json_v1_enabled` 开启；关闭时继续使用旧型号与旧计价协议，
不支持用未知公开别名绕过旧售价限制。

## 异常与凭证

创建已发出但结果未知时，网关返回 `X-Video-Task-ID` 并保留预占，进入 `needs_review`。
不要重发生成请求或切换账号重新生成；未知提交无法保证供应商去重或找回。
管理端“视频任务复核”支持恢复原任务轮询、确认失败、确认取消，必须填写依据和原因。
复核接口不接受金额；扣款与释放通过同一任务行锁、租约和账单去重互斥。
若扣款已提交但任务状态写入失败，释放操作只修复为已结算，不能再次退回预占。
扣款失败后重试复用已保存的精确计量与冻结价格；上游后补或更改时长不会重新定价。
恢复轮询对超期任务提供一次决策后 1 小时窗口，仍无结果会再次进入复核。

在途/待复核任务和完成后 7 天内的原账号受保护，不能删除或覆盖其 Key。
轮换 Key 时创建新账号，原账号停止新调度并保留 Key，供旧任务恢复和下载使用。
任务冻结协议、URL、能力和售采价，后续编辑不会改变已受理任务。

## 路由与验收

公开接口沿用 Bearer Key 鉴权和当前用户/Key 归属隔离；管理接口复用管理员登录鉴权：

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| POST | `/v1/videos/generations`（保留 `/v1/videos`） | JSON 创建 |
| GET | `/v1/videos/{id}` | 查询原任务 |
| GET | `/v1/videos/{id}/content` | 本地结算完成后下载，支持 Range/If-Range |
| DELETE | `/v1/videos/jobs/{id}` | 依能力取消，BBLabu 返回 501 |
| GET | `/v1/media/models` | 当前 Key 的视频能力目录 |
| GET | `/api/v1/admin/accounts/video-model-templates` | 管理员读取能力模板，不发起上游生成或授权 |
| GET | `/api/v1/admin/video-tasks/reviews` | 管理员复核列表，limit/offset 分页 |
| POST | `/api/v1/admin/video-tasks/reviews/{state_id}/resolve` | 管理员决策，action/reason |

复核 action 为 `retry_poll` / `confirmed_failed` / `confirmed_canceled`；
未登录 401、非管理员 403、任务不存在 404、状态冲突 409、无效输入 422、不可用 503。
生成/查询保留原八字段和五状态；未知规格为空字符串/0，completed 必须已在本地结算。
Nginx 检查以上路径、下载 Range 与超时、管理端新资源，不需增加支付或 OAuth 回调。
小范围真实联调须覆盖不同 profile、768p/2K、按条型号、四种售采组合及供应商账单。
分别观察未知提交、预占/释放、结算积压、采购统计和下载成功率；异常先停止该组合创建。

## 审查修复（2026-10-17）

审查会话 `01a14901-f9a5-7e10-9d73-296de20e1f01` 的五项业务问题和一项集成夹具问题均成立，已修复：

- 旧 `bblabu_v1` / `fflink_v1` 保留参考素材与 `watermark` 等透传字段，覆盖开关开启及关闭。
- 已知时长超过按秒销售或采购上限时，提交生成前拒绝；覆盖旧协议使用显式 v1 售价。
- 扣款重试先读取首次冻结计量，复用销售和采购金额；覆盖后续响应补充、更改及缺失时长。
- 管理端接受显式 v1 视频采购价的 `768p`、`2k` / `2K` 分档，旧格式校验继续生效。
- MM3 公开目录不再声明未开放的参考视频输入，图片、音频及仍有效的时长约束保留。
- PostgreSQL 计费集成夹具补齐真实测试账号和冻结执行快照；本地仅编译，须补实际运行。

另外，原 HEAD 也复现了 CommandCode 目录测试的缓存串扰；该测试改用明确序号和缓存清理，
修复后连续运行 50 次通过，生产目录缓存逻辑未改。

## 本地验证记录（2026-10-17）

- `go test -p 4 ./...`、`go vet -p 4 ./...`：通过。
- `go test -p 1 -parallel 1 -tags=unit ./...`：全部包通过，未跳过任何测试。
  Windows 执行前临时将已安装的 `D:/Environ/Git/bin` 加入当前进程 PATH，供既有备份测试调用 `sh`。
  此前全包执行曾遇到既有内容审核异步时限及进程堆采样断言波动；最终串行全包通过，相关业务代码未改。
- 前端 `typecheck`、`test:run`（389 文件、3172 项测试）、`build`：通过。
- `go test -p 4 -tags embed ./internal/web`、`git diff --check`：通过。
- 桌面与 390px 窄屏组件预览通过；使用本地 mock，未执行生产登录或真实收费生成。
- `golangci-lint run --timeout=30m ./...` 未通过：现有 lint 的 importer 不支持 Go 1.27.2 的 export data version 5，须换用兼容工具后重跑。
- `go test -tags=integration ./...` 未通过：本机缺少 Docker。新增仓储集成用例编译通过，但没有实际执行 PostgreSQL/Redis 并发验证；发布前须在完整环境或 CI 补验。
- 当时未执行供应商真实生成、精确计量与账单核对，开关默认关闭。本地验证阶段未执行生产部署。

## v0.1.57 发布约束（历史记录）

先推送 `feat/my-sub`，待同一提交的 CI（含数据库集成测试、lint）和 Security Scan 全部通过，
再创建 annotated Tag `v0.1.57`，信息为 `Release v0.1.57`，单独推送并核对远端 peeled commit。
Tag 会触发现有 Release 流水线发布构建产物及镜像；生产部署和视频功能启用需另行完成验收。
该版本要求真实生成、计量与账单核对完成前，保持 `gateway.video_tasks_json_v1_enabled: false`。

## v0.1.58 按条分辨率定价修复（2026-10-18）

- 账号采购成本与渠道售价直接选择按秒或按条（按次），两种方式均支持分辨率价格档位。
- 切换单位保留默认价和分辨率价格；已有旧格式配置在未主动切换或添加档位时保持原计费语义。
- 复用已有 v1 视频计价协议。渠道按条及独立时长售价需服务端启用
  `gateway.video_tasks_json_v1_enabled`；该版本未改变当时默认关闭的行为，未增加数据库迁移。
- 本地 `typecheck`、本次文件 ESLint、全量前端测试（389 文件、3180 项）、
  `build`、构建完成后的 `go test -tags embed ./internal/web` 及 `git diff --check` 通过。
- 发布仍须等待待发布提交的 CI 与 Security Scan 全部成功，再推送 annotated Tag；
  生产部署、真实供应商生成及账单验收另行执行。

## 账号模型映射验证（2026-10-24）

- `go test -p 1 -parallel 1 ./...`、`go test -p 1 -parallel 1 -tags=unit ./...`：全部包通过。
  Windows 测试进程临时将已安装的 `D:/Environ/Git/bin` 加入 PATH，供既有备份测试调用 `sh`。
- 四种视频协议的回归覆盖公开售价、精确上游采购价、缺失采购价拒绝、账号切换、
  单次映射、最大供应商预扣、公开响应及任务快照恢复；既有无映射和同名白名单保持兼容。
- `corepack pnpm@9.15.9 --dir frontend run typecheck`、`run build`：通过，包含 i18n 检查。
- `corepack pnpm@9.15.9 --dir frontend run test:run --maxWorkers=4 --minWorkers=1`：
  394 文件、3297 项测试全部通过；最后样式与文案调整后，相关 3 文件、170 项测试再次通过。
- 桌面与 390px 本地 mock 预览通过；没有生产登录、真实收费生成或线上账号修改。
- `go test -p 1 -tags embed ./internal/web`、修改文件 ESLint、`gofmt` 检查及 `git diff --check`：通过。
- 使用 Go 1.27.3 临时构建官方 `golangci-lint v2.15.0`，解决旧工具无法读取 export data v5 的问题。
  完整 `run --timeout=30m ./...` 已执行，仍有 3 个未修改的 OpenAI WS 测试 `errcheck` 告警：
  `openai_ws_client_read_test.go:139`、`openai_ws_client_session_test.go:47`、
  `openai_ws_passthrough_turn_pricing_test.go:279`。
  补充 `run --timeout=30m --new-from-rev=HEAD --whole-files ./...` 通过，本次修改文件为 0 告警。
- `go test -p 1 -parallel 1 -tags=integration ./...` 已执行但未通过：本机没有可用 Docker，
  `TestRequestAuditPostgresIntegration` 报 `rootless Docker is not supported on Windows`。
  其他 Docker 用例自动跳过不算集成验证完成；发布前须在具备 PostgreSQL/Redis 容器的环境或 CI 补验。
- 本轮未提交、发布或部署；实际供应商生成、计量与账单核对仍需部署验收。
