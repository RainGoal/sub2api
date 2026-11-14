# 管理端 wsapi UI 实施与验收

日期：2026-11-14。分支：`feat/my-sub`。本记录随 `v0.1.74` 发布变更提交，记录发布前本地验收；远端扫描与发布状态以对应提交的 GitHub Actions 为准。

## 已完成

- 管理工作区使用石墨灰暗色、余烬橙焦点、暖白主按钮、克制圆角和本地字体。
- 覆盖侧栏、顶栏、列表、表单、弹窗、公告、首次引导、统计卡片和 canvas 图表。
- 样式集中于 `frontend/src/brand/`，共享组件仅增加必要语义类；保留业务事件和权限。
- 管理路由及管理员访问的“我的账户”工作区均使用品牌；后台模式 `/login` 使用相同风格。
- 普通用户身份、公开页及独立MCP授权页保留原主题偏好，Tailwind 变量缺省值保留原色板、圆角和阴影。
- 无后端源码、数据库、API、计费或支付逻辑改动；未修改 wsapi-front。

## 实际验证

以下前端命令在 `D:/Code/Other/sub2api` 执行，固定使用现有依赖安装器版本 pnpm 9.15.9。

| 命令 | 结果 |
| --- | --- |
| `npx --yes pnpm@9.15.9 --dir D:/Code/Other/sub2api/frontend run typecheck` | 通过 |
| `npx --yes pnpm@9.15.9 --dir D:/Code/Other/sub2api/frontend run test:run --maxWorkers=4 --minWorkers=2` | 补齐个人工作区后完整重跑：415 个文件、3573 个测试通过 |
| `npx --yes pnpm@9.15.9 --dir D:/Code/Other/sub2api/frontend run build` | 最终资源构建通过，含 i18n 完整性 3 个测试 |
| `npx --yes pnpm@9.15.9 --dir D:/Code/Other/sub2api/frontend exec vite build` | 补齐字体许可证后重建通过，3份许可证与依赖原文一致 |
| `go test -tags embed ./internal/web`（工作目录 `D:/Code/Other/sub2api/backend`） | 补齐个人工作区最终构建后通过，0.966s |
| `git diff --check` | 通过 |
| `git status --short` | 仅本次前端改动及原有无关 `docs-verification.txt`；无构建产物或配置纳入暂存 |

首次默认并发全量测试发生一次 worker 意外退出，不能计为通过；上表为限制并发后的完整成功重跑。
最后的纯样式收口另经模板编译、相关既有测试、最终构建及浏览器检查；没有引入新的业务行为。

## 浏览器结果

Vite `http://127.0.0.1:3006` 连接本地模拟 API `http://127.0.0.1:8766`，未使用生产数据。
实际运行源码页面；模拟服务不转发外部请求，不执行生产写操作。

- 30/30 管理路由在 1440×1000 成功渲染；品牌及暗色正确，无 pageerror、console error、HTTP 错误或页面级横向溢出。
- 用户列表检查 1440、1024、390px；桌面内部横向滚动、手机卡片模式正常。
- 侧栏展开 232px、收起 68px，内容和 Prompt Audit 保存条偏移一致。
- 用户创建弹窗桌面/手机正常，Esc 关闭后焦点回到创建按钮；账号创建/编辑表面与层级正常。
- 账号创建/编辑/统计、分组创建/编辑和390px窄屏弹窗检查通过；加载骨架、空数据及500/403恢复流程已覆盖，结果见 `forms-audit.json`。
- 分页 Select 传送至 BODY 后仍为品牌面板；键盘选择正常。日期筛选打开和选择正常。
- 首轮曾将管理员个人页排除；用户指出后已修正范围。当前管理员在 `/keys`、`/profile` 和管理页之间保持品牌暗色，公开页恢复原偏好，localStorage 未改写。
- 独立浏览器模拟普通用户身份及非后台模式，`/profile` 保持原浅色、256px侧栏与主题切换；管理员侧栏保持232px。见 `personal-scope-results.json`。
- “我的账户”10个可见页面均已巡检，品牌生效、无页面级横溢或运行异常；结果见 `personal-route-results.log`，核心接口使用模拟空数据。
- 渠道状态的本地mock缺失接口，另按真实接口类型构造1行3色块fixture补测；1440/390px下BODY浮层颜色和位置正确，见 `personal-monitor-result.json`。
- 个人资料的顶部身份与头像预览、兑换余额面板、订阅无限额、个人概览统计/图表、自定义页面外框均补齐品牌样式；不改变支付标识和二维码白底。
- 本轮日志前缀为 `personal-`；截图重点为 `personal-profile-final.png`、`personal-profile-mobile.png`、`personal-redeem.png`、`personal-monitor-1440.png` 和 `personal-monitor-390.png`。
- 后台模式登录页及首次引导浮层已验收；English 界面在 720×500 等效窄视口无横向溢出。
- 公告和 Toast 的减少动效使用真实加载样式及临时 DOM fixture 实测：reduce 后过渡为 0s、transform 为 none；结果见 `theme-audit-results.json`。
- 720 CSS px 为 1440px 屏幕 200% 缩放的等效工作区检查，未声称执行原生浏览器 200% 缩放。

浏览器原始记录、截图和命令日志均位于 `D:/Code/Other/sub2api/output/playwright/admin-wsapi/`。
完整路由记录：`route-audit.json`。重点截图：`dashboard-final.png`、`users-desktop.png`、
`users-mobile.png`、`create-user-dialog.png`、`create-user-mobile.png`、`users-1024-select.png`、
`dashboard-date-picker.png`、`route-ops.png`、`route-settings.png`、`route-orders-dashboard.png`。

## 限制与发布提示

- 次要路由主要以模拟空数据验收，未覆盖全部生产数据组合；Prompt Audit 模拟运行状态为空造成 3 条 i18n warning，属于 fixture 限制。
- 账号编辑原有嵌套 Esc 会同时关闭 Select 和外层弹窗；本次未改键盘事件，未扩大范围修复。
- 账号列表原有500/403初次加载失败只记录console并显示空态，未展示专用错误提示；移除模拟拦截后恢复3条数据。本次未修改 `useTableLoader` 行为。
- 最终字体资产共 107 个 WOFF2、4,755,324 字节，中文按 Unicode 分片加载；不是首屏一次下载全部文件。
- 3份字体OFL许可证原文保留于 `frontend/public/fonts/licenses/`，随Vite静态资源分发。
- 构建仍提示已有大于 500kB 的分块及 Browserslist 数据过旧，构建成功，未扩展到无关依赖升级。
- 发布需重新构建应用镜像，将 `backend/internal/web/dist` 打入内嵌资源；无需迁移或新增后端开关。
- 本机未安装Docker，未本地执行需要容器的后端集成测试；发布Tag前需确认同一提交的远端CI完整unit/integration及Security Scan全部成功。
- 后台登录样式依赖既有 `backend_mode_enabled`；发布后检查登录、管理深链接刷新、字体/静态资源和健康接口，并回归 OAuth/支付入口与回调页。
- 本次未部署。原有 `docs-verification.txt` 未修改；本地模拟脚本、截图、构建产物均保持忽略。
