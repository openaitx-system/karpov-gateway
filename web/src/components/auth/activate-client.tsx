"use client";

import * as React from "react";
import Link from "next/link";
import { useSearchParams, useRouter } from "next/navigation";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { authApi } from "@/lib/api/auth";
import { ApiError } from "@/lib/api/client";
import { cn } from "@/lib/utils";

type Phase =
  | { kind: "loading" }
  | { kind: "success"; email: string }
  | { kind: "expired" }
  | { kind: "invalid" }
  | { kind: "alreadyActive"; email?: string }
  | { kind: "missingToken" }
  | { kind: "network" };

/**
 * 客户端激活落地：
 *   1. 取 ?token=xxx；空 → missingToken
 *   2. 调 verifyEmail：
 *        200 → success（5 秒后自动跳登录）
 *        404 → invalid（不可恢复，提示重新注册或检查链接）
 *        DeadlineExceeded(504) → expired（提供"重发激活邮件"输入框）
 *        409 → alreadyActive（直接给登录按钮）
 *        其它 → network
 */
export function ActivateClient() {
  const router = useRouter();
  const params = useSearchParams();
  const token = params.get("token") ?? "";

  const [phase, setPhase] = React.useState<Phase>({ kind: "loading" });
  // 用 ref 保证严格模式 mount/unmount 不会触发两次 verify。
  const triggered = React.useRef(false);

  React.useEffect(() => {
    if (triggered.current) return;
    triggered.current = true;
    if (!token) {
      setPhase({ kind: "missingToken" });
      return;
    }
    let cancelled = false;
    authApi
      .verifyEmail(token)
      .then((r) => {
        if (cancelled) return;
        setPhase({ kind: "success", email: r.email });
      })
      .catch((err) => {
        if (cancelled) return;
        setPhase(mapVerifyError(err));
      });
    return () => {
      cancelled = true;
    };
  }, [token]);

  // success 状态自动倒计时跳登录（5s）
  const [countdown, setCountdown] = React.useState(5);
  React.useEffect(() => {
    if (phase.kind !== "success") return;
    if (countdown <= 0) {
      router.replace("/login");
      return;
    }
    const id = setTimeout(() => setCountdown((c) => c - 1), 1000);
    return () => clearTimeout(id);
  }, [phase.kind, countdown, router]);

  switch (phase.kind) {
    case "loading":
      return <Loading />;
    case "success":
      return <SuccessView email={phase.email} secondsLeft={countdown} />;
    case "expired":
      return <ExpiredView />;
    case "invalid":
      return <InvalidView />;
    case "alreadyActive":
      return <AlreadyActiveView />;
    case "missingToken":
      return <MissingTokenView />;
    case "network":
      return <NetworkErrorView onRetry={() => window.location.reload()} />;
  }
}

function mapVerifyError(err: unknown): Phase {
  if (!(err instanceof ApiError)) return { kind: "network" };
  // mapAuthError 把 ErrActivationTokenExpired → DeadlineExceeded → grpc-gateway 翻 504。
  // ErrActivationTokenInvalid → NotFound → 404；ErrActivationTokenUsed/AlreadyActive → AlreadyExists → 409。
  switch (err.status) {
    case 504:
    case 408: // 兼容某些反代把 DeadlineExceeded 翻成 408
    case 410: // 若反代翻成 410 也归一到 expired
      return { kind: "expired" };
    case 404:
      return { kind: "invalid" };
    case 409:
      return { kind: "alreadyActive" };
    case 400: {
      const m = (err.message || "").toLowerCase();
      if (m.includes("expired")) return { kind: "expired" };
      if (m.includes("used") || m.includes("already")) return { kind: "alreadyActive" };
      return { kind: "invalid" };
    }
    default:
      return { kind: "network" };
  }
}

function Loading() {
  return (
    <div className="flex flex-col items-center justify-center gap-3 py-8">
      <div className="size-10 animate-spin rounded-full border-4 border-muted border-t-primary" />
      <p className="text-sm text-muted-foreground">正在激活账号，请稍候…</p>
    </div>
  );
}

function StatusCard({
  tone,
  icon,
  title,
  description,
  children,
}: {
  tone: "success" | "warning" | "error" | "info";
  icon: React.ReactNode;
  title: string;
  description: React.ReactNode;
  children?: React.ReactNode;
}) {
  const ring = {
    success: "from-emerald-500/15 to-emerald-500/5 ring-emerald-500/20 text-emerald-500",
    warning: "from-amber-500/15 to-amber-500/5 ring-amber-500/20 text-amber-500",
    error: "from-rose-500/15 to-rose-500/5 ring-rose-500/20 text-rose-500",
    info: "from-sky-500/15 to-sky-500/5 ring-sky-500/20 text-sky-500",
  }[tone];
  return (
    <div className="flex flex-col items-center text-center">
      <div
        className={cn(
          "mb-4 flex size-16 items-center justify-center rounded-full bg-gradient-to-br ring-1 ring-inset",
          ring,
        )}
      >
        {icon}
      </div>
      <h2 className="text-xl font-semibold">{title}</h2>
      <div className="mt-2 text-sm text-muted-foreground">{description}</div>
      {children && <div className="mt-5 w-full">{children}</div>}
    </div>
  );
}

function SuccessView({ email, secondsLeft }: { email: string; secondsLeft: number }) {
  return (
    <StatusCard
      tone="success"
      icon={<CheckIcon className="size-7" />}
      title="账号激活成功"
      description={
        <>
          <span className="font-medium text-foreground">{email}</span> 已可登录。
          <br />
          <span className="text-xs">{secondsLeft} 秒后自动跳转登录页…</span>
        </>
      }
    >
      <Button asChild className="w-full">
        <Link href="/login">立即去登录</Link>
      </Button>
    </StatusCard>
  );
}

