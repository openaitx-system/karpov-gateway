"use client";

import * as React from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { useForm } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { ApiError } from "@/lib/api/client";
import { authApi } from "@/lib/api/auth";
import { registerSchema, type RegisterInput } from "@/lib/validators";
import { useRuntimeConfig } from "@/components/runtime-config-provider";
import { cn } from "@/lib/utils";
import { SsoButtons } from "@/components/auth/sso-buttons";

interface RegisteredState {
  email: string;
  activationEmailSent: boolean;
}

export function RegisterForm() {
  const router = useRouter();
  const { config } = useRuntimeConfig();
  const [registered, setRegistered] = React.useState<RegisteredState | null>(null);
  // 邮箱子系统未拉到（首屏 / 拉取失败）时按"未启用"渲染——保守降级，
  // 不要因为 runtime-config 抖动让用户看到空验证码框。
  const emailCfg = config?.email_verification ?? {
    enabled: false,
    required: false,
    cooldown_seconds: 60,
    code_ttl_seconds: 600,
    hourly_limit_per_email: 5,
    allowed_domains: [],
    blocked_domains: [],
  };
  const showCodeField = emailCfg.enabled;
  const codeRequired = emailCfg.enabled && emailCfg.required;
  const allowedDomains = emailCfg.allowed_domains ?? [];
  const activation = config?.account_activation ?? {
    enabled: false,
    required: false,
    ttl_seconds: 86400,
  };

  const [submitting, setSubmitting] = React.useState(false);
  const [sendingCode, setSendingCode] = React.useState(false);
  const [cooldownLeft, setCooldownLeft] = React.useState(0);

  const {
    register,
    handleSubmit,
    watch,
    formState: { errors },
    setError,
    clearErrors,
  } = useForm<RegisterInput>({
    resolver: zodResolver(registerSchema),
    defaultValues: { email: "", password: "", confirm: "", verificationCode: "" },
  });

  const watchedEmail = watch("email");

  // 倒计时 tick：cooldownLeft > 0 时每秒减 1。
  // 用 ref 跑 setInterval 避免 React 18 严格模式重复挂载导致计时翻倍。
  React.useEffect(() => {
    if (cooldownLeft <= 0) return;
    const id = setInterval(() => {
      setCooldownLeft((s) => (s <= 1 ? 0 : s - 1));
    }, 1000);
    return () => clearInterval(id);
  }, [cooldownLeft]);

  const sendCode = async () => {
    const email = watchedEmail.trim();
    if (!email) {
      setError("email", { message: "请先输入邮箱" });
      return;
    }
    if (!isValidEmailShape(email)) {
      setError("email", { message: "邮箱格式不正确" });
      return;
    }
    if (!isDomainAllowedClientSide(email, allowedDomains)) {
      setError("email", {
        message: `仅支持以下域名：${allowedDomains.slice(0, 5).join(" / ")}`,
      });
      return;
    }
    clearErrors("email");
    setSendingCode(true);
    try {
      const resp = await authApi.sendEmailCode({ email, purpose: "register" });
      const cd = resp?.cooldownSeconds ?? emailCfg.cooldown_seconds ?? 60;
      setCooldownLeft(cd);
      toast.success(`验证码已发送，请查收（${Math.round((emailCfg.code_ttl_seconds ?? 600) / 60)} 分钟内有效）`);
    } catch (err) {
      handleSendError(err);
    } finally {
      setSendingCode(false);
    }
  };

  const handleSendError = (err: unknown) => {
    if (!(err instanceof ApiError)) {
      toast.error("网络异常，请稍后再试");
      return;
    }
    switch (err.status) {
      case 412: // domain not allowed
        setError("email", {
          message: allowedDomains.length
            ? `仅支持：${allowedDomains.join(" / ")}`
            : "邮箱域名不被允许",
        });
        return;
      case 429: {
        // cooldown 或 hourly limit；服务器没回 cooldown 时按本地默认估算
        const left = emailCfg.cooldown_seconds || 60;
        setCooldownLeft(left);
        const m = (err.message || "").toLowerCase();
        toast.error(
          m.includes("rate limit")
            ? `发送过于频繁，已达每小时上限 ${emailCfg.hourly_limit_per_email} 次`
            : `请等待 ${left} 秒后再次发送`,
        );
        return;
      }
      case 501:
        toast.error("后端未启用邮件服务，请联系管理员");
        return;
      default:
        toast.error(err.message || "发送验证码失败");
    }
  };

  const onSubmit = async (values: RegisterInput) => {
    if (codeRequired && !values.verificationCode) {
      setError("verificationCode", { message: "请输入邮箱验证码" });
      return;
    }
    setSubmitting(true);
    try {
      const resp = await authApi.register({
        email: values.email,
        password: values.password,
        verificationCode: values.verificationCode || undefined,
      });
      // 后端返回 activation_email_sent=true 时，切到"已发送激活邮件"成功页；
      // 否则照旧跳登录。runtime config 也可能 enabled=true 但 required=false，那时不切页。
      const sent = Boolean(resp?.activationEmailSent ?? activation.required);
      if (sent) {
        setRegistered({ email: values.email, activationEmailSent: true });
        return;
      }
      toast.success("注册成功，请登录");
      router.replace("/login");
    } catch (err) {
      handleRegisterError(err);
    } finally {
      setSubmitting(false);
    }
  };

  if (registered) {
    return (
      <ActivationPendingCard
        email={registered.email}
        ttlSeconds={activation.ttl_seconds || 86400}
        cooldownSeconds={emailCfg.cooldown_seconds || 60}
      />
    );
  }

  const handleRegisterError = (err: unknown) => {
    if (!(err instanceof ApiError)) {
      toast.error("网络异常，请稍后再试");
      return;
    }
    if (err.status === 409) {
      setError("email", { message: "邮箱已被注册" });
      return;
    }
    if (err.status === 403 && isIPAlreadyRegistered(err)) {
      const msg = "该 IP 已注册过账号，每个 IP 仅可注册一个账号";
      setError("email", { message: msg });
      toast.error(msg);
      return;
    }
    if (err.status === 412) {
      setError("email", {
        message: allowedDomains.length
          ? `仅支持：${allowedDomains.join(" / ")}`
          : "邮箱域名不被允许",
      });
      return;
    }
    if (err.status === 400) {
      const m = (err.message || "").toLowerCase();
      if (m.includes("verification code") || m.includes("email code")) {
        setError("verificationCode", { message: "验证码错误或已过期" });
        return;
      }
      // 后端 zxcvbn 强度评分不通过 / 邮箱格式
      setError("password", { message: err.message || "密码强度不足" });
      return;
    }
    toast.error(err.message || "注册失败");
  };

  return (
    <form
      className="space-y-4"
      onSubmit={handleSubmit(onSubmit)}
      noValidate
      autoComplete="on"
    >
      <div className="space-y-1.5">
        <Label htmlFor="email">邮箱</Label>
        <Input
          id="email"
          type="email"
          autoComplete="username"
          placeholder="you@example.com"
          {...register("email")}
        />
        {errors.email && (
          <p className="text-xs text-destructive">{errors.email.message}</p>
        )}
        {!errors.email && allowedDomains.length > 0 && (
          <p className="text-xs text-muted-foreground">
            仅支持邮箱：
            <span className="font-medium text-foreground">
              {allowedDomains.slice(0, 5).join(" · ")}
              {allowedDomains.length > 5 ? " 等" : ""}
            </span>
          </p>
        )}
      </div>

      {showCodeField && (
        <div className="space-y-1.5">
          <Label htmlFor="verificationCode">
            邮箱验证码
            {codeRequired && <span className="ml-1 text-destructive">*</span>}
            {!codeRequired && (
              <span className="ml-1 text-xs text-muted-foreground">（选填）</span>
            )}
          </Label>
          <div className="flex gap-2">
            <Input
              id="verificationCode"
              type="text"
              inputMode="numeric"
              autoComplete="one-time-code"
              maxLength={6}
              placeholder="6 位数字"
              className={cn("font-mono tracking-[0.4em] text-center")}
              {...register("verificationCode")}
            />
            <Button
              type="button"
              variant="outline"
              className="shrink-0 min-w-[120px]"
              disabled={sendingCode || cooldownLeft > 0}
              onClick={sendCode}
            >
              {sendingCode
                ? "发送中..."
                : cooldownLeft > 0
                  ? `${cooldownLeft}s 后重发`
                  : "发送验证码"}
            </Button>
          </div>
          {errors.verificationCode && (
            <p className="text-xs text-destructive">{errors.verificationCode.message}</p>
          )}
          {!errors.verificationCode && (
            <p className="text-xs text-muted-foreground">
              {codeRequired
                ? `验证码将发送到上方邮箱，${Math.round((emailCfg.code_ttl_seconds ?? 600) / 60)} 分钟内有效`
                : `已启用邮箱验证码，可选填以验证邮箱身份`}
            </p>
          )}
        </div>
      )}

      <div className="space-y-1.5">
        <Label htmlFor="password">密码</Label>
        <Input
          id="password"
          type="password"
          autoComplete="new-password"
          placeholder="至少 8 位，字母 + 数字"
          {...register("password")}
        />
        {errors.password && (
          <p className="text-xs text-destructive">{errors.password.message}</p>
        )}
        <p className="text-xs text-muted-foreground">
          至少 8 位，含字母与数字；后端会再做强度评分（zxcvbn）。
        </p>
      </div>

      <div className="space-y-1.5">
        <Label htmlFor="confirm">确认密码</Label>
        <Input
          id="confirm"
          type="password"
          autoComplete="new-password"
          placeholder="再次输入上方密码"
          {...register("confirm")}
        />
        {errors.confirm && (
          <p className="text-xs text-destructive">{errors.confirm.message}</p>
        )}
      </div>

      <Button type="submit" className="w-full" disabled={submitting}>
        {submitting ? "提交中..." : "注册"}
      </Button>
      <p className="text-center text-sm text-muted-foreground">
        已有账号？{" "}
        <Link href="/login" className="text-foreground hover:underline">
          去登录
        </Link>
      </p>
      <SsoButtons intent="login" next="/" />
    </form>
  );
}

