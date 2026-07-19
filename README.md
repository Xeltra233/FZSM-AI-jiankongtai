# fzsm 炒股 Bot（Go-only）

本项目主路径为 **Go**。

- `bin/fzsm-bot.exe`：主循环 + 交易/模块执行 + cookie 保活
- `bin/fzsm-dashboard.exe`：监控面板，默认 `http://127.0.0.1:8787/`
- `bin/fzsm-doctor.exe`：健康检查 / 路由映射

Python `src/` 已移除，不再维护。

## 快速启动

```bat
start_bot.bat
```

手动启动：

```bat
bin\fzsm-bot.exe -c config/config.yaml -primary -mode live -every 18
bin\fzsm-dashboard.exe -c config/config.yaml -host 127.0.0.1 -port 8787 -html web\dashboard.html
```

停止：

```bat
stop_bot.bat
```

状态 / 体检：

```bat
status_bot.bat
bin\fzsm-doctor.exe -c config/config.yaml
bin\fzsm-doctor.exe --map
```

## 管理登录（整页门禁）

Dashboard 支持专门登录页：

1. 配置环境变量：
 ```bat
 setx FZSM_ADMIN_USERNAME "admin"
 setx FZSM_ADMIN_PASSWORD "your-password"
 ```
2. 重启 `fzsm-dashboard.exe`
3. 打开 `http://127.0.0.1:8787/`
4. 用户名：`admin`
5. 输入密码登录后，才能进入监控面板

说明：
- 未配置密码时，本机默认 `open_local`，可直接进面板（方便开发）
- optional `FZSM_ADMIN_USERNAME` (default `admin`)
- required on server: `FZSM_ADMIN_PASSWORD`
- fallback: `FZSM_ADMIN_TOKEN` as password if password unset
- 详情见：`docs/COOKIE_MANAGEMENT.md`

## Cookie 管理

业务 cookie 文件：`auth/cookies.json`（通常包含 `fz_lottery`）

### 从浏览器拿 cookie

1. 用浏览器打开并登录目标站：`https://fanzisima.xyz`
2. 确认你已经登录成功（能看到自己的账户/资产页）
3. 按 `F12` 打开开发者工具
4. 切到 **Application / 应用程序**（有的浏览器叫“存储”）
5. 左侧打开 **Cookies**
6. 点选站点：`https://fanzisima.xyz` 或 `https://api.fanzisima.xyz`
7. 在列表里找到 `fz_lottery`
8. 双击 **Value / 值**，全选复制完整原值
9. 注意：
   - 必须复制**完整原值**
   - 不要复制脱敏后的值（带 `***` / 打码的那种）
   - 不要只复制一半

### 导入到本项目

#### 方式 A：面板导入（推荐）
1. 打开监控面板：`http://127.0.0.1:8787/`
2. 进入 **控制 → Cookie 管理**
3. 把刚才复制的 cookie 原值粘贴进去
4. 点 **导入并探测**
5. 看到探测成功 / 已登录，即可

#### 方式 B：直接写文件
把 cookie 写到 `auth/cookies.json`，例如：

```json
[
  {
    "name": "fz_lottery",
    "value": "这里粘贴完整cookie原值",
    "domain": "fanzisima.xyz",
    "path": "/"
  }
]
```

### 面板支持的导入格式

1. **直接粘贴 cookie 原值**（最省事）
```text
eyJkaXNjb3JkX2lkIjoi....完整值
```
默认写入：`name=fz_lottery`，`domain=fanzisima.xyz`，`path=/`

2. `name=value`
```text
fz_lottery=完整值
```

3. JSON
```json
[
  {
    "name": "fz_lottery",
    "value": "完整值",
    "domain": "fanzisima.xyz",
    "path": "/"
  }
]
```

### 说明

- Bot 与 Dashboard 共用同一 cookie 文件
- Cookie 保活是自动的：导入后 bot 会定期探测并回写 cookie
- 不需要 Python 续登；失效时只需重新导入浏览器 cookie
- cookie 保活会定期探测 stocks / lottery 登录态
- 失效后重新按上面步骤从浏览器复制并导入
- `auth/cookies.json` 是密钥材料，不要提交到 git
- 更细说明见：`docs/COOKIE_MANAGEMENT.md`

- 更细的 Cookie 安全与轮换说明见 `docs/COOKIE_MANAGEMENT.md`

