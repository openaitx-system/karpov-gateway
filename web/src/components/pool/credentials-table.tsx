"use client";

import * as React from "react";
import { Copy, FlaskConical, Power, PowerOff, RefreshCcw, Trash2 } from "lucide-react";
import { toast } from "sonner";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Separator } from "@/components/ui/separator";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { ApiError } from "@/lib/api/client";
import { poolApi } from "@/lib/api/pool";
import { formatDate, formatDateTime } from "@/lib/datetime";
import { cn } from "@/lib/utils";
import type { PoolCredential } from "@/types/api";

interface CredentialsTableProps {
  items: PoolCredential[];
  loading?: boolean;
  total: number;
  page: number;
  pageSize: number;
  onPageChange: (next: number) => void;
  onChanged?: () => void;
}

/**
 * 号池凭证表格 + 翻页 + 单条操作（复制 ID / 启用 / 禁用 / 删除）。
 *
 * 列：标签 / Provider / 状态 / 能力 / 健康 / 失败 / 最近使用 / 创建时间 / 操作
 * 状态着色：active=绿，disabled=灰，banned=红，冷却中=黄。
 */
export function CredentialsTable({
  items,
  loading,
  total,
  page,
  pageSize,
  onPageChange,
  onChanged,
}: CredentialsTableProps) {
  const [pendingDelete, setPendingDelete] =
    React.useState<PoolCredential | null>(null);
  const [busyId, setBusyId] = React.useState<string | null>(null);

  const totalPages = Math.max(1, Math.ceil(total / pageSize));

  const onConfirmDelete = async () => {
    if (!pendingDelete) return;
    setBusyId(pendingDelete.id);
    try {
      await poolApi.remove(pendingDelete.id);
      toast.success(`凭证「${pendingDelete.label || pendingDelete.id}」已删除`);
      setPendingDelete(null);
      onChanged?.();
    } catch (err) {
      toast.error(
        err instanceof ApiError ? `删除失败：${err.message}` : "网络异常",
      );
    } finally {
      setBusyId(null);
    }
  };

  const onToggleStatus = async (c: PoolCredential) => {
    if (c.status === "banned") {
      toast.warning("已封禁的凭证由后台自动管理，请直接删除或修复后重新添加");
      return;
    }
    const next = c.status === "active" ? "disabled" : "active";
    setBusyId(c.id);
    try {
      await poolApi.setStatus(c.id, next);
      toast.success(next === "active" ? "已启用" : "已禁用");
      onChanged?.();
    } catch (err) {
      toast.error(
        err instanceof ApiError ? `操作失败：${err.message}` : "网络异常",
      );
    } finally {
      setBusyId(null);
    }
  };

  const onRefresh = async (c: PoolCredential) => {
    setBusyId(c.id);
    try {
      await poolApi.refresh(c.id);
      toast.success("凭据刷新成功");
      onChanged?.();
    } catch (err) {
      toast.error(
        err instanceof ApiError ? `刷新失败：${err.message}` : "网络异常",
      );
    } finally {
      setBusyId(null);
    }
  };

  const onTest = async (c: PoolCredential, capability: string) => {
    setBusyId(c.id);
    try {
      const res = await poolApi.test(c.id, capability);
      if (res.success) {
        toast.success(`测试通过：${capability}（${res.latencyMs}ms）`);
      } else {
        toast.error(`测试失败：${capability} — ${res.error}`);
      }
    } catch (err) {
      toast.error(
        err instanceof ApiError ? `测试失败：${err.message}` : "网络异常",
      );
    } finally {
      setBusyId(null);
    }
  };

  const onCopy = async (c: PoolCredential) => {
    try {
      await navigator.clipboard.writeText(c.id);
      toast.success("已复制 ID");
    } catch {
      toast.error("浏览器拒绝访问剪贴板");
    }
  };

  const showRangeStart = total === 0 ? 0 : page * pageSize + 1;
  const showRangeEnd = Math.min((page + 1) * pageSize, total);

  return (
    <>
      <div className="rounded-lg border bg-card">
        <Table>
          <TableHeader>
            <TableRow className="hover:bg-transparent">
              <TableHead className="min-w-[160px] pl-4">标签</TableHead>
              <TableHead>Provider</TableHead>
              <TableHead>状态</TableHead>
              <TableHead className="min-w-[200px]">能力</TableHead>
              <TableHead className="text-right">健康</TableHead>
              <TableHead className="text-right">失败</TableHead>
              <TableHead>最近使用</TableHead>
              <TableHead>创建时间</TableHead>
              <TableHead>过期时间</TableHead>
              <TableHead className="w-32 pr-4 text-right">操作</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {loading && items.length === 0 ? (
              <SkeletonRows count={5} />
            ) : items.length === 0 ? (
              <TableRow className="hover:bg-transparent">
                <TableCell colSpan={10} className="h-32 text-center">
                  <div className="flex flex-col items-center gap-1">
                    <p className="text-sm text-muted-foreground">暂无凭证</p>
                    <p className="text-xs text-muted-foreground">
                      点击「添加凭证」新建一条
                    </p>
                  </div>
                </TableCell>
              </TableRow>
            ) : (
              items.map((c) => {
                const busy = busyId === c.id;
                return (
                  <TableRow
                    key={c.id}
                    data-busy={busy}
                    className={busy ? "opacity-60" : undefined}
                  >
                    <TableCell className="pl-4 font-medium">
                      <div className="flex flex-col gap-0.5">
                        <span>{c.label || "(未命名)"}</span>
                        <code className="font-mono text-[10px] text-muted-foreground">
                          {c.id}
                        </code>
                      </div>
                    </TableCell>
                    <TableCell className="font-mono text-xs">
                      {c.provider}
                    </TableCell>
                    <TableCell>
                      <StatusBadge
                        status={c.status}
                        cooldownUntil={c.cooldownUntil}
                      />
                    </TableCell>
                    <TableCell>
                      <CapabilityList capabilities={c.capabilities} />
                    </TableCell>
                    <TableCell className="text-right tabular-nums">
                      <HealthCell value={c.healthScore} />
                    </TableCell>
                    <TableCell className="text-right tabular-nums">
                      {c.failCount ?? 0}
                    </TableCell>
                    <TableCell className="whitespace-nowrap text-xs text-muted-foreground">
                      {formatRelative(c.lastUsedAt)}
                    </TableCell>
                    <TableCell className="whitespace-nowrap text-xs text-muted-foreground">
                      {formatRelative(c.createdAt)}
                    </TableCell>
                    <TableCell className="whitespace-nowrap text-xs">
                      <ExpiryCell expiresAt={c.expiresAt} />
                    </TableCell>
                    <TableCell className="pr-4">
                      <div className="flex justify-end gap-0.5">
                        <IconButton
                          title="复制 ID"
                          onClick={() => void onCopy(c)}
                          disabled={busy}
                        >
                          <Copy className="size-4" />
                        </IconButton>
                        <DropdownMenu>
                          <DropdownMenuTrigger asChild>
                            <Button
                              variant="ghost"
                              size="icon"
                              className="size-8 p-0"
                              title="测试凭据"
                              disabled={busy || c.status === "banned"}
                            >
                              <FlaskConical className="size-4" />
                            </Button>
                          </DropdownMenuTrigger>
                          <DropdownMenuContent align="end">
                            {TEST_CAPABILITIES.map((t) => (
                              <DropdownMenuItem
                                key={t.value}
                                onClick={() => void onTest(c, t.value)}
                              >
                                {t.label}
                              </DropdownMenuItem>
                            ))}
                          </DropdownMenuContent>
                        </DropdownMenu>
                        <IconButton
                          title="刷新凭据"
                          onClick={() => void onRefresh(c)}
                          disabled={busy || c.status === "banned" || c.status === "refreshing"}
                        >
                          <RefreshCcw className={cn("size-4", c.status === "refreshing" && "animate-spin")} />
                        </IconButton>
                        <IconButton
                          title={c.status === "active" ? "禁用" : "启用"}
                          onClick={() => void onToggleStatus(c)}
                          disabled={busy || c.status === "banned" || c.status === "refreshing"}
                        >
                          {c.status === "active" ? (
                            <PowerOff className="size-4" />
                          ) : (
                            <Power className="size-4" />
                          )}
                        </IconButton>
                        <IconButton
                          title="删除"
                          onClick={() => setPendingDelete(c)}
                          disabled={busy}
                          tone="destructive"
                        >
                          <Trash2 className="size-4" />
                        </IconButton>
                      </div>
                    </TableCell>
                  </TableRow>
                );
              })
            )}
          </TableBody>
        </Table>

        <Separator />

        <div className="flex flex-col gap-2 px-4 py-3 sm:flex-row sm:items-center sm:justify-between">
          <span className="text-xs tabular-nums text-muted-foreground">
            {total === 0
              ? "共 0 条"
              : `第 ${showRangeStart}–${showRangeEnd} 条 · 共 ${total} 条`}
          </span>
          <div className="flex items-center gap-2">
            <span className="text-xs tabular-nums text-muted-foreground">
              第 {Math.min(page + 1, totalPages)} / {totalPages} 页
            </span>
            <Button
              variant="outline"
              size="sm"
              disabled={page <= 0 || loading}
              onClick={() => onPageChange(page - 1)}
            >
              上一页
            </Button>
            <Button
              variant="outline"
              size="sm"
              disabled={page + 1 >= totalPages || loading}
              onClick={() => onPageChange(page + 1)}
            >
              下一页
            </Button>
          </div>
        </div>
      </div>

      <Dialog
        open={pendingDelete !== null}
        onOpenChange={(o) => {
          if (!o) setPendingDelete(null);
        }}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>
              删除凭证「{pendingDelete?.label || pendingDelete?.id}」？
            </DialogTitle>
            <DialogDescription>
              此操作不可撤销。如仅想暂停调度，请使用「禁用」。
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button
              variant="ghost"
              onClick={() => setPendingDelete(null)}
              disabled={busyId !== null}
            >
              取消
            </Button>
            <Button
              variant="destructive"
              onClick={onConfirmDelete}
              disabled={busyId !== null}
            >
              {busyId === pendingDelete?.id ? "删除中…" : "确认删除"}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  );
}

