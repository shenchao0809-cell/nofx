import React, { useEffect, useRef, useState } from 'react';
import { createChart, CandlestickData, Time, CandlestickSeries } from 'lightweight-charts';
import { useAuth } from '../contexts/AuthContext';
import { api } from '../lib/api';

// 格式化时间为中国时区（UTC+8）
const formatChinaTime = (timestamp: number): string => {
  // timestamp是Unix时间戳（秒）
  const date = new Date(timestamp * 1000);
  // 使用Intl.DateTimeFormat格式化为中国时区
  const formatter = new Intl.DateTimeFormat('zh-CN', {
    timeZone: 'Asia/Shanghai',
    hour: '2-digit',
    minute: '2-digit',
    hour12: false,
  });
  return formatter.format(date);
};

// K线图表组件 - 显示实时K线和形态分析
interface KlineData {
  openTime: number;
  open: number;
  high: number;
  low: number;
  close: number;
  volume: number;
}

interface PatternSignal {
  name: string;
  type: string;
  confidence: number;
  description: string;
  position: number;
}

interface PatternAnalysis {
  symbol: string;
  interval: string;
  patterns: PatternSignal[];
  support_levels: number[];
  resistance_levels: number[];
  key_levels: Record<string, number>;
  summary: string;
  recommendation: string;
}

interface KlineChartProps {
  symbol?: string;  // 可选：直接指定币种
  traderId?: string;  // 可选：交易员ID，用于获取配置的币种
  interval?: string;
  height?: number;
  autoRefresh?: boolean;  // 是否自动刷新（默认true）
  refreshInterval?: number;  // 刷新间隔（毫秒，默认3000=3秒，实时更新）
}

