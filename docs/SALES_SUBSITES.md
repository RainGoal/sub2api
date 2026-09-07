# 销售分站（首期实施说明）

## 已确认范围

- 销售域名仅作为推广入口，跳转主站；登录、充值、消费和支付回调仍在主站。
- 首次有效推广来源在注册流程中保留，创建用户时原子绑定销售；已有用户不改归属。
- 首期仅对余额按量消费分成，套餐不计佣。
- 结算收入采用实际扣减余额，赠送额度和充值优惠成本由平台承担。
- 结算毛利为收入减约定账号统计成本，按消费时生效的销售比例计佣。
- 保留负毛利和负佣金，按周期净额结算；分站客户不叠加普通邀请返利。
- 销售只查看所属客户的脱敏身份、消费、毛利、佣金与结算；管理员配置与登记付款。

## 使用入口与计费口径

管理端 `/admin/sales` 用于维护销售档案、查看客户与账本、生成月结单、确认和登记付款。
独立用户端 `wsapi-front` 的 `/sales` 提供销售工作台；已有普通用户被管理员创建为销售后，重新进入应用即可显示入口。
销售继续使用原有用户账号登录，不能访问管理员接口，也不能按其他销售 ID 查询数据。

约定成本沿用账号统计成本：优先使用消费记录的 `AccountStatsCost`，否则使用本次计费基础总价，再乘账号统计倍率。
批量图片按既有批量图片统计口径，在最终扣费时采集成本；预冻结和释放不计佣。
该口径是双方约定的结算成本，不是供应商实际对账净成本。

例如扣减余额 $100、约定成本 $60、比例 30%，毛利为 $40，销售分成为 $12。
扣减余额 $1、成本 $5、比例 50%，分成为 -$2；不能将单笔负利润归零。
金额为 USD，数据库固定保留 8 位小数。充值和套餐扣减不产生本模块佣金。

每次成功扣费与分账事件在同一事务提交；后台将事件幂等写入不可变账本。
分成比例、归属、成本和收入在计佣事件中固定，之后改价不重算历史。
用户消费退款或账目更正首期由管理员创建调整流水，可关联原流水；付款登记仅记录线下付款凭证，不执行银行转账。

## 推广与归属

1. 用户访问已配置销售域名，跳到主站 `/api/v1/sales/referral?code=销售代码`。
2. 后端验证代码、功能开关和推广状态，在主站设置签名、HttpOnly、Secure、SameSite=Lax 的 `sales_referral` Cookie，再跳转 `/register`。
3. 有效来源保留 30 天；同一浏览器首次有效来源优先，访问其他销售入口不会覆盖或延长它。
4. 新用户创建与归属绑定共用数据库事务；邮箱与 OAuth 注册均接入，未完成 OAuth 注册回滚会清理新增归属。
5. 已有用户登录、OAuth 绑号和后续访问不改变归属；注册/登录成功或退出会清理来源 Cookie。

首次访问只保存待绑定来源，注册成功才产生客户。浏览器清理 Cookie、无痕或跨设备访问不能自动延续未注册来源。
主站的页面与 `/api` 必须处于同一 HTTPS origin；不依赖销售域名的跨域 Cookie。

## 开关和结算

设置保存在既有 `settings` 的 `custom_sales_config`：`{"enabled":false,"main_frontend_url":""}`。
主站 URL 只接受 HTTPS origin，例如 `https://app.example.com`，不接受子路径、查询参数或片段。

| 控制项 | 行为 |
|---|---|
| 全局 enabled | 关闭时停止新增推广绑定和消费计佣；仍保留并处理已有财务事件、账本和结算 |
| promotion_enabled | 控制该销售能否新增推广注册；已归属客户不转移 |
| accrual_enabled | 控制该销售后续扣费是否计佣 |
| payout_frozen | 阻止新的付款登记；仍可计佣和查看历史 |

