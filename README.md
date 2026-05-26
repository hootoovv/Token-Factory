# Token Factory - 企业级 LLM API 代理中心

Token Factory 是一个企业级的大语言模型（LLM）API 代理网关，提供统一的 API 入口，将请求智能转发到多个上游 LLM 供应商（如 OpenAI、Azure、Anthropic 等），并附带完整的 Web 管理界面，支持用户管理、供应商管理、模型映射、流量统计与监控、审计日志等功能。

## 核心特性

- **统一 API 代理**：兼容 OpenAI `/v1/chat/completions` 和 Ollama `/api/tags` 接口规范，现有客户端无需修改即可接入
- **多供应商负载均衡**：支持顺序优先（Sequential）、轮询（Round-Robin）、随机（Random）三种供应商选择策略
- **会话亲和性**：同一用户对同一模型的请求优先路由到上次成功的供应商，提升上游 Prompt 缓存命中率
- **自动故障转移**：当首选供应商不可用时，自动重试其他供应商（最多尝试 3 个）
- **四阶段超时控制**：连接建立超时 → 首 Token 返回超时 → 流传输 Idle 超时 → 总超时，精细控制每个阶段
- **API Key 自动状态管理**：根据上游错误码和错误信息自动检测欠费、冷却状态；支持连续失败计数防抖、冷却自动恢复、禁用即时断连
- **多 API Key 支持**：每个供应商可配置多个 API Key，状态下沉到 Key 级别，支持独立禁用/启用
- **流量监控与统计**：按月分表记录流量数据，提供模型排行、供应商排行、用户用量等多维度统计
- **API Key 管理**：支持为每个用户生成和管理多个 API Key，`tk-` 前缀格式
- **Web 管理界面**：基于 Vue 3 + Element Plus 构建的管理后台，含仪表盘、用户管理、供应商管理、模型管理、审计日志等功能
- **调用记录**：在内存中保留最近 N 次 API 调用的完整信息（输入/输出参数、调用者、模型、耗时等），管理员可在后台查看和点击查看详情（JSON 美化显示），N 可配置（默认 10，最大 20），重启后清空
- **审计日志**：记录所有关键操作（API Key 创建/删除、用户管理、供应商管理等），支持按操作类型、对象类型、操作者、时间范围过滤查询
- **多数据库支持**：支持 SQLite（默认）、MySQL、PostgreSQL 三种数据库后端
- **JWT 认证**：基于 JWT 的用户认证与角色权限控制（管理员/普通用户）
- **内存缓存**：热数据常驻内存，数据变更后自动重载（带防抖），代理层零数据库查询
- **安全增强**：供应商 API Key 加密存储（AES-256-GCM）、传输加密、CORS 限制、速率限制、请求体大小限制、错误信息脱敏

## 系统架构

```
┌──────────────┐     ┌──────────────────────────────────────────────┐
│   客户端      │────▶│  代理服务器 (Proxy Server) :11444           │
│  (OpenAI SDK │     │  - API Key 验证                              │
│   / Ollama)  │     │  - 模型解析 & 供应商选择                     │
└──────────────┘     │  - 请求转发 & 故障转移                       │
                     │  - 四阶段超时控制                             │
                     │  - 自动状态检测 & 活跃请求管理                │
                     │  - 流量记录                                   │
                     │  - 调用记录 (内存环形缓冲)                     │
                     └──────────────┬───────────────────────────────┘
                                    │
                     ┌──────────────▼───────────────────────────────┐
                     │  管理服务器 (Admin Server) :8080              │
                     │  - RESTful API (/api/*)                      │
                     │  - Web 管理界面 (SPA)                        │
                     │  - JWT 认证 & 权限控制                       │
                     │  - 审计日志记录                                │
                     │  - 调用记录查询接口                             │
                     └──────────────┬───────────────────────────────┘
                                    │
                     ┌──────────────▼───────────────────────────────┐
                     │  数据层                                       │
                     │  - 内存缓存 (Cache + Debounced Reload)        │
                     │  - 数据库 (SQLite / MySQL / PostgreSQL)       │
                     │  - 按月分表流量记录                            │
                     │  - 审计日志表                                  │
                     │  - 调用记录 (内存, 不持久化)                     │
                     └──────────────────────────────────────────────┘
```

