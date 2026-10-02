# Studio 创作历史接口

此能力保存图片／Seedance 视频任务的用户历史元数据，不提交生成请求，不操作计费记录。

## 接口

两个接口均位于现有 JWT 用户路由，继承用户守卫、全局限流和审计；无需另一个功能开关。

- `GET /api/v1/studio/tasks?page=1&page_size=100`：返回当前登录用户的记录，按创建时间倒序。`page_size` 为 1–100，`page` 为 1–10000。
- `PUT /api/v1/studio/tasks/:id`：接收完整 `StudioTask`，创建或更新该用户记录。URL 与请求体的 `id` 必须一致，最大 128 KiB。

均使用现有 `{code, message, data}` 响应格式。列表 `data` 为 `{items, page, page_size, has_more}`；保存 `data` 为实际保存的任务。

`StudioTask` 使用 camelCase：`id, kind, keyId, keyName, title, project, prompt, model, mode, payload, referenceNames, createdAt, status`，可选 `imageResult, videoResult, adopted, parentId`。`createdAt` 为 Unix 毫秒。嵌套生成参数和网关结果沿用其原有字段名。

完整白名单以 `backend/internal/service/studio.go` 的结构体为准；未知字段会返回 400。禁止保存 API Key/token、File、base64 图片。参考上传文件仅保存文件名。媒体地址接受 HTTP/HTTPS；视频结果和采用记录亦接受当前任务的 `/v1/videos/:id/content` 或 `/v1/videos/jobs/:id/content`。

错误：未登录 401，字段不合法 `STUDIO_INVALID_TASK` / 400，超限 `STUDIO_RECORD_TOO_LARGE` / 413，非本人或不存在任务 `STUDIO_TASK_NOT_FOUND` / 404，存储不可用 `STUDIO_UNAVAILABLE` / 503。

## 归属与更新

首次登记必须验证 API Key 属于登录用户，并通过既有图片任务服务／Seedance 持久化仓储验证任务同时属于该用户和 Key。

图片记录可查询时使用服务端真实结果及状态。视频结果是客户端已观察的显示快照；不会被用于执行或结算。客户端应继续通过既有网关查询未终止的任务。

首次保存后固定任务种类、Key、创建时间、提示词、模型、生成模式、原始参数、参考名称与来源任务。后续允许更新标题、项目、采用记录和观察结果。数据库 upsert 同样保护这些固定字段，并阻止延迟请求把终态退回进行中。

已登记的历史可在原网关任务过期或 Key 删除后继续查看、整理。没有提前登记的图片任务在原 24 小时缓存过期后无法补录，因为已无法验证归属。新接口不会自动导入旧任务。

## 部署与回滚

部署后端时自动应用新增的 `241_custom_studio_tasks.sql`，只新增隔离表和索引。保留现有生成路由、存储配置及计费逻辑。回滚旧应用可以保留新表，无须删除数据。

前端应在接口尚未部署或同步失败时保留本机记录，并明确当前记录尚未云端同步。保存成功后使用响应中的服务器创建时间与图片结果。

元数据长期保存不等于媒体永久保存：图片、参考素材及视频地址仍遵守原存储或供应商的有效期，本能力不会自动复制媒体文件。

发布前需要在有 Docker 的环境执行全量 integration 测试；新增的 PostgreSQL 集成用例验证用户隔离、输入快照和终态保护，不能将缺少 Docker 导致的跳过视为通过。