// 后端 ErrIPAlreadyRegistered 文案是 "auth: this IP already registered an account"。
// 用关键字匹配而不是依赖 error_code，是因为 grpc-gateway 把 PermissionDenied 翻成 403
// 但 message 字段保留原样；其他 403 路径（账号 locked / API key IP 不允许）走不到注册接口。
function isIPAlreadyRegistered(err: ApiError): boolean {
  const m = (err.message || "").toLowerCase();
  return m.includes("ip already registered") || m.includes("this ip");
}

// 客户端最小邮箱形态校验（@ + 后段含点）；详细 zod 校验在 emailSchema。
function isValidEmailShape(s: string): boolean {
  const at = s.lastIndexOf("@");
  return at > 0 && at < s.length - 3 && s.slice(at + 1).includes(".");
}

// 客户端域名白名单预检（仅"尽早提示"，最终判定在后端）。
// 规则与后端 emailDomainPolicy 保持一致：允许列表为空 ⇒ 任意域；"*.edu.cn" 走后缀匹配。
function isDomainAllowedClientSide(email: string, allowed: string[]): boolean {
  if (allowed.length === 0) return true;
  const at = email.lastIndexOf("@");
  if (at < 0) return false;
  const domain = email.slice(at + 1).toLowerCase();
  for (const raw of allowed) {
    const r = raw.toLowerCase().trim();
    if (!r) continue;
    if (r.startsWith("*.")) {
      if (domain.endsWith(r.slice(1))) return true; // ".edu.cn"
    } else if (r.startsWith(".")) {
      if (domain.endsWith(r)) return true;
    } else if (r === domain) {
      return true;
    }
  }
  return false;
}

