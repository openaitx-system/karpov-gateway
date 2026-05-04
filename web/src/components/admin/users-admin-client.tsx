"use client";

import * as React from "react";
import {
  KeyRound,
  Pencil,
  RefreshCw,
  Search,
  ShieldCheck,
  Wallet,
  Zap,
} from "lucide-react";
import { toast } from "sonner";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Card,
  CardContent,
  CardDescription,
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
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import {
  type AdjustBalanceBody,
  type AdminUser,
  type AdminUserDetail,
  type BalanceOp,
  adminUsersApi,
} from "@/lib/api/admin-users";
import type { BalanceTransaction } from "@/lib/api/balance";
import { ApiError } from "@/lib/api/client";
import { formatDateTime } from "@/lib/datetime";
import { billingApi, type Plan } from "@/lib/api/billing";

const ROLES = ["user", "admin", "superadmin"];
const STATUSES = ["active", "locked", "disabled", "pending_email"];
const PAGE_SIZE = 20;

function formatYuan(cents: number): string {
  const sign = cents < 0 ? "-" : "";
  return `${sign}¥${(Math.abs(cents) / 100).toFixed(2)}`;
}

function formatTime(s?: string): string {
  return formatDateTime(s, { fallback: "—" });
}

function statusVariant(s: string): "default" | "secondary" | "destructive" {
  if (s === "active") return "default";
  if (s === "locked" || s === "disabled") return "destructive";
  return "secondary";
}

function roleVariant(r: string): "default" | "secondary" | "destructive" {
  if (r === "superadmin") return "destructive";
  if (r === "admin") return "default";
  return "secondary";
}

