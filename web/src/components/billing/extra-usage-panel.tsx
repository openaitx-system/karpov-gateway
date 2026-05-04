"use client";

import * as React from "react";
import { AlertTriangle, Info, Wallet, Zap } from "lucide-react";
import { toast } from "sonner";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Card,
  CardContent,
  CardDescription,
  CardFooter,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Progress } from "@/components/ui/progress";
import { Switch } from "@/components/ui/switch";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { ApiError } from "@/lib/api/client";
import {
  type ExtraUsageReport,
  type ExtraUsageSettings,
  type OverageCharge,
  type OverageEvent,
  extraUsageApi,
} from "@/lib/api/extra-usage";
import { formatDateTime } from "@/lib/datetime";

function formatYuan(cents: number): string {
  return `¥${(cents / 100).toFixed(2)}`;
}

function formatTime(s?: string): string {
  return formatDateTime(s, { fallback: "—" });
}

export function ExtraUsagePanel() {
  const [loading, setLoading] = React.useState(true);
  const [saving, setSaving] = React.useState(false);
  const [settings, setSettings] = React.useState<ExtraUsageSettings | null>(null);
  const [report, setReport] = React.useState<ExtraUsageReport | null>(null);
  const [charges, setCharges] = React.useState<OverageCharge[]>([]);
  const [events, setEvents] = React.useState<OverageEvent[]>([]);

  const [enabledDraft, setEnabledDraft] = React.useState(false);
  const [capYuanDraft, setCapYuanDraft] = React.useState("");
  const [thresholdDraft, setThresholdDraft] = React.useState(80);

  const refresh = React.useCallback(async () => {
    setLoading(true);
    try {
      const [s, r, c, e] = await Promise.all([
        extraUsageApi.getSettings(),
        extraUsageApi.getReport().catch(() => null),
        extraUsageApi.listCharges(12).catch(() => ({ items: [] as OverageCharge[], total: 0 })),
        extraUsageApi.listEvents(50).catch(() => ({ items: [] as OverageEvent[], total: 0 })),
      ]);
      setSettings(s);
      setReport(r);
      setCharges(c.items ?? []);
      setEvents(e.items ?? []);
      setEnabledDraft(s.enabled);
      setCapYuanDraft(((s.monthlyCapCents ?? 0) / 100).toFixed(2));
      setThresholdDraft(s.notifyThresholdPct ?? 80);
    } catch (err) {
      toast.error(err instanceof ApiError ? `加载失败：${err.message}` : "网络异常");
    } finally {
      setLoading(false);
    }
  }, []);

  React.useEffect(() => {
    refresh();
  }, [refresh]);

  const handleSave = async () => {
    setSaving(true);
    try {
      const yuan = Number.parseFloat(capYuanDraft || "0");
      if (Number.isNaN(yuan) || yuan < 0) {
        toast.error("月度上限必须是非负数");
        return;
      }
      const cents = Math.round(yuan * 100);
      const updated = await extraUsageApi.updateSettings({
        enabled: enabledDraft,
        monthlyCapCents: cents,
        notifyThresholdPct: Math.max(0, Math.min(100, thresholdDraft)),
      });
      setSettings(updated);
      toast.success("设置已保存");
      // 刷新一次报告
      const r = await extraUsageApi.getReport().catch(() => null);
      setReport(r);
    } catch (err) {
      toast.error(err instanceof ApiError ? `保存失败：${err.message}` : "网络异常");
    } finally {
      setSaving(false);
    }
  };

  const dirty = React.useMemo(() => {
    if (!settings) return false;
    const cents = Math.round((Number.parseFloat(capYuanDraft || "0") || 0) * 100);
    return (
      enabledDraft !== settings.enabled ||
      cents !== settings.monthlyCapCents ||
      thresholdDraft !== settings.notifyThresholdPct
    );
  }, [settings, enabledDraft, capYuanDraft, thresholdDraft]);

  if (loading && !settings) {
    return (
      <Card className="animate-pulse">
        <CardHeader className="h-20" />
        <CardContent className="h-40" />
      </Card>
    );
  }

  return (
    <div className="space-y-6">
      {/* 概览 */}
      <Card>
        <CardHeader>
          <div className="flex items-center justify-between">
            <div>
              <div className="flex items-center gap-2">
                <Zap className="size-5 text-amber-500" />
                <CardTitle className="text-base">超额使用 / Extra Usage</CardTitle>
                {settings?.enabled ? (
                  <Badge>已开启</Badge>
                ) : (
                  <Badge variant="secondary">未开启</Badge>
                )}
              </div>
              <CardDescription className="mt-1">
                开启后，月度配额耗尽时请求继续放行，按当前套餐的"按量价格"自动计费。
                未开启时配额耗尽请求会返回 429。
              </CardDescription>
            </div>
            <Button variant="outline" size="sm" onClick={refresh} disabled={loading}>
              {loading ? "刷新中..." : "刷新"}
            </Button>
          </div>
        </CardHeader>
        <CardContent>
          <ReportSummary report={report} />
        </CardContent>
      </Card>

      {/* 配置 */}
      <Card>
        <CardHeader>
          <CardTitle className="text-base">配置</CardTitle>
          <CardDescription>修改将立即生效；上限以本月累计费用计</CardDescription>
        </CardHeader>
        <CardContent className="space-y-5">
          <div className="flex items-start justify-between gap-4">
            <div className="space-y-0.5">
              <Label className="text-sm font-medium">允许超额使用</Label>
              <p className="text-xs text-muted-foreground">
                月度配额耗尽后是否继续放行（按量计费）。
                {report?.overagePricePer_1k && report.overagePricePer_1k > 0 ? (
                  <>
                    {" "}当前套餐：
                    <span className="font-medium">
                      ¥{(report.overagePricePer_1k / 100).toFixed(2)}/千次
                    </span>
                  </>
                ) : (
                  <>
                    {" "}
                    <span className="text-amber-600">当前套餐不支持按量；请先升级到专业版/企业版。</span>
                  </>
                )}
              </p>
            </div>
            <Switch
              checked={enabledDraft}
              onCheckedChange={setEnabledDraft}
              aria-label="切换 Extra Usage"
            />
          </div>

          <div className="grid gap-4 md:grid-cols-2">
            <div className="space-y-1.5">
              <Label htmlFor="monthlyCap" className="text-sm font-medium">
                月度上限（CNY）
              </Label>
              <Input
                id="monthlyCap"
                type="number"
                inputMode="decimal"
                min={0}
                step="0.01"
                value={capYuanDraft}
                onChange={(e) => setCapYuanDraft(e.target.value)}
                disabled={!enabledDraft}
                placeholder="0 表示不设上限"
              />
              <p className="text-xs text-muted-foreground">
                当本月累计超额费用达到该上限时，后续请求将被拒绝（HTTP 429）。0 表示无上限。
              </p>
            </div>

            <div className="space-y-1.5">
              <Label htmlFor="threshold" className="text-sm font-medium">
                提醒阈值（%）
              </Label>
              <Input
                id="threshold"
                type="number"
                min={0}
                max={100}
                step={1}
                value={thresholdDraft}
                onChange={(e) => setThresholdDraft(Number.parseInt(e.target.value || "0", 10))}
                disabled={!enabledDraft}
              />
              <p className="text-xs text-muted-foreground">
                达到上限的此百分比时在控制台显示提醒。默认 80%。
              </p>
            </div>
          </div>

          {!enabledDraft && (
            <div className="flex items-start gap-2 rounded-md border bg-muted/40 p-3 text-xs text-muted-foreground">
              <Info className="mt-0.5 size-4 shrink-0" />
              <span>
                未开启 Extra Usage 时，月度配额耗尽后请求会被立即拒绝；
                如需"流量保底"，请打开开关并设置一个保守的上限。
              </span>
            </div>
          )}
        </CardContent>
        <CardFooter className="justify-end gap-2">
          <Button
            variant="outline"
            onClick={() => {
              if (!settings) return;
              setEnabledDraft(settings.enabled);
              setCapYuanDraft((settings.monthlyCapCents / 100).toFixed(2));
              setThresholdDraft(settings.notifyThresholdPct);
            }}
            disabled={!dirty || saving}
          >
            重置
          </Button>
          <Button onClick={handleSave} disabled={!dirty || saving}>
            {saving ? "保存中..." : "保存设置"}
          </Button>
        </CardFooter>
      </Card>

      {/* 历史 */}
      <Card>
        <CardHeader>
          <CardTitle className="text-base">超额账单与流水</CardTitle>
          <CardDescription>最近 12 个月的累计账单 + 最近 50 条事件流水</CardDescription>
        </CardHeader>
        <CardContent>
          <Tabs defaultValue="charges">
            <TabsList>
              <TabsTrigger value="charges">月度账单</TabsTrigger>
              <TabsTrigger value="events">事件流水</TabsTrigger>
            </TabsList>
            <TabsContent value="charges" className="mt-3">
              <ChargesTable items={charges} />
            </TabsContent>
            <TabsContent value="events" className="mt-3">
              <EventsTable items={events} />
            </TabsContent>
          </Tabs>
        </CardContent>
      </Card>
    </div>
  );
}

