# 赚钱模型（Profit-Max Math）

原则：**每个功能区都要回答“为什么这动作能赚钱/避损”**，禁止纯点击自动化。

统一符号：
- `E[·]` 期望
- `fee` 手续费/滑点
- `r_opp` 现金机会成本
- `score/EV` 决策分数，`<=0` 默认不做

---

## 1) 行情 / 加密 / 订单（现货）
技术分 `score ∈ [-1,1]` 映射期望收益：

\[
E[r]=f(score, ATR\%, ret5)
\]

\[
net\_edge=E[r]-2\cdot fee
\]

仓位：

\[
pct=\min(max\_pos,\ \max(base\cdot score,\ fractional\_kelly(net\_edge, risk)))
\]

规则：
- `net_edge <= 0` → 不买
- 卖出仍走止损/ROI/移动止损

实现：`src/econ.py` `analyze_trade_signal`，`src/strategy.py`，`src/risk.py`

## 2) 持仓风控
目标不是多交易，而是提高**净值路径期望**：
- hard stop：`pnl <= -sl`
- ROI ladder：持有时间越长，止盈阈值下降
- trailing stop：利润超过 offset 后锁盈
- regime：risk_off/crash 降低仓位/禁开新仓/强制减仓

实现：`src/risk.py` `src/regime.py` `src/trader.py`

## 3) 农场
作物原始时产：

\[
raw = yield / grow\_sec \times 3600
\]

偷菜风险折现：

\[
E[y]=yield\cdot(1-p_{steal}),\quad EV_h=E[y]/grow\_sec\times3600
\]

- `p_steal`：作物吸引力先验 + feed 动态强度
- 在线 bot 用成熟暴露时长（默认 ~25s）估计损失概率
- 选 `EV_h` 最大作物（当前常见最优：lobster）

实现：`src/farm.py` `src/econ.py`

## 4) IPO
\[
fill=\begin{cases}1,& progress\le 1\\ 1/progress,& progress>1\end{cases}
\]

\[
EV=amount\cdot fill\cdot R_{list}-amount\cdot r_{opp}\cdot hours_{lock}
\]

- `R_list` 先验默认 12%，有上市样本后 shrink 校准
- 只在 score/EV>阈值且预算允许时认购
- payload：`{stock_id, amount}`

实现：`src/side_hustle.py` `src/econ.py`

## 5) 券商 / 承销（已接入 modules.brokers）
目标函数：

\[
EV=E[underwrite\_reward]+E[allocation\_value]-cost-risk\_haircut
\]

实现要点：
- `modules.brokers`：me/list/candidates/like/register/underwriter 扫描
- 天价注册（异常 deposit）默认拒绝
- `brokers.auto_like` 可自动点赞；`auto_underwrite` 默认 false
- 仅当 list 中有可决定项目且净 EV>0 时才考虑 `decide`

## 6) 对赌
有模型概率 `p`、小数赔率 `o` 时：

\[
EV=p\cdot(o-1)\cdot stake-(1-p)\cdot stake
\]

无可靠 `p` 时：
- 使用保守 `assumed_edge < 0`
- 默认扫描 open bets，但无正 EV 不接单（不是未实现）
- 不允许“看热闹就 accept”

实现骨架：`analyze_bet`

## 7) 基金
\[
EV=amount\cdot r_{30d}-perf\_fee-mgmt\_fee
\]

- `r_30d<=0` 或净 EV<=0 → 不申购
- 赎回：预期前景转负或机会成本更高时退出

## 8) 期货 / 保证金
基差：

\[
basis=(F-U)/U
\]

临近到期收敛权重 `conv(tte)`：

\[
E[move]=-basis\cdot conv(tte)
\]

\[
net=E[move]-2fee
\]

杠杆：

\[
edge_L=net\cdot L-extra\_fee,\quad net_{eq}=edge_L-liq\_penalty
\]

- 实现完整：分析 + executable plan + 可选开平仓路径
- 默认 `trade_enabled=false`（只出计划不自动开杠杆）
- `trade_enabled=true` 才允许小额试点
- 基差单位必须用分数，不能把 -19.5% 当成 -1950%

实现：`src/derivatives.py` `src/econ.py`

## 9) 动态 / 新闻 / 农场 feed
不是直接下单模块，而是**提高其他模块 EV 的信息输入**：
- 新闻情绪进入策略分数
- farm feed 修正 `p_steal`
- 事件冲击进入 regime

## 10) 日历（已接入 modules.calendar）
事件前：
- 降仓 / 提 reserve / 提高开仓阈值
事件后：
- 按方向偏置 score 或允许均值回归

实现要点：
- `modules.calendar` 采集 events/news（calendar 端点缺失时回退）
- 输出 `bias.calendar`（情绪/事件偏置）
- 目标：提高风险调整后收益，不是多交易

## 11) 排行榜
用途：
- 拥挤交易降权
- 复制“可解释且可检验”的高收益行为特征（不是盲跟单）
- 偷菜目标优先级

## 12) 荣誉
- 扫描可完成且有奖励的条目
- 有领取 API 才自动领
- 没收益就只记录状态，不刷无意义动作

## 13) 治理
仅自动**响应型**正 EV：
- 例如 tender accept、ESOP exercise（当折价/条款期望为正）
发起类（私有化/诉讼/并购）默认高门槛或人工确认配置项。

## 14) Admin
- 无赚钱目标
- 只做权限探测与安全拒绝
- 结果必须写日志/状态：`authorized=false` / `forbidden`
- 绝不静默忽略

## 15) 组合资金分配
现金竞争模块：交易 / IPO / 杠杆。  
农场不占现金。

\[
spendable=cash-equity\cdot reserve\%
\]

按正 EV 权重分配到：
- `trade_budget`
- `ipo_budget`
- `margin_budget`

regime=risk_off/crash 时提高现金储备。

实现：`allocate_capital`

---

## 决策总开关哲学
1. **能算 EV 的自动执行**  
2. **算不清的用保守先验，默认少做**  
3. **但代码与文档必须覆盖全部功能区，不能漏**  
4. 所有 skip 都要有原因：`non_positive_ev` / `disabled` / `no_permission` / `throttle`


## 20) 抽奖站全局模型（api.fanzisima.xyz）

入口：`https://api.fanzisima.xyz/lottery/page`

### 20.1 签到 / 免费抽奖
- `POST /lottery/api/checkin`：成本 0，优先执行
- `POST /lottery/api/draw` / `draw-premium`：
  - 若 `draws_available>0` 且单次边际成本为 0 → 默认抽完免费次数
  - 付费抽：需要奖池分布；未知分布时不盲抽

### 20.2 VIP 贵宾房
- `GET /lottery/api/vip/state` 提供 `min_balance`、`base_bets`、房间列表
- 仅当 `balance >= min_balance` 且策略 edge>0 时 `join/bet`
- 默认高门槛：当前账户远低于 1e8 时只观察不进房

### 20.3 借贷 / 存款 / 放贷
- 借：`EV = E[use_return] - interest - penalty`
- 存：`EV = deposit_yield - best_alt_yield(stocks/farm/ipo)`
- 放贷挂单：`EV = interest * (1-default_p) - capital_lock_opp`

### 20.4 破产保护
- 仅当继续经营的期望净值 < 破产重置后的期望净值时触发

### 20.5 全局优先级（赚钱自动化调度）
1. 免费正 EV（签到、免费抽次数）
2. 农场 EV（不占现金）
3. 现货/加密正 EV
4. IPO 正 EV
5. 存款/放贷（低风险资金利用）
6. VIP/高波动玩法（高门槛）
7. 高风险杠杆/对赌（最低优先级）
