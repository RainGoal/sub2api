# Sub2API 对外媒体契约与创作平台连接器

日期：2026-10-17。现状依据 `03c3c50cc`；“计划扩展”均尚未实现。

## 1. 已有视频 API

使用 `Authorization: Bearer <Sub2API服务Key>`；创建是 JSON，请勿直接使用其他 OpenAI 视频 SDK 的 multipart 请求。本统一链路当前由 Seedance 平台分组承载，其他平台的 Grok 原生视频路径不能当作同一契约。

| 方法和规范路径 | 当前响应 |
| --- | --- |
| `POST /v1/videos/generations` | 202，统一任务对象 |
| `GET /v1/videos/{id}` | 200，统一任务对象 |
| `GET /v1/videos/{id}/content` | 二进制媒体，支持 Range/If-Range |
| `DELETE /v1/videos/jobs/{id}` | 支持时 200；BBLabu 进行中任务为 501 |

`POST /v1/videos` 是已有创建别名。创作平台固定使用表中路径；不依赖供应商路径或无前缀别名。

### 创建示例（当前可解析的格式）

```json
{
  "model": "seedance-2.5",
  "prompt": "产品置于窗边，镜头缓慢推进",
  "duration": 10,
  "resolution": "720p",
  "aspect_ratio": "16:9",
  "audio": false,
  "referenceImages": [],
  "referenceVideos": [],
  "referenceAudios": []
}
```

| 字段 | 当前类型与语义 |
| --- | --- |
| model | 必填字符串；已登记 Seedance 型号及别名 |
| prompt | 必填非空字符串 |
| duration | 秒；已知型号省略时取最短时长，当前为 4，不是上游文档默认 5 |
| resolution | 字符串；已知型号省略取首个规格，当前为 480p |
| aspect_ratio | 字符串；默认 16:9，支持 auto；ratio 是旧别名 |
| audio | 布尔；默认 false，不代表每个上游都支持控制音频 |
| referenceImages / referenceVideos / referenceAudios | HTTPS URL 字符串数组，按型号和协议限制 |
| start_frame_url / end_frame_url | 可选 HTTPS 首尾帧；现有 image_url/first_image/last_image 等别名保留 |

当前目录还含 seedance-2.0、seedance-2.0-fast、seedance-2.0-mini；BBLabu 仅支持原两款，fast/mini 属其他现有协议能力，不应交叉声明。

### 创建与查询对象（当前固定八字段）

```json
{
  "id": "opaque-task-id",
  "object": "video",
  "status": "queued",
  "model": "seedance-2.5",
  "resolution": "720p",
  "duration": 10,
  "content_url": null,
  "error": null
}
```

状态只用 `queued/in_progress/completed/failed/canceled`。结算中、未知供应商状态统一保持非终态，不将上游 success 创建应答直接当作已生成。

completed 时 content_url 为本地 `/v1/videos/{id}/content`，不返回供应商结果 URL；failed 时 error 为 `{code,message}`。未本地结算完成不会公开可下载结果。

当前 resolution/duration 主要来自任务请求快照，并可能覆盖供应商回显；不能将其描述为实测输出规格。当前 id 实际来自上游，但契约要求当作不透明字符串，客户端禁止解析或拼接供应商 URL。

查询、取消、下载必须使用创建任务的同一 Key，检查 user/Key/group/IP/任务归属。轮询和内容请求不产生第二次生成费用；余额耗尽不等于已受理任务不可查，Key 被禁用等鉴权限制仍有效。

BBLabu 不支持主动取消；关闭网页或中断 HTTP 请求不取消任务。终态任务的 DELETE 幂等返回原终态，不能把 completed 改为 canceled。终态失败、已释放或仍结算中的内容请求可能为 409/video_content_unavailable；并发取消也可能返回 409，调用方按错误码处理。

## 2. 图片连接器（当前）

同步标准 Images API 已有，但创作平台采用已有异步包装，避免长 HTTP 连接：

| 操作 | 路径 | 契约 |
| --- | --- | --- |
| 文生图 | `POST /v1/images/generations/async` | 标准 Images JSON，202 |
| 图片编辑 | `POST /v1/images/edits/async` | JSON/multipart，202 |
| 任务查询 | `GET /v1/images/tasks/{id}` | processing/completed/failed |

提交返回 id/task_id/poll_url；成功结果读取 `result.data[].url`。图片对象与视频对象不是同一 schema，独立平台后端归一化成自己的业务任务，不强迫前端分别理解网关细节。

异步图片默认关闭，须启用并配置对象存储；任务缓存距最近更新约 24 小时、最长执行 30 分钟，不是永久任务库。使用创建时同一 Key；平台及时保存结果到自己的存储。