function ExpiredView() {
  return (
    <StatusCard
      tone="warning"
      icon={<ClockIcon className="size-7" />}
      title="链接已过期"
      description="激活链接超出有效期。请输入注册邮箱，我们会重发一封新的激活邮件。"
    >
      <ResendForm reason="expired" />
    </StatusCard>
  );
}

function InvalidView() {
  return (
    <StatusCard
      tone="error"
      icon={<XIcon className="size-7" />}
      title="链接无效"
      description="未找到对应的激活记录。可能是链接被截断、复制不完整，或账号已被删除。"
    >
      <div className="flex flex-col gap-2">
        <ResendForm reason="invalid" />
        <Button variant="outline" asChild className="w-full">
          <Link href="/register">重新注册</Link>
        </Button>
      </div>
    </StatusCard>
  );
}

function AlreadyActiveView() {
  return (
    <StatusCard
      tone="info"
      icon={<CheckIcon className="size-7" />}
      title="账号已激活"
      description="该账号此前已激活过，无需重复操作。"
    >
      <Button asChild className="w-full">
        <Link href="/login">直接去登录</Link>
      </Button>
    </StatusCard>
  );
}

function MissingTokenView() {
  return (
    <StatusCard
      tone="error"
      icon={<XIcon className="size-7" />}
      title="缺少激活令牌"
      description="请从注册邮件里点击完整的激活链接进入本页。"
    >
      <div className="flex flex-col gap-2">
        <Button asChild className="w-full">
          <Link href="/login">去登录</Link>
        </Button>
        <Button variant="outline" asChild className="w-full">
          <Link href="/register">去注册</Link>
        </Button>
      </div>
    </StatusCard>
  );
}

function NetworkErrorView({ onRetry }: { onRetry: () => void }) {
  return (
    <StatusCard
      tone="error"
      icon={<XIcon className="size-7" />}
      title="网络异常"
      description="后端服务暂时不可达，请稍后重试。"
    >
      <Button onClick={onRetry} className="w-full">
        重新加载
      </Button>
    </StatusCard>
  );
}

/** 重发激活邮件子表单：在 expired/invalid 卡片里嵌入。 */
function ResendForm({ reason }: { reason: "expired" | "invalid" }) {
  const [email, setEmail] = React.useState("");
  const [submitting, setSubmitting] = React.useState(false);
  const [done, setDone] = React.useState(false);
  const [error, setError] = React.useState<string | null>(null);
  const [cooldown, setCooldown] = React.useState(0);

  React.useEffect(() => {
    if (cooldown <= 0) return;
    const id = setInterval(() => setCooldown((s) => (s <= 1 ? 0 : s - 1)), 1000);
    return () => clearInterval(id);
  }, [cooldown]);

  const onSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!email.includes("@")) {
      setError("邮箱格式不正确");
      return;
    }
    setError(null);
    setSubmitting(true);
    try {
      const r = await authApi.resendActivation(email);
      setDone(true);
      setCooldown(r?.cooldownSeconds ?? 60);
    } catch (err) {
      if (err instanceof ApiError && err.status === 429) {
        setCooldown(60);
        setError("发送过于频繁，请稍后再试");
      } else if (err instanceof ApiError && err.status === 412) {
        setError("该邮箱域名不被允许");
      } else if (err instanceof ApiError && err.status === 501) {
        setError("后端未启用邮件服务");
      } else {
        setError("发送失败，请稍后再试");
      }
    } finally {
      setSubmitting(false);
    }
  };

  if (done) {
    return (
      <div className="rounded-lg border border-emerald-500/20 bg-emerald-500/5 p-3 text-left text-sm text-emerald-700 dark:text-emerald-400">
        如该邮箱有未激活账号，激活邮件已重发；请检查邮箱（含垃圾箱）。
        {cooldown > 0 && (
          <div className="mt-1 text-xs text-muted-foreground">
            可在 {cooldown}s 后再次重发
          </div>
        )}
      </div>
    );
  }

  return (
    <form onSubmit={onSubmit} className="flex flex-col gap-2 text-left">
      <Label htmlFor="resend-email" className="text-xs">
        {reason === "expired" ? "重发激活邮件到：" : "把新链接发给我："}
      </Label>
      <Input
        id="resend-email"
        type="email"
        autoComplete="email"
        placeholder="you@example.com"
        value={email}
        onChange={(e) => setEmail(e.target.value)}
      />
      {error && <p className="text-xs text-destructive">{error}</p>}
      <Button type="submit" className="w-full" disabled={submitting || cooldown > 0}>
        {submitting
          ? "发送中..."
          : cooldown > 0
            ? `${cooldown}s 后可重发`
            : "重发激活邮件"}
      </Button>
    </form>
  );
}

// ---- icons ----
function CheckIcon({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 24 24" fill="none" className={className}>
      <path
        d="M5 12.5l4 4L19 6"
        stroke="currentColor"
        strokeWidth="2.4"
        strokeLinecap="round"
        strokeLinejoin="round"
      />
    </svg>
  );
}

function ClockIcon({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 24 24" fill="none" className={className}>
      <circle cx="12" cy="12" r="9" stroke="currentColor" strokeWidth="2.2" />
      <path
        d="M12 7v5l3.5 2"
        stroke="currentColor"
        strokeWidth="2.2"
        strokeLinecap="round"
        strokeLinejoin="round"
      />
    </svg>
  );
}

function XIcon({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 24 24" fill="none" className={className}>
      <path
        d="M6 6l12 12M18 6L6 18"
        stroke="currentColor"
        strokeWidth="2.4"
        strokeLinecap="round"
      />
    </svg>
  );
}
