# Token Factory - 企业级 LLM API 代理中心

Token Factory 是一个企业级的大语言模型（LLM）API 代理网关，提供统一的 API 入口，将请求智能转发到多个上游 LLM 供应商（如 OpenAI、Azure、Anthropic 等），并附带完整的 Web 管理界面，支持用户管理、供应商管理、模型映射、流量统计与监控等功能。

## 核心特性

- **统一 API 代理**：兼容 OpenAI `/v1/chat/completions` 和 Ollama `/api/tags` 接口规范，现有客户端无需修改即可接入
- **多供应商负载均衡**：支持顺序优先（Sequential）、轮询（Round-Robin）、随机（Random）三种供应商选择策略
- **会话亲和性**：同一用户对同一模型的请求优先路由到上次成功的供应商，提升上游 Prompt 缓存命中率
- **自动故障转移**：当首选供应商不可用时，自动重试其他供应商（最多尝试 3 个）
- **流量监控与统计**：按月分表记录流量数据，提供模型排行、供应商排行、用户用量等多维度统计
- **API Key 管理**：支持为每个用户生成和管理多个 API Key，`tk-` 前缀格式
- **Web 管理界面**：基于 Vue 3 + Element Plus 构建的管理后台，含仪表盘、用户管理、供应商管理、模型管理等功能
- **多数据库支持**：支持 SQLite（默认）、MySQL、PostgreSQL 三种数据库后端
- **JWT 认证**：基于 JWT 的用户认证与角色权限控制（管理员/普通用户）
- **内存缓存**：热数据常驻内存，数据变更后自动重载，代理层零数据库查询

## 系统架构

```
┌──────────────┐     ┌──────────────────────────────────────────────┐
│   客户端      │────▶│  代理服务器 (Proxy Server) :11444           │
│  (OpenAI SDK │     │  - API Key 验证                              │
│   / Ollama)  │     │  - 模型解析 & 供应商选择                     │
└──────────────┘     │  - 请求转发 & 故障转移                       │
                     │  - 流量记录                                   │
                     └──────────────┬───────────────────────────────┘
                                    │
                     ┌──────────────▼───────────────────────────────┐
                     │  管理服务器 (Admin Server) :8080              │
                     │  - RESTful API (/api/*)                      │
                     │  - Web 管理界面 (SPA)                        │
                     │  - JWT 认证 & 权限控制                       │
                     └──────────────┬───────────────────────────────┘
                                    │
                     ┌──────────────▼───────────────────────────────┐
                     │  数据层                                       │
                     │  - 内存缓存 (Cache)                           │
                     │  - 数据库 (SQLite / MySQL / PostgreSQL)       │
                     │  - 按月分表流量记录                            │
                     └──────────────────────────────────────────────┘
```

## 项目结构

```
token_factory/
├── config.yaml                # 配置文件
├── server/                    # 后端 (Go)
│   ├── main.go                # 入口文件，初始化并启动服务
│   ├── go.mod                 # Go 模块定义
│   ├── go.sum                 # Go 依赖锁定
│   ├── admin/
│   │   └── admin.go           # 管理端 HTTP 服务器 & API 路由
│   ├── cache/
│   │   └── cache.go           # 内存缓存，供应商选择策略实现
│   ├── config/
│   │   └── config.go          # 配置文件加载与解析
│   ├── database/
│   │   └── database.go        # 数据库初始化、模型定义、迁移
│   ├── middleware/
│   │   └── auth.go            # JWT 认证 & 管理员权限中间件
│   ├── proxy/
│   │   └── proxy.go           # API 代理转发服务器核心逻辑
│   ├── traffic/
│   │   └── traffic.go         # 流量记录器（批量写入）& 统计查询
│   └── web/
│       └── dist/              # 前端构建产物（embed 嵌入）
├── web/                       # 前端 (Vue 3)
│   ├── index.html
│   ├── package.json
│   ├── vite.config.ts
│   ├── tsconfig.json
│   └── src/
│       ├── main.ts            # Vue 应用入口
│       ├── App.vue            # 根组件
│       ├── api/
│       │   └── index.ts       # Axios API 封装
│       ├── router/
│       │   └── index.ts       # 路由配置 & 导航守卫
│       ├── stores/
│       │   └── user.ts        # Pinia 用户状态管理
│       ├── components/
│       │   └── Layout.vue     # 全局布局组件（顶栏导航）
│       └── views/
│           ├── Dashboard.vue  # 公开仪表盘
│           ├── Login.vue      # 登录页
│           ├── admin/         # 管理员页面
│           │   ├── Layout.vue
│           │   ├── Users.vue
│           │   ├── Providers.vue
│           │   ├── Models.vue
│           │   └── Stats.vue
│           └── user/          # 普通用户页面
│               ├── Layout.vue
│               ├── ApiKeys.vue
│               ├── Usage.vue
│               └── MyStats.vue
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
| embed | 前端静态资源嵌入二进制 |

### 前端

| 技术 | 用途 |
|------|------|
| Vue 3 | 前端框架 |
| TypeScript | 类型安全 |
| Vite 5 | 构建工具 |
| Element Plus | UI 组件库 |
| Pinia | 状态管理 |
| Vue Router | 路由管理 |
| Axios | HTTP 请求 |
| ECharts | 数据可视化图表 |
| Tailwind CSS | 原子化 CSS |

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

# 代理策略配置
proxy:
  provider_strategy: "round-robin"          # sequential / round-robin / random
  session_affinity: true                     # 启用会话亲和性
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
[缓存] 数据加载完成
[流量] 记录器已启动
[代理] 服务器启动在 0.0.0.0:11444 (策略: round-robin, 亲和性: true)
[管理] 服务器启动在 0.0.0.0:8080
```

