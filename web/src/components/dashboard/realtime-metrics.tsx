"use client";

import * as React from "react";
import { Activity, AlertTriangle, CheckCircle, Clock, TrendingUp, Zap } from "lucide-react";

import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { usageApi, type RealtimeMetrics } from "@/lib/api/usage";

export function RealtimeMetricsCards() {
  const [data, setData] = React.useState<RealtimeMetrics | null>(null);

  React.useEffect(() => {
    const poll = () => usageApi.realtime().then(setData).catch(() => {});
    poll();
    const timer = setInterval(poll, 3000);
    return () => clearInterval(timer);
  }, []);

  if (!data) {
    return (
      <div className="grid gap-4 md:grid-cols-2 lg:grid-cols-4">
        {[0, 1, 2, 3].map((i) => (
          <Card key={i}>
            <CardContent className="pt-6 text-sm text-muted-foreground animate-pulse">
              加载中...
            </CardContent>
          </Card>
        ))}
      </div>
    );
  }

  const successPct = Math.round(data.successRate * 100);
  const latencyStatus =
    data.avgLatencyMs < 100 ? "优秀" : data.avgLatencyMs < 300 ? "正常" : "偏慢";

  return (
    <div className="grid gap-4 md:grid-cols-2 lg:grid-cols-4">
      <Card>
        <CardHeader className="flex flex-row items-center justify-between pb-2">
          <div>
            <CardTitle className="text-sm">QPM</CardTitle>
            <CardDescription>每分钟请求数</CardDescription>
          </div>
          <Activity className="size-4 text-muted-foreground" />
        </CardHeader>
        <CardContent>
          <div className="text-3xl font-bold tabular-nums">
            <AnimatedNumber value={data.qpm} />
          </div>
          <p className="text-xs text-muted-foreground mt-1">
            QPS: {data.qps ?? 0} · 今日 {(data.totalToday ?? 0).toLocaleString()} 次
          </p>
        </CardContent>
      </Card>

      <Card>
        <CardHeader className="flex flex-row items-center justify-between pb-2">
          <div>
            <CardTitle className="text-sm">响应延迟</CardTitle>
            <CardDescription>{latencyStatus}</CardDescription>
          </div>
          <Clock className="size-4 text-muted-foreground" />
        </CardHeader>
        <CardContent>
          <div className="text-3xl font-bold tabular-nums">
            {data.avgLatencyMs > 0 ? Math.round(data.avgLatencyMs) : "—"}
            <span className="text-sm font-normal text-muted-foreground"> ms</span>
          </div>
          <p className="text-xs text-muted-foreground mt-1">
            最小 {data.minLatencyMs ?? 0}ms · 最大 {data.maxLatencyMs ?? 0}ms
          </p>
        </CardContent>
      </Card>

      <Card>
        <CardHeader className="flex flex-row items-center justify-between pb-2">
          <div>
            <CardTitle className="text-sm">成功率</CardTitle>
            <CardDescription>最近 1 分钟</CardDescription>
          </div>
          {successPct >= 95 ? (
            <CheckCircle className="size-4 text-emerald-500" />
          ) : (
            <AlertTriangle className="size-4 text-amber-500" />
          )}
        </CardHeader>
        <CardContent>
          <div className="text-3xl font-bold tabular-nums">
            {data.qpm > 0 ? `${successPct}%` : "—"}
          </div>
          <p className="text-xs text-muted-foreground mt-1">
            错误 {data.errorCount ?? 0} 次
          </p>
        </CardContent>
      </Card>

      <Card>
        <CardHeader className="flex flex-row items-center justify-between pb-2">
          <div>
            <CardTitle className="text-sm">本月用量</CardTitle>
            <CardDescription>配额消耗</CardDescription>
          </div>
          <TrendingUp className="size-4 text-muted-foreground" />
        </CardHeader>
        <CardContent>
          <div className="text-3xl font-bold tabular-nums">
            {(data.monthUsed ?? 0).toLocaleString()}
          </div>
          <div className="mt-2 h-2 rounded-full bg-muted overflow-hidden">
            <div
              className="h-full rounded-full bg-primary transition-all"
              style={{ width: `${data.monthLimit ? Math.min((data.monthUsed / data.monthLimit) * 100, 100) : 0}%` }}
            />
          </div>
          <p className="text-xs text-muted-foreground mt-1">
            上限 {(data.monthLimit ?? 0).toLocaleString()} · 日用 {(data.dayUsed ?? 0).toLocaleString()}/{(data.dayLimit ?? 0).toLocaleString()}
          </p>
        </CardContent>
      </Card>
    </div>
  );
}

function AnimatedNumber({ value }: { value: number }) {
  const [display, setDisplay] = React.useState(value);
  const prev = React.useRef(value);

  React.useEffect(() => {
    const from = prev.current;
    const to = value;
    prev.current = value;
    if (from === to) return;

    const duration = 400;
    const start = performance.now();
    let raf: number;
    const tick = (now: number) => {
      const t = Math.min((now - start) / duration, 1);
      const ease = 1 - (1 - t) * (1 - t);
      setDisplay(Math.round(from + (to - from) * ease));
      if (t < 1) raf = requestAnimationFrame(tick);
    };
    raf = requestAnimationFrame(tick);
    return () => cancelAnimationFrame(raf);
  }, [value]);

  return <>{display.toLocaleString()}</>;
}
