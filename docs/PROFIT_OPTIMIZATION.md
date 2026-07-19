# 收益优化、配置与回滚说明

本文记录当前 Go 实现的资金部署、杠杆、抽奖净期望和跨模块资金分配口径。所有收益判断均以可实现的**净期望**为准，不以毛收入或点击次数代替收益。

## 1. 全部持仓为什么仍会保留现金

`all_in` 的含义是在正净期望、可成交和风险边界允许时尽量投入，而不是机械达到 100% 仓位。

当前执行逻辑：

1. 对候选计算净边际收益，非正值不买。
2. 允许在持仓数量达到上限后继续补足已有正期望仓位。
3. 单笔额度按权益动态计算，并接受服务端 preview 的实际数量上限。
4. 数量超限时最多缩量重试 3 次；失败不占用成功名额，继续尝试后续候选。
5. 组合保留约 2% 最小现金缓冲；单标的最多占 30%，每轮最多新增 3 个机会，单笔硬上限 1,000 万。

云端数据库快照的 30 周期离线回放中，旧逻辑期末现金率为 90.83%，新逻辑为 2.00%，额外部署约 1.7332 亿。真实成交仍受当时流动性、服务端限额和正期望候选数量影响。

关键配置位于 `risk.*`：

- `all_in_max_positions`
- `all_in_max_new_entries_per_cycle`
- `all_in_max_notional_pct`
- `all_in_max_notional_per_order`
- `all_in_max_single_position_pct`
- `buy_limit_retry_*`

实现：`go/internal/risk`、`go/internal/trader`。

## 2. 杠杆链路

接口链路已覆盖合约读取、持仓读取、开仓和平仓。分析与下单分为两个开关：

- `derivatives.enabled: true`：读取合约并产生计划。
- `derivatives.trade_enabled: true`：允许新增杠杆仓；默认关闭，需在控制页主动开启。

计划使用基差收敛净收益：

```text
basis = (future_price - spot_price) / spot_price
net_edge = expected_convergence - round_trip_fee - slippage - liquidation_penalty
```

只有 `net_edge > 0` 且通过以下边界才可开仓：最大 3 倍、单笔名义价值不超过 500 万、最多 2 仓、保证金不超过现金和权益各 5%、强平缓冲至少 18%。关闭新增开仓后，保护性平仓仍保持工作。

实现：`go/internal/modules/derivatives.go`。

## 3. 抽奖真实净期望

普通抽和高级抽使用实际执行结果记录净余额变化，并按活动配置版本隔离样本。滚动统计由以下配置控制：

- `draw_rolling_samples`
- `draw_confidence_z`
- `paid_premium_min_samples`
- `paid_premium_min_net_ev`

付费高级抽必须同时满足：用户主动开启、当前版本样本充分、毛收益置信下界扣除入场费后仍为正、共享资金池分配足够预算。

官方老虎机配置计算得到 RTP 约 74.3336%，单次净期望约 -256,664，因此当前为硬阻断。YOLO 与 VIP 下注的现有理论期望同样为负，获得零预算。

实现：`go/internal/modules/lottery.go`、`lottery_ev.go`、`slot_edge.go`。

## 4. 跨模块资金分配

占用现金的副模块共享资金池：

```text
pool = min(cash × 5%, equity × 5%, 10,000,000)
score = (net_ev / capital) / duration_hours × success_probability × confidence
```

只给正分数机会分配预算。分配采用带单机会容量上限的比例 water-filling：某个机会达到自身上限后，剩余预算会继续分配给其他正期望机会，避免闲置。

连续执行失败 3 次会冷却 15 分钟，之后以 10% 预算探测恢复。免费抽、签到、农场等零资金机会不被该资金池限流。

实现：`go/internal/modules/capital_allocator.go`。

## 5. 验证命令

在仓库根目录运行：

```powershell
go -C go test ./...
go -C go build ./...
go -C go vet ./...
go -C go test -race ./...
go -C go mod verify
python scripts/analyze_cloud_db.py fzsm_bot_20260719_235256.db
python scripts/replay_all_in.py fzsm_bot_20260719_235256.db
python scripts/replay_cross_module_allocator.py fzsm_bot_20260719_235256.db
python scripts/security_audit.py
```

数据库脚本以只读方式打开快照，不会修改原文件。

## 6. 回滚

优先通过配置即时降级：

1. 关闭 `derivatives.trade_enabled`，停止新增杠杆仓；保护性平仓继续运行。
2. 关闭 `lottery.auto_draw_premium_paid` 及其他高波动开关。
3. 将资金风格切回 `balanced`，恢复常规仓位和现金储备。
4. 如需代码级回滚，按主题回退对应提交：
   - `a9b0c49`：全部持仓资金部署
   - `772f5ba`：杠杆链路
   - `0c4288f`：抽奖净期望门控
   - `c8c6e19`：跨模块资金分配
   - `2dbf1de`：资金再分配、抽奖统计与面板补齐

回滚前保留 `data/` 与 `auth/` 的受控备份；数据库、Cookie、日志和管理凭证不得进入 Git。
