# Sub2API 定制分支开发约束

本文件是 `sub2api` fork 的长期开发约束。当前仓库用于维护基于上游
`Wei-Shaw/sub2api` 的个人定制版本，后续 Agent 和开发者开始工作前必须先阅读本文件。

## 项目与分支

- `origin`：`git@github.com:RainGoal/sub2api.git`，用于保存个人 fork。
- `upstream`：`git@github.com:Wei-Shaw/sub2api.git`，用于同步上游更新。
- `feat/my-sub`：长期定制分支，承载个人后端能力、管理端 UI 和管理端新页面。
- `upstream/main`：上游基线，不在上面直接开发个人需求。
- `wsapi-front` 是独立的用户端前端；本仓库的 `frontend/` 主要维护 Sub2API 管理端及其内嵌前端。

定制目标按以下顺序处理：

1. 优先吸收上游安全修复、稳定性修复和重要功能。
2. 在不破坏上游行为的前提下实现个人后端和管理端需求。
3. 最后处理品牌、布局和视觉差异。

## 个人版本与发布

- 个人发布版本独立于上游版本，当前个人版本为 `v0.1.23`。
- 个人版本的唯一依据是 `origin` 上严格匹配 `v0.1.<整数>` 的 Tag；不得依据
  `upstream` Tag、本地混合 Tag 列表或 `backend/cmd/server/VERSION` 推导个人版本。
- 创建新版本前必须先读取 `origin` 的全部个人版本 Tag，取最大的补丁号并加一。
  例如最新版本为 `v0.1.13` 时，新版本必须为 `v0.1.14`。
- 发布前必须确认新 Tag 在本地和 `origin` 均不存在，并确认 Tag 指向
  `feat/my-sub` 的待发布提交；禁止覆盖、移动或强制推送已有 Tag。
- 个人版本使用 annotated Tag，Tag 信息采用 `Release v0.1.<补丁号>`；先推送
  `feat/my-sub`，再单独推送新 Tag，并在推送后核对远程 Tag 的 peeled commit。
- 每次创建个人版本时，必须同步更新本节的“当前个人版本”并与发布变更一起提交。

## 最小侵入原则

- 修改前先读取目标文件、检查 `git status --short`，并搜索现有 handler、route、service、repository、类型和测试。
- 不猜接口、配置字段、权限名称或第三方库 API；优先依据本仓库代码、测试和上游实现。
- 优先新增隔离文件和小范围挂载点，避免无关格式化、文件搬迁、大范围重构。
- 上游文件必须只做必要的增量修改；不要为了个人需求重写公共基础组件或核心流程。
- 新能力尽量使用独立命名空间、独立 service/handler 和清晰的功能开关，避免把定制逻辑散落到多个核心文件。
- 发现本次需求之外的严重问题时，不直接顺手修复，在交付说明中以 `Note` 提醒。

## 后端定制规则

- 新接口必须明确 HTTP 方法、路径、请求/响应结构、错误码、鉴权和权限要求，并补充接口测试或 handler 测试。
- API 复用现有 `/api/v1` 体系和响应格式；需要扩展时保持已有客户端兼容，不随意修改既有字段语义。
- 管理端接口必须复用现有登录、角色、权限和审计机制；禁止通过隐藏前端按钮代替后端鉴权。
- 新配置放入现有配置体系，默认值必须保持上游行为；实验性或高风险能力默认关闭。
- 数据库迁移只能新增，不修改已经执行过的迁移文件；迁移必须可重复部署并有必要的回滚/兼容考虑。
- 计费、支付、渠道调度、鉴权和网关链路属于高风险区域，修改前必须先确认调用链和现有测试覆盖。
- 不为纯 UI 需求新增数据库表、迁移或后端逻辑。

## 管理端前端规则