const KlineChart: React.FC<KlineChartProps> = ({ 
  symbol: propSymbol, 
  traderId,
  interval = '1h',
  height = 400,
  autoRefresh = true,
  refreshInterval: _refreshInterval = 3000  // 参数保留用于兼容，但实际使用30秒固定间隔
}) => {
  const { token } = useAuth();
  const chartContainerRef = useRef<HTMLDivElement>(null);
  const chartRef = useRef<any>(null);
  const candlestickSeriesRef = useRef<any>(null);
  const priceLinesRef = useRef<any[]>([]);  // 保存所有价格线的引用，用于清理
  
  const [klineData, setKlineData] = useState<KlineData[]>([]);
  const [patternAnalysis, setPatternAnalysis] = useState<PatternAnalysis | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [currentSymbol, setCurrentSymbol] = useState<string | null>(propSymbol || null);
  const [traderSymbols, setTraderSymbols] = useState<string[]>([]);
  const [traderTimeframes, setTraderTimeframes] = useState<string[]>([]);  // 交易员配置的时间周期
  const [currentInterval, setCurrentInterval] = useState<string>(interval);  // 当前显示的时间周期
  const [realtimePrice, setRealtimePrice] = useState<number | null>(null);
  const [priceChange, setPriceChange] = useState<'up' | 'down' | null>(null);
  const [isChartReady, setIsChartReady] = useState(false);  // 图表是否已准备好
  const [configRefreshKey, setConfigRefreshKey] = useState(0);  // 配置刷新触发器
  const prevPriceRef = useRef<number | null>(null); // 使用useRef避免闭包问题

  // 监听页面可见性变化，自动刷新配置（用户修改配置后切换回来时会自动更新）
  useEffect(() => {
    const handleVisibilityChange = () => {
      if (!document.hidden && traderId) {
        console.log('[KlineChart] 页面重新可见，刷新交易员配置');
        setConfigRefreshKey(prev => prev + 1);
      }
    };

    document.addEventListener('visibilitychange', handleVisibilityChange);
    return () => {
      document.removeEventListener('visibilitychange', handleVisibilityChange);
    };
  }, [traderId]);

  // ✅ 实时更新最后一根K线（不重新加载所有数据）
  useEffect(() => {
    if (!currentSymbol || !token || !autoRefresh || !isChartReady) {
      return;
    }

    // 根据时间周期调整更新频率，避免触发速率限制
    const getUpdateInterval = (interval: string): number => {
      switch (interval) {
        case '1m':
          return 15000; // 1分钟周期：每15秒更新
        case '3m':
          return 20000; // 3分钟周期：每20秒更新
        case '15m':
          return 30000; // 15分钟周期：每30秒更新
        case '1h':
        case '4h':
        case '1d':
          return 60000; // 1小时及以上：每60秒更新
        default:
          return 30000; // 默认30秒
      }
    };

    const updateInterval = getUpdateInterval(currentInterval);
    console.log(`[KlineChart] 🚀 启动实时K线更新: ${currentSymbol}, 周期=${currentInterval}, 间隔=${updateInterval/1000}秒`);
    
    let isMounted = true;
    let retryCount = 0;
    const maxRetries = 3;

    const updateLastKline = async () => {
      if (!isMounted) return;
      
      try {
        // 获取最新的K线数据（只获取最后2根，用于更新）
        const response = await fetch(
          `/api/klines?symbol=${currentSymbol}&interval=${currentInterval}&limit=2`,
          {
            headers: {
              'Authorization': `Bearer ${token}`,
            },
          }
        );

        if (!isMounted) return;

        // 处理429错误（速率限制）
        if (response.status === 429) {
          console.warn(`[KlineChart] ⚠️ 触发速率限制，暂停更新60秒`);
          retryCount++;
          if (retryCount < maxRetries) {
            // 指数退避：等待更长时间后重试
            setTimeout(() => {
              if (isMounted) updateLastKline();
            }, 60000); // 等待60秒
          }
          return;
        }

        if (!response.ok) {
          console.error(`[KlineChart] ❌ 获取K线失败: ${response.status}`);
          return;
        }

        // 请求成功，重置重试计数
        retryCount = 0;

        const result = await response.json();
        const latestKlines = result.klines || [];
        
        if (latestKlines.length > 0) {
          const lastKline = latestKlines[latestKlines.length - 1];
          
          // 更新图表中的最后一根K线
          if (candlestickSeriesRef.current && klineData.length > 0) {
            const chartData: CandlestickData[] = klineData.map(k => ({
              time: Math.floor(k.openTime / 1000) as Time,
              open: k.open,
              high: k.high,
              low: k.low,
              close: k.close,
            }));
            
            // 更新最后一根K线
            const lastChartKline = chartData[chartData.length - 1];
            const newLastKline: CandlestickData = {
              time: Math.floor(lastKline.openTime / 1000) as Time,
              open: lastKline.open,
              high: lastKline.high,
              low: lastKline.low,
              close: lastKline.close,
            };
            
            // 如果时间戳相同，更新；如果不同，添加新的
            if (lastChartKline.time === newLastKline.time) {
              candlestickSeriesRef.current.update(newLastKline);
              console.log(`[KlineChart] ✅ 更新K线: ${currentSymbol} @ ${lastKline.close.toFixed(2)}`);
            } else {
              // 新的K线周期开始，添加新K线
              candlestickSeriesRef.current.update(newLastKline);
              // 同时更新状态数组
              setKlineData(prev => {
                const newData = [...prev];
                newData[newData.length - 1] = lastKline;
                return newData;
              });
              console.log(`[KlineChart] 🆕 新K线周期: ${currentSymbol} @ ${lastKline.close.toFixed(2)}`);
            }
            
            // 更新实时价格显示
            const newPrice = parseFloat(lastKline.close);
            const oldPrice = prevPriceRef.current;
            
            if (oldPrice !== null && Math.abs(newPrice - oldPrice) > 0.01) {
              setPriceChange(newPrice > oldPrice ? 'up' : 'down');
              setTimeout(() => {
                if (isMounted) setPriceChange(null);
              }, 500);
            }
            
            prevPriceRef.current = newPrice;
            setRealtimePrice(newPrice);
          }
        }
      } catch (err) {
        console.error(`[KlineChart] ❌ 更新K线失败:`, err);
      }
    };

    // 立即执行一次
    updateLastKline();

    // 根据时间周期动态调整更新频率
    const timer = setInterval(updateLastKline, updateInterval);

    return () => {
      console.log(`[KlineChart] 清理实时K线更新: ${currentSymbol}`);
      isMounted = false;
      clearInterval(timer);
    };
  }, [currentSymbol, currentInterval, token, autoRefresh, isChartReady, klineData]);

  // 独立的实时价格获取（已禁用，使用上面的K线更新代替）
  useEffect(() => {
    // 禁用独立的实时价格更新，统一使用K线更新
    return;
    
    if (!currentSymbol || !token || !autoRefresh) {
      console.log(`[KlineChart] 实时价格更新被跳过: currentSymbol=${currentSymbol}, token=${!!token}, autoRefresh=${autoRefresh}`);
      // 重置状态
      setRealtimePrice(null);
      prevPriceRef.current = null;
      return;
    }

    // 使用更短的间隔（1秒）来获取实时价格，实现价格跳动效果
    const realtimePriceInterval = 1000; // 1秒更新一次价格
    console.log(`[KlineChart] 开始实时价格更新循环: ${currentSymbol}, 间隔=${realtimePriceInterval}ms`);

    let isMounted = true; // 防止组件卸载后更新状态

    const fetchRealtimePrice = async () => {
      if (!isMounted) return;
      
      try {
        console.log(`[KlineChart] 正在获取实时价格: ${currentSymbol}`);
        // 获取1分钟K线的最新价格（最实时）
        const response = await fetch(
          `/api/klines?symbol=${currentSymbol}&interval=1m&limit=1`,
          {
            headers: {
              'Authorization': `Bearer ${token}`,
            },
          }
        );

        if (!isMounted) return;

        if (response.ok) {
          const result = await response.json();
          const klines = result.klines || [];
          if (klines.length > 0) {
            const newPrice = parseFloat(klines[klines.length - 1].close);
            const oldPrice = prevPriceRef.current;
            
            console.log(`[KlineChart] 价格数据: 新价格=${newPrice.toFixed(2)}, 旧价格=${oldPrice !== null ? oldPrice.toFixed(2) : 'null'}`);
            
            // 检测价格变化方向（只有当价格真正变化时才更新）
            if (oldPrice !== null && Math.abs(newPrice - oldPrice) > 0.01) {
              if (newPrice > oldPrice) {
                console.log(`[KlineChart] 价格上涨: ${oldPrice.toFixed(2)} -> ${newPrice.toFixed(2)}`);
                setPriceChange('up');
                // 500ms后清除动画效果
                setTimeout(() => {
                  if (isMounted) setPriceChange(null);
                }, 500);
              } else if (newPrice < oldPrice) {
                console.log(`[KlineChart] 价格下跌: ${oldPrice.toFixed(2)} -> ${newPrice.toFixed(2)}`);
                setPriceChange('down');
                // 500ms后清除动画效果
                setTimeout(() => {
                  if (isMounted) setPriceChange(null);
                }, 500);
              }
            } else if (oldPrice === null) {
              console.log(`[KlineChart] 首次设置价格: ${newPrice.toFixed(2)}`);
            } else {
              console.log(`[KlineChart] 价格无变化: ${newPrice.toFixed(2)} (变化量: ${Math.abs(newPrice - oldPrice).toFixed(4)})`);
            }
            
            // 更新价格（无论是否变化都更新，确保显示最新价格）
            prevPriceRef.current = newPrice;
            setRealtimePrice(newPrice);
            console.log(`[KlineChart] ✅ 实时价格已更新: ${currentSymbol} = ${newPrice.toFixed(2)}`);
          } else {
            console.warn(`[KlineChart] ⚠️ 获取到的K线数据为空: ${currentSymbol}`);
          }
        } else {
          const errorText = await response.text();
          console.error(`[KlineChart] ❌ 获取实时价格失败: ${response.status} - ${errorText}`);
        }
      } catch (err) {
        console.error('[KlineChart] ❌ 获取实时价格异常:', err);
      }
    };

    // 立即执行一次
    fetchRealtimePrice();

    // 设置定时器
    const priceTimer = setInterval(() => {
      if (isMounted) {
        fetchRealtimePrice();
      }
    }, realtimePriceInterval);

    return () => {
      console.log(`[KlineChart] 清理实时价格更新: ${currentSymbol}`);
      isMounted = false;
      clearInterval(priceTimer);
      // 清理时重置状态
      prevPriceRef.current = null;
    };
  }, [currentSymbol, token, autoRefresh]);

  // 获取交易员配置中的币种列表
  useEffect(() => {
    if (!traderId || !token) {
      // 如果没有traderId但有propSymbol，直接使用propSymbol
      if (propSymbol) {
        setCurrentSymbol(propSymbol);
      } else if (!traderId) {
        // 如果既没有traderId也没有propSymbol，使用BTCUSDT作为默认
        setCurrentSymbol('BTCUSDT');
      }
      return;
    }

    const fetchTraderConfig = async () => {
      try {
        const config = await api.getTraderConfig(traderId);
        
        // 解析币种列表
        if (config.trading_symbols) {
          const symbols = config.trading_symbols
            .split(',')
            .map((s: string) => s.trim())
            .filter((s: string) => s.length > 0);
          
          setTraderSymbols(symbols);
          
          // 如果没有指定symbol，使用配置中的第一个币种
          if (!propSymbol && symbols.length > 0) {
            setCurrentSymbol(symbols[0]);
          }
        } else {
          // 如果配置中没有trading_symbols，使用BTCUSDT作为默认
          if (!propSymbol) {
            setCurrentSymbol('BTCUSDT');
          }
        }
        
        // 解析时间周期列表
        if (config.timeframes) {
          const timeframes = config.timeframes
            .split(',')
            .map((t: string) => t.trim())
            .filter((t: string) => t.length > 0);
          
          console.log('KlineChart: 获取到时间周期配置:', timeframes);
          setTraderTimeframes(timeframes);
          
          // 如果配置的第一个时间周期和当前不同，切换到配置的第一个
          if (timeframes.length > 0) {
            setCurrentInterval(timeframes[0]);
          }
        } else {
          // 如果没有配置时间周期，使用默认值
          console.log('KlineChart: 未配置时间周期，使用默认4h');
          setTraderTimeframes(['4h']);
          setCurrentInterval('4h');
        }
      } catch (err) {
        console.error('获取交易员配置失败:', err);
        // 如果获取配置失败，使用BTCUSDT作为fallback
        if (!propSymbol) {
          setCurrentSymbol('BTCUSDT');
        }
        setTraderTimeframes(['4h']);
        setCurrentInterval('4h');
      }
    };

    fetchTraderConfig();
  }, [traderId, token, propSymbol, configRefreshKey]);  // 添加 configRefreshKey 以支持手动刷新

  // 获取K线数据
  useEffect(() => {
    if (!currentSymbol || !token) {
      if (!currentSymbol) {
        console.log('KlineChart: 等待币种设置...', { traderId, propSymbol });
      }
      if (!token) {
        console.log('KlineChart: 等待登录...');
      }
      return;
    }

    const fetchKlineData = async () => {
      try {
        // 只在首次加载时设置loading，避免频繁刷新
        if (klineData.length === 0) {
        setLoading(true);
        }
        setError(null);

        // 根据时间周期动态调整K线数量，避免图表过于拥挤
        const getKlineLimit = (interval: string): number => {
          switch (interval) {
            case '1m':
            case '3m':
            case '5m':
              return 500;  // 短周期：获取更多数据（约8-40小时）
            case '15m':
            case '30m':
              return 300;  // 中短周期：约3-10天
            case '1h':
            case '2h':
              return 200;  // 中期：约8-16天
            case '4h':
            case '6h':
            case '8h':
              return 150;  // 中长期：约25-50天
            case '12h':
              return 120;  // 长期：约60天
            case '1d':
              return 90;   // 日线：约3个月
            case '3d':
              return 60;   // 3日线：约6个月
            case '1w':
              return 52;   // 周线：约1年
            case '1M':
              return 24;   // 月线：约2年
            default:
              return 200;
          }
        };

        const limit = getKlineLimit(currentInterval);
        console.log(`KlineChart: 获取K线数据 ${currentSymbol} ${currentInterval}, 数量=${limit}根`);

        // 获取K线数据（使用当前选择的时间周期和对应的数据量）
        const klineResponse = await fetch(
          `/api/klines?symbol=${currentSymbol}&interval=${currentInterval}&limit=${limit}`,
          {
            headers: {
              'Authorization': `Bearer ${token}`,
            },
          }
        );

        if (!klineResponse.ok) {
          const errorText = await klineResponse.text();
          console.error('KlineChart: API错误', klineResponse.status, errorText);
          throw new Error(`获取K线数据失败: ${klineResponse.status} ${errorText}`);
        }

        const klineResult = await klineResponse.json();
        const klines = klineResult.klines || [];
        console.log(`KlineChart: 获取到${klines.length}根K线数据`);
        
        if (klines.length === 0) {
          console.warn('KlineChart: 获取到的K线数据为空');
          setLoading(false);
          setError('未获取到K线数据');
          return;
        }
        
        setKlineData(klines);
        
        // 🔧 关键修复：获取到数据后立即设置loading=false，让图表可以初始化
        setLoading(false);
        console.log('[KlineChart] ✅ K线数据已获取，loading设为false，允许图表初始化');

        // 注意：实时价格现在由独立的useEffect处理（每1秒更新），这里不再设置
        // 这样可以避免价格更新被K线数据刷新覆盖，实现价格实时跳动效果

        // 获取形态分析（使用当前选择的时间周期）
        const patternResponse = await fetch(
          `/api/klines/pattern-analysis?symbol=${currentSymbol}&interval=${currentInterval}&limit=100`,
          {
            headers: {
              'Authorization': `Bearer ${token}`,
            },
          }
        );

        if (patternResponse.ok) {
          const patternResult = await patternResponse.json();
          setPatternAnalysis(patternResult.analysis);
        }

      } catch (err) {
        console.error('获取K线数据失败:', err);
        setError(err instanceof Error ? err.message : '未知错误');
        setLoading(false);
      }
    };

    fetchKlineData();

    // 自动刷新逻辑 - 使用更长的间隔完全重新加载（降低频率，实时更新由另一个useEffect处理）
    if (autoRefresh) {
      const refreshTimer = setInterval(() => {
        console.log(`[KlineChart] 📊 完全刷新K线数据: ${currentSymbol}`);
        fetchKlineData();
      }, 300000); // 5分钟完全刷新一次，防止数据偏移和触发速率限制

      return () => clearInterval(refreshTimer);
    }
  }, [currentSymbol, currentInterval, token, autoRefresh, klineData]);

  // 初始化图表（确保DOM准备好后再初始化）
  useEffect(() => {
    // 如果图表已存在，跳过初始化
    if (chartRef.current) {
      console.log('[KlineChart] 图表已存在，跳过初始化');
      return;
    }

    // 如果还在loading，等待loading完成
    if (loading) {
      console.log('[KlineChart] 等待loading完成...');
      return;
    }

    let chart: any = null;
    let handleResize: (() => void) | null = null;
    let timer: number | null = null;
    let rafId: number | null = null;

    const initializeChart = () => {
      if (!chartContainerRef.current) {
        console.error('[KlineChart] 图表容器不存在');
        return;
      }

      if (chartRef.current) {
        console.log('[KlineChart] 图表已存在，跳过初始化');
        return;
      }

      console.log('[KlineChart] 开始初始化图表...');

    // 创建图表
      chart = createChart(chartContainerRef.current, {
      width: chartContainerRef.current.clientWidth,
      height: height,
      layout: {
        background: { color: '#1a1a1a' },
        textColor: '#d1d4dc',
      },
      grid: {
        vertLines: { color: '#2a2e39' },
        horzLines: { color: '#2a2e39' },
      },
      crosshair: {
        mode: 1,
      },
      rightPriceScale: {
        borderColor: '#2a2e39',
      },
      timeScale: {
        borderColor: '#2a2e39',
        timeVisible: true,
        secondsVisible: false,
          rightOffset: 12,
          barSpacing: 3,
          fixLeftEdge: false,
          fixRightEdge: false,
          lockVisibleTimeRangeOnResize: true,
          rightBarStaysOnScroll: true,
        },
        // 设置本地化选项，使用中国时区格式化时间
        localization: {
          locale: 'zh-CN',
          // 自定义时间格式化函数，确保使用中国时区（UTC+8）
          timeFormatter: (businessDayOrTimestamp: any) => {
            // 如果是时间戳（数字），格式化为中国时区
            if (typeof businessDayOrTimestamp === 'number') {
              return formatChinaTime(businessDayOrTimestamp);
            }
            // 如果是businessDay对象，转换为时间戳后格式化
            if (businessDayOrTimestamp && typeof businessDayOrTimestamp === 'object') {
              const date = new Date(
                businessDayOrTimestamp.year,
                businessDayOrTimestamp.month - 1,
                businessDayOrTimestamp.day
              );
              return formatChinaTime(Math.floor(date.getTime() / 1000));
            }
            // 默认返回原始值
            return String(businessDayOrTimestamp);
          },
      },
    });

    chartRef.current = chart;
      console.log('[KlineChart] 图表对象已创建');

    // 创建K线系列
    if (!chart || typeof chart.addSeries !== 'function') {
      console.error('KlineChart: addSeries method not found on chart object');
      setError('K线图表初始化失败：图表库方法不可用');
      if (chart && typeof chart.remove === 'function') {
        chart.remove();
      }
        chartRef.current = null;
      return;
    }

    let candlestickSeries: any;
    try {
      candlestickSeries = chart.addSeries(CandlestickSeries, {
        upColor: '#26a69a',
        downColor: '#ef5350',
        borderVisible: false,
        wickUpColor: '#26a69a',
        wickDownColor: '#ef5350',
      });

      candlestickSeriesRef.current = candlestickSeries;
        setIsChartReady(true);  // 标记图表已准备好
        console.log('[KlineChart] ✅ K线系列已创建，图表初始化完成');
    } catch (err) {
      console.error('KlineChart: Failed to create candlestick series', err);
      setError(`K线图表初始化失败: ${err instanceof Error ? err.message : String(err)}`);
      if (chart && typeof chart.remove === 'function') {
        chart.remove();
      }
      chartRef.current = null;
        candlestickSeriesRef.current = null;
      return;
    }

    // 自适应大小
      handleResize = () => {
      if (chartContainerRef.current && chart) {
        chart.applyOptions({
          width: chartContainerRef.current.clientWidth,
        });
      }
    };

    window.addEventListener('resize', handleResize);
    };

    // 等待DOM准备好 - 使用更可靠的方式
    const tryInitialize = () => {
      if (chartContainerRef.current && !chartRef.current && !loading) {
        console.log('[KlineChart] DOM容器已准备好，开始初始化图表');
        initializeChart();
        return true;
      }
      return false;
    };

    // 立即尝试一次
    if (tryInitialize()) {
      return;
    }

    // 如果DOM还没准备好，使用requestAnimationFrame循环等待
    console.log('[KlineChart] 等待DOM容器准备好...');
    let attempts = 0;
    const maxAttempts = 50; // 最多尝试50次（约1秒）
    
    const checkAndInit = () => {
      attempts++;
      if (tryInitialize()) {
        return;
      }
      if (attempts < maxAttempts) {
        rafId = requestAnimationFrame(checkAndInit);
      } else {
        console.error('[KlineChart] DOM容器等待超时，可能DOM还未渲染');
      }
    };
    
    rafId = requestAnimationFrame(checkAndInit);

    // 清理函数
    return () => {
      if (rafId !== null) {
        cancelAnimationFrame(rafId);
      }
      if (timer) {
        clearTimeout(timer);
      }
      if (handleResize) {
      window.removeEventListener('resize', handleResize);
      }
      // 使用chartRef.current来清理图表，确保能访问到图表实例
      const currentChart = chartRef.current;
      if (currentChart && typeof currentChart.remove === 'function') {
        currentChart.remove();
      }
      chartRef.current = null;
      candlestickSeriesRef.current = null;
      priceLinesRef.current = [];  // 清空价格线引用
      setIsChartReady(false);  // 重置图表准备状态
    };
  }, [height, loading]);  // 添加loading依赖，当loading完成时重新尝试初始化

  // 更新K线数据（增量更新）
  useEffect(() => {
    // 等待图表初始化完成
    if (!isChartReady) {
      console.log('[KlineChart] 等待图表初始化完成... (isChartReady=false)');
      return;
    }
    
    if (klineData.length === 0) {
      console.log('[KlineChart] K线数据为空');
      return;
    }

    const candlestickSeries = candlestickSeriesRef.current;
    if (!candlestickSeries) {
      console.log('[KlineChart] candlestickSeries 引用丢失，等待重新初始化...');
      setIsChartReady(false);
      return;
    }
    
    // 转换数据格式
    // Binance API返回的时间戳是毫秒级（UTC时间），需要转换为秒级
    // Lightweight Charts会自动将UTC时间戳转换为本地时区显示
    const chartData: CandlestickData[] = klineData.map((k) => ({
      time: Math.floor(k.openTime / 1000) as Time,  // 确保转换为整数秒级时间戳
      open: k.open,
      high: k.high,
      low: k.low,
      close: k.close,
    }));

    console.log(`[KlineChart] 设置 K 线数据: ${chartData.length} 条`);
    
    // 调试：显示第一条和最后一条K线的时间戳
    if (chartData.length > 0) {
      const firstTimeNum = Number(chartData[0].time);
      const lastTimeNum = Number(chartData[chartData.length - 1].time);
      const firstTime = new Date(firstTimeNum * 1000).toLocaleString('zh-CN');
      const lastTime = new Date(lastTimeNum * 1000).toLocaleString('zh-CN');
      console.log(`[KlineChart] 时间范围: ${firstTime} ~ ${lastTime} (本地时区)`);
    }

    // 更新K线数据（lightweight-charts会自动处理增量更新）
    try {
    candlestickSeries.setData(chartData);
      console.log('[KlineChart] ✅ K 线数据已设置到图表');
    } catch (err) {
      console.error('[KlineChart] ❌ 设置 K 线数据失败:', err);
    }
  }, [klineData, isChartReady]);

  // 更新支撑阻力位和当前价格（增量更新）
  useEffect(() => {
    if (!candlestickSeriesRef.current || !patternAnalysis) return;

    const candlestickSeries = candlestickSeriesRef.current;
    
    // 🔧 关键修复：删除所有旧的价格线
    console.log(`[KlineChart] 清理旧价格线: ${priceLinesRef.current.length} 条`);
    priceLinesRef.current.forEach((priceLine) => {
      try {
        candlestickSeries.removePriceLine(priceLine);
      } catch (err) {
        // 忽略删除失败的错误
      }
    });
    priceLinesRef.current = [];  // 清空引用数组
    
    // 添加支撑位（绿色虚线）
    patternAnalysis.support_levels?.forEach((level) => {
      try {
        const priceLine = candlestickSeries.createPriceLine({
          price: level,
          color: '#26a69a',
          lineWidth: 1,
          lineStyle: 2, // 虚线
          axisLabelVisible: true,
          title: `支撑 ${level.toFixed(2)}`,
        });
        priceLinesRef.current.push(priceLine);  // 保存引用
      } catch (err) {
        console.error('[KlineChart] 创建支撑位失败:', err);
      }
    });

    // 添加阻力位（红色虚线）
    patternAnalysis.resistance_levels?.forEach((level) => {
      try {
        const priceLine = candlestickSeries.createPriceLine({
          price: level,
          color: '#ef5350',
          lineWidth: 1,
          lineStyle: 2,
          axisLabelVisible: true,
          title: `阻力 ${level.toFixed(2)}`,
        });
        priceLinesRef.current.push(priceLine);  // 保存引用
      } catch (err) {
        console.error('[KlineChart] 创建阻力位失败:', err);
      }
    });

    // 当前价格（黄色实线）
    if (patternAnalysis.key_levels?.current_price) {
      try {
        const priceLine = candlestickSeries.createPriceLine({
          price: patternAnalysis.key_levels.current_price,
          color: '#ffa726',
          lineWidth: 2,
          lineStyle: 0,
          axisLabelVisible: true,
          title: `当前 ${patternAnalysis.key_levels.current_price.toFixed(2)}`,
        });
        priceLinesRef.current.push(priceLine);  // 保存引用
      } catch (err) {
        console.error('[KlineChart] 创建当前价格线失败:', err);
      }
    }
    
    console.log(`[KlineChart] ✅ 创建新价格线: ${priceLinesRef.current.length} 条`);
  }, [patternAnalysis]);

  if (loading) {
    return (
      <div className="flex items-center justify-center" style={{ height: `${height}px` }}>
        <div className="text-gray-400">加载K线数据中...</div>
      </div>
    );
  }

  if (error) {
    return (
      <div className="flex items-center justify-center" style={{ height: `${height}px` }}>
        <div className="text-red-400">加载失败: {error}</div>
      </div>
    );
  }

  return (
    <div className="space-y-6">
      {/* 币种选择器（显示所有已配置的币种） */}
      {traderSymbols.length > 0 && (
        <div className="p-3 rounded-lg" style={{ background: '#0B0E11', border: '1px solid #2B3139' }}>
          <div className="flex items-center justify-between mb-2">
            <div className="flex items-center gap-2">
              <span className="text-xs font-medium" style={{ color: '#F0B90B' }}>📊 已配置币种</span>
              <span className="px-1.5 py-0.5 rounded text-xs" style={{ background: '#1E2329', color: '#848E9C' }}>
                {traderSymbols.length} 个
              </span>
              {traderId && (
                <button
                  onClick={() => {
                    console.log('[KlineChart] 手动刷新配置');
                    setConfigRefreshKey(prev => prev + 1);
                  }}
                  className="px-2 py-0.5 rounded text-xs transition-all hover:scale-105"
                  style={{
                    background: '#1E2329',
                    color: '#848E9C',
                    border: '1px solid #2B3139',
                  }}
                  title="刷新配置（修改交易员配置后点击此按钮）"
                >
                  🔄 刷新
                </button>
              )}
            </div>
            {currentSymbol && (
              <span className="text-xs" style={{ color: '#848E9C' }}>
                当前: <span style={{ color: '#F0B90B' }}>{currentSymbol}</span>
              </span>
            )}
          </div>
        <div className="flex items-center gap-2 flex-wrap">
          {traderSymbols.map((sym) => (
            <button
              key={sym}
              onClick={() => setCurrentSymbol(sym)}
                className={`px-3 py-1.5 rounded text-xs font-medium transition-all ${
                currentSymbol === sym
                    ? 'scale-105'
                    : 'hover:scale-105 hover:opacity-80'
              }`}
                style={{
                  background: currentSymbol === sym 
                    ? 'linear-gradient(135deg, #F0B90B 0%, #FFC107 100%)' 
                    : '#1E2329',
                  color: currentSymbol === sym ? '#000000' : '#EAECEF',
                  border: currentSymbol === sym ? '1px solid #F0B90B' : '1px solid #2B3139',
                  boxShadow: currentSymbol === sym ? '0 2px 8px rgba(240, 185, 11, 0.25)' : 'none',
                }}
            >
              {sym}
                {currentSymbol === sym && (
                  <span className="ml-1">✓</span>
                )}
            </button>
          ))}
          </div>
        </div>
      )}

      {/* 时间周期选择器（显示所有已配置的时间周期） */}
      {traderTimeframes.length > 0 && (
        <div className="p-3 rounded-lg" style={{ background: '#0B0E11', border: '1px solid #2B3139' }}>
          <div className="flex items-center justify-between mb-2">
            <div className="flex items-center gap-2">
              <span className="text-xs font-medium" style={{ color: '#0ECB81' }}>📈 K线时间周期</span>
              <span className="px-1.5 py-0.5 rounded text-xs" style={{ background: '#1E2329', color: '#848E9C' }}>
                {traderTimeframes.length} 个
              </span>
              {traderId && (
                <button
                  onClick={() => {
                    console.log('[KlineChart] 手动刷新配置');
                    setConfigRefreshKey(prev => prev + 1);
                  }}
                  className="px-2 py-0.5 rounded text-xs transition-all hover:scale-105"
                  style={{
                    background: '#1E2329',
                    color: '#848E9C',
                    border: '1px solid #2B3139',
                  }}
                  title="刷新配置（修改交易员配置后点击此按钮）"
                >
                  🔄 刷新
                </button>
            )}
          </div>
            {currentInterval && (
              <span className="text-xs" style={{ color: '#848E9C' }}>
                当前: <span style={{ color: '#0ECB81' }}>{currentInterval}</span>
              </span>
            )}
              </div>
          <div className="flex items-center gap-2 flex-wrap">
            {traderTimeframes.map((tf) => {
              // 时间周期显示名称映射
              const timeframeLabels: Record<string, string> = {
                '1m': '1分钟',
                '3m': '3分钟',
                '5m': '5分钟',
                '15m': '15分钟',
                '30m': '30分钟',
                '1h': '1小时',
                '2h': '2小时',
                '4h': '4小时',
                '6h': '6小时',
                '8h': '8小时',
                '12h': '12小时',
                '1d': '1天',
                '3d': '3天',
                '1w': '1周',
                '1M': '1月',
              };
              
              return (
                <button
                  key={tf}
                  onClick={() => setCurrentInterval(tf)}
                  className={`px-3 py-1.5 rounded text-xs font-medium transition-all ${
                    currentInterval === tf
                      ? 'scale-105'
                      : 'hover:scale-105 hover:opacity-80'
                  }`}
                  style={{
                    background: currentInterval === tf 
                      ? 'linear-gradient(135deg, #0ECB81 0%, #0DDC7D 100%)' 
                      : '#1E2329',
                    color: currentInterval === tf ? '#000000' : '#EAECEF',
                    border: currentInterval === tf ? '1px solid #0ECB81' : '1px solid #2B3139',
                    boxShadow: currentInterval === tf ? '0 2px 8px rgba(14, 203, 129, 0.25)' : 'none',
                  }}
                >
                  {timeframeLabels[tf] || tf}
                  {currentInterval === tf && (
                    <span className="ml-1">✓</span>
          )}
                </button>
              );
            })}
          </div>
        </div>
      )}


      {/* K线图表 */}
      <div className="relative">
        {loading && (
          <div 
            className="absolute inset-0 flex items-center justify-center z-10 rounded-lg"
            style={{ height: `${height}px`, background: 'rgba(0, 0, 0, 0.5)' }}
          >
            <div className="text-center">
              <div className="animate-spin rounded-full h-12 w-12 border-b-2 border-yellow-500 mx-auto mb-4"></div>
              <div className="text-gray-400">加载 K 线数据中...</div>
            </div>
          </div>
        )}
        <div 
          ref={chartContainerRef} 
          className="rounded-lg overflow-hidden" 
          style={{ 
            height: `${height}px`, 
            minHeight: `${height}px`,
            width: '100%',
            background: '#1a1a1a',
          }} 
        />
      </div>

      {/* 形态分析信息 */}
      {patternAnalysis && (
        <div className="rounded-lg p-4 space-y-3 mt-6" style={{ background: '#0B0E11', border: '1px solid #2B3139' }}>
          <div className="flex items-center justify-between">
            <h3 className="text-base font-semibold" style={{ color: '#EAECEF' }}>
              K线形态分析 ({patternAnalysis.interval})
            </h3>
            <span className="text-sm text-gray-400">{patternAnalysis.symbol}</span>
          </div>

          {/* 总结 */}
          <div className="text-sm text-gray-300">
            <span className="font-medium">总结：</span> {patternAnalysis.summary}
          </div>

          {/* 建议 */}
          <div className="text-sm">
            <span className="font-medium text-gray-300">建议：</span>{' '}
            <span
              className={
                patternAnalysis.recommendation.includes('偏多')
                  ? 'text-green-400'
                  : patternAnalysis.recommendation.includes('偏空')
                  ? 'text-red-400'
                  : 'text-yellow-400'
              }
            >
              {patternAnalysis.recommendation}
            </span>
          </div>

          {/* 识别的形态 */}
          {patternAnalysis.patterns && patternAnalysis.patterns.length > 0 && (
            <div className="space-y-2">
              <div className="text-sm font-medium text-gray-300">识别形态：</div>
              <div className="grid grid-cols-1 md:grid-cols-2 gap-2">
                {patternAnalysis.patterns.slice(0, 6).map((pattern, idx) => (
                  <div
                    key={idx}
                    className="flex items-start space-x-2 text-xs bg-gray-700 rounded p-2"
                  >
                    <span
                      className={
                        pattern.type === 'bullish'
                          ? 'text-green-400'
                          : pattern.type === 'bearish'
                          ? 'text-red-400'
                          : 'text-blue-400'
                      }
                    >
                      {pattern.type === 'bullish'
                        ? '🟢'
                        : pattern.type === 'bearish'
                        ? '🔴'
                        : '🔵'}
                    </span>
                    <div className="flex-1">
                      <div className="font-medium text-white">{pattern.name}</div>
                      <div className="text-gray-400">
                        {pattern.description} (置信度: {pattern.confidence.toFixed(0)}%)
                      </div>
                    </div>
                  </div>
                ))}
              </div>
            </div>
          )}

          {/* 关键价位 */}
          {patternAnalysis.key_levels && (
            <div className="grid grid-cols-2 md:grid-cols-4 gap-2 text-xs">
              {/* 优先显示实时价格，如果没有则使用形态分析中的价格 */}
              {(realtimePrice !== null || patternAnalysis.key_levels.current_price) && (
                <div className="bg-gray-700 rounded p-2 border-2" style={{ 
                  borderColor: priceChange === 'up' ? '#0ECB81' : priceChange === 'down' ? '#F6465D' : '#2B3139',
                  transition: 'border-color 0.3s ease'
                }}>
                  <div className="text-gray-400">当前价格</div>
                  <div className={`text-white font-medium flex items-center gap-1 transition-all duration-300 ${
                    priceChange === 'up' ? 'text-green-400' : priceChange === 'down' ? 'text-red-400' : ''
                  }`}>
                    <span>{realtimePrice !== null ? realtimePrice.toFixed(2) : patternAnalysis.key_levels.current_price?.toFixed(2)}</span>
                    {realtimePrice !== null && (
                      <span className="text-xs animate-pulse" style={{ color: '#0ECB81' }}>实时</span>
                    )}
                    {priceChange === 'up' && <span className="ml-1 text-xs">↑</span>}
                    {priceChange === 'down' && <span className="ml-1 text-xs">↓</span>}
                  </div>
                </div>
              )}
              {patternAnalysis.key_levels.high_20 && (
                <div className="bg-gray-700 rounded p-2">
                  <div className="text-gray-400">20周期最高</div>
                  <div className="text-red-400 font-medium">
                    {patternAnalysis.key_levels.high_20.toFixed(2)}
                  </div>
                </div>
              )}
              {patternAnalysis.key_levels.low_20 && (
                <div className="bg-gray-700 rounded p-2">
                  <div className="text-gray-400">20周期最低</div>
                  <div className="text-green-400 font-medium">
                    {patternAnalysis.key_levels.low_20.toFixed(2)}
                  </div>
                </div>
              )}
              {patternAnalysis.key_levels.position_pct !== undefined && (
                <div className="bg-gray-700 rounded p-2">
                  <div className="text-gray-400">区间位置</div>
                  <div className="text-yellow-400 font-medium">
                    {patternAnalysis.key_levels.position_pct.toFixed(1)}%
                  </div>
                </div>
              )}
            </div>
          )}
        </div>
      )}
    </div>
  );
};

export default KlineChart;

