# 视频参考素材上传

`wsapi-front` 的视频页面支持选择或拖放本地图片、视频和音频，上传成功后
自动把链接放入原有视频生成请求。手动 HTTPS 链接入口继续保留。

## 开启方式

1. 更新 Sub2API 和 wsapi-front。
2. 在管理端「备份 → 图片与视频素材存储」确认现有 R2/S3 配置，开启
   「启用视频素材上传」并保存。此开关默认关闭，独立于「启用异步生图」。
   同一区域可设置每用户每分钟上传次数和每日上传容量，默认 20 次和 1024 MiB。
   保存后新请求立即使用新限额，无需重启；调整额度不会清空当前分钟或当日已用量。
3. 支持复用备份的端点和凭证，也可以继续使用图片存储的独立凭证。
   视频上传要求 HTTPS 存储端点，凭证需要目标桶的写入和读取权限。
4. 输入素材统一写入 `video-inputs/YYYY/MM/DD/<user-id>/<random>.<ext>`。
   在 R2 为 `video-inputs/` 配置生命周期规则，例如上传 8 天后删除；保留时间
   必须超过签名链接和上游读取窗口。应用不会修改桶策略或生命周期。
5. 素材始终使用签名读取链接，存储桶可保持私有。图片结果的公开域名设置
   不影响素材链接。签名有效期沿用设置中的小时数，限制在 24–168 小时。

移除素材只从当前表单移除并取消正在进行的上传。已经保存的对象由上述生命周期
清理，避免影响上游已开始读取的任务。页面不会持久化本地文件；刷新后需要重新选择。
链接过期时会阻止提交并提供重新上传入口。上游是否接受特定编码、尺寸、时长及数量，
仍由现有模型和协议校验决定，上传功能不做转码。

## 用户 API

接口位于现有用户 JWT 鉴权、BackendModeUserGuard、面板限流和审计链中。
不接受匿名上传；上传不触发生成计费。生成继续使用原来的 Seedance API Key。

### GET /api/v1/video-assets/config

返回标准 `code/message/data` 响应，`data` 包含：

```json
{
  "enabled": true,
  "max_uploads_per_minute": 20,
  "daily_limit_bytes": 1073741824,
  "limits": {
    "image": {"max_bytes": 20971520, "mime_types": ["image/jpeg", "image/png", "image/webp"]},
    "video": {"max_bytes": 52428800, "mime_types": ["video/mp4", "video/webm"]},
    "audio": {"max_bytes": 20971520, "mime_types": ["audio/mpeg", "audio/wav", "audio/ogg", "audio/mp4"]}
  }
}
```

凭证或存储位置不会下发。开关关闭或凭证未配齐时 `enabled=false`，用户可继续使用链接。

### POST /api/v1/video-assets

请求为 `multipart/form-data`，包含一个 `file` 文件和一个 `media_type` 字段
（`image`、`video` 或 `audio`）。成功返回 HTTP 201：

```json
{
  "code": 0,
  "message": "success",
  "data": {
    "url": "https://<signed-object-url>",
    "media_type": "image",
    "size": 12345,
    "url_expires_at": "2026-09-07T12:00:00Z"
  }
}
```

文件真实内容决定 MIME 和扩展名，不信任文件名或客户端 MIME；图片会检查可解码性
和尺寸。大文件通过受限临时文件上传，处理结束后清理本地临时文件。

| HTTP | reason | 含义 |
| --- | --- | --- |
| 401 | — | 未登录或会话失效 |
| 403 | VIDEO_ASSET_UPLOAD_DISABLED | 上传开关关闭 |
| 400 | INVALID_UPLOAD / INVALID_MEDIA_TYPE | 文件、表单或类型参数无效 |
| 413 | FILE_TOO_LARGE | 文件或请求体超限 |
| 415 | UNSUPPORTED_MEDIA_TYPE | 内容格式不支持或图片损坏 |
| 429 | UPLOAD_BUSY / UPLOAD_RATE_LIMITED | 并发或频率超限 |
| 429 | UPLOAD_QUOTA_EXCEEDED | 当日上传字节数超限 |
| 502 | VIDEO_ASSET_UPLOAD_FAILED | 对象存储写入失败 |
| 503 | VIDEO_ASSET_STORAGE_UNAVAILABLE / UPLOAD_LIMIT_UNAVAILABLE | 存储配置或限流服务不可用 |

上传限制可在后台动态配置，默认每用户每分钟 20 次、每天 1 GiB（1024 MiB）。每日容量
以 UTC 日界线重置，按已接收文件的尝试字节数计算，包括内容校验或存储失败的尝试，
由 Redis 在实例间共享；分钟次数包含被限流拒绝的上传请求。每实例最多 8 个上传、每用户
每实例最多 2 个；wsapi-front 五个素材控件共享 2 个网络上传位置。Redis 故障时暂停上传。

### 管理端限额配置

沿用 `GET/PUT /api/v1/admin/backups/image-storage` 的管理员鉴权与保存时的二次验证。
保存仍提交完整图片存储配置，新增字段如下（无需数据库迁移）：

| 字段 | 默认值 | 可配置范围 |
| --- | --- | --- |
| `video_upload_max_per_minute` | 20 | 1–10,000 次/用户/分钟，整数 |
| `video_upload_daily_limit_mib` | 1024 | 1–1,048,576 MiB/用户/天，整数 |

后台以 MiB 展示每日容量，1 GiB = 1024 MiB。旧配置或旧客户端缺失字段时沿用默认值，
0 同样回落到默认值，不表示不限额。非法限额返回 HTTP 400 / `INVALID_VIDEO_UPLOAD_LIMIT`，
保存失败时保留原设置。每个上传请求开始时读取一组限额，本次请求全程使用该组值。
调高后后续请求按更高额度判断；调低到小于已用量后会拒绝新的上传，已用量不会扣减。
其他现有面板限流、并发及反向代理限制仍会生效。

## 部署检查

- 先上线后端，再发布 wsapi-front，然后开启上传功能。
- 本次限额配置需同步更新 Sub2API 后端和内嵌管理端；上传开关与已有额度会保留。
- wsapi-front 的 Nginx 对 `/api/v1/video-assets` 单独允许 51 MiB 请求体及较长超时。
  检查外层 Nginx/Cloudflare 和 Sub2API 的 `server.max_request_body_size`：整条链路
  都需容纳 50 MiB 文件及 multipart 开销。不要把所有普通接口统一放大。
- `/api/`、`/v1/` 保持转发至 Sub2API；支付/OAuth 回调路由继续沿用原配置。
- 验证 R2 的签名 HTTPS 链接能从外部直接读取，并用实际配置的上游完成一次生成。
- 清理规则只匹配 `video-inputs/`，不要套用到 `backups/` 或 `images/`。

验证命令：后端 `go test ./...`、`go test -tags embed ./internal/web`；管理端
`pnpm --dir frontend run typecheck`、`test:run`、`build`；wsapi-front `npm test`、
`npm run build`。两个仓库最后执行 `git diff --check`。