function ReportSummary({ report }: { report: ExtraUsageReport | null }) {
  if (!report) {
    return (
      <p className="text-sm text-muted-foreground">本账期暂无数据。</p>
    );
  }
  const usedPct = Math.round(Math.min(1, report.usedCapPct ?? 0) * 100);
  const hasCap = report.monthlyCapCents > 0;
  const balanceLow = report.enabled && report.balanceCents < 1000; // < ¥10
  return (
    <div className="space-y-4">
      <div className="grid gap-4 md:grid-cols-4">
        <Stat
          label="钱包余额"
          value={formatYuan(report.balanceCents ?? 0)}
          icon={<Wallet className="size-3.5" />}
          accent={balanceLow ? "warn" : undefined}
        />
        <Stat label="本月超额次数" value={String(report.overageCount)} />
        <Stat label="本月累计费用" value={formatYuan(report.overageCents)} />
        <Stat
          label={hasCap ? "剩余额度" : "月度上限"}
          value={hasCap ? formatYuan(report.remainingCapCents) : "未设置"}
          accent={hasCap && report.nearLimit ? "warn" : undefined}
        />
      </div>
      {balanceLow && (
        <div className="flex items-start gap-2 rounded-md border border-amber-200 bg-amber-50 p-3 text-xs text-amber-900 dark:border-amber-900/50 dark:bg-amber-950/30 dark:text-amber-200">
          <AlertTriangle className="mt-0.5 size-4 shrink-0" />
          <span>
            余额较低，超额请求可能因余额不足被拒绝。请前往
            <strong className="mx-1">账户余额</strong>
            进行充值。
          </span>
        </div>
      )}
      {hasCap && (
        <div className="space-y-1">
          <div className="flex items-center justify-between text-xs">
            <span className="text-muted-foreground">
              已用 {formatYuan(report.overageCents)} / {formatYuan(report.monthlyCapCents)}
            </span>
            <span className="font-medium">{usedPct}%</span>
          </div>
          <Progress value={usedPct} className="h-2" />
          {report.nearLimit && (
            <div className="flex items-center gap-1.5 pt-1 text-xs text-amber-600">
              <AlertTriangle className="size-3.5" />
              <span>已超过提醒阈值（{report.notifyThresholdPct}%）</span>
            </div>
          )}
        </div>
      )}
      <p className="text-xs text-muted-foreground">
        账期：<span className="font-medium">{report.yearMonth}</span>
        {report.planName && (
          <>
            {" · "}基础套餐：<span className="font-medium">{report.planName}</span>
          </>
        )}
        {report.overagePricePer_1k > 0 && (
          <>
            {" · "}按量价格：
            <span className="font-medium">
              ¥{(report.overagePricePer_1k / 100).toFixed(2)}/千次
            </span>
          </>
        )}
      </p>
    </div>
  );
}