## 项目结构

```
token_factory_v3/
├── config.yaml                # 配置文件
├── Dockerfile                 # 多阶段 Docker 构建
├── .gitignore
├── README.md
│
├── server/                    # 后端 (Go)
│   ├── main.go                # 入口文件，初始化并启动服务，优雅关闭
│   ├── go.mod                 # Go 模块定义 (token_factory, Go 1.21)
│   ├── go.sum                 # Go 依赖锁定
│   ├── admin/
│   │   └── admin.go           # 管理端 HTTP 服务器 & API 路由 (30+ 接口)
│   ├── callrecords/
│   │   └── callrecords.go     # API 调用记录内存存储 (环形缓冲区, 线程安全)
│   ├── cache/
│   │   └── cache.go           # 内存缓存，供应商选择策略，防抖缓存，自动状态管理
│   ├── config/
│   │   └── config.go          # 配置文件加载与解析 (含默认值填充)
│   ├── database/
│   │   └── database.go        # 数据库初始化、模型定义、迁移、AES-256-GCM 加密/解密
│   ├── middleware/
│   │   ├── auth.go            # JWT 认证 & 管理员权限中间件
│   │   └── ratelimiter.go     # 基于 IP 的速率限制中间件
│   ├── proxy/
│   │   └── proxy.go           # API 代理转发服务器核心 (四阶段超时、流式、故障转移、状态检测、调用记录采集)
│   ├── traffic/
│   │   └── traffic.go         # 流量记录器 (批量写入、按月分表) & 统计查询
│   └── web/
│       └── dist/              # 前端构建产物 (Go embed 嵌入二进制)
│
└── web/                       # 前端 (Vue 3 + TypeScript)
    ├── index.html             # SPA 入口
    ├── package.json           # 依赖定义
    ├── package-lock.json      # 依赖锁定
    ├── vite.config.ts         # Vite 配置 (开发代理、构建输出)
    ├── tsconfig.json          # TypeScript 配置
    ├── tsconfig.node.json     # Node 环境 TS 配置
    └── src/
        ├── main.ts            # Vue 应用入口 (Pinia、Router、Element Plus)
        ├── App.vue            # 根组件 (传输密钥刷新时重新获取)
        ├── api/
        │   └── index.ts       # Axios API 封装 (XOR+Base64 传输加密/解密)
        ├── router/
        │   └── index.ts       # 路由配置 & JWT 过期导航守卫
        ├── stores/
        │   └── user.ts        # Pinia 用户状态 (token、role、transmissionKey)
        ├── components/
        │   └── Layout.vue     # 全局布局 (顶栏导航、用户下拉)
        └── views/
            ├── Dashboard.vue       # 公开仪表盘 (统计、排行、供应商状态)
            ├── UserDashboard.vue   # 认证用户仪表盘 (按模型/供应商/用户过滤)
            ├── Login.vue           # 登录页
            ├── admin/              # 管理员页面
            │   ├── Layout.vue      # 管理员侧栏布局
            │   ├── Users.vue       # 用户管理 (CRUD、重置密码)
            │   ├── Providers.vue   # 供应商管理 (多 API Key、禁用/启用、连通性测试)
            │   ├── Models.vue      # 模型管理 (内联供应商映射)
            │   ├── Stats.vue       # 管理员统计概览 (含缓存重载)
            │   ├── AuditLogs.vue   # 审计日志查看 (多维度过滤)
            │   └── CallRecords.vue  # 调用记录查看 (表格 + JSON 详情对话框)
            └── user/               # 普通用户页面
                ├── Layout.vue      # 用户侧栏布局
                ├── ApiKeys.vue     # API Key 管理 (前端生成 Key)
                ├── Usage.vue       # 使用记录 (分页)
                └── MyStats.vue     # 个人统计 & 修改密码
```

## 技术栈

### 后端

