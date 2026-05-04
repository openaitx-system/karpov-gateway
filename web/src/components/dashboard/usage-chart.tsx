"use client";

import * as React from "react";
import { Activity, ArrowDownRight, ArrowUpRight, Minus } from "lucide-react";
import {
  Area,
  AreaChart,
  CartesianGrid,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from "recharts";

import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import {
  ChartContainer,
  ChartLegendItem,
  ChartTooltipContent,
  type ChartConfig,
} from "@/components/ui/chart";
import { cn } from "@/lib/utils";
import { usageApi, type UsageDayRecord } from "@/lib/api/usage";

const PERIOD_OPTIONS = [
  { id: 7, label: "7 天" },
  { id: 14, label: "14 天" },
  { id: 30, label: "30 天" },
  { id: 90, label: "90 天" },
] as const;

// 同主题下的 5 色 chart 调色板；按 provider 字典序循环分配，保证每次渲染一致。
const CHART_COLORS = [
  "var(--chart-1)",
  "var(--chart-2)",
  "var(--chart-3)",
  "var(--chart-4)",
  "var(--chart-5)",
] as const;

interface DailyRow {
  date: string;
  /** 当日所有 provider 合计（用于 tooltip 总量、KPI 计算）。 */
  total: number;
  /** 每个 provider 的计数，键名 = provider 名（如 "qqmusic"）。 */
  [provider: string]: number | string;
}

interface PivotResult {
  rows: DailyRow[];
  providers: string[];
  /** 每个 provider 在整个窗口内的累计调用次数。 */
  perProviderTotal: Record<string, number>;
}

/**
 * 把 [{date, provider, count}, ...] 旋转为 [{date, qqmusic, netease, ...}, ...]，
 * 同时补齐缺失日期的 0 值——堆叠图需要每个 X 都有所有 series 的值才能稳定渲染。
 */
function pivotByProvider(records: UsageDayRecord[], days: number): PivotResult {
  const providers = new Set<string>();
  const perDate = new Map<string, Record<string, number>>();
  for (const r of records) {
    if (!r.provider) continue;
    providers.add(r.provider);
    const cell = perDate.get(r.date) ?? {};
    cell[r.provider] = (cell[r.provider] ?? 0) + r.count;
    perDate.set(r.date, cell);
  }
  const provList = Array.from(providers).sort();

  // 用窗口长度生成连续日期序列（如果某天没数据则填 0），
  // 让 area 不出现"空洞"的视觉断点。
  const today = new Date();
  today.setHours(0, 0, 0, 0);
  const rows: DailyRow[] = [];
  for (let i = days - 1; i >= 0; i--) {
    const d = new Date(today);
    d.setDate(today.getDate() - i);
    const key = isoDate(d);
    const cell = perDate.get(key) ?? {};
    let total = 0;
    const row: DailyRow = { date: key, total: 0 };
    for (const p of provList) {
      const v = cell[p] ?? 0;
      row[p] = v;
      total += v;
    }
    row.total = total;
    rows.push(row);
  }

  const perProviderTotal: Record<string, number> = {};
  for (const p of provList) perProviderTotal[p] = 0;
  for (const row of rows) {
    for (const p of provList) {
      perProviderTotal[p] = (perProviderTotal[p] ?? 0) + Number(row[p] ?? 0);
    }
  }
  return { rows, providers: provList, perProviderTotal };
}

function isoDate(d: Date): string {
  const y = d.getFullYear();
  const m = String(d.getMonth() + 1).padStart(2, "0");
  const day = String(d.getDate()).padStart(2, "0");
  return `${y}-${m}-${day}`;
}

/** "2026-05-04" → "5/4"（轴标签紧凑显示） */
function shortAxisLabel(iso: string): string {
  const parts = iso.split("-");
  if (parts.length !== 3) return iso;
  const m = parts[1] ?? "";
  const d = parts[2] ?? "";
  return `${parseInt(m, 10) || 0}/${parseInt(d, 10) || 0}`;
}

const WEEKDAY_NAMES = ["周日", "周一", "周二", "周三", "周四", "周五", "周六"] as const;

/** "2026-05-04" → "5月4日 · 周一"（tooltip 头部） */
function richDateLabel(iso: string): string {
  const parts = iso.split("-");
  if (parts.length !== 3) return iso;
  const y = Number(parts[0]);
  const m = Number(parts[1]);
  const d = Number(parts[2]);
  if (!y || !m || !d) return iso;
  const date = new Date(y, m - 1, d);
  const weekday = WEEKDAY_NAMES[date.getDay()] ?? "";
  return `${m}月${d}日 · ${weekday}`;
}

function fmt(n: number): string {
  if (!Number.isFinite(n)) return "0";
  if (Math.abs(n) >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`;
  if (Math.abs(n) >= 10_000) return `${(n / 1_000).toFixed(1)}K`;
  return n.toLocaleString();
}

export function UsageChart() {
  const [data, setData] = React.useState<UsageDayRecord[]>([]);
  const [loading, setLoading] = React.useState(true);
  const [days, setDays] = React.useState<(typeof PERIOD_OPTIONS)[number]["id"]>(30);

  React.useEffect(() => {
    setLoading(true);
    let cancelled = false;
    usageApi
      .history(days)
      .then((res) => {
        if (cancelled) return;
        setData(res.days ?? []);
      })
      .catch(() => {
        if (cancelled) return;
        setData([]);
      })
      .finally(() => {
        if (cancelled) return;
        setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [days]);

  const pivot = React.useMemo(() => pivotByProvider(data, days), [data, days]);
  const { rows, providers, perProviderTotal } = pivot;

  // 给每个 provider 分配稳定的 chart 调色板颜色。
  const colorByProvider = React.useMemo(() => {
    const map: Record<string, string> = {};
    providers.forEach((p, i) => {
      map[p] = CHART_COLORS[i % CHART_COLORS.length] ?? "var(--chart-1)";
    });
    return map;
  }, [providers]);

  const config: ChartConfig = React.useMemo(() => {
    const c: ChartConfig = {};
    for (const p of providers) {
      c[p] = {
        label: p,
        color: colorByProvider[p] ?? "var(--chart-1)",
        unit: "次",
      };
    }
    return c;
  }, [providers, colorByProvider]);

  // KPI：累计 / 日均 / 峰值 / 趋势（前一半 vs 后一半 的均值差）
  const kpi = React.useMemo(() => computeKPIs(rows), [rows]);

  return (
    <Card className="overflow-hidden">
      <CardHeader className="space-y-3 pb-4">
        <div className="flex flex-wrap items-start justify-between gap-3">
          <div className="space-y-1">
            <CardTitle className="flex items-center gap-2 text-base">
              <Activity className="size-4 text-primary" />
              历史用量趋势
            </CardTitle>
            <CardDescription className="flex flex-wrap items-center gap-x-2 gap-y-1">
              <span>近 {days} 天</span>
              <span className="text-muted-foreground/40">·</span>
              <span>
                累计{" "}
                <span className="font-medium text-foreground tabular-nums">
                  {kpi.total.toLocaleString()}
                </span>{" "}
                次调用
              </span>
              <TrendBadge value={kpi.trendPct} />
            </CardDescription>
          </div>
          <PeriodSelector value={days} onChange={setDays} />
        </div>
        <KPIRow kpi={kpi} />
      </CardHeader>

      <CardContent className="pt-0">
        {loading ? (
          <ChartSkeleton />
        ) : rows.length === 0 || providers.length === 0 ? (
          <EmptyState />
        ) : (
          <>
            <ChartContainer config={config} className="h-[260px]">
              <ResponsiveContainer width="100%" height="100%">
                <AreaChart
                  data={rows}
                  margin={{ top: 8, right: 8, bottom: 0, left: -8 }}
                >
                  <defs>
                    {providers.map((p) => (
                      <linearGradient
                        key={p}
                        id={`fill-${p}`}
                        x1="0"
                        y1="0"
                        x2="0"
                        y2="1"
                      >
                        <stop
                          offset="0%"
                          stopColor={colorByProvider[p]}
                          stopOpacity={0.55}
                        />
                        <stop
                          offset="55%"
                          stopColor={colorByProvider[p]}
                          stopOpacity={0.18}
                        />
                        <stop
                          offset="100%"
                          stopColor={colorByProvider[p]}
                          stopOpacity={0.04}
                        />
                      </linearGradient>
                    ))}
                  </defs>
                  <CartesianGrid
                    vertical={false}
                    strokeDasharray="4 6"
                    className="stroke-border/60"
                  />
                  <XAxis
                    dataKey="date"
                    tick={{ fontSize: 11, fill: "currentColor" }}
                    className="text-muted-foreground"
                    tickLine={false}
                    axisLine={false}
                    minTickGap={28}
                    tickFormatter={shortAxisLabel}
                  />
                  <YAxis
                    tick={{ fontSize: 11, fill: "currentColor" }}
                    className="text-muted-foreground"
                    tickLine={false}
                    axisLine={false}
                    width={36}
                    tickFormatter={fmt}
                  />
                  <Tooltip
                    cursor={{
                      stroke: "var(--color-primary)",
                      strokeWidth: 1,
                      strokeDasharray: "3 3",
                      strokeOpacity: 0.5,
                    }}
                    content={
                      <ChartTooltipContent
                        config={config}
                        labelFormatter={richDateLabel}
                      />
                    }
                  />
                  {providers.map((p) => (
                    <Area
                      key={p}
                      type="monotone"
                      dataKey={p}
                      stackId="usage"
                      stroke={colorByProvider[p]}
                      fill={`url(#fill-${p})`}
                      strokeWidth={1.75}
                      activeDot={{
                        r: 4,
                        strokeWidth: 2,
                        stroke: "var(--color-background)",
                      }}
                      isAnimationActive
                      animationDuration={500}
                    />
                  ))}
                </AreaChart>
              </ResponsiveContainer>
            </ChartContainer>
            <div className="mt-3 flex flex-wrap items-center gap-x-4 gap-y-1.5 px-1">
              {providers.map((p) => (
                <ChartLegendItem
                  key={p}
                  name={p}
                  color={colorByProvider[p] ?? "var(--chart-1)"}
                  value={perProviderTotal[p] ?? 0}
                />
              ))}
            </div>
          </>
        )}
      </CardContent>
    </Card>
  );
}

