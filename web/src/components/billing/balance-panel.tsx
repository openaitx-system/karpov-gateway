"use client";

import * as React from "react";
import { ArrowDownCircle, ArrowUpCircle, RefreshCw, Wallet } from "lucide-react";
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
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { PaymentMethodSelect } from "@/components/billing/payment-method-select";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import {
  type Balance,
  type BalanceTransaction,
  type BalanceTxnKind,
  balanceApi,
} from "@/lib/api/balance";
import { ApiError } from "@/lib/api/client";
import { formatDateTime } from "@/lib/datetime";

const QUICK_AMOUNTS_CENTS = [1000, 5000, 10000, 50000, 100000];

const KIND_LABEL: Record<BalanceTxnKind, string> = {
  topup: "充值",
  overage: "超额扣费",
  refund: "退款",
  adjust: "管理员调整",
  subscription: "套餐消费",
};

function formatYuan(cents: number): string {
  const sign = cents < 0 ? "-" : "";
  return `${sign}¥${(Math.abs(cents) / 100).toFixed(2)}`;
}

function formatTime(s?: string): string {
  return formatDateTime(s, { fallback: "—" });
}

export interface BalancePanelProps {
  /** 充值/扣费后通知外部刷新（如 ExtraUsage 报告）。 */
  onChanged?: () => void;
}

export function BalancePanel({ onChanged }: BalancePanelProps) {
  const [loading, setLoading] = React.useState(true);
  const [balance, setBalance] = React.useState<Balance | null>(null);
  const [txns, setTxns] = React.useState<BalanceTransaction[]>([]);
  const [showRecharge, setShowRecharge] = React.useState(false);

  const refresh = React.useCallback(async () => {
    setLoading(true);
    try {
      const [b, t] = await Promise.all([
        balanceApi.get(),
        balanceApi.listTransactions(50, 0).catch(() => ({
          items: [] as BalanceTransaction[],
          total: 0,
          limit: 50,
          offset: 0,
        })),
      ]);
      setBalance(b);
      setTxns(t.items ?? []);
    } catch (err) {
      toast.error(err instanceof ApiError ? `加载失败：${err.message}` : "网络异常");
    } finally {
      setLoading(false);
    }
  }, []);

  React.useEffect(() => {
    refresh();
  }, [refresh]);

  return (
    <div className="space-y-6">
      <Card>
        <CardHeader>
          <div className="flex items-center justify-between">
            <div className="flex items-center gap-2">
              <Wallet className="size-5 text-primary" />
              <CardTitle className="text-base">账户余额</CardTitle>
            </div>
            <Button
              variant="ghost"
              size="sm"
              onClick={refresh}
              disabled={loading}
              aria-label="刷新"
            >
              <RefreshCw className={"size-4 " + (loading ? "animate-spin" : "")} />
            </Button>
          </div>
          <CardDescription>用于按量计费（超额使用），可随时充值</CardDescription>
        </CardHeader>
        <CardContent>
          <div className="flex flex-col gap-4 md:flex-row md:items-end md:justify-between">
            <div>
              <p className="text-xs text-muted-foreground">当前余额</p>
              <p className="mt-1 text-4xl font-semibold tabular-nums">
                {balance ? formatYuan(balance.balanceCents) : "¥—.—"}
              </p>
              <p className="mt-1 text-xs text-muted-foreground">
                {balance?.currency || "CNY"} · 更新于{" "}
                {formatTime(balance?.updatedAt)}
              </p>
            </div>
            <Button onClick={() => setShowRecharge(true)} size="lg">
              <ArrowUpCircle className="size-4" />
              充值
            </Button>
          </div>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle className="text-base">最近交易</CardTitle>
          <CardDescription>最新 50 条流水（含充值、扣费、退款）</CardDescription>
        </CardHeader>
        <CardContent>
          <TransactionsTable items={txns} loading={loading} />
        </CardContent>
      </Card>

      <RechargeDialog
        open={showRecharge}
        onClose={() => setShowRecharge(false)}
        onSuccess={() => {
          refresh();
          onChanged?.();
        }}
      />
    </div>
  );
}

