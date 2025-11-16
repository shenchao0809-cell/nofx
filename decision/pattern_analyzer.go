package decision

import (
	"fmt"
	"math"
	"nofx/market"
	"strings"
	"time"
)

// PatternAnalysis K线形态分析结果
type PatternAnalysis struct {
	Symbol           string              `json:"symbol"`
	Interval         string              `json:"interval"`
	Patterns         []PatternSignal     `json:"patterns"`          // 识别到的形态
	SupportLevels    []float64           `json:"support_levels"`    // 支撑位
	ResistanceLevels []float64           `json:"resistance_levels"` // 阻力位
	TrendLines       []TrendLine         `json:"trend_lines"`       // 趋势线
	KeyLevels        map[string]float64  `json:"key_levels"`        // 关键价位
	Summary          string              `json:"summary"`           // 形态总结
	Recommendation   string              `json:"recommendation"`    // 操作建议
}

// PatternSignal 形态信号
type PatternSignal struct {
	Name       string  `json:"name"`        // 形态名称
	Type       string  `json:"type"`        // bullish/bearish/neutral
	Confidence float64 `json:"confidence"`  // 置信度 0-100
	Description string `json:"description"` // 形态描述
	Position   int     `json:"position"`    // 形态在K线序列中的位置
}

// TrendLine 趋势线
type TrendLine struct {
	Type      string  `json:"type"`       // support/resistance
	StartIdx  int     `json:"start_idx"`  // 起始K线索引
	EndIdx    int     `json:"end_idx"`    // 结束K线索引
	StartPrice float64 `json:"start_price"` // 起始价格
	EndPrice   float64 `json:"end_price"`   // 结束价格
	Slope      float64 `json:"slope"`       // 斜率
	Strength   float64 `json:"strength"`    // 强度 0-100
}

// AnalyzeKlinePatterns 分析K线形态
func AnalyzeKlinePatterns(klines []market.Kline, symbol string, interval string) *PatternAnalysis {
	if len(klines) < 20 {
		return &PatternAnalysis{
			Symbol:   symbol,
			Interval: interval,
			Summary:  "K线数据不足，无法进行形态分析",
		}
	}

	analysis := &PatternAnalysis{
		Symbol:           symbol,
		Interval:         interval,
		Patterns:         []PatternSignal{},
		SupportLevels:    []float64{},
		ResistanceLevels: []float64{},
		TrendLines:       []TrendLine{},
		KeyLevels:        make(map[string]float64),
	}

	// 1. 识别经典K线形态
	analysis.detectCandlePatterns(klines)

	// 2. 识别支撑阻力位
	analysis.detectSupportResistance(klines)

	// 3. 识别趋势线
	analysis.detectTrendLines(klines)

	// 4. 识别关键价位
	analysis.detectKeyLevels(klines)

	// 5. 生成总结和建议
	analysis.generateSummary(klines)

	return analysis
}