interface PeriodSelectorProps {
  value: number;
  onChange: (v: (typeof PERIOD_OPTIONS)[number]["id"]) => void;
}

/** 段控件风格的周期切换；shadcn token，与 Tabs 风格一致。 */
function PeriodSelector({ value, onChange }: PeriodSelectorProps) {
  return (
    <div
      role="radiogroup"
      aria-label="时间范围"
      className="inline-flex items-center rounded-md border bg-muted/40 p-0.5 text-xs"
    >
      {PERIOD_OPTIONS.map((o) => {
        const active = o.id === value;
        return (
          <button
            key={o.id}
            type="button"
            role="radio"
            aria-checked={active}
            onClick={() => onChange(o.id)}
            className={cn(
              "rounded-[5px] px-2.5 py-1 font-medium transition-colors",
              active
                ? "bg-background text-foreground shadow-sm"
                : "text-muted-foreground hover:text-foreground",
            )}
          >
            {o.label}
          </button>
        );
      })}
    </div>
  );
}

interface KPI {
  total: number;
  daily: number;
  peak: number;
  peakDate: string | null;
  trendPct: number | null;
}

function computeKPIs(rows: DailyRow[]): KPI {
  if (rows.length === 0) {
    return { total: 0, daily: 0, peak: 0, peakDate: null, trendPct: null };
  }
  let total = 0;
  let peak = 0;
  let peakDate: string | null = null;
  for (const r of rows) {
    const v = Number(r.total ?? 0);
    total += v;
    if (v > peak) {
      peak = v;
      peakDate = r.date;
    }
  }
  const daily = total / rows.length;

  // 前一半 vs 后一半的均值变化（不依赖具体单位，描述近期趋势）
  let trendPct: number | null = null;
  if (rows.length >= 4) {
    const mid = Math.floor(rows.length / 2);
    const head = rows.slice(0, mid);
    const tail = rows.slice(rows.length - mid);
    const headAvg = head.reduce((s, r) => s + Number(r.total ?? 0), 0) / head.length;
    const tailAvg = tail.reduce((s, r) => s + Number(r.total ?? 0), 0) / tail.length;
    if (headAvg > 0) {
      trendPct = ((tailAvg - headAvg) / headAvg) * 100;
    } else if (tailAvg > 0) {
      // 之前没有数据但近期有 → 用 +∞ 表达；这里展示成 null 让 badge 不显示
      trendPct = null;
    } else {
      trendPct = 0;
    }
  }

  return { total, daily, peak, peakDate, trendPct };
}

