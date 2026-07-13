# Cookie 管理与服务器部署

## 能力概览
- **整页登录门禁**：未登录只能看到登录页，登录成功后才进入监控面板
- Cookie 管理：状态 / 脱敏列表 / 导入 / 探测 / 清除
- 导入支持：JSON、`name=value`、**直接粘贴 cookie 原值**

数据流：
`浏览器登录 → session → Go dashboard API → auth/cookies.json → bot/keepalive 共用`

## 登录系统（方案 B）
### 环境变量
- optional `FZSM_ADMIN_USERNAME` (default `admin`)
- required on server: `FZSM_ADMIN_PASSWORD`
- fallback: `FZSM_ADMIN_TOKEN` as password if password unset

Windows：
```bat
setx FZSM_ADMIN_USERNAME "admin"
setx FZSM_ADMIN_PASSWORD "your-password"
```
然后重启 dashboard。

### 行为
1. 打开 `http://127.0.0.1:8787/`
2. 若配置了密码：
   - 先显示「FZSM 管理登录」整页
   - 用户名：`admin`
   - 输入密码登录
   - 登录成功后才显示监控面板
3. 未配置密码（本机开发）：
   - `auth_mode=open_local`
   - 自动进入面板

### API 保护
未登录时，以下接口返回 401：
- `/api/overview`
- `/api/control`
- `/api/feature-flags`
- `/api/auth/cookies*`

公开接口：
- `/`
- `/api/health`
- `/api/admin/auth/status`
- `/api/admin/login`
- `/api/admin/logout`

Session：
- Cookie 名：`fzsm_admin_session`
- HttpOnly
- 有效期 12 小时

## Cookie 导入格式
1. **直接粘贴原值（推荐）**
```
eyJkaXNjb3JkX2lkIjoi....完整值
```
默认写入 `name=fz_lottery, domain=fanzisima.xyz, path=/`

2. name=value
```
fz_lottery=完整值
```

3. JSON
```json
[{"name":"fz_lottery","value":"完整值","domain":"fanzisima.xyz","path":"/"}]
```

## 服务器部署建议
1. 必须设置 `FZSM_ADMIN_PASSWORD`
2. 不要裸奔公网（内网/反代+TLS）
3. git 忽略：
   - `auth/cookies.json`
   - `auth/cookies.backup.*.json`
4. 管理登录只保护控制台；业务 cookie 仍是远端账号登录态

## 注意
- 直接粘贴时请贴完整原值，不要贴脱敏显示值
- 清除会备份，但仍可能导致 bot 掉登录
- 本项目 Go-only