月结使用 UTC 自然月，只能生成已结束月份。生成时领取截止点之前所有未结算流水，包括旧月份延迟入账和上期负数结转。
有对应月份未处理事件时拒绝关账；事件处理完再重试。结算顺序必须向前，不能倒回已结算月份。
净佣金为正时形成应付款；为负时应付款为零，负额进入下期抵扣。总佣金统计排除结转副本，避免重复累计。
结算状态依次为 `draft`（待确认）、`confirmed`（待付款）、`paid`（已付款），不提供修改或删除财务历史的接口。

## 不变量与验证重点

- 客户绑定后从任意入口消费仍归原销售；重复注册、登录、OAuth 绑号不能覆盖归属。
- 同一笔扣费只记录一次分账事件；扣费回滚不得留下有效佣金。
- 事件固定消费时的归属、比例、成本与收入；后台重试和调价不能重算历史。
- 余额冻结不计佣，最终扣费才计佣；未扣费、套餐和无归属消费不计佣。
- 账目与付款登记防重复；已付款记录通过关联调整更正。
- 销售查询、明细和结算接口全部校验所属关系；管理员变更沿用现有审计。
- 新开关默认关闭；主站支付订单和回调语义保持兼容。

## API

沿用 `/api/v1` 的成功、错误及分页响应结构。用户接口要求原有 JWT 登录；管理接口要求管理员身份并沿用审计机制。
公开推广入口继承现有 IP 限流，登录接口继承现有认证及访问限制。

| 方法与路径 | 请求与返回 |
|---|---|
| GET /api/v1/sales/referral?code=... | 公开入口，设置主站 Cookie 后 302 跳转注册页 |
| GET /api/v1/sales/me | 本人销售档案及统计；非销售返回 404 |
| GET /api/v1/sales/customers | 仅本人客户，邮箱脱敏 |
| GET /api/v1/sales/ledger | 仅本人 usage / adjustment / carry 流水 |
| GET /api/v1/sales/settlements | 仅本人结算单 |
| GET、PUT /api/v1/admin/sales/settings | 读取或保存 enabled、main_frontend_url |
| GET、POST /api/v1/admin/sales/partners | 分页销售列表或创建销售 |
| PUT /api/v1/admin/sales/partners/:id | 更新完整销售配置，user_id 不可更换 |
| GET /api/v1/admin/sales/partners/:id/overview | 指定销售统计 |
| GET /api/v1/admin/sales/customers | 分页客户列表，可按 partner_id 筛选 |
| GET /api/v1/admin/sales/ledger | 分页账本，可按 partner_id 筛选 |
| GET /api/v1/admin/sales/settlements | 分页结算单，可按 partner_id 筛选 |
| POST /api/v1/admin/sales/settlements | `{partner_id,month:"2026-08",request_key}` |
| POST /api/v1/admin/sales/settlements/:id/confirm | `{request_key}` |
| POST /api/v1/admin/sales/settlements/:id/pay | `{request_key,payment_reference}` |
| POST /api/v1/admin/sales/adjustments | `{partner_id,source_ledger_id?,commission,note,request_key}` |

创建和更新销售使用 `user_id,name,code,hostname,commission_rate,promotion_enabled,accrual_enabled,payout_frozen`。
比例为 0–100 的百分数，代码为 3–48 位小写字母、数字或短横线；域名填完整二级域名，不带协议、端口或路径。
销售账号必须是现有未删除普通用户；代码、域名、销售账号分别唯一。

列表支持 `page,page_size`（最多 100）；销售列表支持 `search`。
日期参数为 `start_date,end_date`，格式 `YYYY-MM-DD`，按 UTC 包含首尾日期。
统计的收入、成本、利润和分成按消费日期过滤；客户数、未入账单分成、待付款、已付款和待处理事件数为累计值。
客户列表日期筛选注册时间，行内金额为累计值；结算列表筛选结算单创建时间。

概览字段为 `partner,customer_count,revenue,cost,profit,commission,unsettled_commission,pending_payout,paid_commission,pending_events`。
客户字段为 `user_id,partner_id,email,created_at,revenue,profit,commission`。
账本包含归属、金额快照、比例、流水类型、时间、备注及关联结算单；结算包含月份、截止时间、净额、应付、结转和付款凭证。

