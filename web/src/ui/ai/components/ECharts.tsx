import { memo, useEffect, useRef } from 'react'
import { init, use, type ECharts as EChartsInstance } from 'echarts/core'
import type { EChartsOption } from 'echarts'
import { LineChart, PieChart } from 'echarts/charts'
import { GridComponent, TooltipComponent } from 'echarts/components'
import { LabelLayout, UniversalTransition } from 'echarts/features'
import { CanvasRenderer } from 'echarts/renderers'

use([
  LineChart,
  PieChart,
  GridComponent,
  TooltipComponent,
  LabelLayout,
  UniversalTransition,
  CanvasRenderer,
])

interface EChartsProps {
  option: EChartsOption
  style?: React.CSSProperties
  className?: string
  theme?: 'light' | 'dark'
  onChartReady?: (chart: EChartsInstance) => void | (() => void)
}

/**
 * Lightweight React lifecycle wrapper for Apache ECharts. Initializes,
 * updates via setOption, observes container resize, and disposes on unmount.
 *
 * Theme changes are handled by the parent: pass `key` or `theme` prop to force
 * a remount so the chart instance is re-created with the correct theme variant.
 */
export const ECharts = memo(function ECharts({ option, style, className, theme, onChartReady }: EChartsProps) {
  const containerRef = useRef<HTMLDivElement>(null)
  const chartRef = useRef<EChartsInstance | null>(null)

  // Init + dispose
  useEffect(() => {
    const el = containerRef.current
    if (!el) return

    const chart = init(el, theme)
    chartRef.current = chart
    const cleanupReady = onChartReady?.(chart)

    const observer = new ResizeObserver(() => chart.resize())
    observer.observe(el)

    return () => {
      cleanupReady?.()
      observer.disconnect()
      chart.dispose()
      chartRef.current = null
    }
  }, [onChartReady, theme])

  // Update option
  useEffect(() => {
    chartRef.current?.setOption(option, { notMerge: true })
  }, [option])

  return <div ref={containerRef} style={style} className={className} />
})

export type { EChartsProps }
export default ECharts