| 技术 | 用途 |
|------|------|
| Go 1.21+ | 后端开发语言 |
| Gin | HTTP Web 框架 |
| GORM | ORM 数据库操作 |
| SQLite / MySQL / PostgreSQL | 数据持久化 |
| JWT (golang-jwt/jwt/v5) | 用户认证 |
| bcrypt | 密码哈希 |
| AES-256-GCM | 供应商 API Key 加密存储 |
| embed | 前端静态资源嵌入二进制 |
| golang.org/x/time/rate | IP 速率限制 |
| golang.org/x/net/http2 | HTTP/2 协议支持 |

### 前端

| 技术 | 用途 |
|------|------|
| Vue 3.4 | 前端框架 |
| TypeScript | 类型安全 |
| Vite 5 | 构建工具 |
| Element Plus 2.7 | UI 组件库 |
| Pinia | 状态管理 |
| Vue Router 4 | 路由管理 |
| Axios | HTTP 请求 |
| ECharts 5.5 | 数据可视化图表 |
| Tailwind CSS 3.4 | 原子化 CSS |

## 快速开始

### 前置要求

- Go 1.21+
- Node.js 18+（仅前端开发时需要）

### 1. 配置

编辑 `config.yaml` 文件（首次运行会自动创建默认配置）：

```yaml
# 代理服务监听地址（API 转发端口）
proxy_listen: "0.0.0.0:11444"

# 管理端监听地址（Web 管理界面）
admin_listen: "0.0.0.0:8080"

# 数据库配置
database:
  type: sqlite                              # sqlite / mysql / postgres
  dsn: "data/token_factory.db"              # 连接字符串

# 默认管理员账户（仅首次启动时创建）
admin:
  username: admin
  password: admin123

# JWT 密钥（为空则自动生成，重启后旧 Token 失效）
jwt_secret: ""

# 供应商 API Key 加密密钥（base64 编码的 32 字节密钥，必填项）
# 生成方式: openssl rand -base64 32
# 首次配置后请勿更改，否则已加密的数据将无法解密
encryption_key: ""

# CORS 允许的来源（逗号分隔，为空则默认允许本地开发环境）
cors_origins: ""

# 内存中保留的最近API调用记录条数（默认10，最大20）
# 仅保存在内存中，不持久化到磁盘，重启后清空
call_record_limit: 10

# 代理策略配置
proxy:
  # 供应商选择策略: sequential / round-robin(默认) / random
  provider_strategy: "round-robin"
  # 会话亲和性: 启用后同一用户的同一模型请求优先使用上次成功的供应商
  session_affinity: true
  # 供应商默认超时配置（未配置供应商超时时的回退值，单位：秒）
  default_timeouts:
    total: 300        # 总超时：从请求发送到响应完成的绝对最大时间
    connect: 10       # 连接建立超时：TCP+TLS 握手完成的最大时间
    first_token: 120  # 首 Token 返回超时：收到第一个响应字节的时间
    stream_idle: 60   # 流传输 Idle 超时：两次数据传输之间的最大空闲时间

  # API Key 自动状态管理配置
  auto_status:
    # 是否启用自动状态检测（默认 true）
    enabled: true
    # 连续失败 N 次后标记为冷却状态（默认 2 次，防止偶发网络抖动误判）
    consecutive_failures: 2
    # 冷却状态自动恢复时间（秒），超过此时间后自动恢复为 active（默认 300 = 5 分钟）
    cooldown_recovery_sec: 300
    # 冷却恢复检查间隔（秒），后台定期扫描冷却中的 Key 并尝试恢复（默认 60）
    cooldown_check_interval: 60
```

### 2. 构建前端（可选）

如果需要修改前端代码，先构建前端：

```bash
cd web
npm install
npm run build
```

构建产物会自动输出到 `server/web/dist/` 目录，Go 后端通过 `embed` 嵌入到二进制中。

### 3. 启动服务

```bash
cd server
go run main.go [config_path]
```

默认配置路径为 `config.yaml`，可通过命令行参数指定其他路径。

