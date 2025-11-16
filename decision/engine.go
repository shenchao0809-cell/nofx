package decision

import (
	"encoding/json"
	"fmt"
	"log"
	"math"
	"nofx/logger"
	"nofx/market"
	"nofx/mcp"
	"nofx/pool"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// 预编译正则表达式（性能优化：避免每次调用时重新编译）
var (
	// ✅ 安全的正則：精確匹配 ```json 代碼塊
	// 使用反引號 + 拼接避免轉義問題
	reJSONFence      = regexp.MustCompile(`(?is)` + "```json\\s*(\\[\\s*\\{.*?\\}\\s*\\])\\s*```")
	reJSONArray      = regexp.MustCompile(`(?is)\[\s*\{.*?\}\s*\]`)
	reArrayHead      = regexp.MustCompile(`^\[\s*\{`)
	reArrayOpenSpace = regexp.MustCompile(`^\[\s+\{`)
	reInvisibleRunes = regexp.MustCompile("[\u200B\u200C\u200D\uFEFF]")

	// 新增：XML标签提取（支持思维链中包含任何字符）
	reReasoningTag = regexp.MustCompile(`(?s)<reasoning>(.*?)</reasoning>`)
	reDecisionTag  = regexp.MustCompile(`(?s)<decision>(.*?)</decision>`)
)

// PositionInfo 持仓信息
type PositionInfo struct {
	Symbol           string  `json:"symbol"`
	Side             string  `json:"side"` // "long" or "short"
	EntryPrice       float64 `json:"entry_price"`
	MarkPrice        float64 `json:"mark_price"`
	Quantity         float64 `json:"quantity"`
	Leverage         int     `json:"leverage"`
	UnrealizedPnL    float64 `json:"unrealized_pnl"`
	UnrealizedPnLPct float64 `json:"unrealized_pnl_pct"`
	PeakPnLPct       float64 `json:"peak_pnl_pct"` // 历史最高收益率（百分比）
	LiquidationPrice float64 `json:"liquidation_price"`
	MarginUsed       float64 `json:"margin_used"`
	UpdateTime       int64   `json:"update_time"`           // 持仓更新时间戳（毫秒）
	StopLoss         float64 `json:"stop_loss,omitempty"`   // 止损价格（用于推断平仓原因）
	TakeProfit       float64 `json:"take_profit,omitempty"` // 止盈价格（用于推断平仓原因）
}

// OpenOrderInfo represents an open order for AI decision context
type OpenOrderInfo struct {
	Symbol       string  `json:"symbol"`        // Trading pair
	OrderID      int64   `json:"order_id"`      // Order ID
	Type         string  `json:"type"`          // Order type: STOP_MARKET, TAKE_PROFIT_MARKET, LIMIT, MARKET
	Side         string  `json:"side"`          // Order side: BUY, SELL
	PositionSide string  `json:"position_side"` // Position side: LONG, SHORT, BOTH
	Quantity     float64 `json:"quantity"`      // Order quantity
	Price        float64 `json:"price"`         // Limit order price (for limit orders)
	StopPrice    float64 `json:"stop_price"`    // Trigger price (for stop-loss/take-profit orders)
}

// AccountInfo 账户信息
type AccountInfo struct {
	TotalEquity      float64 `json:"total_equity"`      // 账户净值
	AvailableBalance float64 `json:"available_balance"` // 可用余额
	UnrealizedPnL    float64 `json:"unrealized_pnl"`    // 未实现盈亏
	TotalPnL         float64 `json:"total_pnl"`         // 总盈亏
	TotalPnLPct      float64 `json:"total_pnl_pct"`     // 总盈亏百分比
	MarginUsed       float64 `json:"margin_used"`       // 已用保证金
	MarginUsedPct    float64 `json:"margin_used_pct"`   // 保证金使用率
	PositionCount    int     `json:"position_count"`    // 持仓数量
}

// CandidateCoin 候选币种（来自币种池）
type CandidateCoin struct {
	Symbol  string   `json:"symbol"`
	Sources []string `json:"sources"` // 来源: "ai500" 和/或 "oi_top"
}

// OITopData 持仓量增长Top数据（用于AI决策参考）
type OITopData struct {
	Rank              int     // OI Top排名
	OIDeltaPercent    float64 // 持仓量变化百分比（1小时）
	OIDeltaValue      float64 // 持仓量变化价值
	PriceDeltaPercent float64 // 价格变化百分比
	NetLong           float64 // 净多仓
	NetShort          float64 // 净空仓
}

// Context 交易上下文（传递给AI的完整信息）
type Context struct {
	CurrentTime        string                      `json:"current_time"`
	RuntimeMinutes     int                         `json:"runtime_minutes"`
	CallCount          int                         `json:"call_count"`
	Account            AccountInfo                 `json:"account"`
	Positions          []PositionInfo              `json:"positions"`
	OpenOrders         []OpenOrderInfo             `json:"open_orders"` // List of open orders for AI context
	CandidateCoins     []CandidateCoin             `json:"candidate_coins"`
	MarketDataMap      map[string]*market.Data                `json:"-"` // 不序列化，但内部使用
	OITopDataMap       map[string]*OITopData                   `json:"-"` // OI Top数据映射
	Performance        interface{}                            `json:"-"` // 历史表现分析（logger.PerformanceAnalysis，包含 RecentTrades）
	MarketSummary      *MarketSummary                          `json:"-"` // 全局市场状态
	PatternAnalysisMap map[string]*PatternAnalysis             `json:"-"` // K线形态分析映射 (symbol -> analysis) [已废弃，保留兼容性]
	MultiTimeframeAnalysisMap map[string]map[string]*PatternAnalysis `json:"-"` // 多时间周期K线形态分析映射 (symbol -> interval -> analysis)
	Timeframes         []string                                `json:"-"` // 配置的时间周期列表（例如：["1m", "3m", "1h", "4h", "1d"]）
	BTCETHLeverage     int                                     `json:"-"` // BTC/ETH杠杆倍数（从配置读取）
	AltcoinLeverage    int                                     `json:"-"` // 山寨币杠杆倍数（从配置读取）
	TakerFeeRate       float64                                 `json:"-"` // Taker fee rate (from config, default 0.0004)
	MakerFeeRate       float64                                 `json:"-"` // Maker fee rate (from config, default 0.0002)
}

// Decision AI的交易决策
type Decision struct {
	Symbol string `json:"symbol"`
	Action string `json:"action"` // "open_long", "open_short", "close_long", "close_short", "update_stop_loss", "update_take_profit", "partial_close", "hold", "wait"

	// 开仓参数
	Leverage        int     `json:"leverage,omitempty"`
	PositionSizeUSD float64 `json:"position_size_usd,omitempty"`
	StopLoss        float64 `json:"stop_loss,omitempty"`
	TakeProfit      float64 `json:"take_profit,omitempty"`

	// 调整参数（新增）
	NewStopLoss     float64 `json:"new_stop_loss,omitempty"`    // 用于 update_stop_loss
	NewTakeProfit   float64 `json:"new_take_profit,omitempty"`  // 用于 update_take_profit
	ClosePercentage float64 `json:"close_percentage,omitempty"` // 用于 partial_close (0-100)

	// 通用参数
	Confidence int     `json:"confidence,omitempty"` // 信心度 (0-100)
	RiskUSD    float64 `json:"risk_usd,omitempty"`   // 最大美元风险
	Reasoning  string  `json:"reasoning"`
}

// FullDecision AI的完整决策（包含思维链）
type FullDecision struct {
	SystemPrompt string     `json:"system_prompt"` // 系统提示词（发送给AI的系统prompt）
	UserPrompt   string     `json:"user_prompt"`   // 发送给AI的输入prompt
	CoTTrace     string     `json:"cot_trace"`     // 思维链分析（AI输出）
	Decisions    []Decision `json:"decisions"`     // 具体决策列表
	Timestamp    time.Time  `json:"timestamp"`
	// AIRequestDurationMs 记录 AI API 调用耗时（毫秒）方便排查延迟问题
	AIRequestDurationMs int64 `json:"ai_request_duration_ms,omitempty"`
}

// GetFullDecision 获取AI的完整交易决策（批量分析所有币种和持仓）
func GetFullDecision(ctx *Context, mcpClient mcp.AIClient) (*FullDecision, error) {
	return GetFullDecisionWithCustomPrompt(ctx, mcpClient, "", false, "")
}

// GetFullDecisionWithCustomPrompt 获取AI的完整交易决策（支持自定义prompt和模板选择）
func GetFullDecisionWithCustomPrompt(ctx *Context, mcpClient mcp.AIClient, customPrompt string, overrideBase bool, templateName string) (*FullDecision, error) {
	// 1. 为所有币种获取最新市场数据（确保使用最新数据）
	log.Printf("📊 [决策] 开始获取最新市场数据...")
	if err := fetchMarketDataForContext(ctx); err != nil {
		return nil, fmt.Errorf("获取市场数据失败: %w", err)
	}
	
	// 记录BTC当前价格（用于确认数据是最新的）
	if btcData, hasBTC := ctx.MarketDataMap["BTCUSDT"]; hasBTC {
		log.Printf("📊 [决策] BTC当前价格: %.2f (1h: %+.2f%%, 4h: %+.2f%%) | MACD: %.4f | RSI: %.2f",
			btcData.CurrentPrice, btcData.PriceChange1h, btcData.PriceChange4h,
			btcData.CurrentMACD, btcData.CurrentRSI7)
	}
	
	ctx.MarketSummary = analyzeMarketSummary(ctx)
	
	// 2. 获取K线形态分析（异步，不阻塞主流程）
	fetchPatternAnalysisForContext(ctx)

	// 2. 构建 System Prompt（固定规则）和 User Prompt（动态数据）
	systemPrompt := buildSystemPromptWithCustom(ctx.Account.TotalEquity, ctx.BTCETHLeverage, ctx.AltcoinLeverage, customPrompt, overrideBase, templateName)
	userPrompt := buildUserPrompt(ctx)

	// 3. 调用AI API（使用 system + user prompt）
	aiCallStart := time.Now()
	aiResponse, err := mcpClient.CallWithMessages(systemPrompt, userPrompt)
	aiCallDuration := time.Since(aiCallStart)
	if err != nil {
		return nil, fmt.Errorf("调用AI API失败: %w", err)
	}

	// 4. 解析AI响应
	decision, err := parseFullDecisionResponse(aiResponse, ctx.Account.TotalEquity, ctx.BTCETHLeverage, ctx.AltcoinLeverage)

	// 无论是否有错误，都要保存 SystemPrompt 和 UserPrompt（用于调试和决策未执行后的问题定位）
	if decision != nil {
		decision.Timestamp = time.Now()
		decision.SystemPrompt = systemPrompt // 保存系统prompt
		decision.UserPrompt = userPrompt     // 保存输入prompt
		decision.AIRequestDurationMs = aiCallDuration.Milliseconds()
	}

	if err != nil {
		return decision, fmt.Errorf("解析AI响应失败: %w", err)
	}

	decision.Timestamp = time.Now()
	decision.SystemPrompt = systemPrompt // 保存系统prompt
	decision.UserPrompt = userPrompt     // 保存输入prompt
	return decision, nil
}

// fetchPatternAnalysisForContext 为上下文中的币种获取K线形态分析
// ⚡ 关键修复：使用 MarketDataMap 中已获取的K线数据，确保与价格数据同步
func fetchPatternAnalysisForContext(ctx *Context) {
	ctx.PatternAnalysisMap = make(map[string]*PatternAnalysis)
	ctx.MultiTimeframeAnalysisMap = make(map[string]map[string]*PatternAnalysis)

	// 确定要分析的时间周期（从配置获取，如果没有则使用默认值）
	timeframes := ctx.Timeframes
	if len(timeframes) == 0 {
		// 默认时间周期（包含15分钟线）
		timeframes = []string{"15m", "1h", "4h"}
		log.Printf("⚠️ 未配置时间周期，使用默认值: %v", timeframes)
	} else {
		log.Printf("📊 使用配置的时间周期进行分析: %v（共%d个时间周期）", timeframes, len(timeframes))
	}
	
	// 收集需要分析的币种（BTCUSDT + 所有持仓 + 用户选择的币种）
	symbolsToAnalyze := make(map[string]bool)
	
	// 0. 强制分析BTCUSDT（用于市场概览和决策参考）
	symbolsToAnalyze["BTCUSDT"] = true
	
	// 1. 持仓币种
	for _, pos := range ctx.Positions {
		symbolsToAnalyze[pos.Symbol] = true
	}
	
	// 2. 用户选择的币种（只分析用户配置的币种）
	for _, coin := range ctx.CandidateCoins {
		symbolsToAnalyze[coin.Symbol] = true
	}
	
	// 辅助函数：获取币种列表用于日志
	symbolList := make([]string, 0, len(symbolsToAnalyze))
	for symbol := range symbolsToAnalyze {
		symbolList = append(symbolList, symbol)
	}
	log.Printf("📊 需要分析的币种: %v（共%d个币种）", symbolList, len(symbolsToAnalyze))
	
	// 并发分析K线形态（多时间周期）
	var wg sync.WaitGroup
	var mu sync.Mutex
	apiClient := market.NewAPIClient()
	
	for symbol := range symbolsToAnalyze {
		// 为每个币种初始化多时间周期分析map
		mu.Lock()
		ctx.MultiTimeframeAnalysisMap[symbol] = make(map[string]*PatternAnalysis)
		mu.Unlock()

		// 为每个时间周期进行分析
		for _, interval := range timeframes {
		wg.Add(1)
			go func(sym string, tf string) {
			defer wg.Done()
			
				// 获取K线数据
				var klines []market.Kline
				var err error

				// 对于1小时周期，优先使用已获取的数据
				if tf == "1h" {
					if marketData, hasData := ctx.MarketDataMap[sym]; hasData && len(marketData.RawKlines1h) > 0 {
						klines = marketData.RawKlines1h
						log.Printf("✓ [K线形态] %s %s 使用已获取的K线数据（%d根）", sym, tf, len(klines))
					} else {
						klines, err = apiClient.GetKlines(sym, tf, 100)
			if err != nil {
							log.Printf("⚠️ 获取%s %s K线数据失败: %v", sym, tf, err)
							return
						}
					}
				} else {
					// 其他时间周期直接从API获取
					klines, err = apiClient.GetKlines(sym, tf, 100)
					if err != nil {
						log.Printf("⚠️ 获取%s %s K线数据失败: %v", sym, tf, err)
						return
					}
				}

				if len(klines) < 20 {
					log.Printf("⚠️ %s %s K线数据不足（%d根），跳过形态分析", sym, tf, len(klines))
				return
			}
			
			// 进行形态分析
				analysis := AnalyzeKlinePatterns(klines, sym, tf)
			
			// 安全地写入map
			mu.Lock()
				ctx.MultiTimeframeAnalysisMap[sym][tf] = analysis
				// 兼容性：如果是1小时周期，也写入旧的PatternAnalysisMap
				if tf == "1h" {
			ctx.PatternAnalysisMap[sym] = analysis
				}
			mu.Unlock()
			
				log.Printf("✓ %s %s K线形态分析完成: %s", sym, tf, analysis.Summary)
			}(symbol, interval)
		}
	}
	
	// 等待所有分析完成（最多等待5秒，避免阻塞决策）
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	
	select {
	case <-done:
		totalAnalyses := 0
		for _, analyses := range ctx.MultiTimeframeAnalysisMap {
			totalAnalyses += len(analyses)
		}
		log.Printf("✓ 多时间周期K线形态分析完成，共分析%d个币种，%d个时间周期", len(ctx.MultiTimeframeAnalysisMap), totalAnalyses)
		// 详细日志：显示每个币种分析了哪些时间周期
		for symbol, analyses := range ctx.MultiTimeframeAnalysisMap {
			intervals := make([]string, 0, len(analyses))
			for interval := range analyses {
				intervals = append(intervals, interval)
			}
			// 按时间周期排序显示
			sort.Strings(intervals)
			log.Printf("  • %s: %v", symbol, intervals)
		}
	case <-time.After(5 * time.Second):
		totalAnalyses := 0
		for _, analyses := range ctx.MultiTimeframeAnalysisMap {
			totalAnalyses += len(analyses)
		}
		log.Printf("⚠️ K线形态分析超时，已完成%d个币种，%d个时间周期", len(ctx.MultiTimeframeAnalysisMap), totalAnalyses)
		// 详细日志：显示每个币种分析了哪些时间周期
		for symbol, analyses := range ctx.MultiTimeframeAnalysisMap {
			intervals := make([]string, 0, len(analyses))
			for interval := range analyses {
				intervals = append(intervals, interval)
			}
			sort.Strings(intervals)
			log.Printf("  • %s: %v", symbol, intervals)
		}
	}
}

// fetchMarketDataForContext 为上下文中的所有币种获取市场数据和OI数据
func fetchMarketDataForContext(ctx *Context) error {
	ctx.MarketDataMap = make(map[string]*market.Data)
	ctx.OITopDataMap = make(map[string]*OITopData)

	// 收集所有需要获取数据的币种
	symbolSet := make(map[string]bool)

	// 0. 强制获取BTCUSDT数据（用于决策提示词中的市场概览）
	// 无论BTC是否在持仓或候选列表中，都需要获取最新数据
	symbolSet["BTCUSDT"] = true

	// 1. 优先获取持仓币种的数据（这是必须的）
	for _, pos := range ctx.Positions {
		symbolSet[pos.Symbol] = true
	}

	// 2. 候选币种数量根据账户状态动态调整
	maxCandidates := calculateMaxCandidates(ctx)
	for i, coin := range ctx.CandidateCoins {
		if i >= maxCandidates {
			break
		}
		symbolSet[coin.Symbol] = true
	}

	// 并发获取市场数据
	// 持仓币种集合（用于判断是否跳过OI检查）
	positionSymbols := make(map[string]bool)
	for _, pos := range ctx.Positions {
		positionSymbols[pos.Symbol] = true
	}

	for symbol := range symbolSet {
		// ⚡ 关键修复：AI决策时强制从API获取最新数据，不使用WebSocket缓存
		// 确保AI决策基于最新的实时价格
		data, err := market.GetFresh(symbol)
		if err != nil {
			// 如果GetFresh失败，回退到Get（使用WebSocket缓存）
			log.Printf("⚠️  [决策] GetFresh失败，回退到Get: %v", err)
			data, err = market.Get(symbol)
			if err != nil {
				// 单个币种失败不影响整体，只记录错误
				log.Printf("❌ [决策] 获取 %s 市场数据失败: %v", symbol, err)
				continue
			}
		}

		// ⚠️ 流动性过滤：持仓价值低于阈值的币种不做（多空都不做）
		// 持仓价值 = 持仓量 × 当前价格
		// 但现有持仓必须保留（需要决策是否平仓）
		// 💡 OI 門檻配置：用戶可根據風險偏好調整
		const minOIThresholdMillions = 15.0 // 可調整：15M(保守) / 10M(平衡) / 8M(寬鬆) / 5M(激進)

		isExistingPosition := positionSymbols[symbol]
		isBTCUSDT := symbol == "BTCUSDT" // BTCUSDT必须保留，用于市场概览
		
		// 流动性过滤：跳过非持仓且非BTCUSDT的币种
		if !isExistingPosition && !isBTCUSDT && data.OpenInterest != nil && data.CurrentPrice > 0 {
			// 计算持仓价值（USD）= 持仓量 × 当前价格
			oiValue := data.OpenInterest.Latest * data.CurrentPrice
			oiValueInMillions := oiValue / 1_000_000 // 转换为百万美元单位
			if oiValueInMillions < minOIThresholdMillions {
				log.Printf("⚠️  %s 持仓价值过低(%.2fM USD < %.1fM)，跳过此币种 [持仓量:%.0f × 价格:%.4f]",
					symbol, oiValueInMillions, minOIThresholdMillions, data.OpenInterest.Latest, data.CurrentPrice)
				continue
			}
		}

		ctx.MarketDataMap[symbol] = data
	}

	// 加载OI Top数据（不影响主流程）
	oiPositions, err := pool.GetOITopPositions()
	if err == nil {
		for _, pos := range oiPositions {
			// 标准化符号匹配
			symbol := pos.Symbol
			ctx.OITopDataMap[symbol] = &OITopData{
				Rank:              pos.Rank,
				OIDeltaPercent:    pos.OIDeltaPercent,
				OIDeltaValue:      pos.OIDeltaValue,
				PriceDeltaPercent: pos.PriceDeltaPercent,
				NetLong:           pos.NetLong,
				NetShort:          pos.NetShort,
			}
		}
	}

	return nil
}

// calculateMaxCandidates 根据账户状态计算需要分析的候选币种数量
func calculateMaxCandidates(ctx *Context) int {
	// ⚠️ 重要：限制候选币种数量，避免 Prompt 过大
	// 根据持仓数量动态调整：持仓越少，可以分析更多候选币
	const (
		maxCandidatesWhenEmpty    = 30 // 无持仓时最多分析30个候选币
		maxCandidatesWhenHolding1 = 25 // 持仓1个时最多分析25个候选币
		maxCandidatesWhenHolding2 = 20 // 持仓2个时最多分析20个候选币
		maxCandidatesWhenHolding3 = 15 // 持仓3个时最多分析15个候选币（避免 Prompt 过大）
	)

	positionCount := len(ctx.Positions)
	var maxCandidates int

	switch positionCount {
	case 0:
		maxCandidates = maxCandidatesWhenEmpty
	case 1:
		maxCandidates = maxCandidatesWhenHolding1
	case 2:
		maxCandidates = maxCandidatesWhenHolding2
	default: // 3+ 持仓
		maxCandidates = maxCandidatesWhenHolding3
	}

	// 返回实际候选币数量和上限中的较小值
	return min(len(ctx.CandidateCoins), maxCandidates)
}

// buildSystemPromptWithCustom 构建包含自定义内容的 System Prompt
func buildSystemPromptWithCustom(accountEquity float64, btcEthLeverage, altcoinLeverage int, customPrompt string, overrideBase bool, templateName string) string {
	// 如果覆盖基础prompt且有自定义prompt，只使用自定义prompt
	if overrideBase && customPrompt != "" {
		return customPrompt
	}

	// 获取基础prompt（使用指定的模板）
	basePrompt := buildSystemPrompt(accountEquity, btcEthLeverage, altcoinLeverage, templateName)

	// 如果没有自定义prompt，直接返回基础prompt
	if customPrompt == "" {
		return basePrompt
	}

	// 添加自定义prompt部分到基础prompt
	var sb strings.Builder
	sb.WriteString(basePrompt)
	sb.WriteString("\n\n")
	sb.WriteString("# 📌 个性化交易策略\n\n")
	sb.WriteString(customPrompt)
	sb.WriteString("\n\n")
	sb.WriteString("注意: 以上个性化策略是对基础规则的补充，不能违背基础风险控制原则。\n")

	return sb.String()
}

// buildSystemPrompt 构建 System Prompt（使用模板+动态部分）
func buildSystemPrompt(accountEquity float64, btcEthLeverage, altcoinLeverage int, templateName string) string {
	var sb strings.Builder

	// 1. 加载提示词模板（核心交易策略部分）
	if templateName == "" {
		templateName = "default" // 默认使用 default 模板
	}

	template, err := GetPromptTemplate(templateName)
	if err != nil {
		// 如果模板不存在，记录错误并使用 default
		log.Printf("⚠️  提示词模板 '%s' 不存在，使用 default: %v", templateName, err)
		template, err = GetPromptTemplate("default")
		if err != nil {
			// 如果连 default 都不存在，使用内置的简化版本
			log.Printf("❌ 无法加载任何提示词模板，使用内置简化版本")
			sb.WriteString("你是专业的加密货币交易AI。请根据市场数据做出交易决策。\n\n")
		} else {
			sb.WriteString(template.Content)
			sb.WriteString("\n\n")
		}
	} else {
		sb.WriteString(template.Content)
		sb.WriteString("\n\n")
	}

	// 2. 硬约束（风险控制）- 动态生成
	sb.WriteString("# 硬约束（风险控制）\n\n")
	sb.WriteString("1. 风险回报比: 必须 ≥ 1:3（冒1%风险，赚3%+收益）\n")
	sb.WriteString("2. 最多持仓: 3个币种（质量>数量）\n")
	sb.WriteString(fmt.Sprintf("3. 单币仓位: 山寨%.0f-%.0f U | BTC/ETH %.0f-%.0f U\n",
		accountEquity*2.5, accountEquity*5, accountEquity*5, accountEquity*10))
	sb.WriteString(fmt.Sprintf("4. 杠杆限制: **山寨币最大%dx杠杆** | **BTC/ETH最大%dx杠杆** (⚠️ 严格执行，不可超过)\n", altcoinLeverage, btcEthLeverage))
	sb.WriteString("5. 保证金: 总使用率 ≤ 90%\n")
	sb.WriteString("6. **决策稳定性要求（⚠️ 严格执行）**：\n")
	sb.WriteString("   - **开仓置信度必须≥80（建议≥85）**：如果置信度<80，必须选择 `wait` 或 `hold`，不能开仓\n")
	sb.WriteString("   - **风险回报比必须≥3:1**：确保决策质量和稳定性\n")
	sb.WriteString("   - ⚠️ **违反以上要求将导致决策被拒绝，请严格遵守**\n")

	// 7. 开仓金额：根据账户规模动态提示（使用统一的配置规则）
	minBTCETH := calculateMinPositionSize("BTCUSDT", accountEquity)

	// 根据账户规模生成不同的提示语
	var btcEthHint string
	if accountEquity < btcEthSizeRules[1].MinEquity {
		// 小账户模式（< 20U）
		btcEthHint = fmt.Sprintf(" | BTC/ETH≥%.0f USDT (⚠️ 小账户模式，降低门槛)", minBTCETH)
	} else if accountEquity < btcEthSizeRules[2].MinEquity {
		// 中型账户（20-100U）
		btcEthHint = fmt.Sprintf(" | BTC/ETH≥%.0f USDT (根据账户规模动态调整)", minBTCETH)
	} else {
		// 大账户（≥100U）
		btcEthHint = fmt.Sprintf(" | BTC/ETH≥%.0f USDT", minBTCETH)
	}

	sb.WriteString("7. 开仓金额: 山寨币≥12 USDT")
	sb.WriteString(btcEthHint)
	sb.WriteString("\n\n")

	// ⚠️ 重要提醒：防止 AI 误读市场数据中的数字
	sb.WriteString("⚠️ **重要提醒：计算 position_size_usd 的正确方法**\n\n")
	sb.WriteString(fmt.Sprintf("- 当前账户净值：**%.2f USDT**\n", accountEquity))
	sb.WriteString(fmt.Sprintf("- 可用余额：**%.2f USDT**（系统会自动计算）\n", accountEquity*0.9)) // 假设90%可用
	sb.WriteString(fmt.Sprintf("- 山寨币开仓范围：**%.0f - %.0f USDT** (净值的 2.5-5 倍，建议使用中上值)\n", accountEquity*2.5, accountEquity*5))
	sb.WriteString(fmt.Sprintf("- BTC/ETH开仓范围：**%.0f - %.0f USDT** (净值的 5-10 倍，建议使用中上值)\n", accountEquity*5, accountEquity*10))
	sb.WriteString("- ⚠️ **不要使用最小值**：避免使用范围下限，建议使用中上值（如山寨币用3.5-4.5倍，BTC/ETH用7-9倍）\n")
	sb.WriteString("- ✅ **如果可用余额充足（>账户净值的50%），应该充分利用可用余额，使用更大的仓位和更高的杠杆**\n")
	sb.WriteString("- ✅ **高置信度（≥85）时，可以使用接近上限的仓位和杠杆，充分利用可用资金**\n")
	sb.WriteString("- ⚠️ **置信度要求（严格执行）**：开仓时 `confidence` 必须≥80，如果置信度<80，必须选择 `wait` 或 `hold`，不能开仓\n")
	sb.WriteString("- ❌ **不要使用市场数据中的任何数字**（如 Open Interest 合约数、Volume、价格等）作为 position_size_usd\n")
	sb.WriteString("- ✅ **position_size_usd 必须根据账户净值和上述范围计算，优先使用中上值而非最小值**\n\n")

	// 3. 输出格式 - 动态生成
	sb.WriteString("# 输出格式 (严格遵守)\n\n")
	sb.WriteString("**必须使用XML标签 <reasoning> 和 <decision> 标签分隔思维链和决策JSON，避免解析错误**\n\n")
	sb.WriteString("## 格式要求\n\n")
	sb.WriteString("<reasoning>\n")
	sb.WriteString("你的思维链分析...\n")
	sb.WriteString("- 简洁分析你的思考过程 \n")
	sb.WriteString("</reasoning>\n\n")
	sb.WriteString("<decision>\n")
	sb.WriteString("```json\n[\n")
	sb.WriteString(fmt.Sprintf("  {\"symbol\": \"BTCUSDT\", \"action\": \"open_short\", \"leverage\": %d, \"position_size_usd\": %.0f, \"stop_loss\": 97000, \"take_profit\": 91000, \"confidence\": 85, \"risk_usd\": 300, \"reasoning\": \"下跌趋势+MACD死叉+多重确认\"},\n", btcEthLeverage, accountEquity*7.5)) // 使用中上值，置信度85确保稳定性
	sb.WriteString("  // ⚠️ 注意：如果置信度<80，必须使用 \"action\": \"wait\" 而不是开仓\n")
	sb.WriteString("  {\"symbol\": \"SOLUSDT\", \"action\": \"update_stop_loss\", \"new_stop_loss\": 155, \"reasoning\": \"移动止损至保本位\"},\n")
	sb.WriteString("  {\"symbol\": \"ETHUSDT\", \"action\": \"close_long\", \"reasoning\": \"止盈离场\"}\n")
	sb.WriteString("]\n```\n")
	sb.WriteString("</decision>\n\n")
	sb.WriteString("## 字段说明\n\n")
	sb.WriteString("- `action`: open_long | open_short | close_long | close_short | update_stop_loss | update_take_profit | partial_close | hold | wait\n")
	sb.WriteString("- `confidence`: 0-100（⚠️ **开仓必须≥80，建议≥85**；如果置信度<80，必须选择 `wait` 或 `hold`，不能开仓）\n")
	sb.WriteString("- 开仓时必填: leverage, position_size_usd, stop_loss, take_profit, confidence, risk_usd, reasoning\n")
	sb.WriteString("- update_stop_loss 时必填: new_stop_loss (注意是 new_stop_loss，不是 stop_loss)\n")
	sb.WriteString("- update_take_profit 时必填: new_take_profit (注意是 new_take_profit，不是 take_profit)\n")
	sb.WriteString("- partial_close 时必填: close_percentage (0-100)\n\n")
	sb.WriteString("## 🛡️ 未成交挂单提醒\n\n")
	sb.WriteString("在「当前持仓」部分，你会看到每个持仓的挂单状态：\n\n")
	sb.WriteString("- 🛡️ **止损单**: 表示该持仓已有止损保护\n")
	sb.WriteString("- 🎯 **止盈单**: 表示该持仓已设置止盈目标\n")
	sb.WriteString("- ⚠️ **该持仓没有止损保护！**: 表示该持仓缺少止损单，需要立即设置\n\n")
	sb.WriteString("**重要**: \n")
	sb.WriteString("- ✅ 如果看到 🛡️ 止损单已存在，且你想调整止损价格，仍可使用 `update_stop_loss` 动作（系统会自动取消旧单并设置新单）\n")
	sb.WriteString("- ⚠️ 如果看到 🛡️ 止损单已存在，且当前止损价格合理，**不要重复发送相同的 update_stop_loss 指令**\n")
	sb.WriteString("- 🚨 如果看到 ⚠️ **该持仓没有止损保护！**，必须立即使用 `update_stop_loss` 设置止损，否则风险极高\n")
	sb.WriteString("- 同样规则适用于 `update_take_profit` 和 🎯 止盈单\n\n")

	return sb.String()
}

// buildUserPrompt 构建 User Prompt（动态数据）
func buildUserPrompt(ctx *Context) string {
	var sb strings.Builder

	// 系统状态
	sb.WriteString(fmt.Sprintf("时间: %s | 周期: #%d | 运行: %d分钟\n\n",
		ctx.CurrentTime, ctx.CallCount, ctx.RuntimeMinutes))

	// BTC 市场（多时间周期分析）
	if btcData, hasBTC := ctx.MarketDataMap["BTCUSDT"]; hasBTC {
		price := btcData.RealtimePrice
		if price <= 0 {
			price = btcData.CurrentPrice
		}
		sb.WriteString(fmt.Sprintf("BTC: %.2f (1h: %+.2f%%, 4h: %+.2f%%) | MACD: %.4f | RSI: %.2f\n",
			price, btcData.PriceChange1h, btcData.PriceChange4h,
			btcData.CurrentMACD, btcData.CurrentRSI7))
		
		// 添加BTC的多时间周期K线形态分析（完整详细信息）
		if btcAnalyses, hasBTCAnalyses := ctx.MultiTimeframeAnalysisMap["BTCUSDT"]; hasBTCAnalyses && len(btcAnalyses) > 0 {
			sb.WriteString("\n### BTC 多时间周期K线形态分析\n\n")
			
			// 按时间周期排序显示（短周期到长周期）
			intervalOrder := []string{"1m", "3m", "5m", "15m", "30m", "1h", "2h", "4h", "6h", "8h", "12h", "1d", "3d", "1w", "1M"}
			for _, interval := range intervalOrder {
				if analysis, exists := btcAnalyses[interval]; exists {
					// 使用FormatForPrompt显示完整的形态分析（包括所有形态、支撑位、阻力位等）
					sb.WriteString(analysis.FormatForPrompt())
					
					// 添加K线可视化（对关键时间周期：1m, 15m, 1h, 4h, 1d）
					if interval == "1m" || interval == "15m" || interval == "1h" || interval == "4h" || interval == "1d" {
						// 获取K线数据用于可视化
						apiClient := market.NewAPIClient()
						klines, err := apiClient.GetKlines("BTCUSDT", interval, 50) // 获取最近50根用于可视化
						if err == nil && len(klines) > 0 {
							visualization := FormatKlineVisualization(klines, "BTCUSDT", interval, 50)
							if visualization != "" {
								sb.WriteString(visualization)
							}
						}
					}
				}
			}
		} else {
			// 兼容性：如果没有多时间周期分析，使用旧的单时间周期分析
		if btcPatternAnalysis, hasBTCPattern := ctx.PatternAnalysisMap["BTCUSDT"]; hasBTCPattern {
				sb.WriteString(btcPatternAnalysis.FormatForPrompt())
			}
		}
		
		sb.WriteString("\n")
	}

	// 账户
	availableBalancePct := (ctx.Account.AvailableBalance / ctx.Account.TotalEquity) * 100
	sb.WriteString(fmt.Sprintf("账户: 净值%.2f | 余额%.2f (%.1f%%) | 盈亏%+.2f%% | 保证金%.1f%% | 持仓%d个\n",
		ctx.Account.TotalEquity,
		ctx.Account.AvailableBalance,
		availableBalancePct,
		ctx.Account.TotalPnLPct,
		ctx.Account.MarginUsedPct,
		ctx.Account.PositionCount))
	
	// 🔧 如果可用余额充足，提示AI使用更大的仓位
	if availableBalancePct > 50 {
		sb.WriteString(fmt.Sprintf("💡 **可用余额充足（%.1f%%），建议充分利用可用资金，使用更大的仓位和更高的杠杆**\n", availableBalancePct))
	} else if availableBalancePct > 30 {
		sb.WriteString(fmt.Sprintf("💡 **可用余额较多（%.1f%%），可以使用中上值的仓位和杠杆**\n", availableBalancePct))
	}
	sb.WriteString("\n")

	// 市场状态概览
	if ctx.MarketSummary != nil {
		summary := ctx.MarketSummary
		sb.WriteString(fmt.Sprintf("市场状态: 趋势=%s | 波动=%s | 流动性=%s\n",
			summary.TrendLabelCN(),
			summary.VolatilityLabelCN(),
			summary.LiquidityLabelCN()))
		if summary.SuggestedAction != "" {
			sb.WriteString(fmt.Sprintf("行动建议: %s\n", summary.SuggestedAction))
		}
		if len(summary.Notes) > 0 {
			for _, note := range summary.Notes {
				sb.WriteString(fmt.Sprintf("- %s\n", note))
			}
		}
		sb.WriteString("\n")
	}

	// 持仓（完整市场数据）
	if len(ctx.Positions) > 0 {
		sb.WriteString("## 当前持仓\n")
		for i, pos := range ctx.Positions {
			// 计算持仓时长
			holdingDuration := ""
			if pos.UpdateTime > 0 {
				durationMs := time.Now().UnixMilli() - pos.UpdateTime
				durationMin := durationMs / (1000 * 60) // 转换为分钟
				if durationMin < 60 {
					holdingDuration = fmt.Sprintf(" | 持仓时长%d分钟", durationMin)
				} else {
					durationHour := durationMin / 60
					durationMinRemainder := durationMin % 60
					holdingDuration = fmt.Sprintf(" | 持仓时长%d小时%d分钟", durationHour, durationMinRemainder)
				}
			}

			// 计算仓位价值（用于 partial_close 检查）
			positionValue := math.Abs(pos.Quantity) * pos.MarkPrice

			sb.WriteString(fmt.Sprintf("%d. %s %s | 入场价%.4f 当前价%.4f | 数量%.4f | 仓位价值%.2f USDT | 盈亏%+.2f%% | 盈亏金额%+.2f USDT | 最高收益率%.2f%% | 杠杆%dx | 保证金%.0f | 强平价%.4f%s\n",
				i+1, pos.Symbol, strings.ToUpper(pos.Side),
				pos.EntryPrice, pos.MarkPrice, pos.Quantity, positionValue, pos.UnrealizedPnLPct, pos.UnrealizedPnL, pos.PeakPnLPct,
				pos.Leverage, pos.MarginUsed, pos.LiquidationPrice, holdingDuration))

			// Display stop-loss/take-profit orders for this position to prevent duplicate orders
			hasStopLoss := false

			for _, order := range ctx.OpenOrders {
				if order.Symbol != pos.Symbol {
					continue
				}

				if order.Type == "STOP_MARKET" || order.Type == "STOP" {
					sb.WriteString(fmt.Sprintf("   🛡️ 止损单: %.4f (%s)\n", order.StopPrice, order.Side))
					hasStopLoss = true
				} else if order.Type == "TAKE_PROFIT_MARKET" || order.Type == "TAKE_PROFIT" {
					sb.WriteString(fmt.Sprintf("   🎯 止盈单: %.4f (%s)\n", order.StopPrice, order.Side))
				}
			}

			if !hasStopLoss {
				sb.WriteString("   ⚠️ **该持仓没有止损保护！**\n")
			}

			sb.WriteString("\n")

			// 使用FormatMarketData输出完整市场数据
			if marketData, ok := ctx.MarketDataMap[pos.Symbol]; ok {
				// 添加简洁格式的市场指标（类似BTC的显示格式）
				price := marketData.RealtimePrice
				if price <= 0 {
					price = marketData.CurrentPrice
				}
				sb.WriteString(fmt.Sprintf("%s: %.2f (1h: %+.2f%%, 4h: %+.2f%%) | MACD: %.4f | RSI: %.2f\n\n",
					pos.Symbol, price, marketData.PriceChange1h, marketData.PriceChange4h,
					marketData.CurrentMACD, marketData.CurrentRSI7))
				
				sb.WriteString(market.Format(marketData))
				sb.WriteString("\n")
			}

			// 添加多时间周期K线形态分析（完整详细信息 + K线可视化）
			if analyses, hasAnalyses := ctx.MultiTimeframeAnalysisMap[pos.Symbol]; hasAnalyses && len(analyses) > 0 {
				sb.WriteString("\n#### 多时间周期K线形态分析（重点关注持仓币种的K线状态）\n\n")
				sb.WriteString("**重要提示**: 请仔细分析该持仓币种在各个时间周期的K线形态，结合当前持仓方向和盈亏情况，判断是否需要调整止损止盈、加仓或减仓。\n\n")
				
				intervalOrder := []string{"1m", "3m", "5m", "15m", "30m", "1h", "2h", "4h", "6h", "8h", "12h", "1d", "3d", "1w", "1M"}
				for _, interval := range intervalOrder {
					if analysis, exists := analyses[interval]; exists {
						// 使用FormatForPrompt显示完整的形态分析（包括所有形态、支撑位、阻力位等）
						sb.WriteString(analysis.FormatForPrompt())
						
						// 为关键时间周期添加K线可视化数据（让AI能够更直观地看到K线状态）
						if interval == "1m" || interval == "1h" || interval == "4h" || interval == "1d" {
							// 获取K线数据用于可视化
							apiClient := market.NewAPIClient()
							klines, err := apiClient.GetKlines(pos.Symbol, interval, 50) // 获取最近50根用于可视化
							if err == nil && len(klines) > 0 {
								visualization := FormatKlineVisualization(klines, pos.Symbol, interval, 50)
								if visualization != "" {
									sb.WriteString(visualization)
								}
							}
						}
					}
				}
				
				// 添加持仓决策提示
				sb.WriteString("\n**持仓决策建议**:\n")
				sb.WriteString("请基于以上多时间周期K线分析，综合考虑：\n")
				sb.WriteString("1. 短期（1m, 3m, 5m）和中期（1h, 4h）趋势是否一致？\n")
				sb.WriteString("2. 当前价格是否接近关键支撑位或阻力位？\n")
				sb.WriteString("3. K线形态是否显示反转信号？\n")
				sb.WriteString("4. 是否需要调整止损止盈位置？\n")
				sb.WriteString("5. 是否应该加仓、减仓或平仓？\n\n")
			} else {
				// 兼容性：如果没有多时间周期分析，使用旧的单时间周期分析
			if patternAnalysis, hasPattern := ctx.PatternAnalysisMap[pos.Symbol]; hasPattern {
				sb.WriteString("\n")
				sb.WriteString(patternAnalysis.FormatForPrompt())
				sb.WriteString("\n")
				}
			}
		}
	} else {
		sb.WriteString("当前持仓: 无\n\n")
	}

	// 候选币种（完整市场数据 + K线形态分析）
	sb.WriteString(fmt.Sprintf("## 候选币种 (%d个)\n\n", len(ctx.MarketDataMap)))
	displayedCount := 0
	for _, coin := range ctx.CandidateCoins {
		marketData, hasData := ctx.MarketDataMap[coin.Symbol]
		if !hasData {
			continue
		}
		displayedCount++

		sourceTags := ""
		if len(coin.Sources) > 1 {
			sourceTags = " (AI500+OI_Top双重信号)"
		} else if len(coin.Sources) == 1 && coin.Sources[0] == "oi_top" {
			sourceTags = " (OI_Top持仓增长)"
		}

		// 使用FormatMarketData输出完整市场数据
		sb.WriteString(fmt.Sprintf("### %d. %s%s\n\n", displayedCount, coin.Symbol, sourceTags))
		
		// 添加简洁格式的市场指标（类似BTC的显示格式）
		price := marketData.RealtimePrice
		if price <= 0 {
			price = marketData.CurrentPrice
		}
		sb.WriteString(fmt.Sprintf("%s: %.2f (1h: %+.2f%%, 4h: %+.2f%%) | MACD: %.4f | RSI: %.2f\n\n",
			coin.Symbol, price, marketData.PriceChange1h, marketData.PriceChange4h,
			marketData.CurrentMACD, marketData.CurrentRSI7))
		
		sb.WriteString(market.Format(marketData))
		
		// 添加多时间周期K线形态分析（完整详细信息 + K线可视化）
		if analyses, hasAnalyses := ctx.MultiTimeframeAnalysisMap[coin.Symbol]; hasAnalyses && len(analyses) > 0 {
			sb.WriteString("\n#### 多时间周期K线形态分析\n\n")
			intervalOrder := []string{"1m", "3m", "5m", "15m", "30m", "1h", "2h", "4h", "6h", "8h", "12h", "1d", "3d", "1w", "1M"}
			for _, interval := range intervalOrder {
				if analysis, exists := analyses[interval]; exists {
					// 使用FormatForPrompt显示完整的形态分析（包括所有形态、支撑位、阻力位等）
					sb.WriteString(analysis.FormatForPrompt())
					
					// 为关键时间周期添加K线可视化数据
					if interval == "1m" || interval == "1h" || interval == "4h" || interval == "1d" {
						// 获取K线数据用于可视化
						apiClient := market.NewAPIClient()
						klines, err := apiClient.GetKlines(coin.Symbol, interval, 50) // 获取最近50根用于可视化
						if err == nil && len(klines) > 0 {
							visualization := FormatKlineVisualization(klines, coin.Symbol, interval, 50)
							if visualization != "" {
								sb.WriteString(visualization)
							}
						}
					}
				}
			}
		} else {
			// 兼容性：如果没有多时间周期分析，使用旧的单时间周期分析
		if patternAnalysis, hasPattern := ctx.PatternAnalysisMap[coin.Symbol]; hasPattern {
			sb.WriteString("\n")
			sb.WriteString(patternAnalysis.FormatForPrompt())
			}
		}
		
		sb.WriteString("\n")
	}
	sb.WriteString("\n")

	// 夏普比率（直接传值，不要复杂格式化）
	if ctx.Performance != nil {
		// 直接从interface{}中提取SharpeRatio
		type PerformanceData struct {
			SharpeRatio float64 `json:"sharpe_ratio"`
		}
		var perfData PerformanceData
		if jsonData, err := json.Marshal(ctx.Performance); err == nil {
			if err := json.Unmarshal(jsonData, &perfData); err == nil {
				sb.WriteString(fmt.Sprintf("## 📊 夏普比率: %.2f\n\n", perfData.SharpeRatio))
			}
		}
	}

	// 历史交易记录（用于 AI 学习）- 使用 Performance.RecentTrades 以显示完整的盈亏数据
	if ctx.Performance != nil {
		// 提取 RecentTrades
		type PerformanceData struct {
			RecentTrades []logger.TradeOutcome `json:"recent_trades"`
		}
		var perfData PerformanceData
		if jsonData, err := json.Marshal(ctx.Performance); err == nil {
			if err := json.Unmarshal(jsonData, &perfData); err == nil && len(perfData.RecentTrades) > 0 {
				sb.WriteString("## 📜 近期交易记录（最近10笔）\n\n")

				for i, trade := range perfData.RecentTrades {
					// 判断盈亏（成功/失败）
					resultIcon := "✅"
					if trade.PnL < 0 {
						resultIcon = "❌"
					}

					// 格式化时间范围
					openTimeStr := trade.OpenTime.Format("01-02 15:04")
					closeTimeStr := trade.CloseTime.Format("15:04")

					// 方向大写
					direction := strings.ToUpper(trade.Side)

					// 止损标记
					stopLossTag := ""
					if trade.WasStopLoss {
						stopLossTag = " 🛡️ 止损"
					}

					// 格式化盈亏百分比（添加符号）
					pnlPctStr := fmt.Sprintf("%+.2f%%", trade.PnLPct)

					// 格式化盈亏金额（添加符号）
					pnlStr := fmt.Sprintf("%+.2f", trade.PnL)

					// 第一行：时间、币种、方向、杠杆
					sb.WriteString(fmt.Sprintf("%s %d. [%s→%s] %s %s (%dx杠杆)%s\n",
						resultIcon, i+1, openTimeStr, closeTimeStr,
						trade.Symbol, direction, trade.Leverage, stopLossTag))

					// 第二行：开倉价 → 平倉价 (盈亏百分比)
					sb.WriteString(fmt.Sprintf("   开仓: @ %.2f → 平仓: @ %.2f (%s)\n",
						trade.OpenPrice, trade.ClosePrice, pnlPctStr))

					// 第三行：盈亏金额 | 持仓时长
					sb.WriteString(fmt.Sprintf("   盈亏: %s USDT | 持仓: %s\n\n",
						pnlStr, trade.Duration))
				}
			}
		}
	}

	sb.WriteString("---\n\n")
	sb.WriteString("现在请分析并输出决策（思维链 + JSON）\n")

	return sb.String()
}

// parseFullDecisionResponse 解析AI的完整决策响应
func parseFullDecisionResponse(aiResponse string, accountEquity float64, btcEthLeverage, altcoinLeverage int) (*FullDecision, error) {
	// 1. 提取思维链
	cotTrace := extractCoTTrace(aiResponse)

	// 2. 提取JSON决策列表
	decisions, err := extractDecisions(aiResponse)
	if err != nil {
		return &FullDecision{
			CoTTrace:  cotTrace,
			Decisions: []Decision{},
		}, fmt.Errorf("提取决策失败: %w", err)
	}

	// 3. 验证决策
	if err := validateDecisions(decisions, accountEquity, btcEthLeverage, altcoinLeverage); err != nil {
		return &FullDecision{
			CoTTrace:  cotTrace,
			Decisions: decisions,
		}, fmt.Errorf("决策验证失败: %w", err)
	}

	return &FullDecision{
		CoTTrace:  cotTrace,
		Decisions: decisions,
	}, nil
}

// extractCoTTrace 提取思维链分析
func extractCoTTrace(response string) string {
	// 方法1: 优先尝试提取 <reasoning> 标签内容
	if match := reReasoningTag.FindStringSubmatch(response); match != nil && len(match) > 1 {
		log.Printf("✓ 使用 <reasoning> 标签提取思维链")
		return strings.TrimSpace(match[1])
	}

	// 方法2: 如果没有 <reasoning> 标签，但有 <decision> 标签，提取 <decision> 之前的内容
	if decisionIdx := strings.Index(response, "<decision>"); decisionIdx > 0 {
		log.Printf("✓ 提取 <decision> 标签之前的内容作为思维链")
		return strings.TrimSpace(response[:decisionIdx])
	}

	// 方法3: 后备方案 - 查找JSON数组的开始位置
	jsonStart := strings.Index(response, "[")
	if jsonStart > 0 {
		log.Printf("⚠️  使用旧版格式（[ 字符分离）提取思维链")
		return strings.TrimSpace(response[:jsonStart])
	}

	// 如果找不到任何标记，整个响应都是思维链
	return strings.TrimSpace(response)
}

// extractDecisions 提取JSON决策列表
func extractDecisions(response string) ([]Decision, error) {
	// 预清洗：去零宽/BOM
	s := removeInvisibleRunes(response)
	s = strings.TrimSpace(s)

	// 🔧 关键修复 (Critical Fix)：在正则匹配之前就先修复全角字符！
	// 否则正则表达式 \[ 无法匹配全角的 ［
	s = fixMissingQuotes(s)

	// 方法1: 优先尝试从 <decision> 标签中提取
	var jsonPart string
	if match := reDecisionTag.FindStringSubmatch(s); match != nil && len(match) > 1 {
		jsonPart = strings.TrimSpace(match[1])
		log.Printf("✓ 使用 <decision> 标签提取JSON")
	} else {
		// 后备方案：使用整个响应
		jsonPart = s
		log.Printf("⚠️  未找到 <decision> 标签，使用全文搜索JSON")
	}

	// 修复 jsonPart 中的全角字符
	jsonPart = fixMissingQuotes(jsonPart)

	// 1) 优先从 ```json 代码块中提取
	if m := reJSONFence.FindStringSubmatch(jsonPart); m != nil && len(m) > 1 {
		jsonContent := strings.TrimSpace(m[1])
		jsonContent = compactArrayOpen(jsonContent) // 把 "[ {" 规整为 "[{"
		jsonContent = fixMissingQuotes(jsonContent) // 二次修复（防止 regex 提取后还有残留全角）
		if err := validateJSONFormat(jsonContent); err != nil {
			return nil, fmt.Errorf("JSON格式验证失败: %w\nJSON内容: %s\n完整响应:\n%s", err, jsonContent, response)
		}
		var decisions []Decision
		if err := json.Unmarshal([]byte(jsonContent), &decisions); err != nil {
			return nil, fmt.Errorf("JSON解析失败: %w\nJSON内容: %s", err, jsonContent)
		}
		return decisions, nil
	}

	// 2) 退而求其次 (Fallback)：全文寻找首个对象数组
	// 注意：此时 jsonPart 已经过 fixMissingQuotes()，全角字符已转换为半角
	jsonContent := strings.TrimSpace(reJSONArray.FindString(jsonPart))
	if jsonContent == "" {
		// 🔧 安全回退 (Safe Fallback)：当AI只输出思维链没有JSON时，生成保底决策（避免系统崩溃）
		log.Printf("⚠️  [SafeFallback] AI未输出JSON决策，进入安全等待模式 (AI response without JSON, entering safe wait mode)")

		// 提取思维链摘要（最多 240 字符）
		cotSummary := jsonPart
		if len(cotSummary) > 240 {
			cotSummary = cotSummary[:240] + "..."
		}

		// 生成保底决策：所有币种进入 wait 状态
		fallbackDecision := Decision{
			Symbol:    "ALL",
			Action:    "wait",
			Reasoning: fmt.Sprintf("模型未输出结构化JSON决策，进入安全等待；摘要：%s", cotSummary),
		}

		return []Decision{fallbackDecision}, nil
	}

	// 🔧 规整格式（此时全角字符已在前面修复过）
	jsonContent = compactArrayOpen(jsonContent)
	jsonContent = fixMissingQuotes(jsonContent) // 二次修复（防止 regex 提取后还有残留全角）

	// 🔧 验证 JSON 格式（检测常见错误）
	if err := validateJSONFormat(jsonContent); err != nil {
		return nil, fmt.Errorf("JSON格式验证失败: %w\nJSON内容: %s\n完整响应:\n%s", err, jsonContent, response)
	}

	// 解析JSON
	var decisions []Decision
	if err := json.Unmarshal([]byte(jsonContent), &decisions); err != nil {
		return nil, fmt.Errorf("JSON解析失败: %w\nJSON内容: %s", err, jsonContent)
	}

	return decisions, nil
}

// fixMissingQuotes 替换中文引号和全角字符为英文引号和半角字符（避免AI输出全角JSON字符导致解析失败）
func fixMissingQuotes(jsonStr string) string {
	// 替换中文引号
	jsonStr = strings.ReplaceAll(jsonStr, "\u201c", "\"") // "
	jsonStr = strings.ReplaceAll(jsonStr, "\u201d", "\"") // "
	jsonStr = strings.ReplaceAll(jsonStr, "\u2018", "'")  // '
	jsonStr = strings.ReplaceAll(jsonStr, "\u2019", "'")  // '

	// ⚠️ 替换全角括号、冒号、逗号（防止AI输出全角JSON字符）
	jsonStr = strings.ReplaceAll(jsonStr, "［", "[") // U+FF3B 全角左方括号
	jsonStr = strings.ReplaceAll(jsonStr, "］", "]") // U+FF3D 全角右方括号
	jsonStr = strings.ReplaceAll(jsonStr, "｛", "{") // U+FF5B 全角左花括号
	jsonStr = strings.ReplaceAll(jsonStr, "｝", "}") // U+FF5D 全角右花括号
	jsonStr = strings.ReplaceAll(jsonStr, "：", ":") // U+FF1A 全角冒号
	jsonStr = strings.ReplaceAll(jsonStr, "，", ",") // U+FF0C 全角逗号

	// ⚠️ 替换CJK标点符号（AI在中文上下文中也可能输出这些）
	jsonStr = strings.ReplaceAll(jsonStr, "【", "[") // CJK左方头括号 U+3010
	jsonStr = strings.ReplaceAll(jsonStr, "】", "]") // CJK右方头括号 U+3011
	jsonStr = strings.ReplaceAll(jsonStr, "〔", "[") // CJK左龟壳括号 U+3014
	jsonStr = strings.ReplaceAll(jsonStr, "〕", "]") // CJK右龟壳括号 U+3015
	jsonStr = strings.ReplaceAll(jsonStr, "、", ",") // CJK顿号 U+3001

	// ⚠️ 替换全角空格为半角空格（JSON中不应该有全角空格）
	jsonStr = strings.ReplaceAll(jsonStr, "　", " ") // U+3000 全角空格

	return jsonStr
}

// validateJSONFormat validates JSON format and detects common errors
func validateJSONFormat(jsonStr string) error {
	trimmed := strings.TrimSpace(jsonStr)

	// Allow any whitespace (including zero-width) between [ and {
	if !reArrayHead.MatchString(trimmed) {
		// Check if it's a pure number/range array (common error)
		if strings.HasPrefix(trimmed, "[") && !strings.Contains(trimmed[:min(20, len(trimmed))], "{") {
			return fmt.Errorf("not a valid decision array (must contain objects {}), actual content: %s", trimmed[:min(50, len(trimmed))])
		}
		return fmt.Errorf("JSON must start with [{ (whitespace allowed), actual: %s", trimmed[:min(20, len(trimmed))])
	}

	// Check for range symbol ~ (common LLM error)
	if strings.Contains(jsonStr, "~") {
		return fmt.Errorf("JSON cannot contain range symbol ~, all numbers must be precise single values")
	}

	// Check for thousands separators (like 98,000) but skip string values
	// Parse through JSON and only check numeric contexts
	if err := checkThousandsSeparatorsOutsideStrings(jsonStr); err != nil {
		return err
	}

	return nil
}

// checkThousandsSeparatorsOutsideStrings checks for thousands separators in JSON numbers
// but ignores commas inside string values
func checkThousandsSeparatorsOutsideStrings(jsonStr string) error {
	inString := false
	escaped := false

	for i := 0; i < len(jsonStr)-4; i++ {
		// Track string boundaries
		if jsonStr[i] == '"' && !escaped {
			inString = !inString
		}
		escaped = (jsonStr[i] == '\\' && !escaped)

		// Skip if we're inside a string value
		if inString {
			continue
		}

		// Check for pattern: digit, comma, 3 digits
		if jsonStr[i] >= '0' && jsonStr[i] <= '9' &&
			jsonStr[i+1] == ',' &&
			jsonStr[i+2] >= '0' && jsonStr[i+2] <= '9' &&
			jsonStr[i+3] >= '0' && jsonStr[i+3] <= '9' &&
			jsonStr[i+4] >= '0' && jsonStr[i+4] <= '9' {
			return fmt.Errorf("JSON numbers cannot contain thousands separator commas, found: %s", jsonStr[i:min(i+10, len(jsonStr))])
		}
	}

	return nil
}

// min 返回两个整数中的较小值
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// removeInvisibleRunes 去除零宽字符和 BOM，避免肉眼看不见的前缀破坏校验
func removeInvisibleRunes(s string) string {
	return reInvisibleRunes.ReplaceAllString(s, "")
}

// compactArrayOpen 规整开头的 "[ {" → "[{"
func compactArrayOpen(s string) string {
	return reArrayOpenSpace.ReplaceAllString(strings.TrimSpace(s), "[{")
}

// validateDecisions 验证所有决策（需要账户信息和杠杆配置）
func validateDecisions(decisions []Decision, accountEquity float64, btcEthLeverage, altcoinLeverage int) error {
	for i, decision := range decisions {
		if err := validateDecision(&decision, accountEquity, btcEthLeverage, altcoinLeverage); err != nil {
			return fmt.Errorf("决策 #%d 验证失败: %w", i+1, err)
		}
	}
	return nil
}

// findMatchingBracket 查找匹配的右括号
func findMatchingBracket(s string, start int) int {
	if start >= len(s) || s[start] != '[' {
		return -1
	}

	depth := 0
	for i := start; i < len(s); i++ {
		switch s[i] {
		case '[':
			depth++
		case ']':
			depth--
			if depth == 0 {
				return i
			}
		}
	}

	return -1
}

// positionSizeConfig 定义账户规模分层配置
type positionSizeConfig struct {
	MinEquity float64 // 账户最小净值阈值
	MinSize   float64 // 最小开仓金额（0 表示使用线性插值）
	MaxSize   float64 // 最大开仓金额（用于线性插值）
}

var (
	// 配置常量
	absoluteMinimum = 12.0 // 交易所绝对最小值 (10 USDT + 20% 安全边际)
	standardBTCETH  = 60.0 // 标准 BTC/ETH 最小值 (因价格高和精度限制)

	// BTC/ETH 动态调整规则（按账户规模分层）
	btcEthSizeRules = []positionSizeConfig{
		{MinEquity: 0, MinSize: absoluteMinimum, MaxSize: absoluteMinimum}, // 小账户(<20U): 12 USDT
		{MinEquity: 20, MinSize: absoluteMinimum, MaxSize: standardBTCETH}, // 中型账户(20-100U): 线性插值
		{MinEquity: 100, MinSize: standardBTCETH, MaxSize: standardBTCETH}, // 大账户(≥100U): 60 USDT
	}

	// 山寨币规则（始终使用绝对最小值）
	altcoinSizeRules = []positionSizeConfig{
		{MinEquity: 0, MinSize: absoluteMinimum, MaxSize: absoluteMinimum},
	}

	// 币种规则映射表（易于扩展，添加新币种只需在此添加一行）
	symbolSizeRules = map[string][]positionSizeConfig{
		"BTCUSDT": btcEthSizeRules,
		"ETHUSDT": btcEthSizeRules,
		// 未来可添加更多币种的特殊规则，例如:
		// "BNBUSDT": bnbSizeRules,
		// "SOLUSDT": solSizeRules,
	}
)

// calculateMinPositionSize 根据账户净值和币种动态计算最小开仓金额
func calculateMinPositionSize(symbol string, accountEquity float64) float64 {
	// 从配置映射表中获取币种规则
	rules, exists := symbolSizeRules[symbol]
	if !exists {
		// 未配置的币种使用山寨币规则（默认绝对最小值）
		rules = altcoinSizeRules
	}

	// 根据规则表动态计算
	for i, rule := range rules {
		// 找到账户所属的规模区间
		if i == len(rules)-1 || accountEquity < rules[i+1].MinEquity {
			// 如果 MinSize == MaxSize，直接返回固定值
			if rule.MinSize == rule.MaxSize {
				return rule.MinSize
			}
			// 否则使用线性插值
			nextRule := rules[i+1]
			equityRange := nextRule.MinEquity - rule.MinEquity
			sizeRange := rule.MaxSize - rule.MinSize
			return rule.MinSize + sizeRange*(accountEquity-rule.MinEquity)/equityRange
		}
	}

	// 默认返回绝对最小值（理论上不会执行到这里）
	return absoluteMinimum
}

// validateDecision 验证单个决策的有效性
func validateDecision(d *Decision, accountEquity float64, btcEthLeverage, altcoinLeverage int) error {
	// 验证action
	validActions := map[string]bool{
		"open_long":          true,
		"open_short":         true,
		"close_long":         true,
		"close_short":        true,
		"update_stop_loss":   true,
		"update_take_profit": true,
		"partial_close":      true,
		"hold":               true,
		"wait":               true,
	}

	if !validActions[d.Action] {
		return fmt.Errorf("无效的action: %s", d.Action)
	}

	// 开仓操作必须提供完整参数
	if d.Action == "open_long" || d.Action == "open_short" {
		// ✅ 稳定性检查1：置信度验证（提高要求以确保决策稳定性）
		if d.Confidence < 80 {
			return fmt.Errorf("置信度过低(%d)，开仓必须≥80以确保决策稳定性（建议≥85）", d.Confidence)
		}

		// 根据币种使用配置的杠杆上限
		maxLeverage := altcoinLeverage        // 山寨币使用配置的杠杆
		maxPositionValue := accountEquity * 5 // 山寨币最多5倍账户净值
		if d.Symbol == "BTCUSDT" || d.Symbol == "ETHUSDT" {
			maxLeverage = btcEthLeverage          // BTC和ETH使用配置的杠杆
			maxPositionValue = accountEquity * 10 // BTC/ETH最多10倍账户净值
		}

		// ✅ Fallback 机制：杠杆超限时自动修正为上限值（而不是直接拒绝决策）
		if d.Leverage <= 0 {
			return fmt.Errorf("杠杆必须大于0: %d", d.Leverage)
		}
		if d.Leverage > maxLeverage {
			log.Printf("⚠️  [Leverage Fallback] %s 杠杆超限 (%dx > %dx)，自动调整为上限值 %dx",
				d.Symbol, d.Leverage, maxLeverage, maxLeverage)
			d.Leverage = maxLeverage // 自动修正为上限值
		}
		if d.PositionSizeUSD <= 0 {
			return fmt.Errorf("仓位大小必须大于0: %.2f", d.PositionSizeUSD)
		}

		// ✅ 验证最小开仓金额（防止数量格式化为 0 的错误）
		// 使用动态计算函数，根据账户规模自适应调整
		minPositionSize := calculateMinPositionSize(d.Symbol, accountEquity)
		if d.PositionSizeUSD < minPositionSize {
			// 小账户特殊提示：引导用户理解动态门槛
			if accountEquity < 20.0 && (d.Symbol == "BTCUSDT" || d.Symbol == "ETHUSDT") {
				return fmt.Errorf("%s 开仓金额过小(%.2f USDT)，当前账户规模(%.2f USDT)要求≥%.2f USDT（小账户动态调整）",
					d.Symbol, d.PositionSizeUSD, accountEquity, minPositionSize)
			}
			// 通用错误提示
			return fmt.Errorf("开仓金额过小(%.2f USDT)，必须≥%.2f USDT（交易所最小名义价值要求）",
				d.PositionSizeUSD, minPositionSize)
		}

		// 验证仓位价值上限（加1%容差以避免浮点数精度问题）
		tolerance := maxPositionValue * 0.01 // 1%容差
		if d.PositionSizeUSD > maxPositionValue+tolerance {
			if d.Symbol == "BTCUSDT" || d.Symbol == "ETHUSDT" {
				return fmt.Errorf("BTC/ETH单币种仓位价值不能超过%.0f USDT（10倍账户净值），实际: %.0f", maxPositionValue, d.PositionSizeUSD)
			} else {
				return fmt.Errorf("山寨币单币种仓位价值不能超过%.0f USDT（5倍账户净值），实际: %.0f", maxPositionValue, d.PositionSizeUSD)
			}
		}
		if d.StopLoss <= 0 || d.TakeProfit <= 0 {
			return fmt.Errorf("止损和止盈必须大于0")
		}

		// 验证止损止盈的合理性
		if d.Action == "open_long" {
			if d.StopLoss >= d.TakeProfit {
				return fmt.Errorf("做多时止损价必须小于止盈价")
			}
		} else {
			if d.StopLoss <= d.TakeProfit {
				return fmt.Errorf("做空时止损价必须大于止盈价")
			}
		}

		// 验证风险回报比（必须≥1:3）
		// 计算入场价（假设当前市价）
		var entryPrice float64
		if d.Action == "open_long" {
			// 做多：入场价在止损和止盈之间
			entryPrice = d.StopLoss + (d.TakeProfit-d.StopLoss)*0.2 // 假设在20%位置入场
		} else {
			// 做空：入场价在止损和止盈之间
			entryPrice = d.StopLoss - (d.StopLoss-d.TakeProfit)*0.2 // 假设在20%位置入场
		}

		var riskPercent, rewardPercent, riskRewardRatio float64
		if d.Action == "open_long" {
			riskPercent = (entryPrice - d.StopLoss) / entryPrice * 100
			rewardPercent = (d.TakeProfit - entryPrice) / entryPrice * 100
			if riskPercent > 0 {
				riskRewardRatio = rewardPercent / riskPercent
			}
		} else {
			riskPercent = (d.StopLoss - entryPrice) / entryPrice * 100
			rewardPercent = (entryPrice - d.TakeProfit) / entryPrice * 100
			if riskPercent > 0 {
				riskRewardRatio = rewardPercent / riskPercent
			}
		}

		// ✅ 稳定性检查2：风险回报比必须≥3.0（硬约束）
		if riskRewardRatio < 3.0 {
			return fmt.Errorf("风险回报比过低(%.2f:1)，必须≥3.0:1以确保决策稳定性 [风险:%.2f%% 收益:%.2f%%] [止损:%.2f 止盈:%.2f]",
				riskRewardRatio, riskPercent, rewardPercent, d.StopLoss, d.TakeProfit)
		}
	}

	// 动态调整止损验证
	if d.Action == "update_stop_loss" {
		if d.NewStopLoss <= 0 {
			return fmt.Errorf("新止损价格必须大于0: %.2f", d.NewStopLoss)
		}
	}

	// 动态调整止盈验证
	if d.Action == "update_take_profit" {
		if d.NewTakeProfit <= 0 {
			return fmt.Errorf("新止盈价格必须大于0: %.2f", d.NewTakeProfit)
		}
	}

	// 部分平仓验证
	if d.Action == "partial_close" {
		if d.ClosePercentage <= 0 || d.ClosePercentage > 100 {
			return fmt.Errorf("平仓百分比必须在0-100之间: %.1f", d.ClosePercentage)
		}
	}

	return nil
}
