# 视频生成 API

通过统一的视频生成 API 提交提示词、首尾帧或参考素材，调用当前分组开放的视频模型异步生成视频。接入流程为：**创建任务 → 轮询状态 → 下载视频**。

本文面向 API 调用方，更新日期：2026-11-13。

## 1. 接入准备

在控制台创建归属**视频生成分组**的 API Key，并确认分组已开放需要使用的模型及价格。一个分组可以提供多种视频模型，具体可用名称由当前 Key 的模型目录和分组权限决定。

API 站点地址为 [https://ai.yusflow.com](https://ai.yusflow.com)，接入信息如下：

```text
BASE_URL=https://ai.yusflow.com
API_KEY=YOUR_API_KEY
```

`BASE_URL` 表示 API 站点根地址，不包含末尾的 `/v1`。例如，完整的创建任务地址为 `https://ai.yusflow.com/v1/videos/generations`，请避免重复拼接 `/v1`。

所有接口，包括任务查询、取消和下载，均需携带：

```http
Authorization: Bearer YOUR_API_KEY
```

创建任务还需携带 `Content-Type: application/json`。请使用**创建任务时的同一把 API Key**进行后续操作，同一用户的另一把 Key 也不能代查。

## 2. 接口总览

| 功能 | 方法 | 路径 | 成功响应 |
| --- | --- | --- | --- |
| 查看模型目录 | GET | `/v1/models` | `200`，JSON |
| 查看视频模型参数与能力 | GET | `/v1/media/models` | `200`，JSON |
| 创建视频任务 | POST | `/v1/videos/generations` | `202`，任务 JSON |
| 查询任务 | GET | `/v1/videos/{id}` | `200`，任务 JSON |
| 下载视频 | GET | `/v1/videos/{id}/content` | `200`，视频二进制；范围请求可返回 `206` |
| 取消任务 | DELETE | `/v1/videos/jobs/{id}` | `200`，任务 JSON；部分模型不支持取消 |

`{id}` 为创建响应中的 `id`。请原样保存，将其视为不透明字符串，放入 URL 路径时进行 URL 编码。

新接入建议统一使用上表路径。兼容路径见文末。

## 3. 模型与能力

使用当前 API Key 查询模型目录：

```bash
curl --request GET 'https://ai.yusflow.com/v1/models' \
  --header 'Authorization: Bearer YOUR_API_KEY'
```

从返回的 `data` 数组读取各项的 `id`，作为创建任务的 `model`。目录用于发现模型名称，不保证每个模型的所有分辨率、素材组合都可用；实际调用还需满足分组权限、渠道能力和价格配置。

本文所有示例中的 `YOUR_VIDEO_MODEL` 都是占位值，请替换为目录返回的完整模型 ID。模型 ID 可能包含前缀、后缀或版本号，请原样使用。

接入前请查看所选模型的说明，确认以下能力：

| 能力 | 需要确认的内容 |
| --- | --- |
| 视频时长 | 是否可配置、默认值、范围或允许的离散值；部分模型使用固定时长或不接受时长参数 |
| 分辨率与比例 | 是否可配置、支持的值和默认值 |
| 图生视频 | 是否支持首帧、是否要求同时提交尾帧、是否要求特定比例 |
| 参考素材 | 支持的图片、视频、音频类型，以及数量、大小、总时长和组合限制 |
| 生成声音 | 是否支持 `audio: true`，以及对应的额外限制 |
| 取消任务 | 是否支持主动取消 |
| 计费 | 按秒或按次，参考视频输入是否计入费用 |

### 3.1 查询实时视频能力

模型广场展示当前分组开放的参数与能力；程序接入请使用同一把 API Key 查询实时目录：

```bash
curl --request GET 'https://ai.yusflow.com/v1/media/models' \
  --header 'Authorization: Bearer YOUR_API_KEY'
```

响应包含 `object: "list"`、`schema_version: 1`、`catalog_version`、`price_version` 和 `data`。`data` 中每个模型包含 `id`、`kind: "video"` 和 `variants`；每个 variant 是一组完整可用的能力与价格，**请在同一个 variant 内选择参数，不要跨方案拼接时长、分辨率或素材上限**。

| variant 字段 | 含义 |
| --- | --- |
| `duration_mode` | `controlled` 可配置时长、`fixed` 固定时长、`unknown` 由模型决定；`unknown` 时省略 `duration` |
| `min_duration` / `max_duration` / `default_duration` | 秒数范围与默认值 |
| `allowed_durations` | 非空时只能选择数组中的秒数 |
| `resolution_control` / `resolutions` / `default_resolution` | 是否接受分辨率参数、选项、默认值；不支持控制时省略该参数 |
| `ratios` / `default_ratio` | 比例选项与默认值；无选项时省略比例参数 |
| `max_images` / `max_videos` / `max_audios` | 各类参考素材上限，`0` 表示不接受该类参考素材 |
| `max_images_by_resolution` | 指定分辨率的参考图片上限，优先于 `max_images` |
| `max_total_assets` | 素材总数上限，包括首尾帧；`0` 表示没有额外的总数限制 |
| `frames` / `allow_first_only` / `allow_frames_with_references` | 首尾帧、只传首帧、与参考素材混用的支持情况 |
| `frames_require_auto_ratio` | 使用首尾帧时是否要求 `aspect_ratio: "auto"` |
| `generate_audio` / `audio_requires_image` | 是否接受 `audio: true`；参考音频是否必须搭配参考图片 |
| `max_reference_video_seconds` / `max_combined_video_seconds` | 参考视频总时长、输出与参考总时长限制；正值生效 |
| `require_reference_video_durations` | 有参考视频时必须逐段提交 `reference_video_durations`，长度与参考视频数组相同 |
| `supports_content` / `supports_cancel` | 下载、取消任务能力 |
| `prices` | 各分辨率的计费单位、单价和数量口径；创建时由服务端冻结最终价格 |

能力由当前渠道对外规则与实际可用上游共同决定。渠道配置只能收窄能力；没有可用完整组合或没有有效价格的方案不会出现在目录中。未设置渠道限制时继承上游配置。目录目前只返回已启用结构化能力协议的模型；旧协议模型可能只出现在 `/v1/models`，请联系管理员完成配置。

每次进入生成页面或切换 Key 时重新读取目录。响应为 `Cache-Control: no-store`；版本字段可用于识别变化，不能代替服务端校验。管理员调整配置只影响新任务；已创建任务继续使用冻结的执行能力和价格。

统一接口不代表所有模型能力相同。`/v1/models` 用于获取模型名称，不应据此推断素材上限、默认参数或取消能力。

## 4. 创建视频任务

```http
POST /v1/videos/generations
```

### 4.1 请求参数

请求体为 JSON 对象。

| 参数 | 类型 | 是否必填 | 说明 |
| --- | --- | --- | --- |
| `model` | string | 是 | 当前 Key 可用的完整模型名称 |
| `prompt` | string | 是 | 非空提示词，描述主体、动作、镜头、场景和风格 |
| `duration` | integer | 按模型能力填写 | 输出视频时长，单位秒；可配置时须符合模型范围或枚举，不接受该参数的模型应省略 |
| `resolution` | string | 按模型能力填写 | 如 `720p`；可配置时使用模型支持的值，不接受该参数的模型应省略 |
| `aspect_ratio` | string | 按模型能力填写 | 画面比例，须符合所选模型能力；不接受该参数的模型应省略 |
| `audio` | boolean | 否 | 是否请求生成声音；默认 `false`，仅支持该能力的模型可设置为 `true` |
| `start_frame_url` | string | 否 | 首帧图片的公网 HTTPS 地址 |
| `end_frame_url` | string | 否 | 尾帧图片的公网 HTTPS 地址；建议与首帧一起提供 |
| `referenceImages` | string[] | 否 | 参考图片的公网 HTTPS 地址数组 |
| `referenceVideos` | string[] | 否 | 参考视频的公网 HTTPS 地址数组 |
| `referenceAudios` | string[] | 否 | 参考音频的公网 HTTPS 地址数组 |
| `reference_video_durations` | integer[] | 条件必填 | 仅适用于明确要求该参数的模型；按 `referenceVideos` 的顺序提供各参考视频时长，单位秒 |

常见比例有 `21:9`、`16:9`、`4:3`、`1:1`、`3:4`、`9:16`、`auto`，实际支持项由所选模型决定。部分模型的首尾帧模式要求 `auto`，另一些模型只接受固定比例，请按对应模型说明填写。

省略可选参数时，系统按所选模型配置处理。不同模型或渠道的默认值可能不同；若响应提示默认值冲突，请显式填写模型支持的时长、分辨率和比例。不要发送所选模型不支持的参数。

### 4.2 文生视频示例

以下使用最小请求体。若所选模型要求显式指定时长、分辨率或比例，请补充其支持的参数。

```bash
curl --request POST 'https://ai.yusflow.com/v1/videos/generations' \
  --header 'Authorization: Bearer YOUR_API_KEY' \
  --header 'Content-Type: application/json' \
  --data '{
    "model": "YOUR_VIDEO_MODEL",
    "prompt": "清晨的海边，一只海鸥掠过水面，镜头缓慢向前推进，电影质感，自然光"
  }'
```

成功返回 `202 Accepted`。以下响应中的 `720p`、10 秒仅为示例值，实际取决于所选模型及本次请求：

```json
{
  "id": "video-example-001",
  "object": "video",
  "status": "queued",
  "model": "YOUR_VIDEO_MODEL",
  "resolution": "720p",
  "duration": 10,
  "content_url": null,
  "error": null
}
```

`202` 表示创建接口返回了任务，**不表示视频已生成成功**。请保存 `id`，根据返回的 `status` 处理；非终态继续查询，`failed` 或 `canceled` 停止轮询。

### 4.3 首帧图生视频

将以下 JSON 作为创建请求体。请把示例模型和素材地址替换为实际值，并确认该模型支持首帧、5 秒、`720p` 和 `auto` 比例；否则按模型能力调整或省略对应参数。

```json
{
  "model": "YOUR_VIDEO_MODEL",
  "prompt": "保持画面中的人物和服装一致，人物缓慢转头看向镜头，背景树叶随风轻动",
  "duration": 5,
  "resolution": "720p",
  "aspect_ratio": "auto",
  "start_frame_url": "https://YOUR_ASSET_HOST/start.jpg"
}
```

需要首尾帧控制时，可另外提供 `end_frame_url`。部分模型只支持首尾帧成对提交，或不支持首尾帧与参考素材数组混用；请先确认模型能力。

### 4.4 多模态参考

以下示例适用于支持相应参考素材的模型，时长、分辨率和比例也应按模型能力调整：

```json
{
  "model": "YOUR_VIDEO_MODEL",
  "prompt": "参考图片中的产品外观与视频中的运镜，生成产品展示短片",
  "duration": 10,
  "resolution": "720p",
  "aspect_ratio": "16:9",
  "referenceImages": ["https://YOUR_ASSET_HOST/product.jpg"],
  "referenceVideos": ["https://YOUR_ASSET_HOST/motion.mp4"]
}
```

如模型说明要求参考视频时长，额外添加 `reference_video_durations`。例如只有一条 6 秒参考视频时，填写 `[6]`；数组长度必须与 `referenceVideos` 一致。不要用该字段替代实际素材时长或预估最终账单。

### 4.5 素材与声音限制

- 素材必须是服务端可访问的公网 HTTPS 地址；不支持本地文件路径、`localhost`、内网地址或 Base64 Data URL。
- 素材链接应允许服务端直接读取，且在任务执行期间保持有效。图片、视频和音频的具体格式、大小、数量及总时长限制，以对应模型说明为准。
- `referenceImages`、`referenceVideos`、`referenceAudios` 使用大小写准确的字段名，数组元素为 URL 字符串。
- 图片、视频、音频的支持情况和数量上限由具体模型决定，首尾帧也可能计入素材总数。不要把一个模型的限制套用到其他模型。
- `referenceAudios` 是参考音频输入，`audio` 是请求生成声音，二者含义不同。部分模型的参考音频必须与参考图片或视频搭配，不能只提交音频。
- 生成声音可能受分辨率、时长和素材组合影响；只有明确支持的模型才可使用 `audio: true`。

## 5. 查询任务与响应字段

```http
GET /v1/videos/{id}
```

```bash
curl --request GET 'https://ai.yusflow.com/v1/videos/video-example-001' \
  --header 'Authorization: Bearer YOUR_API_KEY'
```

成功返回 `200 OK`。创建、查询和取消成功时均使用相同的任务 JSON 结构，直接返回以下字段，无外层 `data` 包装：

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `id` | string | 任务 ID，后续操作均使用此值 |
| `object` | string | 固定为 `video` |
| `status` | string | 任务状态，见下表 |
| `model` | string | 任务对应的模型名称 |
| `resolution` | string | 任务记录的分辨率；未提供分辨率信息的模型可能返回空字符串 |
| `duration` | integer | 任务记录或返回的时长，单位秒；时长未知时可能为 `0`，不应作为实际计费秒数 |
| `content_url` | string 或 null | 仅完成时提供，形如 `/v1/videos/{id}/content` 的相对路径 |
| `error` | object 或 null | 仅失败时提供，包含 `code` 和 `message` |

`content_url`、`error` 即使无值也会返回，值为 `null`。任务响应不提供生成进度百分比或完成时间保证。

| 状态 | 含义 | 客户端操作 |
| --- | --- | --- |
| `queued` | 已受理，等待生成 | 继续轮询 |
| `in_progress` | 生成中或正在完成最终处理 | 继续轮询 |
| `completed` | 已完成，可获取视频内容 | 下载视频，停止轮询 |
| `failed` | 任务失败 | 读取 `error`，停止轮询 |
| `canceled` | 任务已取消 | 停止轮询 |

建议每 3～5 秒查询一次；这是客户端接入建议，不是服务端固定频率。遇到限流或临时查询错误时退避重试，若响应含 `Retry-After` 则优先遵循。客户端等待超时后保留任务 ID，稍后继续查询。

完成响应示例：

```json
{
  "id": "video-example-001",
  "object": "video",
  "status": "completed",
  "model": "YOUR_VIDEO_MODEL",
  "resolution": "720p",
  "duration": 10,
  "content_url": "/v1/videos/video-example-001/content",
  "error": null
}
```

失败响应示例；此时查询 HTTP 状态仍为 `200`，应检查任务的 `status`：

```json
{
  "id": "video-example-001",
  "object": "video",
  "status": "failed",
  "model": "YOUR_VIDEO_MODEL",
  "resolution": "720p",
  "duration": 10,
  "content_url": null,
  "error": {
    "code": "video_generation_failed",
    "message": "Video generation failed"
  }
}
```

## 6. 下载视频

```http
GET /v1/videos/{id}/content
```

仅在 `status` 为 `completed` 后下载。将响应的 `content_url` 拼接到 `BASE_URL`，继续携带原 API Key：

```bash
curl --fail --request GET 'https://ai.yusflow.com/v1/videos/video-example-001/content' \
  --header 'Authorization: Bearer YOUR_API_KEY' \
  --output 'output.mp4'
```

接口直接返回视频二进制，不是包含视频地址的 JSON。支持 `Range` 和 `If-Range` 请求头，可用于分段读取和续传；范围请求可能返回 `206 Partial Content`，无效范围可能返回 `416`。

视频尚未完成、正在最终处理、已经失败或取消时，返回 `409`、错误类型 `video_content_unavailable`。尚未完成时继续查询；失败或取消后停止尝试下载。

浏览器的 `<video src>` 无法直接附带 Bearer 请求头。需要在自己的服务端代理，或通过带鉴权的 `fetch` 获取 Blob 后播放。生产站点应在可信服务端保存 API Key，避免将其暴露给访客。

## 7. 取消任务

```http
DELETE /v1/videos/jobs/{id}
```

```bash
curl --request DELETE 'https://ai.yusflow.com/v1/videos/jobs/video-example-001' \
  --header 'Authorization: Bearer YOUR_API_KEY'
```

无需请求体。成功时返回 `200` 及任务 JSON，正常取消后 `status` 为 `canceled`。

- 取消能力取决于所选模型和渠道。不支持时返回 `501`、`operation_not_supported`，任务并未因此取消，应继续查询。
- 对已完成、失败或取消的任务重复取消，会返回已保存的终态；不会把已完成任务改为取消，也不会撤销已完成任务的费用。
- 正在最终处理或另一个取消请求正在执行时，可能返回 `409`、`video_cancellation_in_progress`；先查询状态再决定是否重试取消。
- 若取消时任务刚好完成，可能返回 `409`、`video_cancellation_conflict`；继续查询，以最终状态为准。
- 取消操作被明确判定为失败时，任务 JSON 可返回 `status: "failed"`，`error.code` 为 `video_cancellation_failed`。

## 8. 费用与任务保留

价格以控制台或模型广场中当前分组、模型和分辨率的实际报价为准。不同模型可以按秒或按次计费；按秒计费时，参考视频输入是否计入计费时长也取决于对应的计费规则。

创建时系统会预留所需余额，成功后按冻结的价格规则结算；明确失败或成功取消后释放对应预留。重复查询、下载和取消不会产生第二笔视频生成费用。最终费用以用量记录为准，不能仅根据响应中的 `duration` 推算。

余额或生成额度用尽后，已有任务的查询、下载和取消仍可使用；API Key、用户、分组权限、IP 限制和任务归属校验仍然生效。提交结果不确定或处于人工核对中的任务，预留可能保持到核对完成。

请在任务完成后及时下载并保存视频。本接口不承诺永久保存媒体，客户端也应自行持久化任务 ID；本文接口不提供任务列表。

## 9. 错误处理与重试

### 9.1 HTTP 请求错误

参数、服务状态或下载条件错误通常使用以下结构，错误标识位于 `error.type`：

```json
{
  "error": {
    "type": "invalid_request_error",
    "message": "prompt is required"
  }
}
```

鉴权中间件也可能返回顶层 `code`、`message`，例如：

```json
{
  "code": "INVALID_API_KEY",
  "message": "Invalid API key"
}
```

客户端应先检查 HTTP 状态，兼容读取 `error.type`、`error.code` 或顶层 `code`，再获取对应的 `message`。不要用 HTTP 错误结构替代第 5 节的任务状态判断。

| HTTP 状态 | 常见错误标识 | 含义与处理 |
| --- | --- | --- |
| `400` / `422` | `invalid_request_error` | JSON、必填参数、时长、分辨率或素材组合不符合要求；修改参数后再提交 |
| `401` | `API_KEY_REQUIRED`、`INVALID_API_KEY`、`API_KEY_DISABLED` | 检查 Key 和鉴权头 |
| `402` / `403` | `insufficient_balance`、`INSUFFICIENT_BALANCE` | 余额或可用额度不足；按错误消息处理 |
| `403` | `ACCESS_DENIED` 或权限相关标识 | 检查 IP、分组和模型权限 |
| `404` | `not_found_error` | 任务不存在，或当前 Key 无权访问；确认 ID 和创建时使用的 Key |
| `409` | `video_content_unavailable` | 当前不可下载，按任务状态处理 |
| `409` | `video_cancellation_in_progress` | 正在最终处理或取消，查询状态后再操作 |
| `409` | `video_cancellation_conflict` | 取消与完成冲突，查询最终状态 |
| `429` | 限流或额度相关标识 | 遵循 `Retry-After`，检查并发和额度；查询请求可退避重试 |
| `501` | `operation_not_supported` | 当前任务不支持取消 |
| `502` | `upstream_error` | 视频服务请求失败；创建请求不要盲目自动重试 |
| `502` | `video_submission_unknown` | 提交结果不确定，见下一节 |
| `503` | `video_pricing_not_configured`、`seedance_account_cost_not_configured` | 对应模型或分辨率尚未就绪，联系支持 |
| `503` | `seedance_no_available_account` | 暂无可用视频服务资源 |
| `503` | `seedance_state_persistence_failed` | 任务状态保存失败；创建可能已经生效，见下一节 |

以上为常见错误，实际状态和错误消息以响应为准。

### 9.2 避免重复创建

创建接口没有对外承诺 `Idempotency-Key` 幂等机制。重复 `POST` 可能创建独立任务并分别计费。

- 已获取任务 `id`：后续查询该任务，不要重新创建来代替轮询。
- 创建请求网络超时或连接中断：任务可能已经被受理，不能仅凭客户端超时判断提交失败。
- 返回 `video_submission_unknown`：保存响应头中的 **`X-Video-Task-ID`**，连同请求时间和错误消息联系支持，**禁止自动重试创建**。
- 创建返回 `seedance_state_persistence_failed`，特别是消息提示任务已创建或不要自动重试：同样可能已生成任务；保留 `X-Video-Task-ID`（如有）并联系支持。
- 查询、下载遇到临时网络错误时，可以保留同一个任务 ID 退避重试；不要在重试逻辑中重新执行创建请求。

## 10. Python 完整调用示例

以下示例仅使用 Python 标准库，演示创建一次、轮询和下载。API 地址已设为 `https://ai.yusflow.com`，运行前设置 `VIDEO_API_KEY` 和 `VIDEO_MODEL`；其中 `VIDEO_MODEL` 必须填写当前 Key 模型目录返回的实际模型 ID。示例只发送模型名和提示词，如模型要求其他参数，请在创建请求中补充其支持的值。

```python
import json
import os
import time
from urllib.error import HTTPError
from urllib.parse import quote, urlsplit
from urllib.request import Request, urlopen

base_url = "https://ai.yusflow.com"
api_key = os.environ["VIDEO_API_KEY"]
model = os.environ["VIDEO_MODEL"]
headers = {"Authorization": f"Bearer {api_key}"}


def api_json(method, path, payload=None):
    data = None if payload is None else json.dumps(payload).encode("utf-8")
    request_headers = dict(headers)
    if data is not None:
        request_headers["Content-Type"] = "application/json"
    request = Request(base_url + path, data=data,
                      headers=request_headers, method=method)
    try:
        with urlopen(request, timeout=120) as response:
            return json.load(response)
    except HTTPError as exc:
        detail = exc.read().decode("utf-8", errors="replace")
        task_hint = exc.headers.get("X-Video-Task-ID", "")
        raise RuntimeError(
            f"HTTP {exc.code}: {detail}; X-Video-Task-ID={task_hint}"
        ) from exc


# 创建仅执行一次；异常直接退出，避免重复提交。
task = api_json("POST", "/v1/videos/generations", {
    "model": model,
    "prompt": "清晨海边的电影镜头，海鸥掠过水面，自然光",
})
print("Task ID:", task["id"], flush=True)
task_path = "/v1/videos/" + quote(task["id"], safe="")
deadline = time.monotonic() + 20 * 60  # 客户端等待上限，并非服务端时限。

while task["status"] in ("queued", "in_progress"):
    if time.monotonic() >= deadline:
        raise TimeoutError(f"等待超时，请稍后继续查询任务 {task['id']}")
    time.sleep(5)
    task = api_json("GET", task_path)
    print("Status:", task["status"], flush=True)

if task["status"] != "completed":
    raise RuntimeError(f"Task {task['id']}: {task['status']}; {task['error']}")

# 只将凭据发送到本站规范的内容路径。
content_path = task["content_url"]
parsed = urlsplit(content_path)
if parsed.scheme or parsed.netloc or content_path != task_path + "/content":
    raise RuntimeError("Unexpected content_url")

request = Request(base_url + content_path, headers=headers)
with urlopen(request, timeout=120) as response, open("output.mp4", "wb") as output:
    while True:
        chunk = response.read(1024 * 1024)
        if not chunk:
            break
        output.write(chunk)
print("Saved: output.mp4")
```

示例遇到 HTTP 错误会直接停止并打印错误；正式服务可为查询和下载补充退避重试，创建请求仍应遵循上一节的规则。

## 11. 兼容路径

以下路径可兼容既有接入，响应结构与本文一致：

| 功能 | 兼容路径 |
| --- | --- |
| 创建 | `POST /v1/videos` |
| 查询 | `GET /v1/videos/generations/{id}`、`GET /v1/videos/jobs/{id}` |
| 下载 | `GET /v1/videos/generations/{id}/content`、`GET /v1/videos/jobs/{id}/content` |

上述视频接口也接受去掉 `/v1` 的路径形式，例如 `POST /videos/generations`。无论使用哪种兼容路径，完成响应中的 `content_url` 均使用 `/v1/videos/{id}/content`。

本文适用于视频生成分组提供的统一视频任务接口。模型通过 `model` 参数选择，接入时统一使用本文的 `prompt`、素材 URL 和任务 JSON 格式，具体可选参数及限制以所选模型能力为准。
