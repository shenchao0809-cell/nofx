import React, { useEffect, useState } from 'react';
import { useAuth } from '../contexts/AuthContext';
import { api } from '../lib/api';
import KlineChart from './KlineChart';

interface MultiKlineChartProps {
  traderId: string;
  interval?: string;
  height?: number;
  autoRefresh?: boolean;
  refreshInterval?: number;
  displayMode?: 'tabs' | 'grid' | 'stack'; // 显示模式：标签页、网格、堆叠
}

/**
 * 多币种K线图组件
 * 根据交易员配置的trading_symbols显示多个币种的K线图
 * 用于在看板中实时显示所有配置币种的K线图，供机器人决策参考
 */
const MultiKlineChart: React.FC<MultiKlineChartProps> = ({
  traderId,
  interval = '1h',
  height = 400,
  autoRefresh = true,
  refreshInterval = 3000,  // 默认3秒刷新（实时更新）
  displayMode = 'stack', // 默认堆叠显示，方便查看所有币种
}) => {
  const { token } = useAuth();
  const [symbols, setSymbols] = useState<string[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [selectedSymbol, setSelectedSymbol] = useState<string | null>(null);

  // 获取交易员配置中的币种列表
  useEffect(() => {
    if (!traderId || !token) {
      setLoading(false);
      return;
    }

    const fetchTraderConfig = async () => {
      try {
        setLoading(true);
        setError(null);

        const config = await api.getTraderConfig(traderId);
        if (config.trading_symbols) {
          // 解析逗号分隔的币种列表
          const symbolList = config.trading_symbols
            .split(',')
            .map((s: string) => s.trim())
            .filter((s: string) => s.length > 0);

          if (symbolList.length > 0) {
            console.log(`[MultiKlineChart] 获取到 ${symbolList.length} 个币种:`, symbolList);
            setSymbols(symbolList);
            // 默认选择第一个币种（标签页模式）
            if (displayMode === 'tabs' && symbolList.length > 0) {
              setSelectedSymbol(symbolList[0]);
            }
          } else {
            // 如果没有配置币种，使用BTCUSDT作为默认
            setSymbols(['BTCUSDT']);
            if (displayMode === 'tabs') {
              setSelectedSymbol('BTCUSDT');
            }
          }
        } else {
          // 如果没有配置，使用BTCUSDT作为默认
          setSymbols(['BTCUSDT']);
          if (displayMode === 'tabs') {
            setSelectedSymbol('BTCUSDT');
          }
        }
      } catch (err) {
        console.error('获取交易员配置失败:', err);
        setError('获取交易员配置失败');
        // 使用BTCUSDT作为fallback
        setSymbols(['BTCUSDT']);
        if (displayMode === 'tabs') {
          setSelectedSymbol('BTCUSDT');
        }
      } finally {
        setLoading(false);
      }
    };

    fetchTraderConfig();
  }, [traderId, token, displayMode]);

  if (loading) {
    return (
      <div className="flex items-center justify-center" style={{ height: `${height}px` }}>
        <div className="text-gray-400">加载币种配置中...</div>
      </div>
    );
  }

  if (error) {
    return (
      <div className="flex items-center justify-center" style={{ height: `${height}px` }}>
        <div className="text-red-400">{error}</div>
      </div>
    );
  }

  if (symbols.length === 0) {
    return (
      <div className="flex items-center justify-center" style={{ height: `${height}px` }}>
        <div className="text-gray-400">未配置交易币种</div>
      </div>
    );
  }

  // 标签页模式
  if (displayMode === 'tabs') {
    return (
      <div className="space-y-4">
        {/* 币种标签页 */}
        <div className="flex items-center gap-2 flex-wrap border-b" style={{ borderColor: '#2B3139' }}>
          {symbols.map((symbol) => (
            <button
              key={symbol}
              onClick={() => setSelectedSymbol(symbol)}
              className={`px-4 py-2 text-sm font-medium transition-colors ${
                selectedSymbol === symbol
                  ? 'border-b-2'
                  : 'hover:opacity-80'
              }`}
              style={{
                color: selectedSymbol === symbol ? '#F0B90B' : '#848E9C',
                borderBottomColor: selectedSymbol === symbol ? '#F0B90B' : 'transparent',
              }}
            >
              {symbol}
            </button>
          ))}
        </div>

        {/* 显示选中的币种K线图 */}
        {selectedSymbol && (
          <KlineChart
            symbol={selectedSymbol}
            interval={interval}
            height={height}
            autoRefresh={autoRefresh}
            refreshInterval={refreshInterval}
          />
        )}
      </div>
    );
  }

  // 网格模式（2列）
  if (displayMode === 'grid') {
    return (
      <div className="grid grid-cols-1 md:grid-cols-2 gap-6">
        {symbols.map((symbol, index) => (
          <div key={symbol} className="space-y-2">
            <div className="flex items-center justify-between mb-2">
              <h3 className="text-lg font-semibold" style={{ color: '#EAECEF' }}>
                {symbol}
              </h3>
              <span className="text-xs" style={{ color: '#848E9C' }}>
                {index + 1} / {symbols.length}
              </span>
            </div>
            <KlineChart
              symbol={symbol}
              interval={interval}
              height={height}
              autoRefresh={autoRefresh}
              refreshInterval={refreshInterval}
            />
          </div>
        ))}
      </div>
    );
  }

  // 堆叠模式（默认）- 垂直排列所有币种
  return (
    <div className="space-y-6">
      {/* 显示当前配置的币种列表 */}
      <div className="mb-4 p-3 rounded" style={{ background: '#0B0E11', border: '1px solid #2B3139' }}>
        <div className="text-xs mb-2" style={{ color: '#848E9C' }}>
          已配置币种 ({symbols.length}个):
        </div>
        <div className="flex flex-wrap gap-2">
          {symbols.map((symbol) => (
            <span
              key={symbol}
              className="px-2 py-1 rounded text-xs font-mono"
              style={{
                background: '#1E2329',
                color: '#F0B90B',
                border: '1px solid #2B3139',
              }}
            >
              {symbol}
            </span>
          ))}
        </div>
        <div className="text-xs mt-2" style={{ color: '#848E9C' }}>
          刷新间隔: {refreshInterval / 1000}秒 | 时间周期: {interval}
        </div>
      </div>

      {/* 显示所有币种的K线图 */}
      {symbols.map((symbol, index) => (
        <div key={symbol} className="space-y-2">
          <div className="flex items-center justify-between mb-2">
            <h3 className="text-lg font-semibold" style={{ color: '#EAECEF' }}>
              {symbol} K线图表
            </h3>
            <span className="text-xs" style={{ color: '#848E9C' }}>
              {index + 1} / {symbols.length}
            </span>
          </div>
          <KlineChart
            symbol={symbol}
            interval={interval}
            height={height}
            autoRefresh={autoRefresh}
            refreshInterval={refreshInterval}
          />
        </div>
      ))}
    </div>
  );
};

export default MultiKlineChart;