function SkeletonRows({ count }: { count: number }) {
  return (
    <>
      {Array.from({ length: count }).map((_, i) => (
        <TableRow key={i} className="hover:bg-transparent">
          <TableCell colSpan={9} className="px-4 py-2">
            <div className="h-8 animate-pulse rounded bg-muted/60" />
          </TableCell>
        </TableRow>
      ))}
    </>
  );
}

function IconButton({
  children,
  title,
  onClick,
  disabled,
  tone,
}: {
  children: React.ReactNode;
  title: string;
  onClick: () => void;
  disabled?: boolean;
  tone?: "default" | "destructive";
}) {
  return (
    <Button
      variant="ghost"
      size="sm"
      type="button"
      title={title}
      aria-label={title}
      onClick={onClick}
      disabled={disabled}
      className={cn(
        "size-8 p-0",
        tone === "destructive" &&
          "text-destructive hover:bg-destructive/10 hover:text-destructive",
      )}
    >
      {children}
    </Button>
  );
}

function StatusBadge({
  status,
  cooldownUntil,
}: {
  status: string;
  cooldownUntil: string | undefined;
}) {
  const inCooldown =
    status === "active" &&
    cooldownUntil !== undefined &&
    new Date(cooldownUntil).getTime() > Date.now();
  if (inCooldown) {
    return <Badge variant="warning">冷却中</Badge>;
  }
  switch (status) {
    case "active":
      return <Badge variant="success">可用</Badge>;
    case "disabled":
      return <Badge variant="secondary">禁用</Badge>;
    case "banned":
      return <Badge variant="destructive">封禁</Badge>;
    case "refreshing":
      return <Badge variant="warning">刷新中</Badge>;
    default:
      return <Badge variant="outline">{status}</Badge>;
  }
}

