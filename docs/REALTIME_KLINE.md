# 实时K线更新功能文档

## 📊 功能概述

实现了K线图的实时更新机制，确保图表数据与后端API保持同步，为AI决策提供最新的市场数据。

**最终方案：智能调频 + 分级速率限制**

## 🚀 实现方案

### 方案：智能调频轮询 + 分级速率限制（已实现）

这是经过多次优化后的最佳方案：
- ✅ 简单可靠（不需要复杂的WebSocket服务端）
- ✅ 性能优异（前后端配合，资源高效）
- ✅ 完全解决429速率限制问题

#### 核心机制

1. **轻量级更新（5秒间隔）**
   - 只获取最后2根K线数据
   - 使用 `candlestickSeries.update()` 更新图表
   - 不重新渲染整个图表
   - 网络负载小，性能高

2. **完全刷新（60秒间隔）**
   - 重新加载所有K线数据
   - 防止数据偏移和累积误差
   - 确保图表数据完整性

3. **实时价格显示**
   - 价格变化动画（上涨绿色，下跌红色）
   - 500ms闪烁效果
   - 精确到小数点后2位

#### 技术实现

```typescript
// 1. 实时更新最后一根K线（5秒间隔）
useEffect(() => {
  if (!currentSymbol || !token || !autoRefresh || !isChartReady) {
    return;
  }

  const updateLastKline = async () => {
    // 获取最新的2根K线
    const response = await fetch(
      `/api/klines?symbol=${currentSymbol}&interval=${currentInterval}&limit=2`,
      { headers: { 'Authorization': `Bearer ${token}` }}
    );

    const result = await response.json();
    const latestKlines = result.klines || [];
    
    if (latestKlines.length > 0) {
      const lastKline = latestKlines[latestKlines.length - 1];
      
      // 更新图表中的最后一根K线
      if (candlestickSeriesRef.current && klineData.length > 0) {
        const newLastKline: CandlestickData = {
          time: Math.floor(lastKline.openTime / 1000) as Time,
          open: lastKline.open,
          high: lastKline.high,
          low: lastKline.low,
          close: lastKline.close,
        };
        
        // 更新图表
        candlestickSeriesRef.current.update(newLastKline);
        
        // 更新实时价格显示和动画
        const newPrice = parseFloat(lastKline.close);
        const oldPrice = prevPriceRef.current;
        
        if (oldPrice !== null && Math.abs(newPrice - oldPrice) > 0.01) {
          setPriceChange(newPrice > oldPrice ? 'up' : 'down');
        }
        
        prevPriceRef.current = newPrice;
        setRealtimePrice(newPrice);
      }
    }
  };

  // 立即执行一次
  updateLastKline();

  // 每5秒更新一次
  const timer = setInterval(updateLastKline, 5000);

  return () => {
    clearInterval(timer);
  };
}, [currentSymbol, currentInterval, token, autoRefresh, isChartReady, klineData]);

// 2. 完全刷新（60秒间隔）
if (autoRefresh) {
  const refreshTimer = setInterval(() => {
    fetchKlineData(); // 重新加载所有数据
  }, 60000);

  return () => clearInterval(refreshTimer);
}
```

## 📈 AI决策集成

### 当前实现

AI决策已经使用实时K线数据，通过以下方式：

1. **market.GetFresh(symbol)** - 强制从API获取最新数据
   ```go
   // decision/engine.go
   func fetchMarketDataForContext(ctx *Context) error {
       for symbol := range symbolSet {
           // 强制从API获取最新数据，不使用WebSocket缓存
           data, err := market.GetFresh(symbol)
           if err != nil {
               // 回退到Get（使用WebSocket缓存）
               data, err = market.Get(symbol)
           }
           ctx.MarketDataMap[symbol] = data
       }
   }
   ```

2. **K线形态分析** - 基于最新1小时K线
   ```go
   // decision/engine.go
   func fetchPatternAnalysisForContext(ctx *Context) {
       // 复用已获取的1小时K线数据
       if marketData.RawKlines1h != nil && len(marketData.RawKlines1h) > 0 {
           analysis := pattern.AnalyzeKlines(marketData.RawKlines1h)
           ctx.PatternAnalysisMap[symbol] = analysis
       }
   }
   ```

