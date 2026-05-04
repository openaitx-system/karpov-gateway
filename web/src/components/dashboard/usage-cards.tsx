"use client";

import * as React from "react";
import { Activity, Calendar, TrendingUp } from "lucide-react";

import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { Progress } from "@/components/ui/progress";
import { usageApi, type UsageSummary } from "@/lib/api/usage";

export function UsageCards() {
  const [data, setData] = React.useState<UsageSummary | null>(null);
  const [error, setError] = React.useState<string | null>(null);

  React.useEffect(() => {
    usageApi
      .summary()
      .then(setData)
      .catch((err) => setError(err instanceof Error ? err.message : "加载失败"));
  }, []);

  if (error) {
    return (
      <Card>
        <CardContent className="pt-6 text-sm text-muted-foreground">
          用量数据加载失败：{error}
        </CardContent>
      </Card>
    );
  }

  if (!data) {
    return (
      <div className="grid gap-4 md:grid-cols-3">
        {[0, 1, 2].map((i) => (
          <Card key={i}>
            <CardContent className="pt-6 text-sm text-muted-foreground animate-pulse">
              加载中...
            </CardContent>
          </Card>
        ))}
      </div>
    );
  }

  const dayPct = data.dayLimit > 0 ? Math.min(100, (data.dayUsed / data.dayLimit) * 100) : 0;
  const monthPct = data.monthLimit > 0 ? Math.min(100, (data.monthUsed / data.monthLimit) * 100) : 0;

  return (
    <div className="grid gap-4 md:grid-cols-3">
      <Card>
        <CardHeader className="flex flex-row items-center justify-between pb-2">
          <div>
            <CardTitle className="text-sm">今日用量</CardTitle>
            <CardDescription>{data.date}</CardDescription>
          </div>
          <Activity className="size-4 text-muted-foreground" />
        </CardHeader>
        <CardContent className="space-y-2">
          <div className="text-2xl font-bold tabular-nums">
            {data.dayUsed.toLocaleString()}
            <span className="text-sm font-normal text-muted-foreground">
              {" "}/ {data.dayLimit.toLocaleString()}
            </span>
          </div>
          <Progress value={dayPct} className="h-2" />
          <p className="text-xs text-muted-foreground">
            已使用 {dayPct.toFixed(1)}%
          </p>
        </CardContent>
      </Card>

      <Card>
        <CardHeader className="flex flex-row items-center justify-between pb-2">
          <div>
            <CardTitle className="text-sm">本月用量</CardTitle>
            <CardDescription>{data.month}</CardDescription>
          </div>
          <Calendar className="size-4 text-muted-foreground" />
        </CardHeader>
        <CardContent className="space-y-2">
          <div className="text-2xl font-bold tabular-nums">
            {data.monthUsed.toLocaleString()}
            <span className="text-sm font-normal text-muted-foreground">
              {" "}/ {data.monthLimit.toLocaleString()}
            </span>
          </div>
          <Progress value={monthPct} className="h-2" />
          <p className="text-xs text-muted-foreground">
            已使用 {monthPct.toFixed(1)}%
          </p>
        </CardContent>
      </Card>

      <Card>
        <CardHeader className="flex flex-row items-center justify-between pb-2">
          <div>
            <CardTitle className="text-sm">配额状态</CardTitle>
            <CardDescription>当前套餐</CardDescription>
          </div>
          <TrendingUp className="size-4 text-muted-foreground" />
        </CardHeader>
        <CardContent className="space-y-1">
          <div className="text-2xl font-bold">
            {monthPct >= 100 ? (
              <span className="text-destructive">已耗尽</span>
            ) : monthPct >= 80 ? (
              <span className="text-amber-600 dark:text-amber-400">接近上限</span>
            ) : (
              <span className="text-emerald-600 dark:text-emerald-400">正常</span>
            )}
          </div>
          <p className="text-xs text-muted-foreground">
            日限额 {data.dayLimit.toLocaleString()} · 月限额 {data.monthLimit.toLocaleString()}
          </p>
        </CardContent>
      </Card>
    </div>
  );
}
