# API Key 备用分组部署

功能直接可用，无需配置开关或环境变量。先部署包含
`240_custom_api_key_fallback_group.sql` 的后端并确认迁移成功，再发布用户端。
现有 Key 的 `fallback_group_id` 为 `NULL`，保持原有路由、计费和重试预算；
不会自动绑定备用分组，也不会额外查询备用分组。用户主动配置备用分组后才参与故障转移。

先备份线上 compose，再运行 `docker compose config` 检查。使用明确版本的应用镜像，
执行 `docker compose up -d --force-recreate sub2api` 并检查状态、日志和健康接口。
保留现有数据卷与线上配置。

Key 创建使用 `POST /api/v1/keys`，编辑使用 `PUT /api/v1/keys/:id`，新增可空整数
`fallback_group_id`，响应也返回该字段。编辑时省略表示不变，
`null` 表示清空并停止该 Key 的备用路由。设置新备用或更换主分组时重新校验组合；
用户端直接展示可选备用渠道，不依赖公共设置字段。

首期只支持明确主分组、同平台的 OpenAI/Anthropic 文本请求，主备均为启用的 standard
余额分组，且用户可绑定。两组都不得设置原有的分组兜底路由，防止形成多层回退链。
用户余额、Key 额度、权限和限流仍需通过；按实际服务分组计费。

支持的入口为 HTTP `POST /v1/messages`、`POST /v1/responses`、
`POST /v1/chat/completions`，覆盖 OpenAI 与 Anthropic 分组。优先在主组内重试，
为备用组预留约一半已有换号预算；最多切组一次，切组也消耗预算，不重置请求超时。
无号或允许换号的上游 401/403/429/5xx 可触发；本地权限、模型白名单、额度、
限流和利润限制不会通过切组绕过。启用利润控制时，无法判明原因的空池保守地不切组。

首版不支持 WebSocket、强制平台路由、图片/音频/文件请求、托管工具、后台任务、
`previous_response_id` / `conversation` 续接及 compaction 请求。响应已提交
（包括 SSE 心跳）或客户端取消后不再切组。目标分组重新应用自己的模型映射、
reasoning 策略、安全审查和用户分组倍率；用量记录的分组为实际服务分组，Key 的主组不变。

所有 Key 接口沿用现有登录鉴权与所有者校验。新增错误码为
`API_KEY_FALLBACK_INVALID`（400，主备组合不合法）；分组授权失败沿用
`GROUP_NOT_ALLOWED`。目标在运行中失效时，
停止备用尝试并沿用原失败响应，不向调用者暴露不可访问的分组信息。

发布检查：主组成功、可恢复故障后转备用、响应开始后不再回退、无权限/额度耗尽不回退、
备用费用与用量归属正确、取消备用后缓存失效。独立用户端的 `/api/`、`/v1/` 转发路径应
继续指向该后端。回滚时同步回退用户端和后端应用镜像，保留新增的可空列，不执行删列迁移。

## 本次实现验证（2026-09-19）

- `go test ./...`：通过。
- `go test -tags=unit ./...`：全部包通过。
- `golangci-lint run --timeout=30m ./...`：0 issues。
- 新增专项回归覆盖六个 HTTP 入口的主组空池/上游故障、备用组倍率与用量归属、
  流式切换、原请求策略和请求头恢复、预算、取消、已输出响应、权限及 RPM。
- 移除功能开关后补充验证：未配置备用的 Key 不复制备用请求体、不查询备用分组，
  保持原有换号预算；主组成功时使用原分组计费；用户端不自动为旧 Key 绑定备用。
- `wsapi-front` 的 `npm run build`、`npm test`：通过；本地模拟接口完成桌面与
  小屏中英文检查，未创建生产 Key。
- 两个仓库的 `git diff --check`：通过。
- `go test -tags=integration ./internal/repository -run '^TestAPIKeyFallbackPersistenceAndGroupDeletion$' -count=1 -v`：
  编译成功，因本机缺少 Docker 被跳过，**不计为集成测试通过**。发布前仍需在具备
  Docker 的环境完成 `go test -tags=integration ./...` 并确认完整 CI 通过。

Windows 执行全量 Go 测试需要将 Git 自带的 `sh` 加入当前进程 PATH，现有备份测试依赖它。