function Stat({
  label,
  value,
  accent,
  icon,
}: {
  label: string;
  value: string;
  accent?: "warn";
  icon?: React.ReactNode;
}) {
  return (
    <div className="rounded-lg border p-3">
      <p className="flex items-center gap-1.5 text-xs text-muted-foreground">
        {icon}
        {label}
      </p>
      <p
        className={
          "mt-1 text-lg font-semibold " +
          (accent === "warn" ? "text-amber-600" : "")
        }
      >
        {value}
      </p>
    </div>
  );
}

function ChargesTable({ items }: { items: OverageCharge[] }) {
  if (!items || items.length === 0) {
    return (
      <div className="flex h-32 items-center justify-center text-sm text-muted-foreground">
        暂无超额账单
      </div>
    );
  }
  return (
    <Table>
      <TableHeader>
        <TableRow>
          <TableHead>账期</TableHead>
          <TableHead>套餐</TableHead>
          <TableHead className="text-right">超额次数</TableHead>
          <TableHead className="text-right">权重合计</TableHead>
          <TableHead className="text-right">累计费用</TableHead>
          <TableHead>更新时间</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {items.map((c) => (
          <TableRow key={c.yearMonth}>
            <TableCell className="font-medium">{c.yearMonth}</TableCell>
            <TableCell className="text-muted-foreground">{c.planId || "—"}</TableCell>
            <TableCell className="text-right">{c.count.toLocaleString()}</TableCell>
            <TableCell className="text-right">{c.weightSum.toLocaleString()}</TableCell>
            <TableCell className="text-right font-medium">
              {formatYuan(c.amountCents)}
            </TableCell>
            <TableCell className="text-xs text-muted-foreground">
              {formatTime(c.updatedAt)}
            </TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  );
}

function EventsTable({ items }: { items: OverageEvent[] }) {
  if (!items || items.length === 0) {
    return (
      <div className="flex h-32 items-center justify-center text-sm text-muted-foreground">
        暂无超额事件
      </div>
    );
  }
  return (
    <Table>
      <TableHeader>
        <TableRow>
          <TableHead>时间</TableHead>
          <TableHead>Provider</TableHead>
          <TableHead>Endpoint</TableHead>
          <TableHead className="text-right">权重</TableHead>
          <TableHead className="text-right">扣费</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {items.map((e) => (
          <TableRow key={e.id}>
            <TableCell className="text-xs text-muted-foreground">
              {formatTime(e.timestamp)}
            </TableCell>
            <TableCell>{e.provider || "—"}</TableCell>
            <TableCell className="max-w-[260px] truncate" title={e.endpoint}>
              {e.endpoint || "—"}
            </TableCell>
            <TableCell className="text-right">{e.weight}</TableCell>
            <TableCell className="text-right">{formatYuan(e.priceCents)}</TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  );
}