`request_key` 长度 8–128，由客户端为一次操作生成；网络重试沿用同一个值。相同键和相同内容返回原结果，键被不同内容复用返回冲突。
错误码包括 `SALES_DISABLED`（403）、`SALES_NOT_FOUND`（404）、`SALES_INVALID`（400）、`SALES_CONFLICT`、`SALES_PENDING_EVENTS`、`SALES_PAYOUT_FROZEN`（409）；未登录为 401，无管理员权限为 403。

## 部署与启用

已确认主站是 **https://ai.yusflow.com**。销售域名由管理员逐个填写，不强制使用“销售代码.yusflow.com”，也不自动拼接域名。
保存销售档案不会自动创建 DNS 记录或 Nginx 配置。本次只交付代码，没有修改 DNS、Nginx、线上配置或付款回调地址。

1. 备份数据库和线上 `docker-compose.yml`，检查现有 `docker compose config`。先更新后端，启动时应用新增迁移 `235_custom_sales.sql`，再发布管理端和 `wsapi-front`。
2. 使用明确提交号或版本号镜像，只替换应用容器；不得执行 `docker compose down -v` 或删除数据卷。检查服务健康、迁移及后台计佣日志。
3. 在管理端“销售分站 → 分站设置”填写主站 `https://ai.yusflow.com`，先保持总开关关闭。
4. 创建或选用销售的普通用户账号，然后创建销售档案，配置代码、域名、比例及推广/计佣开关。
5. 给销售域名配置 DNS、HTTPS 证书和固定跳转。以下以 `sales-a.yusflow.com`、代码 `sales-a` 为例，真实值须与销售档案一致：

```nginx
# 合并到该销售域名的 HTTPS server 块；保留现有证书与其他服务器配置。
server {
    listen 443 ssl;
    server_name sales-a.yusflow.com;
    ssl_certificate     /实际证书路径/fullchain.pem;
    ssl_certificate_key /实际证书路径/privkey.pem;
    location / {
        return 302 https://ai.yusflow.com/api/v1/sales/referral?code=sales-a;
    }
}
```

6. 主站的 `/api/` 必须转发至 Sub2API 后端，并保留 `proxy_set_header Host $host` 及正确的 `X-Forwarded-Proto`；`/register` 和 `/sales` 由用户端 SPA 提供。推广入口响应不可被 CDN 缓存。
7. 保持支付异步通知地址、支付返回地址、OAuth 回调都在现有主站；不需要给销售域名配置支付或 OAuth 回调。确认 `backend_mode_enabled` 没有禁用普通用户注册与用户接口。
8. 完成下面的环境联调后打开总开关。普通用户登录后销售菜单不显示，销售账号登录后出现销售工作台。

生产启用前的环境联调：用新浏览器访问销售域名，确认跳到主站、取得安全 Cookie，然后分别验证邮箱注册和已启用的 OAuth 注册。
连续访问两个销售入口后注册应归首个有效来源；已有账号经销售域名登录不应改归属。
测试一笔小额余额消费，核对实际扣款、成本快照和佣金；测试充值通知正常入账且不产生销售佣金或普通邀请返利。
使用另一个销售账号检查无权查看其他客户，再核对测试月结及付款登记的防重行为。

停用时关闭总开关或指定销售开关即可，保留数据库中的归属和财务表。
若应用需要回退，可先关闭开关再切回旧镜像；旧版忽略新增表，不要删除财务数据或手动逆向执行迁移。

## 验证记录（2026-09-07）

