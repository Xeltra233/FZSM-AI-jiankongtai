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
   setx FZSM_ADMIN_PASSWORD "你的管理密码"
   ```
2. 重启 `fzsm-dashboard.exe`
3. 打开 `http://127.0.0.1:8787/`
4. 用户名：`admin`
5. 输入密码登录后，才能进入监控面板

说明：
- 未配置密码时，本机默认 `open_local`，可直接进面板（方便开发）
- 上服务器必须配置 `FZSM_ADMIN_PASSWORD`
- 兼容：若未设密码，可回退使用 `FZSM_ADMIN_TOKEN` 作为密码
- 详情见：`docs/COOKIE_MANAGEMENT.md`

## Cookie 管理

业务 cookie 文件：`auth/cookies.json`（通常含 `fz_lottery`）

控制页支持：
- 状态查看 / 脱敏列表
- 导入 / 探测 / 清除
- 导入格式：
  1. **直接粘贴 cookie 原值**
  2. `name=value`
  3. JSON 数组/对象

Bot 与 Dashboard 共用同一 cookie 文件；保活会探测 stocks/lottery 登录态。

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
| `./web` | `/app/web` | 前端面板页面 |

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
      FZSM_ADMIN_PASSWORD: "你的管理密码"
    volumes:
      - ./auth:/app/auth
      - ./data:/app/data
      - ./logs:/app/logs
      - ./config:/app/config
      - ./web:/app/web
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

再加 `logs`、`web` 更稳。

## 配置

主要看 `config/config.yaml`：

- `mode: live|paper`
- `dashboard.port: 8787`
- `cookie_file: auth/cookies.json`
- `auth.keepalive_*`
- `lottery.*` / `farm.*` / `risk.*` / `regime.*`

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
bin/                 可执行文件
go/                  Go 源码
web/dashboard.html   前端面板
data/bot.db          状态/成交等
auth/cookies.json    业务 cookie
config/               配置目录（内含 config.yaml）
docs/                文档
```



## Docker 部署（推荐，Zeabur 也用这个）

本项目是 **Go Docker 服务**，不是 Node。

### 关键文件
- `Dockerfile`
- `docker-compose.yml`
- `scripts/start-server.sh`

### 本地 / 服务器 Docker Compose
```bat
set FZSM_ADMIN_PASSWORD=你的管理密码
docker compose up -d --build
```
打开：`http://服务器IP:8787/`  
用户名：`admin`  
密码：`FZSM_ADMIN_PASSWORD`

### 必挂目录
| 宿主机 | 容器 |
|---|---|
| `./auth` | `/app/auth` |
| `./data` | `/app/data` |
| `./config` | `/app/config` |
| `./logs` | `/app/logs`（建议） |
| `./web` | `/app/web`（建议） |

### Zeabur 设置
> 重要：如果你挂载了空的 `config` 卷，启动脚本会自动从镜像默认配置复制 `config/config.yaml`。  
> 也可以不挂 `config`，直接使用镜像内置配置。

1. **构建方式：Dockerfile**（不要选 Node）
2. 端口：`8787`
3. 环境变量：
   - `FZSM_ADMIN_PASSWORD=你的管理密码`
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
   - 建议 `web` → `/app/web`

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