启动后日志输出示例：

```
========================================
 Token Factory - 企业级LLM API代理中心
========================================
[数据库] sqlite 连接成功 (data/token_factory.db)
[数据库] 表结构迁移完成
[管理员] 默认管理员已就绪 (用户名: admin)
[安全] 已从环境变量 ENCRYPTION_KEY 读取加密密钥，供应商API Key将加密存储
[缓存] 数据加载完成
[缓存] 自动状态管理已启用 (连续失败阈值=2, 冷却恢复=300s, 检查间隔=60s)
[流量] 记录器已启动
[安全] 已从环境变量 JWT_SECRET 读取JWT密钥
[代理] 服务器启动在 0.0.0.0:11444 (策略: round-robin, 亲和性: true, 默认超时: 总=300s/连接=10s/首Token=120s/流Idle=60s)
[安全] 已生成API Key传输加密密钥
[管理] 服务器启动在 0.0.0.0:8080
```

### 4. 访问

- **Web 管理界面**：http://localhost:8080
- **API 代理端点**：http://localhost:11444
- 默认管理员：`admin` / `admin123`

### 5. 环境变量

以下配置项优先从环境变量读取，适合生产部署：

| 环境变量 | 说明 | 对应配置项 |
|----------|------|-----------|
| `JWT_SECRET` | JWT 签名密钥 | `jwt_secret` |
| `ENCRYPTION_KEY` | 供应商 API Key 加密密钥（base64 编码 32 字节） | `encryption_key` |
| `ADMIN_PASSWORD` | 管理员密码（覆盖默认值） | `admin.password` |
| `CORS_ORIGINS` | CORS 允许来源（逗号分隔） | `cors_origins` |

### 6. 构建 Docker 镜像

#### 构建镜像（在项目根目录执行）

```shell
docker build -t token-factory:latest .
```

#### 运行容器

```shell
docker run -d \
  --name token-factory \
  -p 11444:11444 \
  -p 8080:8080 \
  -v token-factory-data:/app/data \
  token-factory:latest
```

#### 自定义配置文件

```shell
docker run -d \
  --name token-factory \
  -p 11444:11444 \
  -p 8080:8080 \
  -v /path/to/your/config.yaml:/app/config.yaml \
  -v token-factory-data:/app/data \
  token-factory:latest
```

Docker 镜像采用三阶段构建：`node:20-alpine`（前端编译）→ `golang:1.21-alpine`（后端编译）→ `alpine:3.19`（最小化运行镜像，非 root 用户、健康检查、Asia/Shanghai 时区）。

## 使用指南

### 添加供应商

1. 登录管理后台，进入 **管理后台 → 供应商管理**
2. 点击 **新增供应商**，填写：
   - 名称（如 `openai-us`）
   - Base URL（如 `https://api.openai.com/v1`）
   - 超时时间（总超时、连接超时、首 Token 超时、流 Idle 超时）
   - 重试次数（默认 3 次）
3. 供应商创建后，可为其添加多个 API Key，每个 Key 可独立设置备注名称和状态

### 管理供应商 API Key

1. 在供应商管理页面，展开供应商面板可看到该供应商下所有 API Key
2. 每个API Key 支持：
   - **禁用/启用**：点击禁用按钮后，该 Key 立即停止参与代理转发，正在进行的请求将被即时中断
   - **状态切换**：可在 active/cooldown/arrears/disabled 之间手动切换
   - **连通性测试**：调用上游 `/v1/models` 接口验证 Key 是否可用，显示延迟和模型列表
   - **编辑/删除**：修改备注名称、更新 Key 值、删除 Key
3. 仅 `active` 状态的 API Key 参与代理请求转发

### 添加模型

1. 进入 **管理后台 → 模型管理**
2. 点击 **新增模型**，填写模型名称和描述（如 `gpt-4o`）

### 配置模型-供应商映射

1. 进入 **管理后台 → 模型管理**，为模型关联供应商
2. 指定供应商侧的模型名称（如供应商使用 `gpt-4o-2024-05-13` 而你的模型名为 `gpt-4o`）
3. 同一个模型可映射到多个供应商，代理请求时按策略选择供应商并替换模型名

