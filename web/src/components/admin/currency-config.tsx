"use client";

import * as React from "react";
import { Plus, Save, Trash2 } from "lucide-react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import {
  settingsApi,
  type CurrencyConfig,
  type CurrencyRate,
} from "@/lib/api/settings";
import { ApiError } from "@/lib/api/client";
import { formatDateTime } from "@/lib/datetime";

const ISO4217 = /^[A-Z]{3}$/;

interface DraftRate extends CurrencyRate {
  /** 仅前端用：本地行 ID，用于 React key & 删除定位（保存时不发回后端）。 */
  _id: number;
}

let nextId = 1;
function newDraftRate(code = "", rate = ""): DraftRate {
  return { _id: nextId++, code, rate };
}

export function CurrencyConfigAdmin() {
  const [defaultCurrency, setDefaultCurrency] = React.useState("CNY");
  const [rates, setRates] = React.useState<DraftRate[]>([]);
  const [updatedAt, setUpdatedAt] = React.useState<string | undefined>();
  const [loading, setLoading] = React.useState(true);
  const [saving, setSaving] = React.useState(false);

  const loadFrom = React.useCallback((cfg: CurrencyConfig) => {
    setDefaultCurrency(cfg.defaultCurrency || "CNY");
    setRates((cfg.rates ?? []).map((r) => newDraftRate(r.code, r.rate)));
    setUpdatedAt(cfg.updatedAt);
  }, []);

  React.useEffect(() => {
    settingsApi
      .getCurrency()
      .then((res) => loadFrom(res.config))
      .catch(() => toast.error("加载货币配置失败"))
      .finally(() => setLoading(false));
  }, [loadFrom]);

  const updateRate = (id: number, patch: Partial<CurrencyRate>) => {
    setRates((prev) =>
      prev.map((r) => (r._id === id ? { ...r, ...patch } : r)),
    );
  };
  const removeRate = (id: number) => {
    setRates((prev) => prev.filter((r) => r._id !== id));
  };
  const addRate = () => {
    setRates((prev) => [...prev, newDraftRate()]);
  };

  // 客户端校验（与后端 Validate 同步）
  const validation = React.useMemo(() => {
    const errs: string[] = [];
    const def = defaultCurrency.trim().toUpperCase();
    if (!ISO4217.test(def)) {
      errs.push("基准币必须是 3 个大写字母（ISO 4217）");
    }
    const seen = new Set<string>();
    for (const [i, r] of rates.entries()) {
      const code = r.code.trim().toUpperCase();
      if (!ISO4217.test(code)) {
        errs.push(`第 ${i + 1} 行：货币代码必须是 3 个大写字母`);
        continue;
      }
      if (code === def) {
        errs.push(`第 ${i + 1} 行：基准币不能同时出现在汇率列表里`);
      }
      if (seen.has(code)) {
        errs.push(`第 ${i + 1} 行：${code} 重复`);
      }
      seen.add(code);
      const n = Number(r.rate);
      if (!Number.isFinite(n) || n <= 0) {
        errs.push(`第 ${i + 1} 行：汇率必须是正数`);
      }
    }
    return errs;
  }, [defaultCurrency, rates]);

  const onSave = async () => {
    if (validation.length > 0) {
      toast.error(validation[0]);
      return;
    }
    setSaving(true);
    try {
      const res = await settingsApi.updateCurrency({
        defaultCurrency: defaultCurrency.trim().toUpperCase(),
        rates: rates.map((r) => ({
          code: r.code.trim().toUpperCase(),
          rate: r.rate.trim(),
        })),
      });
      loadFrom(res.config);
      toast.success("货币配置已保存并生效");
    } catch (err) {
      toast.error(
        err instanceof ApiError ? `保存失败：${err.message}` : "网络异常",
      );
    } finally {
      setSaving(false);
    }
  };

  if (loading) {
    return (
      <Card>
        <CardContent className="pt-6 text-sm text-muted-foreground animate-pulse">
          加载中...
        </CardContent>
      </Card>
    );
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-base">货币与汇率</CardTitle>
        <CardDescription>
          基准币与外币汇率。汇率方向：<b>1 单位外币 = X 单位基准币</b>
          （比如基准 CNY、USD = 7.20 表示 $1 ≈ ¥7.20）。
          {updatedAt && (
            <span className="ml-2 text-[11px]">
              最后更新：{formatDateTime(updatedAt)}
            </span>
          )}
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-4">
        <div className="flex items-end gap-3">
          <div className="space-y-1.5">
            <Label htmlFor="default-currency">基准币</Label>
            <Input
              id="default-currency"
              className="w-28 font-mono uppercase"
              maxLength={3}
              value={defaultCurrency}
              onChange={(e) => setDefaultCurrency(e.target.value.toUpperCase())}
              placeholder="CNY"
            />
          </div>
          <p className="pb-2 text-xs text-muted-foreground">
            ISO 4217 三字母（如 CNY / USD / EUR）。所有未列在下方汇率表中的外币订单都会被拒绝。
          </p>
        </div>

        <div className="rounded-md border">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead className="w-32">外币代码</TableHead>
                <TableHead>1 外币 = X 基准币</TableHead>
                <TableHead className="w-16" />
              </TableRow>
            </TableHeader>
            <TableBody>
              {rates.length === 0 ? (
                <TableRow>
                  <TableCell colSpan={3} className="py-6 text-center text-xs text-muted-foreground">
                    暂未配置任何外币。如果只用基准币，可以保留为空。
                  </TableCell>
                </TableRow>
              ) : (
                rates.map((r) => (
                  <TableRow key={r._id}>
                    <TableCell>
                      <Input
                        className="font-mono uppercase"
                        maxLength={3}
                        value={r.code}
                        onChange={(e) =>
                          updateRate(r._id, {
                            code: e.target.value.toUpperCase(),
                          })
                        }
                        placeholder="USD"
                      />
                    </TableCell>
                    <TableCell>
                      <Input
                        type="number"
                        inputMode="decimal"
                        step="0.0001"
                        min={0}
                        value={r.rate}
                        onChange={(e) =>
                          updateRate(r._id, { rate: e.target.value })
                        }
                        placeholder="7.2000"
                      />
                    </TableCell>
                    <TableCell>
                      <Button
                        variant="ghost"
                        size="icon"
                        onClick={() => removeRate(r._id)}
                        title="删除该行"
                      >
                        <Trash2 className="size-4 text-muted-foreground" />
                      </Button>
                    </TableCell>
                  </TableRow>
                ))
              )}
            </TableBody>
          </Table>
        </div>

        <div className="flex items-center justify-between">
          <Button variant="outline" size="sm" onClick={addRate}>
            <Plus className="mr-1 size-4" />
            新增外币
          </Button>
          <div className="flex items-center gap-3">
            {validation.length > 0 && (
              <span className="text-xs text-destructive">
                {validation[0]}
                {validation.length > 1 && ` (+${validation.length - 1})`}
              </span>
            )}
            <Button
              onClick={onSave}
              disabled={saving || validation.length > 0}
            >
              <Save className="mr-2 size-4" />
              {saving ? "保存中..." : "保存并生效"}
            </Button>
          </div>
        </div>
      </CardContent>
    </Card>
  );
}
