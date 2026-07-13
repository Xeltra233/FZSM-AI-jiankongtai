# 启动说明（Go-only）

## 1. 准备 cookie
把业务 cookie 放到 `auth/cookies.json`，通常包含 `fz_lottery`。

也可打开面板后，在 **控制 → Cookie 管理** 中：
- 直接粘贴 cookie 原值
- 或粘贴 `name=value` / JSON
- 再点「导入并探测」

## 2. 启动
```bat
start_bot.bat
```
Dashboard: http://127.0.0.1:8787/

## 3. 管理登录（服务器建议开启）
```bat
setx FZSM_ADMIN_PASSWORD "你的管理密码"
```
重启 dashboard 后：
1. 打开面板会先进入登录页
2. 用户名：`admin`
3. 输入密码后才能进入监控面板

本机不设密码时默认开放，方便调试。

## 4. 服务器挂载（重要）
服务器 / Docker 不要纠结“挂哪个盘符”，挂这些路径：

### 必挂
- `./auth` → `/app/auth`
- `./data` → `/app/data`
- `./config.yaml` → `/app/config.yaml`

### 建议挂
- `./logs` → `/app/logs`
- `./web` → `/app/web`

### 环境变量
- `FZSM_ADMIN_PASSWORD=你的管理密码`

详细说明见 `README.md` 的「服务器部署：要挂载哪些目录」。

## 5. 体检
```bat
bin\fzsm-doctor.exe -c config.yaml
```

## 6. 停止
```bat
stop_bot.bat
```

## 7. 常用命令
```bat
bin\fzsm-bot.exe -c config.yaml -primary -mode live -every 18
bin\fzsm-dashboard.exe -c config.yaml -port 8787
bin\fzsm-bot.exe -c config.yaml --once -mode paper
bin\fzsm-doctor.exe --map
```

## 8. Cookie 管理入口
控制页 → Cookie 管理：
- 导入 / 探测 / 清除 / 脱敏查看
- 共用文件：`auth/cookies.json`
- 服务器部署请设置 `FZSM_ADMIN_PASSWORD`
- 详见 `docs/COOKIE_MANAGEMENT.md`