### 创建 API Key

1. 登录后进入 **个人中心 → API Keys**
2. 点击 **创建 API Key**，系统自动生成 `tk-` 前缀的密钥（32 字符，共 35 字符）
3. 使用该 Key 替代供应商原始 API Key 发起请求

### 查看审计日志

1. 进入 **管理后台 → 审计日志**
2. 可通过操作类型（创建/删除/更新）、对象类型（API Key/用户/供应商/模型）、操作者用户名、时间范围进行过滤查询
3. 审计日志记录所有关键操作，包括 API Key 的创建和删除，便于安全事件追溯

### 查看调用记录

1. 进入 **管理后台 → 调用记录**
2. 表格展示最近 N 次 API 调用的概要信息：时间、调用者、模型名称、输入/输出数据量、总用时、状态
3. 点击任意记录行，弹出详情对话框，展示完整信息包括：
   - 基本信息（记录ID、时间、调用者、模型名称、供应商、供应商模型、流式请求标识等）
   - 输入参数（JSON 格式，自动美化显示，支持一键复制）
   - 输出参数（JSON/SSE 格式，自动美化显示，支持一键复制；流式响应完整记录所有 SSE 事件）
4. 调用记录仅保存在内存中，最多保留 N 条（默认 10，最大 20，可在 `config.yaml` 中通过 `call_record_limit` 配置），服务重启后清空

### 调用 API

使用与 OpenAI 兼容的方式调用：

```bash
curl http://localhost:11444/v1/chat/completions \
  -H "Authorization: Bearer tk-your-api-key" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gpt-4o",
    "messages": [{"role": "user", "content": "Hello!"}]
  }'
```

查询可用模型列表：

```bash
curl http://localhost:11444/v1/models \
  -H "Authorization: Bearer tk-your-api-key"
```

Ollama 兼容接口：

```bash
curl http://localhost:11444/api/tags \
  -H "Authorization: Bearer tk-your-api-key"
```

## API 接口文档