### Cookie 保活怎么处理（解决方案）

**结论：一般只需要导入一个 `fz_lottery`，不要故意导很多个。**

#### 状态含义
- **正常**：股市 + 抽奖探测都通过，自动保活中
- **部分有效**：至少一侧还能登录（例如抽奖通、股市暂不通）。bot 仍可继续跑可用模块
- **失效**：两侧都失败，需要重新导入 cookie

#### 推荐操作
1. 浏览器登录 `https://fanzisima.xyz`
2. `F12` → Application → Cookies → 复制 `fz_lottery` 完整值
3. 面板 **控制 → Cookie 管理** → 粘贴 → **导入并探测**
4. 看到“探测通过”或“部分有效”即可

#### 多次导入 / 换行
- **可以多次导入**：默认是**合并**，同名 cookie 会被新值覆盖
- **可以换行**：支持
  - 多行 `name=value`
  - `a=b; c=d`
  - JSON 数组
- **裸值多行**：每行会当成 `fz_lottery`，最终同名只保留最后一次
- 若要整文件覆盖，用接口参数 `replace=1`（面板默认合并）

#### 不需要
- 不需要 Python 续登
- 不需要因为“部分有效”就导入一堆无关 cookie


## 服务器部署：要挂载哪些目录

服务器 / Docker / 面板部署时，**不是挂系统盘符**，而是挂项目持久化目录。

### 必挂（持久化）

| 宿主机路径 | 容器/服务内路径 | 作用 |
|---|---|---|
| `./auth` | `/app/auth` | 业务 cookie（`cookies.json`） |
| `./data` | `/app/data` | SQLite 状态库（`bot.db`）与服务状态 |
| `./config` | `/app/config` | 运行配置 |

### 建议挂

| 宿主机路径 | 容器/服务内路径 | 作用 |
|---|---|---|
| `./logs` | `/app/logs` | 运行日志 |

### 不建议挂 / 不要提交

- `auth/cookies.json` 内容（密钥材料，勿进 git）
- `auth/cookies.backup.*.json`
- `data/*.db`
- `logs/`
- 管理密码明文

### Docker 示例

```yaml
services:
 fzsm:
 image: your-image
 working_dir: /app
 ports:
 - "8787:8787"
 environment:
 FZSM_ADMIN_USERNAME: "admin"
 FZSM_ADMIN_PASSWORD: "your-password"
 volumes:
 - ./auth:/app/auth
 - ./data:/app/data
 - ./logs:/app/logs
 - ./config:/app/config
```

### 服务器启动前检查

1. 已挂载 `auth`、`data`、`config`
2. 已设置 `FZSM_ADMIN_PASSWORD`
3. `auth/cookies.json` 已放入有效业务 cookie（或登录面板后导入）
4. 端口 `8787` 已放行（或走反代）
5. Dashboard 不要裸奔公网，建议内网 / TLS 反代

### 一句话

服务器挂载重点只有三样：

1. `auth`（登录 cookie）
2. `data`（状态数据库）
3. `config/`（配置目录）

再加 `logs` 更稳。前端 `web/` 已打进镜像，**不要挂 web**。

## 配置

主要看 `config/config.yaml`：

- `mode: live|paper`
- `dashboard.port: 8787`
- `cookie_file: auth/cookies.json`
- `auth.keepalive_*`
- `lottery.*` / `farm.*` / `risk.*` / `regime.*`

### 赚钱速度与风险开关

- **全部持仓**：面板选择 `all_in` 后，系统会在可成交、正净期望和组合上限内尽量部署资金。默认保留约 2% 最小现金缓冲；单标的最多占组合 30%，每轮最多新增 3 个机会，单笔不超过 1,000 万。
- **杠杆**：`derivatives.enabled: true` 负责分析；只有用户显式打开 `derivatives.trade_enabled` 才会新增杠杆仓。最大 3 倍、最多 2 仓，保证金同时受现金和权益各 5% 以及共享资金池约束。
- **付费高级抽**：`lottery.auto_draw_premium_paid` 默认关闭。只有当前活动版本的真实样本达到门槛，净收益置信下界为正且获得共享预算时才执行。
- **老虎机/YOLO/VIP 下注**：当前证据为负期望，算法硬阻断或分配零预算；打开 UI 开关也不会绕过净期望门槛。
- **跨模块资金池**：杠杆、付费高级抽等占现金能力统一按风险调整后的单位时间净收益分配，达到单机会容量后会把剩余预算继续分给其他正期望机会。

