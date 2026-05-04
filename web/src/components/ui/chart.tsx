"use client";

import * as React from "react";
import { cn } from "@/lib/utils";

export interface ChartConfig {
  [key: string]: {
    label: string;
    color: string;
    /** 可选：tooltip / legend 自定义后缀，例如 "次"。 */
    unit?: string;
  };
}

interface ChartContainerProps extends React.HTMLAttributes<HTMLDivElement> {
  config: ChartConfig;
}

const ChartContainer = React.forwardRef<HTMLDivElement, ChartContainerProps>(
  ({ className, config, children, ...props }, ref) => {
    const cssVars = Object.entries(config).reduce<Record<string, string>>(
      (acc, [key, value]) => {
        acc[`--color-${key}`] = value.color;
        return acc;
      },
      {},
    );

    return (
      <div
        ref={ref}
        className={cn("w-full", className)}
        style={cssVars as React.CSSProperties}
        {...props}
      >
        {children}
      </div>
    );
  },
);
ChartContainer.displayName = "ChartContainer";

interface ChartTooltipContentProps {
  active?: boolean;
  payload?: Array<{
    name: string;
    dataKey?: string;
    value: number;
    color: string;
  }>;
  label?: string;
  config?: ChartConfig;
  /** 给 label 做二次格式化（如 ISO 日期 → "5月3日 周日"）。 */
  labelFormatter?: (label: string) => React.ReactNode;
  /** 隐藏列名（label）下方的色彩条小注脚分隔线。 */
  hideIndicator?: boolean;
}

function ChartTooltipContent({
  active,
  payload,
  label,
  config,
  labelFormatter,
  hideIndicator = false,
}: ChartTooltipContentProps) {
  if (!active || !payload?.length) return null;

  // 计算总和让 tooltip 同时显示 stack 总量（堆叠图常见诉求）。
  const total = payload.reduce((s, p) => s + (Number(p.value) || 0), 0);
  const showTotal = payload.length >= 2;

  return (
    <div className="min-w-[10rem] rounded-lg border bg-popover/95 px-3 py-2.5 text-sm shadow-lg backdrop-blur-sm">
      {label !== undefined && (
        <p className="mb-1.5 text-xs font-medium text-muted-foreground">
          {labelFormatter ? labelFormatter(label) : label}
        </p>
      )}
      <div className="space-y-1">
        {payload.map((entry, i) => {
          const key = entry.dataKey ?? entry.name;
          const cfg = config?.[key];
          const cfgByName = config?.[entry.name];
          const display = cfg?.label ?? cfgByName?.label ?? entry.name;
          const unit = cfg?.unit ?? cfgByName?.unit;
          return (
            <div key={i} className="flex items-center gap-2">
              {!hideIndicator && (
                <span
                  aria-hidden
                  className="inline-block size-2.5 rounded-[3px] shrink-0 ring-1 ring-inset ring-black/5 dark:ring-white/10"
                  style={{ backgroundColor: entry.color || cfg?.color }}
                />
              )}
              <span className="flex-1 truncate text-muted-foreground">{display}</span>
              <span className="font-mono text-sm font-medium tabular-nums text-foreground">
                {Number(entry.value).toLocaleString()}
                {unit && <span className="ml-0.5 text-xs text-muted-foreground">{unit}</span>}
              </span>
            </div>
          );
        })}
        {showTotal && (
          <div className="mt-1 flex items-center gap-2 border-t pt-1">
            <span className="size-2.5 shrink-0 opacity-0" aria-hidden />
            <span className="flex-1 text-xs text-muted-foreground">合计</span>
            <span className="font-mono text-sm font-semibold tabular-nums">
              {total.toLocaleString()}
            </span>
          </div>
        )}
      </div>
    </div>
  );
}

interface ChartLegendItemProps {
  name: string;
  color: string;
  /** 可选：右侧数字（如该系列总量） */
  value?: number | string;
}

/** 简洁的图例条目；横向并排时配合 ChartLegend 容器使用。 */
function ChartLegendItem({ name, color, value }: ChartLegendItemProps) {
  return (
    <div className="flex items-center gap-1.5 text-xs">
      <span
        aria-hidden
        className="size-2.5 rounded-[3px] ring-1 ring-inset ring-black/5 dark:ring-white/10"
        style={{ backgroundColor: color }}
      />
      <span className="text-muted-foreground">{name}</span>
      {value !== undefined && (
        <span className="font-mono font-medium tabular-nums text-foreground">
          {typeof value === "number" ? value.toLocaleString() : value}
        </span>
      )}
    </div>
  );
}

export { ChartContainer, ChartTooltipContent, ChartLegendItem };