### 代理端点（:11444）

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/v1/models` | OpenAI 兼容模型列表 |
| POST | `/v1/chat/completions` | OpenAI 兼容对话补全 |
| POST | `/v1/completions` | OpenAI 兼容文本补全 |
| POST | `/v1/embeddings` | OpenAI 兼容嵌入 |
| POST | `/v1/images/generations` | OpenAI 兼容图片生成 |
| GET | `/api/tags` | Ollama 兼容模型列表 |
| * | `/*` | 其他请求代理转发 |

### 管理端点（:8080）

#### 公开接口

| 方法 | 路径 | 说明 |
|------|------|------|
| POST | `/api/login` | 用户登录（返回 JWT + transmission_key） |
| GET | `/api/dashboard/stats` | 仪表盘统计 |
| GET | `/api/dashboard/model-ranking` | 模型使用排行 |
| GET | `/api/dashboard/provider-ranking` | 供应商使用排行 |
| GET | `/api/dashboard/provider-status` | 供应商实时状态 |
| GET | `/api/dashboard/models` | 模型列表（过滤用） |
| GET | `/api/dashboard/providers` | 供应商列表（过滤用） |

#### 用户接口（需认证）

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/api/me` | 获取当前用户信息 |
| GET | `/api/transmission-key` | 获取 API Key 传输加密密钥 |
| GET | `/api/api-keys` | 列出 API Key |
| POST | `/api/api-keys` | 创建 API Key |
| DELETE | `/api/api-keys/:id` | 删除 API Key |
| GET | `/api/usage` | 使用记录（分页） |
| GET | `/api/usage/stats` | 使用统计 |
| PUT | `/api/password` | 修改密码 |
| GET | `/api/my/dashboard/stats` | 个人仪表板统计 |
| GET | `/api/my/dashboard/model-ranking` | 个人模型排行 |
| GET | `/api/my/dashboard/provider-ranking` | 个人供应商排行 |
| GET | `/api/my/dashboard/users` | 用户列表（管理员过滤用） |

#### 管理员接口（需管理员权限）

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/api/users` | 列出用户（分页） |
| POST | `/api/users` | 创建用户 |
| PUT | `/api/users/:id` | 更新用户 |
| DELETE | `/api/users/:id` | 删除用户 |
| GET | `/api/providers` | 列出供应商（分页） |
| POST | `/api/providers` | 创建供应商 |
| PUT | `/api/providers/:id` | 更新供应商 |
| DELETE | `/api/providers/:id` | 删除供应商 |
| GET | `/api/provider-api-keys` | 列出供应商 API Key（分页） |
| POST | `/api/provider-api-keys` | 创建供应商 API Key |
| PUT | `/api/provider-api-keys/:id` | 更新供应商 API Key（含禁用/启用） |
| DELETE | `/api/provider-api-keys/:id` | 删除供应商 API Key |
| POST | `/api/provider-api-keys/test` | 测试供应商 API Key 连通性 |
| GET | `/api/models` | 列出模型（分页） |
| POST | `/api/models` | 创建模型 |
| PUT | `/api/models/:id` | 更新模型 |
| DELETE | `/api/models/:id` | 删除模型 |
| GET | `/api/model-providers` | 列出模型-供应商映射（分页） |
| POST | `/api/model-providers` | 创建映射 |
| DELETE | `/api/model-providers/:id` | 删除映射 |
| GET | `/api/stats/overview` | 管理员统计概览 |
| GET | `/api/audit-logs` | 查询审计日志（分页 + 过滤） |
| GET | `/api/call-records` | 获取最近 N 条 API 调用记录 |
| GET | `/api/call-records/:id` | 获取单条 API 调用记录详情 |
| POST | `/api/cache/reload` | 重载缓存 |

##### 审计日志查询参数

| 参数 | 类型 | 说明 |
|------|------|------|
| `page` | int | 页码（默认 1） |
| `page_size` | int | 每页条数（默认 20，最大 100） |
| `action` | string | 操作类型过滤：create / delete / update / login |
| `target_type` | string | 对象类型过滤：api_key / user / provider / model / model_provider / provider_api_key |
| `operator_name` | string | 操作者用户名（模糊匹配） |
| `start_time` | string | 开始时间（格式：YYYY-MM-DD） |
| `end_time` | string | 结束时间（格式：YYYY-MM-DD） |

## 供应商选择策略

| 策略 | 说明 |
|------|------|
| `sequential` | 按 ID 顺序选择，始终优先使用第一个可用供应商 |
| `round-robin` | 轮询负载均衡，依次轮流使用各供应商（默认） |
| `random` | 随机负载均衡，Fisher-Yates 洗牌算法随机选择 |

**会话亲和性**：启用后，同一用户对同一模型的请求会优先路由到上次成功的供应商。这在供应商使用 Prompt 缓存时特别有效，可以显著降低延迟和费用。当亲和供应商不可用时，自动降级到其他供应商。

## API Key 自动状态管理

代理请求过程中，系统会根据上游供应商返回的 HTTP 状态码和错误信息，自动判断并更新 API Key 的状态，无需人工干预。

### 状态检测规则

| 上游信号 | 检测状态 | 说明 |
|----------|----------|------|
| HTTP 402 | `arrears` | 明确的欠费信号，立即标记 |
| HTTP 429 + 欠费关键词 | `arrears` | 速率限制但响应体含欠费/续费/充值等关键词（中英文），立即标记 |
| HTTP 429 无欠费关键词 | `cooldown` | 纯速率限制，标记冷却 |
| HTTP 502 / 503 / 504 | `cooldown` | 服务不可用，标记冷却 |
| HTTP 401 / 403 + Key 失效关键词 | `cooldown` | Key 可能已失效（如 revoked/expired），标记冷却 |

### 欠费关键词列表

| 语言 | 关键词 |
|------|--------|
| 英文 | arrears, payment, billing, overdue, subscription, expired, insufficient quota, quota exceeded, plan expired, renew, recharge, top up, balance |
| 中文 | 欠费, 续费, 充值, 余额不足, 账户过期, 套餐过期, 配额耗尽, 账单, 到期 |

### 连续失败防抖

为防止偶发网络抖动导致误判，冷却状态需要连续失败 N 次（默认 2 次，通过 `consecutive_failures` 配置）才会被标记。欠费状态不经过防抖，一旦检测到立即标记。请求成功时自动重置连续失败计数。

### 冷却自动恢复

后台协程定期扫描冷却中的 API Key（检查间隔默认 60 秒），如果冷却持续时间超过配置的恢复时间（默认 300 秒 = 5 分钟），自动将其恢复为 `active` 状态。欠费状态不参与自动恢复，需管理员手动恢复。

### 禁用即时断连

当 API Key 被管理员手动禁用时（状态设为 `disabled`），系统会立即取消所有正在使用该 Key 的活跃代理请求（基于 Context 取消机制），使客户端请求按重试规则立即失败或切换到下一个供应商。

## 供应商 API Key 状态

| 状态 | 说明 | 恢复方式 |
|------|------|----------|
| `active` | 正常工作中，参与代理转发 | — |
| `cooldown` | 冷却中，暂时不可用，不参与代理转发 | 自动恢复（超时后）或管理员手动恢复 |
| `arrears` | 欠费，不可用，不参与代理转发 | 仅管理员手动恢复 |
| `disabled` | 已禁用，不参与代理转发，活跃请求立即中断 | 仅管理员手动启用 |

仅 `active` 状态的 API Key 参与代理请求转发。状态管理下沉到 Key 级别，同一供应商下的不同 Key 可有不同状态。

## 数据库

### 支持的数据库类型

| 类型 | DSN 示例 |
|------|---------|
| SQLite | `data/token_factory.db` |
| MySQL | `user:password@tcp(127.0.0.1:3306)/token_factory?charset=utf8mb4&parseTime=True&loc=Local` |
| PostgreSQL | `host=127.0.0.1 port=5432 user=postgres password=postgres dbname=token_factory sslmode=disable` |

### 数据表

| 表名 | 说明 |
|------|------|
| `providers` | 供应商配置（Base URL、超时时间、重试次数） |
| `provider_api_keys` | 供应商 API Key（加密存储，独立状态管理，一个供应商可有多个 Key） |
| `models` | 模型定义 |
| `model_providers` | 模型-供应商映射关系（含供应商侧模型名） |
| `users` | 用户账户（bcrypt 密码，admin/user 角色） |
| `api_keys` | 用户 API 密钥（`tk-` 前缀） |
| `audit_logs` | 审计日志 |
| `traffic_records_YYYYMM` | 流量记录（按月自动分表） |

流量记录按月自动分表，每张分表包含完整的索引（api_key_id、user_id、model_id、provider_id、provider_api_key_id、created_at），确保查询性能。统计查询会根据时间范围智能选择需要查询的分表，避免全表扫描。

## 四阶段超时控制

代理请求采用精细的四阶段超时机制，确保在各种网络条件下都能快速失败并切换供应商：

| 阶段 | 超时项 | 默认值 | 说明 |
|------|--------|--------|------|
| 1 | 连接建立超时 | 10s | TCP + TLS 握手完成的最大时间 |
| 2 | 首 Token 返回超时 | 120s | 从请求发送完毕到收到第一个响应字节的时间 |
| 3 | 流传输 Idle 超时 | 60s | 流式响应中两次数据传输之间的最大空闲时间 |
| 4 | 总超时 | 300s | 从请求发送到响应完成的绝对最大时间 |

每个阶段超时均可按供应商独立配置，未配置时使用全局默认值回退。阶段 1 和阶段 2 失败可重试到其他供应商；阶段 3（流式传输已开始）失败则无法重试。

## 安全特性

| 特性 | 说明 |
|------|------|
| API Key 加密存储 | 供应商 API Key 使用 AES-256-GCM 强制加密存储，启动时必须配置 `ENCRYPTION_KEY` 环境变量或配置文件 `encryption_key`，否则服务拒绝启动 |
| API Key 传输加密 | 前后端之间 API Key 使用 XOR+Base64 加密传输，密钥每次启动随机生成，仅存内存不持久化 |
| API Key 脱敏显示 | 供应商和用户 API Key 在前端仅显示前 4 位和后 4 位，日志中也自动脱敏 |
| CORS 限制 | 仅允许配置的白名单域名跨域访问，支持环境变量动态配置 |
| 速率限制 | 基于 IP 的请求速率限制（每秒 10 次，突发 20 次），自动清理过期记录 |
| 请求体限制 | 代理请求体最大 50MB，防止 OOM 攻击 |
| JWT 安全 | JWT 密钥优先从环境变量读取，支持自动生成（重启后旧 Token 失效），4 小时有效期 |
| 密码安全 | 使用 bcrypt 哈希存储，管理员密码支持环境变量覆盖 |
| 错误脱敏 | 内部错误信息不返回客户端，仅记录到服务端日志 |
| 审计日志 | 记录 API Key 创建/删除等关键操作，包含操作者、IP、时间等信息 |
| 参数化查询 | 数据库查询使用参数化方式，防止 SQL 注入 |
| 请求校验 | 管理接口使用结构体绑定和声明式校验，限制可更新字段白名单 |
| 活跃请求管理 | 禁用 API Key 时立即取消所有使用该 Key 的活跃代理请求，防止已禁用 Key 继续服务 |

## 配置项参考

### 完整配置项

| 配置项 | 类型 | 默认值 | 说明 |
|--------|------|--------|------|
| `proxy_listen` | string | `0.0.0.0:11444` | 代理服务监听地址 |
| `admin_listen` | string | `0.0.0.0:8080` | 管理端监听地址 |
| `database.type` | string | `sqlite` | 数据库类型（sqlite/mysql/postgres） |
| `database.dsn` | string | `data/token_factory.db` | 数据库连接字符串 |
| `admin.username` | string | `admin` | 默认管理员用户名（仅首次启动创建） |
| `admin.password` | string | `admin123` | 默认管理员密码 |
| `jwt_secret` | string | `""`（自动生成） | JWT 签名密钥 |
| `encryption_key` | string | **必填** | Base64 编码的 32 字节 AES-256 加密密钥 |
| `cors_origins` | string | `""`（本地开发） | CORS 允许的来源域名（逗号分隔） |
| `call_record_limit` | int | `10` | 内存中保留的最近 API 调用记录条数（最大 20，仅存内存不持久化） |
| `proxy.provider_strategy` | string | `round-robin` | 供应商选择策略 |
| `proxy.session_affinity` | bool | `true` | 启用会话亲和性 |
| `proxy.default_timeouts.total` | int | `300` | 默认总超时（秒） |
| `proxy.default_timeouts.connect` | int | `10` | 默认连接建立超时（秒） |
| `proxy.default_timeouts.first_token` | int | `120` | 默认首 Token 超时（秒） |
| `proxy.default_timeouts.stream_idle` | int | `60` | 默认流 Idle 超时（秒） |
| `proxy.auto_status.enabled` | bool | `true` | 启用自动状态检测 |
| `proxy.auto_status.consecutive_failures` | int | `2` | 连续失败次数阈值 |
| `proxy.auto_status.cooldown_recovery_sec` | int | `300` | 冷却自动恢复时间（秒） |
| `proxy.auto_status.cooldown_check_interval` | int | `60` | 冷却恢复检查间隔（秒） |

### 环境变量优先级

当同时存在环境变量和配置文件设置时，环境变量优先：

| 环境变量 | 覆盖配置项 |
|----------|-----------|
| `JWT_SECRET` | `jwt_secret` |
| `ENCRYPTION_KEY` | `encryption_key` |
| `ADMIN_PASSWORD` | `admin.password` |
| `CORS_ORIGINS` | `cors_origins` |

## 许可证

MIT

设计+监督：[hootoovv](https://github.com/hootoovv)
代码实现：[GLM5.1 Agent](https://chat.z.ai/)