完整公式、配置口径、验证结果与回滚方法见 [`docs/PROFIT_OPTIMIZATION.md`](docs/PROFIT_OPTIMIZATION.md)。

## 面板功能

Dashboard 主要包含：

- 总览 / 持仓 / 信号 / 成交
- 控制：交易模式、资金风格、农场与功能开关
- 模块 / 资金 / 赚钱 / 信息
- Cookie 管理与保活状态

状态持久化到 SQLite：`data/bot.db` 的 `runtime_state`。

## 编译

```bat
go -C go build -o ..\bin\fzsm-bot.exe .\cmd\bot
go -C go build -o ..\bin\fzsm-dashboard.exe .\cmd\dashboard
go -C go build -o ..\bin\fzsm-doctor.exe .\cmd\doctor
go -C go test ./...
```

## 目录

```text
bin/ 可执行文件
go/ Go 源码
web/dashboard.html 前端面板
data/bot.db 状态/成交等
auth/cookies.json 业务 cookie
config/ 配置目录（内含 config.yaml）
docs/ 文档
```



## Docker 部署

本项目是 **Go Docker 服务**，不是 Node。

### 关键文件
- `Dockerfile`
- `docker-compose.yml`
- `scripts/start-server.sh`

### 本地 / 服务器 Docker Compose
```bat
set FZSM_ADMIN_USERNAME=admin
set FZSM_ADMIN_PASSWORD=your-password
docker compose up -d --build
```
打开：`http://服务器IP:8787/`（对外监听前必须设置高强度管理密码；未配置密码时 API 仅接受实际本机来源）
用户名：`admin` 
密码：`FZSM_ADMIN_PASSWORD`

### 必挂目录
| 宿主机 | 容器 |
|---|---|
| `./auth` | `/app/auth` |
| `./data` | `/app/data` |
| `./config` | `/app/config` |
| `./logs` | `/app/logs`（建议） |

### 设置
> 重要：如果你挂载了空的 `config` 卷，启动脚本会自动从镜像默认配置复制 `config/config.yaml`。 
> 也可以不挂 `config`，直接使用镜像内置配置。

1. **构建方式：Dockerfile**（不要选 Node）
2. 端口：`8787`
3. 环境变量：
 - `FZSM_ADMIN_USERNAME=admin`
 - `FZSM_ADMIN_PASSWORD=your-password`
 - `HOST=0.0.0.0`
 - `PORT=8787`（必须是数字；不要填 `${WEB_PORT}` 这种未展开字符串）
- 也可设 `WEB_PORT=8787`（脚本会识别）
 - `ENABLE_BOT=1`
 - `BOT_MODE=live`
 - `BOT_EVERY=18`
4. 挂载：
 - `auth` → `/app/auth`
 - `data` → `/app/data`
 - `config` → `/app/config`
 - 建议 `logs` → `/app/logs`
 - **不要挂** `web`：前端在镜像内，不是持久数据


### 说明
- 根目录已移除 `package.json`，避免再被识别成 Node 去找 `/src/index.js`
- 本地 Playwright 工具在 `tools/playwright/`
- 容器启动命令：`/app/scripts/start-server.sh`



## 日志自动清理

默认策略（代码内置）：
- 单文件超过 **50MB** 自动轮转
- 同名轮转文件最多保留 **7** 个
- 超过 **7 天** 的旧日志自动删除
- 启动时清理一次，之后每小时再清理
- 日志目录：`logs/`（Docker 挂载 `./logs:/app/logs`）

文件：
- `logs/bot.log`
- `logs/dashboard.log`
- Docker 额外可能有 `logs/bot.out.log` / `logs/bot.err.log`（启动脚本重定向）

可用环境变量（Docker 启动脚本）：
- `LOG_MAX_AGE_DAYS=7`

## 备注

- 赚钱相关：农场 / 抽奖 / 侧线 / 券商 / VIP 等模块会按开关与接口可用性运行
- paper 示例：`bin\fzsm-bot.exe -c config/config.yaml -mode paper --once`
- primary 实例会写 `runtime_state.service`；同机建议只跑一个 primary
- 服务器部署安全说明：`docs/COOKIE_MANAGEMENT.md`
- 启动说明：`docs/STARTUP.md`