// detectCandlePatterns 识别经典K线形态
func (pa *PatternAnalysis) detectCandlePatterns(klines []market.Kline) {
	n := len(klines)
	if n < 3 {
		return
	}

	// 检查最近的3-5根K线形态
	for i := n - 5; i < n-1; i++ {
		if i < 0 {
			continue
		}

		// 锤子线 (Hammer)
		if pa.isHammer(klines[i]) {
			pa.Patterns = append(pa.Patterns, PatternSignal{
				Name:        "锤子线",
				Type:        "bullish",
				Confidence:  65.0,
				Description: "下影线长，实体小，可能是底部反转信号",
				Position:    i,
			})
		}

		// 倒锤子线 (Inverted Hammer)
		if pa.isInvertedHammer(klines[i]) {
			pa.Patterns = append(pa.Patterns, PatternSignal{
				Name:        "倒锤子线",
				Type:        "bearish",
				Confidence:  65.0,
				Description: "上影线长，实体小，可能是顶部反转信号",
				Position:    i,
			})
		}

		// 吞没形态 (Engulfing)
		if i > 0 {
			if pa.isBullishEngulfing(klines[i-1], klines[i]) {
				pa.Patterns = append(pa.Patterns, PatternSignal{
					Name:        "看涨吞没",
					Type:        "bullish",
					Confidence:  75.0,
					Description: "大阳线完全吞没前一根阴线，强烈看涨信号",
					Position:    i,
				})
			}
			if pa.isBearishEngulfing(klines[i-1], klines[i]) {
				pa.Patterns = append(pa.Patterns, PatternSignal{
					Name:        "看跌吞没",
					Type:        "bearish",
					Confidence:  75.0,
					Description: "大阴线完全吞没前一根阳线，强烈看跌信号",
					Position:    i,
				})
			}
		}

		// 十字星 (Doji)
		if pa.isDoji(klines[i]) {
			pa.Patterns = append(pa.Patterns, PatternSignal{
				Name:        "十字星",
				Type:        "neutral",
				Confidence:  60.0,
				Description: "开盘价与收盘价接近，市场犹豫不决",
				Position:    i,
			})
		}

		// 启明星/黄昏星 (Morning/Evening Star)
		if i >= 2 {
			if pa.isMorningStar(klines[i-2], klines[i-1], klines[i]) {
				pa.Patterns = append(pa.Patterns, PatternSignal{
					Name:        "启明星",
					Type:        "bullish",
					Confidence:  80.0,
					Description: "三根K线组成的底部反转形态，强烈看涨",
					Position:    i,
				})
			}
			if pa.isEveningStar(klines[i-2], klines[i-1], klines[i]) {
				pa.Patterns = append(pa.Patterns, PatternSignal{
					Name:        "黄昏星",
					Type:        "bearish",
					Confidence:  80.0,
					Description: "三根K线组成的顶部反转形态，强烈看跌",
					Position:    i,
				})
			}
		}
	}
}

// isHammer 判断是否为锤子线
func (pa *PatternAnalysis) isHammer(k market.Kline) bool {
	body := math.Abs(k.Close - k.Open)
	lowerShadow := math.Min(k.Open, k.Close) - k.Low
	upperShadow := k.High - math.Max(k.Open, k.Close)
	totalRange := k.High - k.Low

	if totalRange == 0 {
		return false
	}

	// 下影线至少是实体的2倍，上影线很小
	return lowerShadow > body*2 && upperShadow < body*0.3 && body/totalRange < 0.3
}

// isInvertedHammer 判断是否为倒锤子线
func (pa *PatternAnalysis) isInvertedHammer(k market.Kline) bool {
	body := math.Abs(k.Close - k.Open)
	lowerShadow := math.Min(k.Open, k.Close) - k.Low
	upperShadow := k.High - math.Max(k.Open, k.Close)
	totalRange := k.High - k.Low

	if totalRange == 0 {
		return false
	}

	// 上影线至少是实体的2倍，下影线很小
	return upperShadow > body*2 && lowerShadow < body*0.3 && body/totalRange < 0.3
}

// isDoji 判断是否为十字星
func (pa *PatternAnalysis) isDoji(k market.Kline) bool {
	body := math.Abs(k.Close - k.Open)
	totalRange := k.High - k.Low

	if totalRange == 0 {
		return false
	}

	// 实体非常小，不超过总范围的5%
	return body/totalRange < 0.05
}

// isBullishEngulfing 判断是否为看涨吞没
func (pa *PatternAnalysis) isBullishEngulfing(prev, curr market.Kline) bool {
	// 前一根是阴线，当前是阳线
	if prev.Close >= prev.Open || curr.Close <= curr.Open {
		return false
	}

	// 当前阳线完全吞没前一根阴线
	return curr.Open < prev.Close && curr.Close > prev.Open
}

// isBearishEngulfing 判断是否为看跌吞没
func (pa *PatternAnalysis) isBearishEngulfing(prev, curr market.Kline) bool {
	// 前一根是阳线，当前是阴线
	if prev.Close <= prev.Open || curr.Close >= curr.Open {
		return false
	}

	// 当前阴线完全吞没前一根阳线
	return curr.Open > prev.Close && curr.Close < prev.Open
}