标准接口路径不等于全部型号已放行：当前 9 个 BBLabu 图片型号中只有 3 个准入通过，另 6 个须补模型识别。真实 edits 请求和返回仍需联调，不能仅根据名称宣布兼容。

## 3. 错误与创建可靠性（当前）

| HTTP | 现有常见含义 |
| --- | --- |
| 401 / 403 | Key、用户、分组或 IP 等鉴权失败 |
| 402 | 生成余额预占不足 |
| 404 | 任务不存在、不属于当前 Key，或图片异步未启用 |
| 409 | 内容尚不可用、取消与结算状态冲突 |
| 422 | 视频创建参数验证失败 |
| 501 | 当前协议不支持取消 |
| 502 / 503 | 上游故障、价格/账号/持久化不可用等；不能据此假定上游未受理 |

错误体存在两层：handler 多为 `{"error":{"type":"…","message":"…"}}`，鉴权中间件还会返回 `{"code":"…","message":"…"}`。连接器按 HTTP 与已知错误字段归一化，不能假定所有错误都带同一个 code；本次不为统一格式改写全站中间件。

当前创建没有 `Idempotency-Key` 处理，也没有公开按客户端去重键找任务的接口；`X-Client-Request-ID` 只是关联标识。平台本地去重不能消除跨系统响应丢失；拿到任务 ID 后只查询原任务，响应丢失则进入提交待确认，不自动重新创建。

统一视频状态已持久化 PostgreSQL，后台恢复不依赖浏览器轮询；旧 Grok/Ark 不是同一套持久恢复保证。图片异步重启/转存失败等边界须实测，不将临时缓存当持久事实。

## 4. 计划扩展：通用视频与模型能力

以下属于第一项网关改造，**当前不可直接调用**：

- `GET /v1/media/models`：同 Key 鉴权，返回可保证的模型能力、默认/允许字段、素材规则、操作能力、带单位售价和目录/价格版本；现有 `/v1/models` 不改 schema。
- 全部 33 个 BBLabu 视频模板，以及可配置同协议型号；保留公开创建/查询/content 路径和八字段响应。
- 新任务公开 ID 改为网关随机 `vid_…`，旧任务保持旧 ID；平台仍将其当不透明字符串。
- 新型号未指定/未知的输出规格为 `resolution=""`、`duration=0`，不改变字段类型；明确这是任务规格快照，不冒充实测结果。可用参数以能力目录为准。
- 公共 `referenceVideos` 向后兼容 URL 字符串，并增加受控 `{url,duration_seconds}` 素材对象形式，支持需逐段视频时长的型号；上游字段由 profile 映射。metadata 用于素材约束，不代替可信的最终计费量。
- 按条/按秒的销售单位、价格版本与参考输入计量规则通过能力价格输出表达；不把金额塞进现有八字段生成响应，也不承诺当前已有单任务精确账单 API。

## 5. 独立平台应固定的连接器行为

浏览器提交项目、素材和商品 ID，由独立平台后端生成网关请求。图片和视频使用分别配置的服务 Key，任务永久绑定原 Key 引用；不能假设两类分组共用一把 Key。

连接器只访问受控 Sub2API origin；规范字段和方法白名单在服务端执行，媒体 content_url 只接受已知网关相对路径。视频由 Worker 使用 Bearer 下载并归档，再向用户提供本平台签名 URL。

平台以自己的任务 ID、资产 ID、积分价和状态向前端提供接口；网关 id、Key、供应商错误和账单不直接下放浏览器。报价和长期任务记录由平台维护；Sub2API 服务账户余额是平台采购余额，不是用户钱包。

验收至少包含：图片与视频各一条真实闭环、相同 Key 查询/下载、重复轮询不重复结算、跨用户拒绝、未知创建不重发、完成后媒体归档、取消 501 提示、价格单位可读、旧任务回归。

## 6. 核实依据

- [已有视频文档](D:/Code/Other/sub2api/docs/SEEDANCE_VIDEO_API.md)、[异步图片文档](D:/Code/Other/sub2api/docs/ASYNC_IMAGE_TASKS.md)。
- [路由分流](D:/Code/Other/sub2api/backend/internal/server/routes/gateway.go:101)、[请求解析](D:/Code/Other/sub2api/backend/internal/pkg/videoprovider/request.go:15)。
- [响应定义](D:/Code/Other/sub2api/backend/internal/service/seedance_video_response.go:31)、[handler 契约测试](D:/Code/Other/sub2api/backend/internal/handler/seedance_video_response_contract_test.go:15)。
- [Key 鉴权](D:/Code/Other/sub2api/backend/internal/server/middleware/api_key_auth.go:49)、[任务查询](D:/Code/Other/sub2api/backend/internal/repository/seedance_video_task_repo.go:171)。