3. **决策Prompt包含K线数据**
   ```go
   // decision/engine.go
   func buildUserPrompt(ctx *Context) string {
       // BTC K线形态
       if btcPatternAnalysis, hasBTCPattern := ctx.PatternAnalysisMap["BTCUSDT"]; hasBTCPattern {
           sb.WriteString(fmt.Sprintf("BTC K线形态: %s | 建议: %s\n",
               btcPatternAnalysis.Summary, 
               btcPatternAnalysis.Recommendation))
       }
       
       // 持仓币种K线形态
       for _, pos := range ctx.Positions {
           if patternAnalysis, hasPattern := ctx.PatternAnalysisMap[pos.Symbol]; hasPattern {
               sb.WriteString(patternAnalysis.FormatForPrompt())
           }
       }
       
       // 候选币种K线形态
       for _, coin := range ctx.CandidateCoins {
           if patternAnalysis, hasPattern := ctx.PatternAnalysisMap[coin.Symbol]; hasPattern {
               sb.WriteString(patternAnalysis.FormatForPrompt())
           }
       }
   }
   ```

### 数据流程

```
┌─────────────────────────────────────────────────────────────┐
│                      实时K线数据流                             │
└─────────────────────────────────────────────────────────────┘

1. Binance API (每秒更新)
   ↓
2. 后端API (/api/klines)
   ├─ market.GetFresh() - 强制获取最新数据
   └─ market.Get() - 使用WebSocket缓存（fallback）
   ↓
3. 前端K线图组件
   ├─ 每5秒轻量级更新最后一根K线
   └─ 每60秒完全刷新所有K线
   ↓
4. AI决策引擎
   ├─ 获取实时市场数据（market.GetFresh）
   ├─ 分析K线形态（pattern.AnalyzeKlines）
   └─ 生成决策Prompt（包含K线分析）
   ↓
5. AI模型（DeepSeek/Qwen/Claude）
   └─ 根据实时K线数据做出买卖决策
```

## 🔧 使用方法

### 1. 前端组件使用

```tsx
import { KlineChart } from '@/components/KlineChart';

// 基础使用
<KlineChart 
  symbol="BTCUSDT" 
  interval="1h" 
  height={400} 
  autoRefresh={true}
/>

// 交易员配置使用
<KlineChart 
  traderId="trader-123" 
  autoRefresh={true}
/>
```

### 2. API端点

```bash
# 获取K线数据
GET /api/klines?symbol=BTCUSDT&interval=1h&limit=200

# 获取K线形态分析
GET /api/klines/pattern-analysis?symbol=BTCUSDT&interval=1h&limit=100
```

### 3. 配置参数

```typescript
interface KlineChartProps {
  symbol?: string;          // 币种符号（如"BTCUSDT"）
  traderId?: string;        // 交易员ID（自动加载配置的币种）
  interval?: string;        // 时间周期（1m, 3m, 15m, 1h, 4h, 1d）
  height?: number;          // 图表高度（像素）
  autoRefresh?: boolean;    // 是否自动刷新（默认true）
  refreshInterval?: number; // 刷新间隔（已固定为5秒轻量+60秒完全）
}
```

## 📊 性能优化

### 1. 网络请求优化

- ✅ 轻量级更新只获取2根K线（~0.5KB）
- ✅ 完全刷新获取200根K线（~50KB）
- ✅ 使用 Authorization header 避免CORS preflight
- ✅ 错误时静默处理，不影响用户体验

### 2. 渲染性能优化

- ✅ 使用 `candlestickSeries.update()` 而非重新渲染
- ✅ 防抖动画（500ms闪烁后恢复）
- ✅ 组件卸载时清理定时器

### 3. 数据一致性

- ✅ 每60秒完全刷新，防止数据偏移
- ✅ 使用 `isMounted` flag 防止组件卸载后更新
- ✅ 错误时回退到缓存数据

