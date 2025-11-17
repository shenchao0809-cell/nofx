package decision

import (
	"fmt"
	"math"
	"nofx/market"
	"sort"
)

// MarketSummary 为 AI 提供的全局市场参考
type MarketSummary struct {
	TrendLabel      string   `json:"trend_label"`
	VolatilityLabel string   `json:"volatility_label"`
	LiquidityLabel  string   `json:"liquidity_label"`
	SuggestedAction string   `json:"suggested_action"`
	Notes           []string `json:"notes"`
}

func analyzeMarketSummary(ctx *Context) *MarketSummary {
	summary := &MarketSummary{
		TrendLabel:      "unknown",
		VolatilityLabel: "normal",
		LiquidityLabel:  "normal",
		SuggestedAction: "wait",
		Notes:           []string{},
	}

	if ctx == nil || len(ctx.MarketDataMap) == 0 {
		return summary
	}

	data := selectPrimaryMarketData(ctx.MarketDataMap)
	summary.TrendLabel = evaluateTrendDirection(data)
	summary.VolatilityLabel = evaluateVolatilityLevel(data)
	summary.LiquidityLabel = evaluateLiquidityLevel(data)
	summary.SuggestedAction = buildSuggestedAction(summary)
	summary.Notes = append(summary.Notes, buildAccountNotes(ctx)...)

	return summary
}

func selectPrimaryMarketData(dataMap map[string]*market.Data) *market.Data {
	if data, ok := dataMap["BTCUSDT"]; ok {
		return data
	}

	// 保持结果可预测：按 symbol 排序后取第一个
	symbols := make([]string, 0, len(dataMap))
	for symbol := range dataMap {
		symbols = append(symbols, symbol)
	}
	sort.Strings(symbols)
	if len(symbols) == 0 {
		return nil
	}
	return dataMap[symbols[0]]
}

func evaluateTrendDirection(data *market.Data) string {
	if data == nil {
		return "unknown"
	}

	score := 0.0
	if data.PriceChange4h >= 2 {
		score += 1.0
	} else if data.PriceChange4h <= -2 {
		score -= 1.0
	}

	if data.PriceChange1h >= 0.8 {
		score += 0.5
	} else if data.PriceChange1h <= -0.8 {
		score -= 0.5
	}

	if data.CurrentEMA20 > 0 && data.CurrentPrice > data.CurrentEMA20 {
		score += 0.5
	} else if data.CurrentEMA20 > 0 {
		score -= 0.5
	}

	if data.CurrentMACD > 0 {
		score += 0.5
	} else if data.CurrentMACD < 0 {
		score -= 0.5
	}

	if data.CurrentRSI7 >= 65 {
		score += 0.5
	} else if data.CurrentRSI7 <= 35 {
		score -= 0.5
	}

	switch {
	case score >= 2:
		return "strong_bull"
	case score >= 0.5:
		return "bull"
	case score <= -2:
		return "strong_bear"
	case score <= -0.5:
		return "bear"
	default:
		return "range"
	}
}

func evaluateVolatilityLevel(data *market.Data) string {
	if data == nil {
		return "unknown"
	}

	atrFast := 0.0
	if data.IntradaySeries != nil {
		atrFast = data.IntradaySeries.ATR14
	}

	var atrBase float64
	if data.LongerTermContext != nil {
		atrBase = data.LongerTermContext.ATR14
	}

	ratio := 1.0
	switch {
	case atrFast > 0 && atrBase > 0:
		ratio = atrFast / atrBase
	case atrFast > 0 && data.CurrentPrice > 0:
		ratio = atrFast / (data.CurrentPrice * 0.01) // 与1%价格比较
	}

	switch {
	case ratio >= 1.8:
		return "extreme"
	case ratio >= 1.3:
		return "high"
	case ratio <= 0.7:
		return "low"
	default:
		return "normal"
	}
}

func evaluateLiquidityLevel(data *market.Data) string {
	if data == nil || data.LongerTermContext == nil {
		return "unknown"
	}

	current := data.LongerTermContext.CurrentVolume
	avg := data.LongerTermContext.AverageVolume
	if current <= 0 || avg <= 0 {
		return "unknown"
	}

	ratio := current / avg
	switch {
	case ratio >= 1.4:
		return "high"
	case ratio <= 0.6:
		return "low"
	default:
		return "normal"
	}
}

func buildSuggestedAction(summary *MarketSummary) string {
	if summary == nil {
		return ""
	}

	switch summary.TrendLabel {
	case "strong_bull":
		if summary.VolatilityLabel == "extreme" {
			return "强势多头但波动极端，观望或减仓等待回调"
		}
		return "强势多头，重点寻找多头回调买点"
	case "bull":
		if summary.VolatilityLabel == "high" {
			return "温和多头且波动偏高，缩小仓位分批建多"
		}
		return "温和多头，顺势择优做多"
	case "bear":
		return "温和空头，谨慎择机做空或观望"
	case "strong_bear":
		return "强势空头，考虑逢高做空或持有防守仓位"
	default:
		return "趋势不明朗，优先等待清晰结构"
	}
}

func buildAccountNotes(ctx *Context) []string {
	if ctx == nil {
		return nil
	}

	acc := ctx.Account
	notes := []string{}

	if acc.MarginUsedPct >= 60 {
		notes = append(notes, fmt.Sprintf("保证金使用率 %.1f%% 偏高，谨慎加仓（建议预留30%%用于多单）", acc.MarginUsedPct))
	}
	if acc.TotalPnLPct <= -8 {
		notes = append(notes, fmt.Sprintf("账户回撤 %.1f%%，应降低仓位或等待修复", acc.TotalPnLPct))
	} else if acc.TotalPnLPct >= 6 {
		notes = append(notes, fmt.Sprintf("账户收益 %.1f%%，可逐步锁定利润", acc.TotalPnLPct))
	}
	if acc.PositionCount >= 3 {
		notes = append(notes, fmt.Sprintf("当前持仓 %d 个，优先管理现有仓位", acc.PositionCount))
	}

	return notes
}

// TrendLabelCN 返回中文描述
func (m *MarketSummary) TrendLabelCN() string {
	if m == nil {
		return "未知"
	}
	switch m.TrendLabel {
	case "strong_bull":
		return "强势多头"
	case "bull":
		return "多头"
	case "range":
		return "震荡"
	case "bear":
		return "空头"
	case "strong_bear":
		return "强势空头"
	default:
		return "未知"
	}
}

// VolatilityLabelCN 返回中文描述
func (m *MarketSummary) VolatilityLabelCN() string {
	if m == nil {
		return "未知"
	}
	switch m.VolatilityLabel {
	case "low":
		return "低波动"
	case "normal":
		return "正常波动"
	case "high":
		return "高波动"
	case "extreme":
		return "极端波动"
	default:
		return "未知"
	}
}

// LiquidityLabelCN 返回中文描述
func (m *MarketSummary) LiquidityLabelCN() string {
	if m == nil {
		return "未知"
	}
	switch m.LiquidityLabel {
	case "low":
		return "流动性不足"
	case "normal":
		return "流动性正常"
	case "high":
		return "流动性充足"
	default:
		return "未知"
	}
}

// Helper to avoid unused import warnings when the compiler optimizes out some branches.
var _ = math.Abs