function TransactionsTable({
  items,
  loading,
}: {
  items: BalanceTransaction[];
  loading: boolean;
}) {
  if (loading && items.length === 0) {
    return (
      <div className="flex h-32 items-center justify-center text-sm text-muted-foreground">
        加载中…
      </div>
    );
  }
  if (!items || items.length === 0) {
    return (
      <div className="flex h-32 items-center justify-center text-sm text-muted-foreground">
        暂无交易
      </div>
    );
  }
  return (
    <Table>
      <TableHeader>
        <TableRow>
          <TableHead>时间</TableHead>
          <TableHead>类型</TableHead>
          <TableHead>说明</TableHead>
          <TableHead className="text-right">金额</TableHead>
          <TableHead className="text-right">余额</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {items.map((t) => {
          const isIn = t.amountCents > 0;
          return (
            <TableRow key={t.id}>
              <TableCell className="text-xs text-muted-foreground">
                {formatTime(t.createdAt)}
              </TableCell>
              <TableCell>
                <Badge variant={isIn ? "default" : "secondary"} className="gap-1">
                  {isIn ? (
                    <ArrowUpCircle className="size-3" />
                  ) : (
                    <ArrowDownCircle className="size-3" />
                  )}
                  {KIND_LABEL[t.kind] ?? t.kind}
                </Badge>
              </TableCell>
              <TableCell
                className="max-w-[280px] truncate text-sm"
                title={t.description || t.reference || ""}
              >
                {t.description || t.reference || "—"}
              </TableCell>
              <TableCell
                className={
                  "text-right font-medium tabular-nums " +
                  (isIn ? "text-emerald-600" : "text-rose-600")
                }
              >
                {isIn ? "+" : ""}
                {formatYuan(t.amountCents)}
              </TableCell>
              <TableCell className="text-right text-xs text-muted-foreground tabular-nums">
                {formatYuan(t.balanceAfterCents)}
              </TableCell>
            </TableRow>
          );
        })}
      </TableBody>
    </Table>
  );
}

function RechargeDialog({
  open,
  onClose,
  onSuccess,
}: {
  open: boolean;
  onClose: () => void;
  onSuccess: () => void;
}) {
  const [amountYuan, setAmountYuan] = React.useState("10.00");
  const [provider, setProvider] = React.useState("mock");
  const [busy, setBusy] = React.useState(false);

  React.useEffect(() => {
    if (!open) {
      setAmountYuan("10.00");
      setProvider("mock");
      setBusy(false);
    }
  }, [open]);

  const cents = Math.round((Number.parseFloat(amountYuan || "0") || 0) * 100);

  const onSubmit = async () => {
    if (cents < 100) {
      toast.error("最低充值金额为 ¥1.00");
      return;
    }
    if (cents > 100 * 10000 * 100) {
      toast.error("单笔充值不得超过 ¥1,000,000");
      return;
    }
    if (!provider) {
      toast.error("请选择支付方式");
      return;
    }
    setBusy(true);
    try {
      const resp = await balanceApi.topup({
        amountCents: cents,
        paymentProvider: provider,
        successUrl: `${window.location.origin}/extra-usage?topup=success`,
      });
      if (resp.payUrl) {
        if (resp.payUrl.startsWith("mock://")) {
          toast.success(
            `测试支付：订单已创建（${resp.orderId}）。生产环境此处会跳转到支付页。`,
          );
        } else {
          window.open(resp.payUrl, "_blank", "noopener,noreferrer");
          toast.success("已创建订单，正在跳转支付页面…");
        }
      } else {
        toast.success(`订单已创建：${resp.orderId}`);
      }
      onClose();
      onSuccess();
    } catch (err) {
      toast.error(
        err instanceof ApiError ? `充值失败：${err.message}` : "网络异常",
      );
    } finally {
      setBusy(false);
    }
  };

  return (
    <Dialog open={open} onOpenChange={(o) => !o && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>账户充值</DialogTitle>
          <DialogDescription>
            充值金额将进入您的钱包，用于"超额使用"按量扣费。最低 ¥1.00。
          </DialogDescription>
        </DialogHeader>
        <div className="space-y-4">
          <div className="space-y-1.5">
            <Label htmlFor="amount" className="text-sm font-medium">
              充值金额（CNY）
            </Label>
            <Input
              id="amount"
              type="number"
              inputMode="decimal"
              min={1}
              step="0.01"
              value={amountYuan}
              onChange={(e) => setAmountYuan(e.target.value)}
              placeholder="10.00"
            />
            <div className="flex flex-wrap gap-2 pt-1">
              {QUICK_AMOUNTS_CENTS.map((c) => (
                <Button
                  key={c}
                  type="button"
                  variant="outline"
                  size="sm"
                  onClick={() => setAmountYuan((c / 100).toFixed(2))}
                >
                  ¥{(c / 100).toFixed(0)}
                </Button>
              ))}
            </div>
          </div>
          <div className="space-y-1.5">
            <Label className="text-sm font-medium">支付方式</Label>
            <PaymentMethodSelect
              value={provider}
              onChange={setProvider}
              disabled={busy}
            />
          </div>
          <div className="rounded-md border bg-muted/30 p-3 text-xs text-muted-foreground">
            预计入账：<span className="font-medium">{formatYuan(cents)}</span>
            ；支付成功后即时到账。
          </div>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={onClose} disabled={busy}>
            取消
          </Button>
          <Button
            onClick={onSubmit}
            disabled={busy || cents < 100 || !provider}
          >
            {busy ? "提交中…" : "确认充值"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