/**
 * ActivationPendingCard：注册成功后展示的"请去邮箱点击激活链接"卡片。
 *
 * - 顶部用大邮件图标 + 轻渐变让用户一眼意识到 next step；
 * - 下方"重发"按钮带 60s 倒计时（与后端 cooldown 一致）；
 * - 始终给"换个邮箱重注册"出口，避免拼错邮箱后只能等待。
 */
function ActivationPendingCard({
  email,
  ttlSeconds,
  cooldownSeconds,
}: {
  email: string;
  ttlSeconds: number;
  cooldownSeconds: number;
}) {
  const [resending, setResending] = React.useState(false);
  const [cooldown, setCooldown] = React.useState(0);
  const [resentTip, setResentTip] = React.useState(false);

  React.useEffect(() => {
    if (cooldown <= 0) return;
    const id = setInterval(() => setCooldown((s) => (s <= 1 ? 0 : s - 1)), 1000);
    return () => clearInterval(id);
  }, [cooldown]);

  const onResend = async () => {
    setResending(true);
    try {
      const r = await authApi.resendActivation(email);
      setCooldown(r?.cooldownSeconds ?? cooldownSeconds);
      setResentTip(true);
      toast.success("激活邮件已重发，请检查邮箱（含垃圾箱）");
    } catch (err) {
      if (err instanceof ApiError && err.status === 429) {
        setCooldown(cooldownSeconds);
        toast.error("发送过于频繁，请稍后再试");
      } else if (err instanceof ApiError && err.status === 501) {
        toast.error("后端未启用邮件服务");
      } else {
        toast.error("重发失败，请稍后再试");
      }
    } finally {
      setResending(false);
    }
  };

  const ttlHours = Math.max(1, Math.round(ttlSeconds / 3600));
  return (
    <div className="space-y-5">
      <div className="flex flex-col items-center text-center">
        <div className="mb-4 flex size-16 items-center justify-center rounded-full bg-gradient-to-br from-indigo-500/15 to-violet-500/10 ring-1 ring-inset ring-indigo-500/20 text-indigo-500">
          <MailIcon className="size-7" />
        </div>
        <h2 className="text-xl font-semibold">请前往邮箱激活账号</h2>
        <p className="mt-2 text-sm text-muted-foreground">
          我们已向 <span className="font-medium text-foreground">{email}</span>{" "}
          发送了一封激活邮件。
          <br />
          请打开邮件并点击"激活账号"按钮 —— 链接 {ttlHours} 小时内有效。
        </p>
      </div>

      <div className="rounded-lg border border-amber-500/20 bg-amber-500/5 p-3 text-xs text-amber-700 dark:text-amber-400">
        <strong className="font-medium">收不到邮件？</strong>{" "}
        请检查垃圾邮件 / 推广邮件分类；部分邮箱（如 QQ / 网易）可能有 1-2 分钟延迟。
      </div>

      <div className="space-y-2">
        <Button
          type="button"
          variant="outline"
          className="w-full"
          disabled={resending || cooldown > 0}
          onClick={onResend}
        >
          {resending
            ? "发送中..."
            : cooldown > 0
              ? `${cooldown}s 后可重发`
              : "重发激活邮件"}
        </Button>
        {resentTip && (
          <p className="text-xs text-muted-foreground text-center">
            如该邮箱有未激活账号，激活邮件已重发。
          </p>
        )}
        <Button asChild variant="secondary" className="w-full">
          <Link href="/login">已激活，去登录</Link>
        </Button>
      </div>
    </div>
  );
}

function MailIcon({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 24 24" fill="none" className={className}>
      <rect
        x="3"
        y="5"
        width="18"
        height="14"
        rx="2.5"
        stroke="currentColor"
        strokeWidth="2"
      />
      <path
        d="M3.5 7.5l8 6 8-6"
        stroke="currentColor"
        strokeWidth="2"
        strokeLinecap="round"
        strokeLinejoin="round"
      />
    </svg>
  );
}