// isMorningStar 判断是否为启明星
func (pa *PatternAnalysis) isMorningStar(k1, k2, k3 market.Kline) bool {
	// 第一根是阴线
	if k1.Close >= k1.Open {
		return false
	}

	// 第二根是小实体（十字星或小阳/阴线）
	body2 := math.Abs(k2.Close - k2.Open)
	range2 := k2.High - k2.Low
	if range2 == 0 || body2/range2 > 0.3 {
		return false
	}

	// 第三根是阳线
	if k3.Close <= k3.Open {
		return false
	}

	// 第三根收盘价高于第一根实体中点
	midPoint1 := (k1.Open + k1.Close) / 2
	return k3.Close > midPoint1
}

// isEveningStar 判断是否为黄昏星
func (pa *PatternAnalysis) isEveningStar(k1, k2, k3 market.Kline) bool {
	// 第一根是阳线
	if k1.Close <= k1.Open {
		return false
	}

	// 第二根是小实体
	body2 := math.Abs(k2.Close - k2.Open)
	range2 := k2.High - k2.Low
	if range2 == 0 || body2/range2 > 0.3 {
		return false
	}

	// 第三根是阴线
	if k3.Close >= k3.Open {
		return false
	}

	// 第三根收盘价低于第一根实体中点
	midPoint1 := (k1.Open + k1.Close) / 2
	return k3.Close < midPoint1
}

// detectSupportResistance 识别支撑和阻力位
func (pa *PatternAnalysis) detectSupportResistance(klines []market.Kline) {
	if len(klines) < 20 {
		return
	}

	// 找出局部高点和低点
	highs := []float64{}
	lows := []float64{}

	for i := 2; i < len(klines)-2; i++ {
		// 局部高点：比前后两根K线都高
		if klines[i].High > klines[i-1].High && klines[i].High > klines[i-2].High &&
			klines[i].High > klines[i+1].High && klines[i].High > klines[i+2].High {
			highs = append(highs, klines[i].High)
		}

		// 局部低点：比前后两根K线都低
		if klines[i].Low < klines[i-1].Low && klines[i].Low < klines[i-2].Low &&
			klines[i].Low < klines[i+1].Low && klines[i].Low < klines[i+2].Low {
			lows = append(lows, klines[i].Low)
		}
	}

	// 聚类相近的价位
	pa.ResistanceLevels = pa.clusterPriceLevels(highs, 0.01) // 1%容差
	pa.SupportLevels = pa.clusterPriceLevels(lows, 0.01)
}

// clusterPriceLevels 聚类价格水平
func (pa *PatternAnalysis) clusterPriceLevels(prices []float64, tolerance float64) []float64 {
	if len(prices) == 0 {
		return []float64{}
	}

	clusters := [][]float64{}

	for _, price := range prices {
		foundCluster := false
		for i := range clusters {
			// 计算与聚类中心的距离
			center := pa.average(clusters[i])
			if math.Abs(price-center)/center < tolerance {
				clusters[i] = append(clusters[i], price)
				foundCluster = true
				break
			}
		}
		if !foundCluster {
			clusters = append(clusters, []float64{price})
		}
	}

	// 返回每个聚类的平均值，并按强度（聚类大小）排序
	result := []float64{}
	for _, cluster := range clusters {
		if len(cluster) >= 2 { // 至少出现2次才算有效
			result = append(result, pa.average(cluster))
		}
	}

	return result
}

// average 计算平均值
func (pa *PatternAnalysis) average(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sum := 0.0
	for _, v := range values {
		sum += v
	}
	return sum / float64(len(values))
}