function CapabilityList({
  capabilities,
}: {
  capabilities: string[] | undefined;
}) {
  if (!capabilities || capabilities.length === 0) {
    return <span className="text-xs text-muted-foreground">全部</span>;
  }
  const max = 3;
  const shown = capabilities.slice(0, max);
  const rest = capabilities.length - shown.length;
  return (
    <div className="flex flex-wrap gap-1">
      {shown.map((cap) => (
        <Badge key={cap} variant="outline" className="font-mono text-[10px]">
          {cap}
        </Badge>
      ))}
      {rest > 0 && (
        <Badge
          variant="outline"
          className="text-[10px]"
          title={capabilities.join(", ")}
        >
          +{rest}
        </Badge>
      )}
    </div>
  );
}

function HealthCell({ value }: { value: number }) {
  const tone =
    value >= 0.8
      ? "text-emerald-600 dark:text-emerald-400"
      : value >= 0.4
        ? "text-amber-600 dark:text-amber-400"
        : "text-destructive";
  return <span className={tone}>{value.toFixed(2)}</span>;
}

function formatRelative(iso: string | undefined): string {
  if (!iso) return "—";
  const t = new Date(iso).getTime();
  if (!Number.isFinite(t)) return "—";
  const delta = Date.now() - t;
  if (delta < 0) {
    // 未来时间（钟漂等）→ 直接绝对时间，不再说 "X 秒前"
    return formatDateTime(iso, { fallback: "—" });
  }
  const sec = Math.floor(delta / 1000);
  if (sec < 60) return `${sec} 秒前`;
  const min = Math.floor(sec / 60);
  if (min < 60) return `${min} 分钟前`;
  const hr = Math.floor(min / 60);
  if (hr < 24) return `${hr} 小时前`;
  const day = Math.floor(hr / 24);
  if (day < 30) return `${day} 天前`;
  return formatDate(iso, { fallback: "—" });
}

const TEST_CAPABILITIES = [
  { value: "HealthCheck", label: "连通性检测" },
  { value: "GetSong", label: "歌曲详情" },
  { value: "SearchSongs", label: "搜索歌曲" },
  { value: "GetSongURL", label: "歌曲链接" },
  { value: "GetLyric", label: "歌词" },
  { value: "GetAlbum", label: "专辑详情" },
  { value: "GetSinger", label: "歌手详情" },
  { value: "GetTopList", label: "排行榜" },
  { value: "GetRecommend", label: "推荐" },
  { value: "GetComments", label: "评论" },
] as const;

function ExpiryCell({ expiresAt }: { expiresAt: string | undefined }) {
  if (!expiresAt) return <span className="text-muted-foreground">—</span>;
  const t = new Date(expiresAt).getTime();
  if (!Number.isFinite(t)) return <span className="text-muted-foreground">—</span>;
  const remaining = t - Date.now();
  if (remaining <= 0) {
    return <span className="text-destructive font-medium">已过期</span>;
  }
  const hours = Math.floor(remaining / 3600_000);
  const days = Math.floor(hours / 24);
  const color = hours < 24 ? "text-destructive" : days < 7 ? "text-amber-600 dark:text-amber-400" : "text-muted-foreground";
  const text = days > 0 ? `${days} 天后` : `${hours} 小时后`;
  return <span className={color}>{text}</span>;
}