### 4. 访问

- **Web 管理界面**：http://localhost:8080
- **API 代理端点**：http://localhost:11444
- 默认管理员：`admin` / `admin123`

### 5. 构建Docker镜像

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

## 使用指南

### 添加供应商

1. 登录管理后台，进入 **管理后台 → 供应商管理**
2. 点击 **新增供应商**，填写：
   - 名称（如 `openai-us`）
   - Base URL（如 `https://api.openai.com`）
   - API Key（供应商的 API 密钥）
   - 超时时间（默认 30 秒）
   - 重试次数（默认 3 次）

### 添加模型

1. 进入 **管理后台 → 模型管理**
2. 点击 **新增模型**，填写模型名称和描述（如 `gpt-4o`）

### 配置模型-供应商映射

1. 进入 **管理后台 → 模型管理**，为模型关联供应商
2. 指定供应商侧的模型名称（如供应商使用 `gpt-4o-2024-05-13` 而你的模型名为 `gpt-4o`）

### 创建 API Key

1. 登录后进入 **个人中心 → API Keys**
2. 点击 **创建 API Key**，系统自动生成 `tk-` 前缀的密钥
3. 使用该 Key 替代供应商原始 API Key 发起请求

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
| POST | `/api/login` | 用户登录 |
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
| GET | `/api/api-keys` | 列出 API Key |
| POST | `/api/api-keys` | 创建 API Key |
| DELETE | `/api/api-keys/:id` | 删除 API Key |
| GET | `/api/usage` | 使用记录 |
| GET | `/api/usage/stats` | 使用统计 |
| PUT | `/api/password` | 修改密码 |

#### 管理员接口（需管理员权限）

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/api/users` | 列出用户 |
| POST | `/api/users` | 创建用户 |
| PUT | `/api/users/:id` | 更新用户 |
| DELETE | `/api/users/:id` | 删除用户 |
| GET | `/api/providers` | 列出供应商 |
| POST | `/api/providers` | 创建供应商 |
| PUT | `/api/providers/:id` | 更新供应商 |
| DELETE | `/api/providers/:id` | 删除供应商 |
| GET | `/api/models` | 列出模型 |
| POST | `/api/models` | 创建模型 |
| PUT | `/api/models/:id` | 更新模型 |
| DELETE | `/api/models/:id` | 删除模型 |
| GET | `/api/model-providers` | 列出模型-供应商映射 |
| POST | `/api/model-providers` | 创建映射 |
| DELETE | `/api/model-providers/:id` | 删除映射 |
| GET | `/api/stats/overview` | 管理员统计概览 |
| POST | `/api/cache/reload` | 重载缓存 |

## 供应商选择策略

| 策略 | 说明 |
|------|------|
| `sequential` | 按 ID 顺序选择，始终优先使用第一个可用供应商 |
| `round-robin` | 轮询负载均衡，依次轮流使用各供应商（默认） |
| `random` | 随机负载均衡，Fisher-Yates 洗牌算法随机选择 |

**会话亲和性**：启用后，同一用户对同一模型的请求会优先路由到上次成功的供应商。这在供应商使用 Prompt 缓存时特别有效，可以显著降低延迟和费用。当亲和供应商不可用时，自动降级到其他供应商。

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
| `providers` | 供应商配置 |
| `models` | 模型定义 |
| `model_providers` | 模型-供应商映射关系 |
| `users` | 用户账户 |
| `api_keys` | API 密钥 |
| `traffic_records_YYYYMM` | 流量记录（按月分表） |

流量记录按月自动分表，每张分表包含完整的索引（api_key_id、user_id、model_id、provider_id、created_at），确保查询性能。

## 供应商状态

| 状态 | 说明 |
|------|------|
| `active` | 正常工作中 |
| `cooldown` | 冷却中，暂时不可用 |
| `arrears` | 欠费，不可用 |

仅 `active` 状态的供应商参与请求转发，管理员可手动切换供应商状态。

## 许可证

MIT

设计+监督：[hootoovv](https://www.github.com/hootoovv)   
代码实现：[GLM5.1 Agent](https://chat.z.ai/)