// detectTrendLines 识别趋势线
func (pa *PatternAnalysis) detectTrendLines(klines []market.Kline) {
	if len(klines) < 10 {
		return
	}

	// 简化版：基于最近10-20根K线识别上升/下降趋势线
	n := len(klines)
	start := n - 20
	if start < 0 {
		start = 0
	}

	// 上升趋势线（连接低点）
	lows := []struct {
		idx   int
		price float64
	}{}
	for i := start; i < n; i++ {
		lows = append(lows, struct {
			idx   int
			price float64
		}{i, klines[i].Low})
	}

	// 找到最低点和次低点
	if len(lows) >= 2 {
		minIdx1, minIdx2 := -1, -1
		minPrice1, minPrice2 := math.MaxFloat64, math.MaxFloat64

		for _, low := range lows {
			if low.price < minPrice1 {
				minPrice2 = minPrice1
				minIdx2 = minIdx1
				minPrice1 = low.price
				minIdx1 = low.idx
			} else if low.price < minPrice2 {
				minPrice2 = low.price
				minIdx2 = low.idx
			}
		}

		if minIdx1 != -1 && minIdx2 != -1 && minIdx1 != minIdx2 {
			// 确保索引顺序
			if minIdx1 > minIdx2 {
				minIdx1, minIdx2 = minIdx2, minIdx1
				minPrice1, minPrice2 = minPrice2, minPrice1
			}

			slope := (minPrice2 - minPrice1) / float64(minIdx2-minIdx1)
			if slope > 0 { // 上升趋势
				pa.TrendLines = append(pa.TrendLines, TrendLine{
					Type:       "support",
					StartIdx:   minIdx1,
					EndIdx:     minIdx2,
					StartPrice: minPrice1,
					EndPrice:   minPrice2,
					Slope:      slope,
					Strength:   70.0,
				})
			}
		}
	}

	// 下降趋势线（连接高点）
	highs := []struct {
		idx   int
		price float64
	}{}
	for i := start; i < n; i++ {
		highs = append(highs, struct {
			idx   int
			price float64
		}{i, klines[i].High})
	}

	if len(highs) >= 2 {
		maxIdx1, maxIdx2 := -1, -1
		maxPrice1, maxPrice2 := 0.0, 0.0

		for _, high := range highs {
			if high.price > maxPrice1 {
				maxPrice2 = maxPrice1
				maxIdx2 = maxIdx1
				maxPrice1 = high.price
				maxIdx1 = high.idx
			} else if high.price > maxPrice2 {
				maxPrice2 = high.price
				maxIdx2 = high.idx
			}
		}

		if maxIdx1 != -1 && maxIdx2 != -1 && maxIdx1 != maxIdx2 {
			if maxIdx1 > maxIdx2 {
				maxIdx1, maxIdx2 = maxIdx2, maxIdx1
				maxPrice1, maxPrice2 = maxPrice2, maxPrice1
			}

			slope := (maxPrice2 - maxPrice1) / float64(maxIdx2-maxIdx1)
			if slope < 0 { // 下降趋势
				pa.TrendLines = append(pa.TrendLines, TrendLine{
					Type:       "resistance",
					StartIdx:   maxIdx1,
					EndIdx:     maxIdx2,
					StartPrice: maxPrice1,
					EndPrice:   maxPrice2,
					Slope:      slope,
					Strength:   70.0,
				})
			}
		}
	}
}

// detectKeyLevels 识别关键价位
func (pa *PatternAnalysis) detectKeyLevels(klines []market.Kline) {
	if len(klines) == 0 {
		return
	}

	n := len(klines)
	currentPrice := klines[n-1].Close

	// 最高价和最低价（最近20根K线）
	start := n - 20
	if start < 0 {
		start = 0
	}

	high20 := klines[start].High
	low20 := klines[start].Low

	for i := start; i < n; i++ {
		if klines[i].High > high20 {
			high20 = klines[i].High
		}
		if klines[i].Low < low20 {
			low20 = klines[i].Low
		}
	}

	pa.KeyLevels["current_price"] = currentPrice
	pa.KeyLevels["high_20"] = high20
	pa.KeyLevels["low_20"] = low20
	pa.KeyLevels["range_20"] = high20 - low20

	// 当前价格在区间中的位置（0-100）
	if high20 != low20 {
		pa.KeyLevels["position_pct"] = (currentPrice - low20) / (high20 - low20) * 100
	}
}