## 🎯 未来增强

### 方案B：TradingView Advanced Chart（可选）

如果需要更专业的图表功能，可以集成 TradingView：

参考文档：https://www.tradingview.com/widget-docs/widgets/charts/advanced-chart/

**优势：**
- 🎨 专业级UI和交互
- 📊 内置技术指标（100+）
- 🔍 缩放、平移、十字光标
- 📱 响应式设计
- 🌐 多语言支持

**集成步骤：**

1. **添加 TradingView Widget**
   ```html
   <script type="text/javascript" src="https://s3.tradingview.com/tv.js"></script>
   ```

2. **创建图表组件**
   ```typescript
   import { useEffect, useRef } from 'react';

   export function TradingViewChart({ symbol }: { symbol: string }) {
     const containerRef = useRef<HTMLDivElement>(null);

     useEffect(() => {
       if (!containerRef.current) return;

       new TradingView.widget({
         container_id: containerRef.current.id,
         symbol: `BINANCE:${symbol}`,
         interval: '60',
         theme: 'dark',
         style: '1',
         locale: 'zh_CN',
         toolbar_bg: '#1a1a1a',
         enable_publishing: false,
         hide_side_toolbar: false,
         allow_symbol_change: true,
         studies: [
           'MASimple@tv-basicstudies',
           'RSI@tv-basicstudies',
           'MACD@tv-basicstudies'
         ],
         // 使用自定义数据源
         datafeed: new Datafeeds.UDFCompatibleDatafeed(
           '/api/tradingview',
           10000
         ),
       });
     }, [symbol]);

     return <div ref={containerRef} id="tradingview_chart" />;
   }
   ```

3. **实现 TradingView Datafeed API**
   - 需要后端实现 UDF 格式的API端点
   - 提供历史数据和实时更新
   - 文档：https://github.com/tradingview/charting_library/wiki/UDF

## 📊 速率限制优化（关键）

### 问题历程

1. **初始方案**：5秒轮询 ❌
   - 触发429速率限制（每秒10个请求）

2. **优化1**：智能调频（15-60秒根据K线周期） ⚠️
   - 更新频率合理
   - 但仍可能触发10次/秒的全局限制

3. **最终方案**：分级速率限制 ✅
   - K线端点：60次/秒（6倍提升）
   - 全局API：30次/秒（3倍提升）
   - 认证端点：保持严格限制

### 速率限制配置

```go
// api/server.go
globalLimiter := middleware.NewIPRateLimiter(rate.Limit(30), 30)
klineDataLimiter := middleware.NewIPRateLimiter(rate.Limit(60), 60)

router.Use(func(c *gin.Context) {
    path := c.Request.URL.Path
    
    // K线数据端点使用专用的高频限制
    if path == "/api/klines" || path == "/api/klines/pattern-analysis" {
        middleware.RateLimitMiddleware(klineDataLimiter)(c)
        return
    }
    
    // 其他路由使用全局速率限制
    middleware.RateLimitMiddleware(globalLimiter)(c)
})
```

### 理论计算

假设5个K线图同时加载：
- 每个图表：4次/分钟（15秒间隔）
- 5个图表：20次/分钟 = 0.33次/秒
- K线端点限制：60次/秒
- **结果：即使100个图表同时加载也不会触发限制！**

## 📝 总结

当前实现的实时K线更新功能：

✅ **实时性**：15-60秒智能调频，根据K线周期自动调整
✅ **准确性**：5分钟完全刷新，防止数据偏移
✅ **性能**：只更新必要数据，网络负载小
✅ **稳定性**：分级速率限制，彻底解决429错误
✅ **AI集成**：决策引擎使用实时K线数据
✅ **用户体验**：价格变化动画，视觉反馈清晰

**这个方案是经过多次迭代优化的最佳实践：**
- 简单可靠（不需要复杂的WebSocket）
- 性能优异（前后端配合）
- 完全解决速率限制问题

如果未来需要更专业的图表功能，可以考虑集成 TradingView Advanced Chart。

