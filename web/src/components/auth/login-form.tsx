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
import { loginSchema, type LoginInput } from "@/lib/validators";
import { SsoButtons } from "@/components/auth/sso-buttons";

interface LoginFormProps {
  next: string;
}

/**
 * 登录表单：支持单步登录 + 双因素登录两种路径。
 *
 * 当后端账号开启 TOTP 时：
 *  1) 第一次提交（密码） → 后端返回 totpRequired=true + challengeId
 *  2) 表单切换到 OTP 输入态，缓存 challengeId
 *  3) 第二次提交（OTP） → 走 verifyTOTP(challengeId, code) 拿 sid
 *
 * 之所以不在第二次提交时复用 login()：
 *   - login(email,password,otp) 的 challenge 是新的，会浪费上次 challenge
 *   - 后端期待 verifyTOTP 路径走 SkipPaths，不需要 session cookie
 */
export function LoginForm({ next }: LoginFormProps) {
  const router = useRouter();
  const [needTotp, setNeedTotp] = React.useState(false);
  const [challengeId, setChallengeId] = React.useState<string | null>(null);
  const [submitting, setSubmitting] = React.useState(false);
  const [pendingActivation, setPendingActivation] = React.useState<string | null>(null);
  const [resendCooldown, setResendCooldown] = React.useState(0);
  const [resending, setResending] = React.useState(false);

  // 重发倒计时 tick
  React.useEffect(() => {
    if (resendCooldown <= 0) return;
    const id = setInterval(() => setResendCooldown((s) => (s <= 1 ? 0 : s - 1)), 1000);
    return () => clearInterval(id);
  }, [resendCooldown]);
  const {
    register,
    handleSubmit,
    formState: { errors },
    setError,
    setValue,
    setFocus,
  } = useForm<LoginInput>({
    resolver: zodResolver(loginSchema),
    defaultValues: { email: "", password: "", totpCode: "" },
  });

  // 切到 TOTP 步骤时聚焦码输入
  React.useEffect(() => {
    if (needTotp) {
      setFocus("totpCode");
    }
  }, [needTotp, setFocus]);

  const finishLogin = React.useCallback(() => {
    toast.success("登录成功");
    router.replace(next);
    router.refresh();
  }, [router, next]);

  const onSubmit = async (values: LoginInput) => {
    setSubmitting(true);
    try {
      // 第二步：用 challengeId 完成 TOTP
      if (needTotp && challengeId) {
        if (!values.totpCode || !/^\d{6}$/.test(values.totpCode)) {
          setError("totpCode", { message: "请输入 6 位数字验证码" });
          return;
        }
        await authApi.verifyTOTP(challengeId, values.totpCode);
        finishLogin();
        return;
      }

      // 第一步：邮箱 + 密码
      const res = await authApi.login({
        email: values.email,
        password: values.password,
        totpCode: values.totpCode || undefined,
      });
      if (res.totpRequired) {
        if (!res.challengeId) {
          // 不可能但兜底
          toast.error("登录服务异常：未返回 challenge_id");
          return;
        }
        setChallengeId(res.challengeId);
        setNeedTotp(true);
        setValue("totpCode", "");
        toast.info("请输入两步验证码");
        return;
      }
      finishLogin();
    } catch (err) {
      if (err instanceof ApiError) {
        if (needTotp) {
          // 第二步失败：通常是码错或 challenge 过期
          if (err.status === 401) {
            // challenge 过期 vs 码错；都让用户回到密码步重试更安全
            if (err.message.includes("challenge") || err.message.includes("expired")) {
              toast.error("会话已过期，请重新登录");
              setNeedTotp(false);
              setChallengeId(null);
              setValue("totpCode", "");
            } else {
              setError("totpCode", { message: "验证码错误，请重新输入" });
            }
          } else {
            toast.error(err.message);
          }
          return;
        }
        if (err.status === 412 && isAccountNotActivated(err)) {
          // 后端 ErrAccountNotActivated → 412：邮箱与密码已经过校验，确实是该账号但未激活。
          // 这里把"未激活"banner 挂出来，附带"重发激活邮件"按钮。
          setPendingActivation(values.email.trim());
          return;
        }
        if (err.status === 401 || err.status === 403) {
          setError("password", { message: "邮箱或密码不正确" });
        } else if (err.status === 429) {
          toast.error("登录尝试过于频繁，请稍后再试");
        } else {
          toast.error(err.message);
        }
      } else {
        toast.error("网络异常，请稍后再试");
      }
    } finally {
      setSubmitting(false);
    }
  };

  const onCancelTOTP = () => {
    setNeedTotp(false);
    setChallengeId(null);
    setValue("totpCode", "");
  };

  const onResendActivation = async () => {
    if (!pendingActivation) return;
    setResending(true);
    try {
      const r = await authApi.resendActivation(pendingActivation);
      setResendCooldown(r?.cooldownSeconds ?? 60);
      toast.success("激活邮件已重发，请检查邮箱（含垃圾箱）");
    } catch (err) {
      if (err instanceof ApiError && err.status === 429) {
        setResendCooldown(60);
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

  return (
    <form
      className="space-y-4"
      onSubmit={handleSubmit(onSubmit)}
      noValidate
      autoComplete="on"
    >
      {pendingActivation && (
        <div className="rounded-lg border border-amber-500/30 bg-amber-500/5 p-3 text-sm">
          <div className="flex items-start gap-2">
            <span aria-hidden className="mt-0.5 inline-block size-2 shrink-0 rounded-full bg-amber-500" />
            <div className="flex-1">
              <p className="font-medium text-amber-700 dark:text-amber-400">
                账号尚未激活
              </p>
              <p className="mt-1 text-xs text-muted-foreground">
                请前往 <span className="font-medium text-foreground">{pendingActivation}</span>{" "}
                查收激活邮件并点击链接激活，或重发一封：
              </p>
              <div className="mt-2 flex gap-2">
                <Button
                  type="button"
                  size="sm"
                  variant="outline"
                  disabled={resending || resendCooldown > 0}
                  onClick={onResendActivation}
                >
                  {resending
                    ? "发送中..."
                    : resendCooldown > 0
                      ? `${resendCooldown}s 后重发`
                      : "重发激活邮件"}
                </Button>
                <Button
                  type="button"
                  size="sm"
                  variant="ghost"
                  onClick={() => setPendingActivation(null)}
                >
                  关闭提示
                </Button>
              </div>
            </div>
          </div>
        </div>
      )}
      <div className="space-y-1">
        <Label htmlFor="email">邮箱</Label>
        <Input
          id="email"
          type="email"
          autoComplete="username"
          autoCapitalize="off"
          autoCorrect="off"
          spellCheck={false}
          disabled={needTotp}
          {...register("email")}
        />
        {errors.email && (
          <p className="text-xs text-destructive">{errors.email.message}</p>
        )}
      </div>
      <div className="space-y-1">
        <Label htmlFor="password">密码</Label>
        <Input
          id="password"
          type="password"
          autoComplete="current-password"
          disabled={needTotp}
          {...register("password")}
        />
        {errors.password && (
          <p className="text-xs text-destructive">{errors.password.message}</p>
        )}
      </div>
      {needTotp && (
        <div className="space-y-1">
          <Label htmlFor="totp">两步验证码</Label>
          <Input
            id="totp"
            inputMode="numeric"
            maxLength={6}
            autoComplete="one-time-code"
            autoFocus
            placeholder="123456"
            className="font-mono tracking-widest"
            {...register("totpCode")}
          />
          {errors.totpCode && (
            <p className="text-xs text-destructive">{errors.totpCode.message}</p>
          )}
          <p className="text-xs text-muted-foreground">
            打开 Authenticator 应用读取当前 6 位码
          </p>
        </div>
      )}
      <Button type="submit" className="w-full" disabled={submitting}>
        {submitting ? "登录中..." : needTotp ? "完成登录" : "登录"}
      </Button>
      {needTotp && (
        <Button
          type="button"
          variant="ghost"
          className="w-full"
          onClick={onCancelTOTP}
          disabled={submitting}
        >
          返回重新输入密码
        </Button>
      )}
      <div className="flex justify-between text-sm text-muted-foreground">
        <Link href="/register" className="hover:text-foreground">
          注册新账号
        </Link>
        <Link href="/forgot-password" className="hover:text-foreground">
          忘记密码？
        </Link>
      </div>
      {!needTotp && <SsoButtons intent="login" next={next} />}
    </form>
  );
}

// 后端 ErrAccountNotActivated 文案是 "auth: account not activated"，
// 走 codes.FailedPrecondition → HTTP 412。其它 412 路径（域名不允许 / TOTP not enabled）
// 不会出现在 Login 入口，所以匹配关键字足够用。
function isAccountNotActivated(err: ApiError): boolean {
  const m = (err.message || "").toLowerCase();
  return m.includes("not activated") || m.includes("not_activated");
}