// generateSummary 生成形态总结和建议
func (pa *PatternAnalysis) generateSummary(klines []market.Kline) {
	if len(klines) == 0 {
		pa.Summary = "无K线数据"
		pa.Recommendation = "等待"
		return
	}

	bullishCount := 0
	bearishCount := 0
	totalConfidence := 0.0

	for _, pattern := range pa.Patterns {
		if pattern.Type == "bullish" {
			bullishCount++
			totalConfidence += pattern.Confidence
		} else if pattern.Type == "bearish" {
			bearishCount++
			totalConfidence += pattern.Confidence
		}
	}

	// 生成总结
	summary := fmt.Sprintf("识别到 %d 个形态信号", len(pa.Patterns))
	if len(pa.Patterns) > 0 {
		summary += fmt.Sprintf("（看涨:%d, 看跌:%d）", bullishCount, bearishCount)
	}

	if len(pa.SupportLevels) > 0 {
		summary += fmt.Sprintf(", %d个支撑位", len(pa.SupportLevels))
	}
	if len(pa.ResistanceLevels) > 0 {
		summary += fmt.Sprintf(", %d个阻力位", len(pa.ResistanceLevels))
	}

	pa.Summary = summary

	// 生成建议
	if bullishCount > bearishCount && totalConfidence > 0 {
		pa.Recommendation = "偏多：形态显示看涨信号较强"
	} else if bearishCount > bullishCount && totalConfidence > 0 {
		pa.Recommendation = "偏空：形态显示看跌信号较强"
	} else if len(pa.Patterns) > 0 {
		pa.Recommendation = "观望：形态信号不明确，建议等待"
	} else {
		pa.Recommendation = "无明显形态，根据其他指标决策"
	}

	// 结合当前价格位置
	if positionPct, ok := pa.KeyLevels["position_pct"]; ok {
		if positionPct > 80 {
			pa.Recommendation += "；当前价格接近区间顶部，注意阻力"
		} else if positionPct < 20 {
			pa.Recommendation += "；当前价格接近区间底部，注意支撑"
		}
	}
}

// FormatForPrompt 格式化为AI Prompt文本
func (pa *PatternAnalysis) FormatForPrompt() string {
	if pa == nil {
		return "无K线形态分析数据"
	}

	text := fmt.Sprintf("### K线形态分析 (%s %s)\n", pa.Symbol, pa.Interval)
	text += fmt.Sprintf("**总结**: %s\n", pa.Summary)
	text += fmt.Sprintf("**建议**: %s\n\n", pa.Recommendation)

	if len(pa.Patterns) > 0 {
		text += fmt.Sprintf("**识别形态 (%d个)**:\n", len(pa.Patterns))
		for _, pattern := range pa.Patterns {
			emoji := "🔵"
			if pattern.Type == "bullish" {
				emoji = "🟢"
			} else if pattern.Type == "bearish" {
				emoji = "🔴"
			}
			text += fmt.Sprintf("- %s %s (置信度:%.0f%%) - %s [位置:%d]\n",
				emoji, pattern.Name, pattern.Confidence, pattern.Description, pattern.Position)
		}
		text += "\n"
	} else {
		text += "**识别形态**: 无\n\n"
	}

	if len(pa.KeyLevels) > 0 {
		text += "**关键价位**:\n"
		if currentPrice, ok := pa.KeyLevels["current_price"]; ok {
			text += fmt.Sprintf("- 当前价格: %.2f\n", currentPrice)
		}
		if high20, ok := pa.KeyLevels["high_20"]; ok {
			text += fmt.Sprintf("- 20周期最高: %.2f\n", high20)
		}
		if low20, ok := pa.KeyLevels["low_20"]; ok {
			text += fmt.Sprintf("- 20周期最低: %.2f\n", low20)
		}
		if positionPct, ok := pa.KeyLevels["position_pct"]; ok {
			text += fmt.Sprintf("- 区间位置: %.1f%%\n", positionPct)
		}
		text += "\n"
	}

	if len(pa.SupportLevels) > 0 {
		text += fmt.Sprintf("**支撑位 (%d个)**: ", len(pa.SupportLevels))
		for i, level := range pa.SupportLevels {
			if i > 0 {
				text += ", "
			}
			text += fmt.Sprintf("%.2f", level)
		}
		text += "\n"
	} else {
		text += "**支撑位**: 无\n"
	}

	if len(pa.ResistanceLevels) > 0 {
		text += fmt.Sprintf("**阻力位 (%d个)**: ", len(pa.ResistanceLevels))
		for i, level := range pa.ResistanceLevels {
			if i > 0 {
				text += ", "
			}
			text += fmt.Sprintf("%.2f", level)
		}
		text += "\n"
	} else {
		text += "**阻力位**: 无\n"
	}

	// 添加趋势线信息
	if len(pa.TrendLines) > 0 {
		text += fmt.Sprintf("**趋势线 (%d条)**:\n", len(pa.TrendLines))
		for _, tl := range pa.TrendLines {
			trendType := "支撑"
			if tl.Type == "resistance" {
				trendType = "阻力"
			}
			text += fmt.Sprintf("- %s趋势线: %.2f → %.2f (斜率:%.4f, 强度:%.0f%%)\n",
				trendType, tl.StartPrice, tl.EndPrice, tl.Slope, tl.Strength)
		}
		text += "\n"
	}

	return text
}

