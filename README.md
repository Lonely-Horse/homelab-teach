# homelab-teach

自托管的单文件 HTML 课件站：把 teach skill 产出的课件存进 SQLite，按课程文件夹组织，公开只读浏览，管理员在后台维护。

- 后端：Go 1.25 标准库 + `modernc.org/sqlite`（纯 Go，无 cgo）
- 前端：原生 HTML/CSS/JS，`go:embed` 打进二进制，无模板引擎、无构建步骤
- 部署：单二进制或 Docker Compose，SQLite 单文件，备份就是一次 `VACUUM INTO`

定位：个人 / 单管理员的学习课件站，不是多租户 SaaS。优先可读、可维护、可回滚，不追求云原生全家桶。

当前状态：本地已跑通；Docker Compose 配置已就绪，但本机还没拉基础镜像做镜像构建验证；按单用户、低并发设计，未做多用户与公网压测。

设计依据是两份本地文档：`homelab-teach 需求文档.md` 与 `homelab-teach 接口文档.md`（v1.0）。实现过程中有三处接口扩展和一处部署调整，见 [§9](#9-与接口文档的差异)。

---

## 1. 它能做什么

**公开侧（只读）**

- 首页 / 课件 / 个人三个视图
- 课程树按文件夹展开，只显示对匿名访问者可见的内容
- 课件列表、标题与正文全文搜索（LIKE，不是 FTS5）
- 单文件 HTML 课件直接渲染，支持内联 `<script>` 与样式（导入会忽略 css/js/图片等非 HTML 文件，课件请把资源全部内联）
- 私有课件默认 404；后台生成的签名 URL 可在 5 分钟内打开

**管理侧（单管理员）**

- 文件夹：新建、改名、拖拽移动、上移/下移、三态可见性（公开 / 私有 / 继承）、递归删除
- 课件：上传（单文件 ≤ 8MB）、批量导入目录（整批 ≤ 200MB）、改标题、拖拽移动、预览、删除
- 站点页面：首页与个人页的标题/正文编辑
- 会话：查看活跃会话、踢掉单个或全部其他会话
- 备份：一键 `VACUUM INTO` 生成一致性快照

**交互特点**

- 后台没有原生 `alert` / `confirm` / `prompt` / `window.open`
- 新建与改名是行内输入框，回车提交、Esc 取消
- 移动靠拖拽：把课件或文件夹拖到左侧目标文件夹
- 删除是两段式：第一次点变「确认删除」，4 秒内再点才执行

---

## 2. 快速开始

> **最短路径**（凭证已填）：`go run . -dev`，打开 <http://localhost:8080/admin/>，Ctrl+C 停止。没填过凭证先做 2.2。

### 2.1 环境要求

- Go 1.25+（`go.mod` 声明 `go 1.25.0`）
- 可选：Docker + Docker Compose

### 2.2 生成管理员凭证（只需一次）

凭证硬编码在 `internal/config/config.go`，不经过数据库、不经过任何路由。

```bash
cd ~/go_learn/homelab-teach
go run . -hash
```

输入密码后，把输出的 `const` 块粘贴进 `internal/config/config.go`，替换原来的 `adminSalt` / `adminHash` / `adminIters` / `pwdSecret`。

建议：密码 ≥ 20 位随机串，或把 `adminIters` 提到 ≥ 1,000,000。`salt/hash` 会随源码进入版本库，安全性完全依赖密码强度与迭代次数。

> 当前仓库里的凭证已经填好；只有换密码或重新部署时才需要再跑一次。

### 2.3 本地运行

```bash
go run . -dev
```

`-dev` 做两件事：把站点 Origin 设为 `http://localhost:8080`，并关闭 cookie 的 `Secure`。

打开：

- 外壳：<http://localhost:8080/>
- 后台：<http://localhost:8080/admin/>
- 课件：`http://localhost:8080/f/{folder_id}/{filename}`

端口被占用时：

```bash
go run . -dev -addr 127.0.0.1:18080
```

### 2.4 Docker 部署

先把 `internal/config/config.go` 里的 `siteOrigin` 改成真实域名（当前是 `https://teach.lonelyhorse.top`），再：

```bash
mkdir -p data
sudo chown -R 10001:10001 data
docker compose up -d --build
docker compose logs -f
```

首次构建需要拉取 `golang:1.25-alpine` 与 `alpine:3.20`；构建完成后容器以 UID 10001 运行，所以 `data/` 必须对这个 UID 可写。

Compose 默认值：

| 项 | 默认 |
|---|---|
| 宿主机发布 | `${BIND_IP:-127.0.0.1}:8080` |
| 容器内监听 | `0.0.0.0:8080`（端口映射需要） |
| 数据卷 | `./data:/data` |
| 运行用户 | UID 10001（非 root） |
| 权限 | `no-new-privileges`、`cap_drop: ALL` |
| 日志 | json-file，10MB × 3 |

**不要把 `BIND_IP` 设成 `0.0.0.0`**，除非前面有可信反向代理和防火墙。应用只需要被本机或 Tailscale 网段访问。

### 2.5 反向代理（示例）

TLS 在 VPS/Nginx 终止，应用只监听回环地址：

```nginx
server {
    listen 443 ssl http2;
    server_name teach.lonelyhorse.top;

    client_max_body_size 210m;   # 批量导入上限 200MB

    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }
}
```

建议传 `Host`（或 `X-Forwarded-Host`）：登录与写操作的 Origin 校验会拿它和请求的 `Origin` 对比；反代改写 Host 时，`siteOrigin` 仍可作为兜底。

---

## 3. 命令行参数

| 参数 | 默认 | 说明 |
|---|---|---|
| `-addr` | `127.0.0.1:8080` | 监听地址 |
| `-db` | `./data/app.db` | SQLite 文件路径 |
| `-dev` | `false` | 开发模式：Origin 用 `http://localhost:<port>`，cookie 不设 `Secure` |
| `-hash` | `false` | 生成管理员凭证常量后退出 |

域名不在参数里：生产值写死在 `internal/config/config.go` 的 `siteOrigin`，改完要重新编译/构建镜像。

---

## 4. 代码结构

```
.
├── main.go                    启动、flag 解析、-hash、优雅关闭
├── internal/
│   ├── config/
│   │   └── config.go          凭证常量、siteOrigin、flag 校验、PBKDF2 生成
│   ├── db/
│   │   ├── db.go              打开 SQLite、PRAGMA、建表、user_version 迁移
│   │   ├── folders.go         文件夹 CRUD、递归删除、可见性继承算法
│   │   ├── pages.go           课件 CRUD、搜索候选、按 SHA 去重
│   │   ├── sessions.go        会话创建/续期/删除/列表
│   │   ├── site.go            首页/个人页读写
│   │   └── backup.go          VACUUM INTO 备份
│   ├── auth/
│   │   ├── auth.go            PBKDF2 校验、cookie、会话续期
│   │   └── ratelimit.go       IP 失败锁定 + 全局限速
│   ├── content/
│   │   └── content.go         <title> 抽取、正文纯文本化、HTML 最低校验
│   └── server/
│       ├── server.go          路由、中间件、安全头、Host/Origin/限流辅助
│       ├── public.go          公开树、列表、搜索、站点页面
│       ├── courseware.go      /f/ 课件读取、签名 URL 校验
│       ├── auth_handlers.go   登录/登出
│       ├── admin.go           /admin/api 路由分发、方法守卫
│       ├── admin_folders.go   文件夹管理接口
│       ├── admin_pages.go     课件上传/列表/修改/删除/预览
│       ├── admin_import.go    流式 multipart 目录导入
│       ├── admin_site.go      站点页面管理
│       ├── admin_backup.go    备份接口
│       ├── admin_sessions.go  会话管理接口
│       ├── json.go            JSON 响应与错误格式
│       └── server_test.go     端到端集成测试（httptest + 临时 SQLite）
├── web/
│   ├── index.html             公开外壳
│   ├── admin.html             管理后台
│   ├── login.html             登录页
│   ├── 404.html               404 页
│   ├── app.js                 公开：树、搜索、课件 iframe
│   ├── admin.js               后台：行内编辑、拖拽、两段式删除
│   ├── app.css                全站样式（含亮/暗主题）
│   └── favicon.svg
├── Dockerfile
├── docker-compose.yml
├── go.mod / go.sum
└── README.md
```

分层原则：`server` 只做 HTTP 与参数校验，`db` 只做 SQL，`auth` 只做密码/会话，`content` 只做 HTML 文本抽取。没有 ORM，没有依赖注入框架。

### 4.1 设计取舍

| 选择 | 原因 | 代价 |
|---|---|---|
| Go 标准库 + `net/http` | 路由简单、依赖少、单二进制 | 没有框架的中间件生态，路由要自己写 |
| `modernc.org/sqlite`（纯 Go） | 无 cgo，`CGO_ENABLED=0` 直接交叉编译 | 比 cgo 版慢一些，但单用户足够 |
| SQLite + WAL + 单连接 | 单文件、备份简单、写入串行化 | 不适合高并发写入 |
| 不用 FTS5 | 数据量小，`LIKE` 够用，少一个依赖 | 课件多了搜索会变慢 |
| 无应用级后台任务 / 定时器 | 行为可预测，退出干净 | 备份、导入都在请求内同步完成 |
| 原生前端 + `go:embed` | 无 Node、无构建链，改完直接编译 | 没有组件化，DOM 代码偏长 |
| 单管理员 + 源码凭证 | 不需要用户表、注册、权限系统 | 换密码要重新编译 |
| 单域名 | 部署简单，不用第二个证书 | 课件与后台同源，见 §7 |

---

## 5. 它是怎么工作的

### 5.1 请求路由

所有请求先过中间件：panic 恢复 → 访问日志 → 方法守卫。

方法守卫只放行两类请求：

- `GET` / `HEAD`
- 路径以 `/admin/` 开头的请求（具体方法再由各 handler 校验）

其他方法一律 `405`。也就是说，公开接口天然只读，写操作只能从后台走。

| 路径 | 作用 |
|---|---|
| `/`、`/static/*` | 公开外壳与静态资源（embed） |
| `/api/tree` | 公开课程树（只含可见文件夹） |
| `/api/pages?folder_id=` | 公开课件列表 |
| `/api/search?q=` | 公开搜索（最多 50 条，只搜公开内容） |
| `/api/site/{home\|about}` | 首页/个人页 |
| `/healthz` | `SELECT 1` 健康检查 |
| `/f/{folder_id}/{filename}` | 课件正文；私有内容必须带签名参数 |
| `/admin/login`、`/admin/logout` | 登录/登出 |
| `/admin/` | 后台页面 |
| `/admin/preview/{id}` | 后台预览跳转（303 到签名 URL） |
| `/admin/api/*` | 管理接口，全部需要会话 |

### 5.2 数据模型

```
folders(id, parent_id, name, is_public, sort)
pages(id, folder_id, filename, title, html, body_text, size, sha256, sort, created_at, updated_at)
sessions(id, created_at, renewed_at, pwd_fp, ip, ua)
site_pages(slug, title, body, updated_at)
```

- 根文件夹固定 `id=1`、`name=_root`、`is_public=0`，不可删除/修改。
- 课件 ID 是随机 base32 字符串，不暴露自增序号。
- `sha256` 用于同一文件夹内的重复上传判定：内容相同则 `skipped`，不同则覆盖。
- `body_text` 是去掉 `<script>`、`<style>`、注释和标签后的纯文本，只用于搜索与摘要，不参与渲染。

### 5.3 可见性：三态继承

`folders.is_public`：

| 值 | 含义 |
|---|---|
| `1` | 公开 |
| `0` | 私有 |
| `NULL` | 继承父级 |

解析时从当前文件夹向上找，第一个非 `NULL` 的值就是结果；根节点是 `0`，保证循环一定终止、默认私有。

公开树只返回解析为公开的文件夹。私有课件即使知道 `/f/{id}/{filename}` 也返回 404，除非带上有效签名。

### 5.4 签名预览

后台点「预览」→ `GET /admin/preview/{id}` → 服务端生成：

```
/f/{folder_id}/{filename}?e={exp}&t={hmac}
```

- 有效期 5 分钟
- `t = hex(HMAC-SHA256(pwdSecret, "{folder_id}/{filename}|{exp}"))`
- 校验用 `hmac.Equal`，过期或签名不对都是 404
- 签名 URL 不依赖会话，适合从后台跳到新标签页打开

### 5.5 上传与导入

上传：

1. `multipart` 解析，单文件上限 8MB
2. 校验 UTF-8、文件名、最低 HTML 结构（含 `<html` 或 `<!doctype`）
3. 抽取 `<title>`（缺失时用文件名）、正文纯文本、SHA-256
4. 同文件夹同文件名：内容相同跳过，不同则覆盖
5. 同文件夹不同文件名但 SHA 相同：也跳过（避免重复课件）

批量导入：

- 整批上限 200MB，流式读取，不把整批塞进内存
- 读取 `webkitRelativePath`，按目录自动创建文件夹（`is_public=NULL`）
- 非 HTML 文件记入 `ignored`，坏文件记入 `errors`，不影响其他文件
- 整个过程在一个事务里；致命错误会回滚

### 5.6 认证与会话

- 密码：PBKDF2-HMAC-SHA256，默认 600,000 次迭代，32 字节 salt/hash，常量时间比较
- Cookie：`sid`，`HttpOnly`、`SameSite=Lax`、生产环境 `Secure`，有效期 7 天
- 续期：每 24 小时续一次；7 天不活跃失效
- 密码指纹：`hex(sha256(adminSalt字符串 + adminHash字符串))[:16]` 写进会话；换密码后旧会话自动失效
- 登录限流：单 IP 连续失败 5 次锁 15 分钟；全局 60 次/分钟
- 写操作 Origin 校验：优先看 `Sec-Fetch-Site`，没有则比较 Origin 与请求 Host / `X-Forwarded-Host` / `siteOrigin`

### 5.7 前端

- `web/` 通过 `//go:embed web` 打进二进制，服务端不做模板渲染
- 公开页与后台共用 `app.css`，支持亮/暗/跟随系统
- 后台无原生对话框：行内输入、拖拽移动、两段式删除
- 预览用普通 `<a target="_blank">`，避免 `window.open` 被浏览器拦截

### 5.8 备份与退出

- 备份：`VACUUM INTO '<dataDir>/backups/app-YYYY-MM-DD-HHMMSS.db'`，同步执行，不覆盖已有文件
- 退出：收到 SIGINT/SIGTERM 后用 10 秒超时优雅关闭 HTTP 服务并关闭数据库
- 日志：`slog` JSON，输出方法、路径、状态码、耗时、IP

---

## 6. 数据库与可靠性

- DSN 固化三个 PRAGMA：`journal_mode=WAL`、`foreign_keys=1`、`busy_timeout=5000`
- `SetMaxOpenConns(1)`：单连接串行化，避免 SQLite 写锁竞争
- 迁移用 `PRAGMA user_version`，整个迁移在一个事务里；数据库版本高于程序版本时拒绝启动
- 删除文件夹用递归 CTE 收集后代，先删课件、再按深度倒序删文件夹，单事务完成

---

## 7. 安全边界

这个项目是**单管理员、单域名**部署，安全模型按这个前提设计：

1. 后台 cookie 是 `HttpOnly + SameSite=Lax + Secure`，JS 读不到 token。
2. 公开接口只读，写操作全部在 `/admin/` 下并要求会话。
3. 私有课件靠 HMAC 签名 URL 放行，5 分钟过期。
4. 登录有 IP 锁定和全局速率限制。
5. 应用默认只监听 `127.0.0.1`；Docker 默认也只发布到 `127.0.0.1`。
6. 响应头带 `X-Content-Type-Options: nosniff`、`X-Robots-Tag: noindex`、`frame-ancestors`。

**必须知道的取舍**：当前是单域名，课件和后台同源。课件是任意 HTML + JS，课件里的脚本可以带着你的会话调用 `/admin/api/*`。所以：

- 只上传你自己信任的课件（自己写的、teach skill 生成的、review 过的）
- 不要把来源不明的 HTML 直接扔进来
- 如果想彻底隔离，有两条路：把代码改回按 Host 分流的独立课件域名，或给 `/f/` 加 `CSP: sandbox`（课件会失去 localStorage/cookie）

---

## 8. API 一览

公开：

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/tree` | 公开课程树 |
| GET | `/api/pages?folder_id=` | 公开课件列表 |
| GET | `/api/search?q=` | 公开搜索 |
| GET | `/api/site/{home\|about}` | 站点页面 |
| GET | `/f/{folder_id}/{filename}` | 课件（私有需签名） |
| GET | `/healthz` | 健康检查 |

管理（需要会话）：

| 方法 | 路径 | 说明 |
|---|---|---|
| POST | `/admin/login` | 表单登录 |
| POST | `/admin/logout` | 登出 |
| GET | `/admin/api/tree` | 全部文件夹（含私有） |
| POST | `/admin/api/folders` | 新建文件夹 |
| PATCH/DELETE | `/admin/api/folders/{id}` | 改名/移动/可见性/删除 |
| GET | `/admin/api/pages?folder_id=` | 管理端课件列表（含私有） |
| POST | `/admin/api/pages` | 上传课件 |
| GET/PATCH/DELETE | `/admin/api/pages/{id}` | 读取/改标题、移动/删除 |
| GET | `/admin/api/pages/{id}/preview` | 生成签名 URL（JSON） |
| POST | `/admin/api/import` | 批量导入目录 |
| GET/POST | `/admin/api/site/{home\|about}` | 站点页面 |
| POST | `/admin/api/backup` | 生成备份 |
| GET/DELETE | `/admin/api/sessions` | 会话列表 / 踢掉其他会话 |
| DELETE | `/admin/api/sessions/{id}` | 踢掉指定会话 |

错误响应统一为 `{"code": "...", "message": "..."}`。完整状态码与错误码以接口文档为准。

---

## 9. 与接口文档的差异

1. **管理端课件列表**：需求要求「选中文件夹 → 右侧课件列表」，但接口文档没有定义。补充：
   `GET /admin/api/pages?folder_id={id}`，返回该文件夹下全部课件（含私有）。
2. **递归删除**：接口文档规定非空文件夹返回 409。后台改用：
   `DELETE /admin/api/folders/{id}?recursive=1`，返回 `{"deleted_folders": n, "deleted_pages": m}`。
   不带 `recursive=1` 时仍是 409，兼容原行为。
3. **预览跳转**：后台用 `GET /admin/preview/{id}` 303 到签名 URL，避免 `window.open` 被拦截；原 JSON 预览接口保留。
4. **单域名**：原设计用 `app.` / `c.` 两个域名隔离后台与课件；现在按实际部署改成单域名 `teach.lonelyhorse.top`，代价见 §7。

---

## 10. 测试与验收

```bash
go build ./...
go vet ./...
go test ./...
```

集成测试 `internal/server/server_test.go` 覆盖：

- 健康检查、未登录 401
- 登录 / 同源 Origin / 跨站 Origin 403 / 登录限流 429
- 文件夹创建、环检测
- 上传、重复上传跳过、私有课件 404
- 公开树、公开列表、搜索转义
- 课件签名 URL、篡改签名 404、预览跳转
- 路径穿越、方法守卫 405
- 递归删除、非空删除 409
- 批量导入、备份、会话列表、登出

手动冒烟：

```bash
curl -i http://127.0.0.1:8080/healthz

# 登录并存 cookie
curl -i -c /tmp/jar -X POST http://127.0.0.1:8080/admin/login -d 'password=...'

# 建文件夹
curl -b /tmp/jar -X POST http://127.0.0.1:8080/admin/api/folders \
  -H 'Content-Type: application/json' -d '{"parent_id":1,"name":"数据结构"}'

# 上传
curl -b /tmp/jar -X POST http://127.0.0.1:8080/admin/api/pages \
  -F 'file=@lesson1.html' -F 'folder_id=2'

# 设为公开
curl -b /tmp/jar -X PATCH http://127.0.0.1:8080/admin/api/folders/2 \
  -H 'Content-Type: application/json' -d '{"is_public":1}'

# 匿名读取
curl -i http://127.0.0.1:8080/f/2/lesson1.html
```

---

## 11. 已知限制

- 单管理员，没有多用户、权限组、审计日志
- 凭证在源码里，换密码要重新 `-hash` + 重新编译
- 单域名部署，课件与后台同源（见 §7）
- 课件只能上传/替换，不能在线编辑；没有版本历史
- 搜索是 `LIKE` 扫描，不是 FTS5；课件量大时性能会下降
- 单文件 ≤ 8MB，整批导入 ≤ 200MB
- 后台拖拽依赖桌面浏览器；触屏设备无法拖拽（可再加点击选择目标文件夹）
- 没有定时备份，需要自己把 `data/backups/` 纳入异地备份

---

## 12. 这个项目是怎么来的（Vibe coding 记录）

这是我从需求到验收完整走了一遍的 vibe coding 项目，不是「AI 一把梭」：

- **我负责**：写需求和接口文档，定边界（标准库优先、单管理员、不上 FTS5、不做在线编辑），做产品决策（单域名、递归删除、后台去弹窗），在浏览器里验收、提返工。
- **AI 负责**：按文档落代码、补集成测试、改交互细节。
- **验收标准**：`go build` / `go vet` / `go test` 全过，再实际启动、登录、增删改查、看日志和退出行为。

这个 README 也是为了让我自己以后能看懂：每一层为什么存在、边界在哪里、哪些地方是刻意不做的。后续可以继续加可观测性、备份策略和更细的权限控制。
