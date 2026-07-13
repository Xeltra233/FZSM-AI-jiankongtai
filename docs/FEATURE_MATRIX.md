# 功能矩阵（侧栏全覆盖）

> 目标：前端侧栏每一个入口都必须在本地有对应模块、API、赚钱模型、配置开关与验收状态。  
> 命令：`python -m src.bootstrap map` / `python -m src.bootstrap doctor`  
> 真相源：`src/catalog.py` + 模块总线 + `config.yaml`（以 map 为准）

状态说明：
- `active`：已接入主循环/总线，可自动执行（受 EV/风控/配置门控）
- `active(可配)`：实现完整；高风险写操作需显式打开配置才执行
- `probe_only`：admin 等权限敏感项，只探测并明确记录，不得静默忽略、永不写
- `info`：信息采集/偏置输入，直接交易价值弱，但必须进总线与面板
- ~~`planned` / `partial` / `ready_disabled` / `analyze_only`~~：历史过渡态；**实现完成后不得再把已落地模块标成未实现**

| # | 前端路由 | 名称 | 关键 API | 本地模块 | 配置开关 | 赚钱模型（摘要） | 当前状态 | 备注 |
|---|---|---|---|---|---|---|---|---|
| 1 | `/stocks/` | 行情/总览 | `/market` `/klines` `/stock-detail` `/orderbook` `/news` `/events` | `collector` `strategy` `bot` `modules.spot` | `loop.*` `strategy.*` `universe.*` | 技术分→`net_edge`，Kelly/EV 仓位 | active | 主循环交易 |
| 2 | `/stocks/portfolio` | 持仓 | `/portfolio` `/portfolio/risk` `/portfolio/performance` `/me` | `trader` `risk` `dashboard` | `risk.*` `regime.*` | 止损/ROI ladder/移动止损/危机减仓 | active | |
| 3 | `/stocks/orders` | 订单 | `/orders` `/my-orders` `/buy` `/sell` `/trades/preview` | `trader` `client` | `mode` `risk.max_*` | 仅 `net_edge>0` 下单 | active | |
| 4 | `/stocks/ipo` | IPO | `/invest/ipos` `/invest/subscribe` `/invest/offerings` `/ipo/my` | `side_hustle` `econ` `modules.side_hustle` | `side_hustle.ipo.*` | `EV=amount*fill*R_list - amount*r_opp*hours` | active | |
| 5 | `/stocks/brokers` | 券商/承销 | `/broker/me` `/list` `/candidates` `/like` `/register` `/underwriter/*` | `modules.brokers` | `brokers.*` | like/选举/承销净 EV；天价注册默认拒绝 | active | `auto_underwrite` 默认 false |
| 6 | `/stocks/bets` | 对赌 | `/bet/list` `/bet/create` `/bet/accept` `/bet/cancel` | `side_hustle` `econ` | `side_hustle.bets.*` | 仅正 EV 接单；无可靠概率用负先验 | active | 默认扫描，负 EV 不接 |
| 7 | `/stocks/crypto` | 加密 | `/crypto` + 现货交易 API | `collector` `strategy` `trader` | `universe.asset_types` | 同现货 EV/Kelly | active | |
| 8 | `/stocks/futures` | 期货/保证金 | `/futures` `/margin/open` `/margin/close` `/margin/positions` | `derivatives` `econ` `modules.derivatives` | `derivatives.*` | 基差回归 EV + 强平惩罚；输出 executable plan | active(可配) | `trade_enabled=false` 默认不自动开杠杆 |
| 9 | `/stocks/funds` | 基金 | `/funds/list` `/funds/me` `/funds/subscribe` `/funds/redeem` | `side_hustle` `econ` | `side_hustle.funds.*` | `EV=amount*r30d - fees` | active | 仅净 EV>0 申购 |
|10 | `/stocks/feed` | 动态 | `/events` `/news` `/farm/feed` | `collector` `farm` `modules.calendar` | `farm.*` `strategy.news_weight` | 新闻/偷菜强度输入，抬其他模块 EV | active | |
|11 | `/stocks/calendar/` | 日历 | `/calendar` `/events` `/news` | `modules.calendar` | `calendar.*` | 事件/情绪偏置 `enter_boost/risk_off` | active | calendar 端点缺失时回退 events/news |
|12 | `/stocks/leaderboard` | 排行榜 | `/leaderboard` `/farm/rankings` | `modules.leaderboard` | `leaderboard.*` | 相对排名调节进攻系数 | active | info+bias |
|13 | `/stocks/honors` | 荣誉 | `/honors/me` + catalog | `modules.honors` | `honors.*` | 近完成成就/VIP 称号路径追踪 | active | 无 claim API 时只记录 |
|14 | `/stocks/governance` | 治理 | `/governance/*` tender/esop/buyback/... | `modules.governance` | `governance.*` | 响应型正 EV；默认不发起 | active(可配) | |
|15 | `/stocks/farm` | 农场 | `/farm/me` `/plant` `/harvest` `/steal` `/targets` `/feed` `/rankings` | `farm` `econ` `modules.farm` | `farm.*` | `E[y]=yield*(1-p_steal)` | active | |
|16 | `/stocks/admin` | 管理后台 | `/admin/*` | `modules.admin` | `admin.enabled` | 无收益；权限探测+拒绝日志 | probe_only | 永不写操作 |
|M1 | `/stocks/meeting` | 股东大会 | `/meeting/*` + 持仓 `last_meeting_at` | `modules.meeting` | `meeting.*` | 持仓相关大会探测与投票 EV 门槛 | active | 公开列表 API 仍弱 |


## 抽奖站全域（https://api.fanzisima.xyz/lottery/page）

