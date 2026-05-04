"use client";

import * as React from "react";

import { Badge } from "@/components/ui/badge";
import {
  Card,
  CardContent,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { cn } from "@/lib/utils";
import type { ProviderPool } from "@/types/api";

interface HealthSummaryCardProps {
  provider: string;
  data: ProviderPool | null;
  loading?: boolean;
}

/** 单 provider 健康概览：可用 / 禁用 / 封禁 / 平均健康度 */
export function HealthSummaryCard({
  provider,
  data,
  loading,
}: HealthSummaryCardProps) {
  const total =
    (data?.active ?? 0) + (data?.disabled ?? 0) + (data?.banned ?? 0);

  return (
    <Card>
      <CardHeader className="flex flex-row items-center justify-between gap-4 space-y-0 pb-3">
        <div className="flex items-center gap-2.5">
          <CardTitle className="text-base font-semibold">健康概览</CardTitle>
          <Badge variant="outline" className="font-mono text-xs">
            {provider}
          </Badge>
        </div>
        <span className="text-xs tabular-nums text-muted-foreground">
          共 {total} 张
        </span>
      </CardHeader>
      <CardContent>
        <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
          {loading
            ? Array.from({ length: 4 }).map((_, i) => (
                <div
                  key={i}
                  className="h-[72px] animate-pulse rounded-md border bg-muted/40"
                />
              ))
            : (
              <>
                <Stat
                  label="可用"
                  value={data?.active ?? 0}
                  tone={(data?.active ?? 0) > 0 ? "ok" : "muted"}
                />
                <Stat
                  label="禁用"
                  value={data?.disabled ?? 0}
                  tone="warn"
                  muteIfZero
                />
                <Stat
                  label="封禁"
                  value={data?.banned ?? 0}
                  tone="bad"
                  muteIfZero
                />
                <Stat
                  label="平均健康"
                  value={
                    data?.avgHealthScore !== undefined
                      ? data.avgHealthScore.toFixed(2)
                      : "—"
                  }
                  tone={pickTone(data?.avgHealthScore)}
                />
              </>
            )}
        </div>
      </CardContent>
    </Card>
  );
}

type Tone = "ok" | "warn" | "bad" | "muted";

function pickTone(score: number | undefined): Tone {
  if (score === undefined) return "muted";
  if (score >= 0.8) return "ok";
  if (score >= 0.4) return "warn";
  return "bad";
}

const TONE_DOT: Record<Tone, string> = {
  ok: "bg-emerald-500",
  warn: "bg-amber-500",
  bad: "bg-destructive",
  muted: "bg-muted-foreground/40",
};

const TONE_VALUE: Record<Tone, string> = {
  ok: "text-emerald-600 dark:text-emerald-400",
  warn: "text-amber-600 dark:text-amber-400",
  bad: "text-destructive",
  muted: "text-foreground",
};

function Stat({
  label,
  value,
  tone,
  muteIfZero,
}: {
  label: string;
  value: number | string;
  tone: Tone;
  /** 数值为 0 时降级为中性灰 — 避免 "禁用 0 / 封禁 0" 这类好状态被警示色误读 */
  muteIfZero?: boolean;
}) {
  const isZero = value === 0 || value === "0";
  const effectiveTone: Tone = muteIfZero && isZero ? "muted" : tone;

  return (
    <div className="rounded-md border bg-card p-3">
      <div className="flex items-center gap-1.5">
        <span
          aria-hidden
          className={cn("size-1.5 rounded-full", TONE_DOT[effectiveTone])}
        />
        <p className="text-xs font-medium text-muted-foreground">{label}</p>
      </div>
      <p
        className={cn(
          "mt-1 text-2xl font-semibold tabular-nums",
          TONE_VALUE[effectiveTone],
        )}
      >
        {value}
      </p>
    </div>
  );
}