// FormatKlineVisualization 生成K线ASCII可视化图表，让AI能够直观看到K线形态
// 返回简化的K线数据描述，更适合AI理解
func FormatKlineVisualization(klines []market.Kline, symbol string, interval string, maxBars int) string {
	if len(klines) == 0 {
		return ""
	}

	// 限制显示的K线数量（避免太长）
	displayKlines := klines
	if len(klines) > maxBars {
		displayKlines = klines[len(klines)-maxBars:]
	}

	// 计算关键数据
	minPrice := displayKlines[0].Low
	maxPrice := displayKlines[0].High
	var totalVolume float64
	upCount := 0
	downCount := 0

	for _, k := range displayKlines {
		if k.Low < minPrice {
			minPrice = k.Low
		}
		if k.High > maxPrice {
			maxPrice = k.High
		}
		totalVolume += k.Volume
		if k.Close > k.Open {
			upCount++
		} else if k.Close < k.Open {
			downCount++
		}
	}

	lastKline := displayKlines[len(displayKlines)-1]
	firstKline := displayKlines[0]
	priceChange := ((lastKline.Close - firstKline.Open) / firstKline.Open) * 100

	// 生成简化的K线描述（更适合AI理解）
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("\n### %s %s K线数据概览\n\n", symbol, interval))
	sb.WriteString(fmt.Sprintf("**价格范围**: %.2f - %.2f (波动: %.2f%%)\n", minPrice, maxPrice, ((maxPrice-minPrice)/minPrice)*100))
	sb.WriteString(fmt.Sprintf("**当前价格**: %.2f (较期初: %+.2f%%)\n", lastKline.Close, priceChange))
	sb.WriteString(fmt.Sprintf("**K线数量**: %d根 | 上涨: %d根 | 下跌: %d根 | 平盘: %d根\n", len(displayKlines), upCount, downCount, len(displayKlines)-upCount-downCount))
	sb.WriteString(fmt.Sprintf("**总成交量**: %.2f\n\n", totalVolume))

	// 显示最近10根K线的详细信息
	sb.WriteString("**最近10根K线详情**:\n")
	startIdx := len(displayKlines) - 10
	if startIdx < 0 {
		startIdx = 0
	}
	for i := startIdx; i < len(displayKlines); i++ {
		k := displayKlines[i]
		change := ((k.Close - k.Open) / k.Open) * 100
		changeSymbol := "→"
		if change > 0 {
			changeSymbol = "↑"
		} else if change < 0 {
			changeSymbol = "↓"
		}
		timeStr := time.Unix(k.OpenTime/1000, 0).Format("15:04")
		sb.WriteString(fmt.Sprintf("  %s %s O:%.2f H:%.2f L:%.2f C:%.2f (%.2f%%) V:%.0f\n",
			timeStr, changeSymbol, k.Open, k.High, k.Low, k.Close, change, k.Volume))
	}
	sb.WriteString("\n")

	return sb.String()
}