function KPIRow({ kpi }: { kpi: KPI }) {
  return (
    <div className="grid grid-cols-3 gap-2">
      <KPICell label="总调用" value={kpi.total} accent />
      <KPICell label="日均" value={Math.round(kpi.daily)} />
      <KPICell
        label="峰值"
        value={kpi.peak}
        hint={kpi.peakDate ? richDateLabel(kpi.peakDate) : undefined}
      />
    </div>
  );
}

function KPICell({
  label,
  value,
  hint,
  accent = false,
}: {
  label: string;
  value: number;
  hint?: string;
  accent?: boolean;
}) {
  return (
    <div
      className={cn(
        "rounded-md border px-3 py-2",
        accent ? "bg-primary/5 border-primary/20" : "bg-card/60",
      )}
    >
      <div className="text-[10px] uppercase tracking-wide text-muted-foreground">
        {label}
      </div>
      <div className="mt-0.5 font-mono text-base font-semibold tabular-nums text-foreground">
        {fmt(value)}
      </div>
      {hint && (
        <div className="mt-0.5 truncate text-[10px] text-muted-foreground">{hint}</div>
      )}
    </div>
  );
}

function TrendBadge({ value }: { value: number | null }) {
  if (value === null) return null;
  const abs = Math.abs(value);
  const Icon = value > 1 ? ArrowUpRight : value < -1 ? ArrowDownRight : Minus;
  const color =
    value > 1
      ? "text-emerald-600 dark:text-emerald-400 bg-emerald-500/10"
      : value < -1
        ? "text-rose-600 dark:text-rose-400 bg-rose-500/10"
        : "text-muted-foreground bg-muted";
  return (
    <span
      className={cn(
        "inline-flex items-center gap-0.5 rounded-full px-1.5 py-0.5 text-[11px] font-medium tabular-nums",
        color,
      )}
      title="较前期均值变化"
    >
      <Icon className="size-3" />
      {abs.toFixed(abs >= 100 ? 0 : 1)}%
    </span>
  );
}