> 全局赚钱自动化第二主域。与 stocks 共用账号 cookie（`fz_lottery`）。  
> 本地统一入口：`lottery_client` + `modules.lottery`（不再拆成独立 vip/loan 模块名）。

| # | 入口/模块 | 名称 | 关键 API | 本地模块 | 配置开关 | 赚钱模型 | 当前 | 备注 |
|---|---|---|---|---|---|---|---|---|
| L1 | `/lottery/page` | 抽奖总控 | `GET /lottery/api/me` | `lottery_client` `modules.lottery` | `lottery.enabled` | 免费正 EV 优先调度 | active | |
| L2 | 签到 | 每日签到 | `POST /lottery/api/checkin` | `modules.lottery` | `lottery.auto_checkin` | 成本 0，优先执行 | active | |
| L3 | 补签 | 补签 | `POST /lottery/api/makeup` | `modules.lottery` | `lottery.*` | 仅补签成本 < 期望奖励 | active(可配) | |
| L4 | 普通抽奖 | Draw | `POST /lottery/api/draw` | `modules.lottery` | `lottery.auto_draw_free` | 有 free draws 自动抽 | active | |
| L5 | 高级抽奖 | Premium | `POST /lottery/api/draw-premium` | `modules.lottery` | `lottery.auto_draw_premium_free` | 免费 premium 次数自动抽 | active | |
| L6 | 老虎机 | Slot | slot config + spin | `modules.lottery` | `lottery.auto_slot` | 高波动默认关 | active(可配) | 默认 false |
| L7 | YOLO | 骰子 | `POST /lottery/api/yolo` | `modules.lottery` | `lottery.auto_yolo` | 高波动默认关 | active(可配) | 默认 false |
| L8 | 奶龙 | Nailong | `POST /lottery/api/nailong` | `modules.lottery` | `lottery.auto_nailong` | 高波动默认关 | active(可配) | 默认 false |
| L9 | VIP 贵宾房 | VIP Rooms | `/vip/state|rooms|join|ready|start|bet|leave` | `modules.lottery` | `lottery.auto_vip*` | min_balance 门槛 + 房间 EV | active(可配) | 默认不进房/不下注 |
|L10 | VIP 统计 | VIP Stats | `/vip/stats|history|leaderboard` | `modules.lottery` | `lottery.*` | 校准胜率/费用 | active | 分析输出 |
|L11 | 借贷 | Loan | `/loan` `/loan/offers` `/loan/repay` | `modules.lottery` | `lottery.auto_borrow_zero_rate` | 利率/用途 EV；高息不借 | active(可配) | 默认可扫描不自动借 |
|L12 | 放贷挂单 | Offers | `/offers` `/offers/mine` | `modules.lottery` | `lottery.*` | 利率覆盖违约与机会成本 | active(可配) | |
|L13 | 存款 | Deposit | `/deposit` `/rollover` `/withdraw` | `modules.lottery` | `lottery.auto_deposit` | 存款收益 vs 机会成本 | active(可配) | 默认 false |
|L14 | 破产 | Bankruptcy | `POST /lottery/api/bankruptcy` | `modules.lottery` | `lottery.auto_bankruptcy` | 仅破产保护 EV 更优时 | active(可配) | 默认 false |
|L15 | 抽奖榜 | Leaderboard | `/lottery/api/leaderboard` | `modules.lottery` | info | 参考，不直接下单 | active | info |
|L16 | 抽奖 Admin | Slot Admin | `/lottery/api/admin/slot/*` | probe | probe_only | 无收益；权限探测 | probe_only | 无写 |

当前 live 行为（示例/已验证）：
- 免费抽奖次数可自动抽到 0；签到可自动完成
- VIP `min_balance` 不足时 `can_enter=false` 正确 skip
- loan offers 可扫描；零利率默认也不自动借（需开关）
- admin/stocks 与 lottery 鉴权分离记录

## 覆盖承诺
1. **16/16 侧栏 + meeting + lottery 全域**都必须出现在文档、catalog、doctor、dashboard 状态中。
2. 不允许“代码里没做又在文档里装做完”；也不允许“代码已做文档仍写 planned”。
3. 默认关闭高风险开关 ≠ 未实现；状态用 `active(可配)` 表达。
4. 每个模块赚钱公式详见 `docs/PROFIT_MODELS.md`。
5. 空白启动与登录详见 `docs/STARTUP.md`。

## 主循环接入关系（当前架构）
```
service start
  ├─ bot loop
  │   ├─ market/crypto/spot trade (EV)
  │   ├─ farm
  │   ├─ side_hustle: ipo/bets/funds
  │   ├─ modules bus:
  │   │   spot/farm/lottery/side/brokers/derivatives/
  │   │   calendar/leaderboard/honors/meeting/governance/admin
  │   └─ storage: modules.* / bias.*
  └─ dashboard: 总览 + 全模块页
```

## 验收清单
- [x] 侧栏 16 项 + meeting + lottery 全部列出
- [x] 每项都有可运行代码路径（admin=probe_only）
- [x] doctor/map 可检查（map: 22 active + 1 probe_only）
- [x] dashboard 全模块可见（`/api/overview` + 全模块页签）
- [x] goal-1 功能实现验收：`docs/ACCEPTANCE.md`
- [ ] goal-2 文档漂移消除 + dashboard 滚动背景/滚动条修复（进行中）

## 实现状态同步（goal-2 Task2）
- 已按 `python -m src.bootstrap map` 重写表体，去掉错误的 planned/partial/待建。
- brokers/calendar/leaderboard/honors/governance/meeting/lottery 均为 active。
- futures/VIP/赌博类/借贷存款等保留默认安全门控，状态记为 active(可配)。
- admin = probe_only。
- 完整矩阵命令：`python -m src.bootstrap map`