export function UsersAdminClient() {
  const [users, setUsers] = React.useState<AdminUser[]>([]);
  const [total, setTotal] = React.useState(0);
  const [loading, setLoading] = React.useState(true);
  const [emailFilter, setEmailFilter] = React.useState("");
  const [roleFilter, setRoleFilter] = React.useState<string>("");
  const [statusFilter, setStatusFilter] = React.useState<string>("");
  const [page, setPage] = React.useState(0);
  const [selectedID, setSelectedID] = React.useState<string | null>(null);

  const refresh = React.useCallback(async () => {
    setLoading(true);
    try {
      const res = await adminUsersApi.list({
        email: emailFilter,
        role: roleFilter,
        status: statusFilter,
        limit: PAGE_SIZE,
        offset: page * PAGE_SIZE,
      });
      setUsers(res.items ?? []);
      setTotal(res.total);
    } catch (err) {
      toast.error(
        err instanceof ApiError ? `加载失败：${err.message}` : "网络异常",
      );
    } finally {
      setLoading(false);
    }
  }, [emailFilter, roleFilter, statusFilter, page]);

  React.useEffect(() => {
    refresh();
  }, [refresh]);

  const totalPages = Math.max(1, Math.ceil(total / PAGE_SIZE));

  return (
    <div className="space-y-6">
      <Card>
        <CardHeader>
          <CardTitle className="text-base">筛选</CardTitle>
          <CardDescription>按邮箱、角色、状态查询用户</CardDescription>
        </CardHeader>
        <CardContent>
          <div className="grid gap-3 md:grid-cols-4">
            <div className="space-y-1.5 md:col-span-2">
              <Label className="text-xs">邮箱模糊匹配</Label>
              <div className="relative">
                <Search className="absolute left-2.5 top-2.5 size-4 text-muted-foreground" />
                <Input
                  className="pl-8"
                  value={emailFilter}
                  onChange={(e) => {
                    setEmailFilter(e.target.value);
                    setPage(0);
                  }}
                  placeholder="例如 example.com"
                />
              </div>
            </div>
            <div className="space-y-1.5">
              <Label className="text-xs">角色</Label>
              <Select
                value={roleFilter || "_all"}
                onValueChange={(v) => {
                  setRoleFilter(v === "_all" ? "" : v);
                  setPage(0);
                }}
              >
                <SelectTrigger>
                  <SelectValue placeholder="全部" />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="_all">全部</SelectItem>
                  {ROLES.map((r) => (
                    <SelectItem key={r} value={r}>
                      {r}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
            <div className="space-y-1.5">
              <Label className="text-xs">状态</Label>
              <Select
                value={statusFilter || "_all"}
                onValueChange={(v) => {
                  setStatusFilter(v === "_all" ? "" : v);
                  setPage(0);
                }}
              >
                <SelectTrigger>
                  <SelectValue placeholder="全部" />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="_all">全部</SelectItem>
                  {STATUSES.map((s) => (
                    <SelectItem key={s} value={s}>
                      {s}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
          </div>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <div className="flex items-center justify-between">
            <div>
              <CardTitle className="text-base">用户列表</CardTitle>
              <CardDescription>
                共 {total} 名 · 第 {page + 1} / {totalPages} 页
              </CardDescription>
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
        </CardHeader>
        <CardContent>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>邮箱</TableHead>
                <TableHead>角色</TableHead>
                <TableHead>套餐</TableHead>
                <TableHead>状态</TableHead>
                <TableHead>注册时间</TableHead>
                <TableHead className="text-right">操作</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {users.length === 0 && !loading && (
                <TableRow>
                  <TableCell colSpan={6} className="h-24 text-center text-sm text-muted-foreground">
                    无匹配用户
                  </TableCell>
                </TableRow>
              )}
              {users.map((u) => (
                <TableRow key={u.id}>
                  <TableCell className="font-medium">
                    <div className="flex items-center gap-2">
                      {u.email}
                      {u.totpEnabled && (
                        <ShieldCheck
                          className="size-3.5 text-emerald-600"
                          aria-label="2FA enabled"
                        />
                      )}
                    </div>
                    <div className="text-[11px] text-muted-foreground">{u.id}</div>
                  </TableCell>
                  <TableCell>
                    <Badge variant={roleVariant(u.role)}>{u.role}</Badge>
                  </TableCell>
                  <TableCell>
                    <Badge variant="outline">{u.planId || "free"}</Badge>
                  </TableCell>
                  <TableCell>
                    <Badge variant={statusVariant(u.status)}>{u.status}</Badge>
                  </TableCell>
                  <TableCell className="text-xs text-muted-foreground">
                    {formatTime(u.createdAt)}
                  </TableCell>
                  <TableCell className="text-right">
                    <Button
                      variant="ghost"
                      size="sm"
                      onClick={() => setSelectedID(u.id)}
                    >
                      <Pencil className="size-4" /> 管理
                    </Button>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>

          <div className="mt-4 flex items-center justify-end gap-2">
            <Button
              variant="outline"
              size="sm"
              disabled={page === 0 || loading}
              onClick={() => setPage((p) => Math.max(0, p - 1))}
            >
              上一页
            </Button>
            <Button
              variant="outline"
              size="sm"
              disabled={page + 1 >= totalPages || loading}
              onClick={() => setPage((p) => p + 1)}
            >
              下一页
            </Button>
          </div>
        </CardContent>
      </Card>

      <UserDetailDialog
        userId={selectedID}
        onClose={() => setSelectedID(null)}
        onChanged={refresh}
      />
    </div>
  );
}

function UserDetailDialog({
  userId,
  onClose,
  onChanged,
}: {
  userId: string | null;
  onClose: () => void;
  onChanged: () => void;
}) {
  const [detail, setDetail] = React.useState<AdminUserDetail | null>(null);
  const [loading, setLoading] = React.useState(false);

  const load = React.useCallback(async () => {
    if (!userId) return;
    setLoading(true);
    try {
      const d = await adminUsersApi.detail(userId);
      setDetail(d);
    } catch (err) {
      toast.error(
        err instanceof ApiError ? `加载失败：${err.message}` : "网络异常",
      );
    } finally {
      setLoading(false);
    }
  }, [userId]);

  React.useEffect(() => {
    if (userId) {
      load();
    } else {
      setDetail(null);
    }
  }, [userId, load]);

  return (
    <Dialog open={!!userId} onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="max-w-3xl">
        <DialogHeader>
          <DialogTitle>用户管理</DialogTitle>
          <DialogDescription>
            {detail?.user.email ?? "加载中…"}
          </DialogDescription>
        </DialogHeader>
        {loading && !detail && (
          <div className="flex h-48 items-center justify-center text-sm text-muted-foreground">
            加载中…
          </div>
        )}
        {detail && (
          <Tabs defaultValue="info">
            <TabsList>
              <TabsTrigger value="info">
                <Pencil className="size-3.5" /> 资料
              </TabsTrigger>
              <TabsTrigger value="balance">
                <Wallet className="size-3.5" /> 余额
              </TabsTrigger>
              <TabsTrigger value="plan">
                <Zap className="size-3.5" /> 套餐
              </TabsTrigger>
              <TabsTrigger value="keys">
                <KeyRound className="size-3.5" /> API Keys
              </TabsTrigger>
            </TabsList>
            <TabsContent value="info" className="mt-4">
              <InfoTab
                detail={detail}
                onChanged={() => {
                  load();
                  onChanged();
                }}
              />
            </TabsContent>
            <TabsContent value="balance" className="mt-4">
              <BalanceTab
                detail={detail}
                onChanged={() => {
                  load();
                  onChanged();
                }}
              />
            </TabsContent>
            <TabsContent value="plan" className="mt-4">
              <PlanTab
                detail={detail}
                onChanged={() => {
                  load();
                  onChanged();
                }}
              />
            </TabsContent>
            <TabsContent value="keys" className="mt-4">
              <KeysTab detail={detail} />
            </TabsContent>
          </Tabs>
        )}
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>
            关闭
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function InfoTab({
  detail,
  onChanged,
}: {
  detail: AdminUserDetail;
  onChanged: () => void;
}) {
  const [role, setRole] = React.useState(detail.user.role);
  const [status, setStatus] = React.useState(detail.user.status);
  const [busy, setBusy] = React.useState(false);
  const [pwd, setPwd] = React.useState("");
  const [pwdBusy, setPwdBusy] = React.useState(false);

  React.useEffect(() => {
    setRole(detail.user.role);
    setStatus(detail.user.status);
  }, [detail.user.id, detail.user.role, detail.user.status]);

  const dirty =
    role !== detail.user.role || status !== detail.user.status;

  const onSave = async () => {
    setBusy(true);
    try {
      const body: { role?: string; status?: string } = {};
      if (role !== detail.user.role) body.role = role;
      if (status !== detail.user.status) body.status = status;
      await adminUsersApi.update(detail.user.id, body);
      toast.success("已保存");
      onChanged();
    } catch (err) {
      toast.error(
        err instanceof ApiError ? `保存失败：${err.message}` : "网络异常",
      );
    } finally {
      setBusy(false);
    }
  };

  const onResetPwd = async () => {
    if (pwd.length < 6) {
      toast.error("密码至少 6 位");
      return;
    }
    setPwdBusy(true);
    try {
      await adminUsersApi.resetPassword(detail.user.id, pwd);
      toast.success("密码已重置，请告知用户");
      setPwd("");
    } catch (err) {
      toast.error(
        err instanceof ApiError ? `重置失败：${err.message}` : "网络异常",
      );
    } finally {
      setPwdBusy(false);
    }
  };

  return (
    <div className="space-y-5">
      <div className="grid gap-3 md:grid-cols-2">
        <div className="space-y-1">
          <Label className="text-xs text-muted-foreground">用户 ID</Label>
          <p className="font-mono text-xs">{detail.user.id}</p>
        </div>
        <div className="space-y-1">
          <Label className="text-xs text-muted-foreground">注册时间</Label>
          <p className="text-xs">{formatTime(detail.user.createdAt)}</p>
        </div>
      </div>

      <div className="grid gap-3 md:grid-cols-2">
        <div className="space-y-1.5">
          <Label className="text-sm">角色（仅 superadmin 可改）</Label>
          <Select value={role} onValueChange={setRole}>
            <SelectTrigger>
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {ROLES.map((r) => (
                <SelectItem key={r} value={r}>
                  {r}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>
        <div className="space-y-1.5">
          <Label className="text-sm">状态</Label>
          <Select value={status} onValueChange={setStatus}>
            <SelectTrigger>
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {STATUSES.map((s) => (
                <SelectItem key={s} value={s}>
                  {s}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>
      </div>

      <div className="flex justify-end">
        <Button onClick={onSave} disabled={!dirty || busy}>
          {busy ? "保存中…" : "保存"}
        </Button>
      </div>

      <div className="rounded-md border bg-muted/30 p-4">
        <Label className="text-sm font-medium">强制重置密码</Label>
        <p className="mt-1 text-xs text-muted-foreground">
          为该用户设置新的临时密码，需通过其他渠道告知用户。最少 6 位。
        </p>
        <div className="mt-3 flex items-center gap-2">
          <Input
            type="text"
            value={pwd}
            onChange={(e) => setPwd(e.target.value)}
            placeholder="至少 6 位"
            className="max-w-xs"
          />
          <Button
            variant="destructive"
            onClick={onResetPwd}
            disabled={pwdBusy || pwd.length < 6}
          >
            {pwdBusy ? "重置中…" : "重置密码"}
          </Button>
        </div>
      </div>
    </div>
  );
}

function BalanceTab({
  detail,
  onChanged,
}: {
  detail: AdminUserDetail;
  onChanged: () => void;
}) {
  const [op, setOp] = React.useState<BalanceOp>("credit");
  const [amount, setAmount] = React.useState("0.00");
  const [desc, setDesc] = React.useState("");
  const [busy, setBusy] = React.useState(false);
  const [txns, setTxns] = React.useState<BalanceTransaction[]>([]);

  const loadTxns = React.useCallback(async () => {
    try {
      const res = await adminUsersApi.listBalanceTransactions(
        detail.user.id,
        50,
        0,
      );
      setTxns(res.items ?? []);
    } catch {
      // ignore
    }
  }, [detail.user.id]);

  React.useEffect(() => {
    loadTxns();
  }, [loadTxns]);

  const submit = async () => {
    const yuan = Number.parseFloat(amount || "0");
    if (Number.isNaN(yuan) || yuan < 0) {
      toast.error("金额必须 ≥ 0");
      return;
    }
    if (op !== "set" && yuan <= 0) {
      toast.error(op === "credit" ? "充值金额必须 > 0" : "扣减金额必须 > 0");
      return;
    }
    const cents = Math.round(yuan * 100);
    setBusy(true);
    try {
      const body: AdjustBalanceBody = {
        operation: op,
        amountCents: cents,
        description: desc || undefined,
      };
      await adminUsersApi.adjustBalance(detail.user.id, body);
      toast.success(
        op === "credit" ? "已充值" : op === "debit" ? "已扣减" : "已设定",
      );
      setAmount("0.00");
      setDesc("");
      onChanged();
      loadTxns();
    } catch (err) {
      toast.error(
        err instanceof ApiError ? `操作失败：${err.message}` : "网络异常",
      );
    } finally {
      setBusy(false);
    }
  };

  const balance = detail.balance;
  return (
    <div className="space-y-5">
      <div className="rounded-lg border bg-muted/30 p-4">
        <Label className="text-xs text-muted-foreground">当前余额</Label>
        <p className="mt-1 text-3xl font-semibold tabular-nums">
          {balance ? formatYuan(balance.balanceCents) : "¥—.—"}
        </p>
        <p className="mt-1 text-xs text-muted-foreground">
          {balance?.currency ?? "CNY"} · 更新于{" "}
          {formatTime(balance?.updatedAt)}
        </p>
      </div>

      <div className="rounded-md border p-4">
        <Label className="text-sm font-medium">余额操作</Label>
        <div className="mt-3 grid gap-3 md:grid-cols-3">
          <div className="space-y-1.5">
            <Label className="text-xs">操作</Label>
            <Select value={op} onValueChange={(v) => setOp(v as BalanceOp)}>
              <SelectTrigger>
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="credit">增加（credit）</SelectItem>
                <SelectItem value="debit">减少（debit）</SelectItem>
                <SelectItem value="set">覆盖（set）</SelectItem>
              </SelectContent>
            </Select>
          </div>
          <div className="space-y-1.5">
            <Label className="text-xs">
              {op === "set" ? "新余额（CNY）" : "金额（CNY）"}
            </Label>
            <Input
              type="number"
              inputMode="decimal"
              min={0}
              step="0.01"
              value={amount}
              onChange={(e) => setAmount(e.target.value)}
            />
          </div>
          <div className="space-y-1.5">
            <Label className="text-xs">说明（可选）</Label>
            <Input
              value={desc}
              onChange={(e) => setDesc(e.target.value)}
              placeholder="审计可见"
            />
          </div>
        </div>
        <div className="mt-3 flex justify-end">
          <Button onClick={submit} disabled={busy}>
            {busy ? "提交中…" : "提交"}
          </Button>
        </div>
      </div>

      <div className="rounded-md border p-4">
        <Label className="text-sm font-medium">最近 50 条流水</Label>
        <div className="mt-3 max-h-80 overflow-auto">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>时间</TableHead>
                <TableHead>类型</TableHead>
                <TableHead>说明</TableHead>
                <TableHead className="text-right">变动</TableHead>
                <TableHead className="text-right">余额</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {txns.length === 0 && (
                <TableRow>
                  <TableCell colSpan={5} className="h-16 text-center text-xs text-muted-foreground">
                    无流水
                  </TableCell>
                </TableRow>
              )}
              {txns.map((t) => (
                <TableRow key={t.id}>
                  <TableCell className="text-xs text-muted-foreground">
                    {formatTime(t.createdAt)}
                  </TableCell>
                  <TableCell className="text-xs">{t.kind}</TableCell>
                  <TableCell
                    className="max-w-[180px] truncate text-xs"
                    title={t.description || t.reference || ""}
                  >
                    {t.description || t.reference || "—"}
                  </TableCell>
                  <TableCell
                    className={
                      "text-right font-medium tabular-nums " +
                      (t.amountCents > 0 ? "text-emerald-600" : "text-rose-600")
                    }
                  >
                    {t.amountCents > 0 ? "+" : ""}
                    {formatYuan(t.amountCents)}
                  </TableCell>
                  <TableCell className="text-right text-xs text-muted-foreground tabular-nums">
                    {formatYuan(t.balanceAfterCents)}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      </div>
    </div>
  );
}

function PlanTab({
  detail,
  onChanged,
}: {
  detail: AdminUserDetail;
  onChanged: () => void;
}) {
  const [plans, setPlans] = React.useState<Plan[]>([]);
  const [planId, setPlanId] = React.useState<string>("");
  const [busy, setBusy] = React.useState(false);

  React.useEffect(() => {
    billingApi
      .listPlans()
      .then((res: unknown) => {
        const items =
          (res as { plans?: Plan[]; items?: Plan[] }).plans ??
          (res as { items?: Plan[] }).items ??
          [];
        setPlans(items);
      })
      .catch(() => {
        // 忽略加载失败，仍可手动输入 planId
      });
  }, []);

  const submit = async () => {
    if (!planId) {
      toast.error("请选择套餐");
      return;
    }
    setBusy(true);
    try {
      const res = await adminUsersApi.setPlan(detail.user.id, planId);
      toast.success(`已切到 ${planId}（${res.updatedKeys}/${res.totalKeys} 个 Key）`);
      onChanged();
    } catch (err) {
      toast.error(
        err instanceof ApiError ? `切换失败：${err.message}` : "网络异常",
      );
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="space-y-5">
      <div className="rounded-md border bg-muted/30 p-4">
        <Label className="text-xs text-muted-foreground">用户当前套餐</Label>
        <div className="mt-1 flex items-center gap-2">
          <Badge className="text-base">{detail.user.planId || "free"}</Badge>
          <span className="text-xs text-muted-foreground">
            （新建 API Key 会继承此套餐）
          </span>
        </div>
      </div>

      <div className="rounded-md border p-4">
        <Label className="text-sm font-medium">已有 API Key 套餐分布</Label>
        {Object.keys(detail.planCounts).length === 0 ? (
          <p className="mt-2 text-xs text-muted-foreground">
            该用户没有可用 API Key
          </p>
        ) : (
          <div className="mt-2 flex flex-wrap gap-2">
            {Object.entries(detail.planCounts).map(([pid, n]) => (
              <Badge key={pid} variant="secondary">
                {pid}: {n} key
              </Badge>
            ))}
          </div>
        )}
      </div>

      <div className="rounded-md border p-4">
        <Label className="text-sm font-medium">切换套餐</Label>
        <p className="mt-1 text-xs text-muted-foreground">
          会同时更新用户的"当前套餐"和所有未吊销的 API Key。新创建的 Key 会继承此套餐。
        </p>
        <div className="mt-3 flex flex-wrap items-end gap-2">
          <div className="min-w-[200px] flex-1 space-y-1.5">
            <Label className="text-xs">目标套餐</Label>
            <Select value={planId} onValueChange={setPlanId}>
              <SelectTrigger>
                <SelectValue placeholder="选择套餐..." />
              </SelectTrigger>
              <SelectContent>
                {plans.map((p) => (
                  <SelectItem key={p.id ?? p.code ?? p.name} value={p.id ?? p.code ?? ""}>
                    {p.name}（{p.id ?? p.code}）
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
          <Button onClick={submit} disabled={busy || !planId}>
            {busy ? "切换中…" : "应用"}
          </Button>
        </div>
      </div>
    </div>
  );
}

function KeysTab({ detail }: { detail: AdminUserDetail }) {
  if (!detail.apiKeys || detail.apiKeys.length === 0) {
    return (
      <div className="flex h-32 items-center justify-center text-sm text-muted-foreground">
        该用户没有 API Key
      </div>
    );
  }
  return (
    <Table>
      <TableHeader>
        <TableRow>
          <TableHead>名称</TableHead>
          <TableHead>前缀</TableHead>
          <TableHead>套餐</TableHead>
          <TableHead>状态</TableHead>
          <TableHead>创建</TableHead>
          <TableHead>最近使用</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {detail.apiKeys.map((k) => (
          <TableRow key={k.id}>
            <TableCell className="font-medium">{k.name || "—"}</TableCell>
            <TableCell className="font-mono text-xs">{k.prefix}…</TableCell>
            <TableCell>
              <Badge variant="secondary">{k.planId || "—"}</Badge>
            </TableCell>
            <TableCell>
              <Badge variant={statusVariant(k.status)}>{k.status}</Badge>
            </TableCell>
            <TableCell className="text-xs text-muted-foreground">
              {formatTime(k.createdAt)}
            </TableCell>
            <TableCell className="text-xs text-muted-foreground">
              {formatTime(k.lastUsedAt)}
            </TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  );
}