function ChartSkeleton() {
  return (
    <div className="space-y-3">
      <div className="h-[260px] w-full overflow-hidden rounded-md bg-muted/30 relative">
        {/* 三条波形线占位条，模拟 area 曲线 */}
        <div className="absolute inset-x-0 bottom-0 h-32 animate-pulse rounded-t-md bg-gradient-to-t from-muted/60 to-muted/10" />
        <div className="absolute inset-x-0 bottom-0 h-24 animate-pulse rounded-t-md bg-gradient-to-t from-muted/40 to-transparent [animation-delay:200ms]" />
      </div>
      <div className="flex gap-3">
        <div className="h-3 w-20 animate-pulse rounded bg-muted/50" />
        <div className="h-3 w-20 animate-pulse rounded bg-muted/50 [animation-delay:120ms]" />
      </div>
    </div>
  );
}

function EmptyState() {
  return (
    <div className="flex h-[260px] flex-col items-center justify-center gap-2 rounded-md border border-dashed bg-muted/20 text-center">
      <div className="grid size-10 place-items-center rounded-full bg-muted text-muted-foreground">
        <Activity className="size-5" />
      </div>
      <div className="text-sm font-medium">暂无历史用量</div>
      <div className="max-w-xs text-xs text-muted-foreground">
        发起任何 /v1/qqmusic 或 /v1/netease 调用后，这里会出现按日聚合的趋势。
      </div>
    </div>
  );
}