- 管理端新页面放在现有管理端目录和路由体系中，复用布局、权限守卫、表格、弹窗、通知和 i18n 能力。
- 新页面必须同时考虑加载中、空状态、错误、无权限、窄屏和重复提交状态。
- 新增或修改用户可见文案时，中文和英文必须同步补齐，不在组件中留下固定中文。
- 优先使用项目已有图标、组件和样式变量；品牌样式可放在 `frontend/src/brand/` 等隔离目录中。
- 当前品牌 UI 保持强制暗色、克制圆角、高对比和可扫描的信息密度；不得为了视觉效果引入大范围重构。
- 管理端和 `wsapi-front` 用户端保持职责分离，不把用户端页面复制进本仓库，也不让管理端依赖用户端构建产物。

## 上游同步流程

不要把 `git pull` 当作同步上游；它通常只同步当前分支跟踪的 `origin`。

同步前确认工作区干净，并执行：

```powershell
git switch feat/my-sub
git fetch upstream --prune
git switch -c sync/upstream-main-YYYYMMDD
git merge --no-commit --no-ff upstream/main
```

解决冲突后至少运行相关后端、前端测试和 `git diff --check`。验证通过后：

```powershell
git commit -m "merge: upstream main"
git switch feat/my-sub
git merge --ff-only sync/upstream-main-YYYYMMDD
git push origin feat/my-sub
```

建议只在本机开启 Git 冲突复用：

```powershell
git config rerere.enabled true
git config rerere.autoupdate true
```

不要对已经共享的 `feat/my-sub` 强制 rebase、改写历史或使用破坏性 reset。同步冲突优先保留上游安全修复，再以最小补丁恢复个人定制行为。

## 修改与验证

使用 `apply_patch` 做小范围编辑；大范围机械变更必须先说明范围和风险。不要使用会删除、覆盖仓库或线上数据的命令。

后端改动至少执行：

```powershell
gofmt -w <changed-go-files>
go test ./...
```

涉及内嵌前端或静态资源时执行：

```powershell
go test -tags embed ./internal/web
pnpm --dir frontend run build
```

前端改动至少执行：

```powershell
pnpm --dir frontend run typecheck
pnpm --dir frontend run test:run
```

交付前必须执行：

```powershell
git diff --check
git status --short
```

并确认没有把 `frontend/dist/`、本地配置、密钥、数据目录或无关未跟踪文件纳入提交。

## Docker 与线上部署

- 禁止执行 `docker compose down -v`，禁止删除或重建 `data`、`postgres_data`、`redis_data` 等数据卷。
- 不直接用仓库 compose 文件覆盖线上配置；部署前先备份线上 `docker-compose.yml` 并检查 `docker compose config`。
- 只构建并替换应用镜像，使用提交号或明确版本号作为镜像 tag。
- 标准更新流程：构建镜像，上传/切换镜像，执行 `docker compose up -d --force-recreate sub2api`，然后检查 `ps`、日志和健康接口。
- 涉及 OAuth、支付回调、Nginx 路由或内嵌前端时，部署说明必须明确列出对应检查项。

## Git 与交付

- 默认在当前 `feat/my-sub` 工作，不随意创建或删除长期分支。
- 提交应小而完整，建议使用 `feat(custom): ...`、`fix(custom): ...`、`merge: upstream main` 等简洁信息。
- 提交前只暂存本次需求相关文件，尤其不要提交 `.env`、配置密钥、构建产物以及无关的 `frontend/dist/`。
- 每次交付必须包含：变更摘要、实际验证命令及结果、部署提示、已知风险/依赖的后端开关或路由检查项。
- 如果创建提交，说明提交 hash 和分支名；如果无法运行某项验证，明确说明原因。

## 禁止事项

- 禁止在 `upstream/main` 直接开发个人需求。
- 禁止为了纯 UI 需求修改核心计费、支付、鉴权或数据库结构。
- 禁止复制维护两套相同的管理端业务实现。
- 禁止提交敏感配置、线上数据、构建产物或用户端仓库的无关文件。
- 禁止未经明确要求执行 `git reset --hard`、`git checkout --`、强制推送或删除分支。