| 工作目录 | 实际验证 | 结果 |
|---|---|---|
| backend | `go test -p 2 ./...` | 通过 |
| backend | `go test -p 2 -tags unit ./...` | 发布前完整单元测试通过 |
| backend | `go test -p 1 -tags unit ./internal/service ./internal/handler -run '^(TestAuthSales\|TestAuthService\|Test.*OAuth\|TestSales)' -count=1 -timeout=120s` | 通过 |
| backend | `go test -p 1 -tags unit ./internal/service ./internal/handler -run '^(TestAuthSales\|TestAuthService\|Test.*OAuth\|TestAffiliateSales\|TestAffiliateOrdinary\|TestSalesReferral)' -count=1 -timeout=120s` | 新增返利互斥与注册清理回归通过 |
| backend | `go test -tags embed ./internal/web` | 通过 |
| sub2api | `pnpm --dir frontend run typecheck` | 通过 |
| sub2api | `pnpm --dir frontend run test:run` | 255 文件、1881 测试通过；随后新增的管理端销售测试单独执行 2 项通过 |
| sub2api | `pnpm --dir frontend run build` | 通过 |
| wsapi-front | `npm run typecheck`、`npm test`、`npm run build` | 通过，35 文件、122 项测试 |
| 两个仓库 | `git diff --check`、`git status --short` | 通过；没有纳入构建产物、密钥或无关新增文件 |

浏览器使用接口桩验证了管理端暗色、销售工作台中英文、桌面和 390px 手机视口、付款弹窗及权限状态；不是线上注册和支付联调。
新迁移及仓储中的实际 SQL 在独立 PGlite 0.5.8 / PostgreSQL 18.3 引擎执行，覆盖重复迁移、默认关闭、归属及事件去重、负数结转、调整关联、不可变保护、注册失败清理。

随后启动隔离的本地 PostgreSQL 16.15，并实际运行生产 Go 仓储的专项数据库测试：

```powershell
# 仅用于独立测试数据库；测试在独立 schema 中创建及清理测试数据。
$env:SUB2API_SALES_QA_DATABASE_URL='postgresql://postgres@127.0.0.1:55440/postgres?sslmode=disable'
go test ./internal/repository -run '^TestSalesDatabase' -count=1 -v
Remove-Item Env:SUB2API_SALES_QA_DATABASE_URL
```

三项测试全部通过：完整结算生命周期；6 个并发同键请求只生成一份账单且关账等待消费事务锁；5 万条流水下客户统计使用 `idx_sales_ledger_customer_partner` 索引。
临时数据库已关闭。未设置上述环境变量时，这些专项数据库测试明确跳过。
本机没有 Docker，仓库原有 `-tags integration` 容器测试入口仍被 harness 跳过；已通过的是独立真实 PostgreSQL 专项测试。
可在有 Docker 的环境执行 `go test -tags integration ./internal/repository -run '^TestSales' -count=1 -v` 验证项目完整迁移与容器环境。
后端及管理端随个人版本 `v0.1.29` 发布，销售工作台在独立 `wsapi-front` 仓库提交。
`v0.1.30` 补齐销售仓储与测试中的资源关闭返回值处理，沿用现有仓储的清理方式。
修复后使用 golangci-lint 2.13.0 执行 `golangci-lint run --timeout=30m --max-issues-per-linter=0 --max-same-issues=0 ./...`，报告 `0 issues`；`go test -p 2 ./...` 通过。
首发版本的 GitHub CI 单元测试、集成测试、前端及部署脚本检查均通过；本次修复针对失败的 `errcheck` 静态检查，并将完整 lint 加入发布前验证要求。
本地验证没有执行线上部署；用户端仓库原有的其他修改不包含在本次销售功能提交中。

## 已知限制

成本快照缺失、为负或不是有限数值时，不会假定成本为零，而是使该销售客户的本次扣费事务失败。
这种异常可能发生在上游消费已完成之后，需结合计费错误日志人工核对；首期没有“待核算成本”队列或自动补算能力。
当前约定成本来自既有账号统计计价，启用前应核对相关渠道的统计价格与倍率；合法的零成本配置仍按零成本计佣。
财务记录含非级联外键以保留历史，正常账号软删除不会移除销售归属或账目；不能直接物理删除已有销售财务历史的用户。
