"use client";

import * as React from "react";
import { QRCodeSVG } from "qrcode.react";
import { Copy, Check, Loader2 } from "lucide-react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
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
import { authApi } from "@/lib/api/auth";
import { ApiError } from "@/lib/api/client";
import type { TOTPSecret } from "@/types/api";

interface EnableTOTPDialogProps {
  open: boolean;
  onOpenChange: (v: boolean) => void;
  onSuccess: () => void;
}

type Step = "loading" | "scan" | "verify" | "done";

/**
 * 启用 TOTP 三步对话框：
 *   loading  → 调用后端 enableTOTP() 获取 secret + otpauth URL
 *   scan     → 显示 QR + secret，用户在 Authenticator 中保存
 *   verify   → 输入当前 6 位码完成确认；后端落库
 *   done     → 成功提示，关闭对话框
 *
 * 用户取消会丢弃 pending（10 分钟自动 TTL；或前端不主动 cancel，简化流程）。
 */
export function EnableTOTPDialog({
  open,
  onOpenChange,
  onSuccess,
}: EnableTOTPDialogProps) {
  const [step, setStep] = React.useState<Step>("loading");
  const [secret, setSecret] = React.useState<TOTPSecret | null>(null);
  const [code, setCode] = React.useState("");
  const [busy, setBusy] = React.useState(false);
  const [err, setErr] = React.useState<string | null>(null);
  const [copied, setCopied] = React.useState(false);

  // 打开时拉一次 secret；关闭时复位
  React.useEffect(() => {
    if (!open) {
      // 等关闭动画走完再清，避免视觉抖动
      const t = setTimeout(() => {
        setStep("loading");
        setSecret(null);
        setCode("");
        setErr(null);
        setCopied(false);
      }, 200);
      return () => clearTimeout(t);
    }
    let cancelled = false;
    setStep("loading");
    authApi
      .enableTOTP()
      .then((s) => {
        if (cancelled) return;
        setSecret(s);
        setStep("scan");
      })
      .catch((e) => {
        if (cancelled) return;
        if (e instanceof ApiError) {
          if (e.status === 409) {
            setErr("两步验证已启用，请先关闭再重新启用");
          } else {
            setErr(e.message);
          }
        } else {
          setErr("网络异常，请稍后再试");
        }
      });
    return () => {
      cancelled = true;
    };
  }, [open]);

  const onCopySecret = async () => {
    if (!secret) return;
    try {
      await navigator.clipboard.writeText(secret.secretBase32);
      setCopied(true);
      toast.success("Secret 已复制");
      setTimeout(() => setCopied(false), 2000);
    } catch {
      toast.error("复制失败，请手动选择");
    }
  };

  const onConfirm = async () => {
    if (code.length !== 6 || !/^\d{6}$/.test(code)) {
      setErr("请输入 6 位数字验证码");
      return;
    }
    setBusy(true);
    setErr(null);
    try {
      await authApi.confirmEnableTOTP(code);
      setStep("done");
      toast.success("两步验证已启用");
      onSuccess();
      // 一秒后自动关闭，给用户看到 done 状态
      setTimeout(() => onOpenChange(false), 800);
    } catch (e) {
      if (e instanceof ApiError) {
        if (e.status === 401) {
          setErr("验证码错误或已失效，请使用 Authenticator 当前显示的码");
        } else if (e.status === 412) {
          setErr("启用流程已超时（10 分钟），请关闭后重新启用");
        } else {
          setErr(e.message);
        }
      } else {
        setErr("网络异常");
      }
    } finally {
      setBusy(false);
    }
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>启用两步验证</DialogTitle>
          <DialogDescription>
            用 Authenticator 扫描二维码，再输入显示的 6 位码完成启用
          </DialogDescription>
        </DialogHeader>

        {step === "loading" && (
          <div className="flex items-center justify-center py-12 text-sm text-muted-foreground">
            <Loader2 className="mr-2 size-4 animate-spin" />
            正在生成 secret...
          </div>
        )}

        {(step === "scan" || step === "verify") && secret && (
          <div className="space-y-4">
            <div className="flex justify-center rounded-lg border bg-white p-4">
              <QRCodeSVG
                value={secret.otpauthUrl}
                size={192}
                level="M"
                includeMargin={false}
              />
            </div>
            <div className="space-y-1.5">
              <Label className="text-xs text-muted-foreground">
                无法扫码？手动输入 secret
              </Label>
              <div className="flex gap-2">
                <Input
                  value={secret.secretBase32}
                  readOnly
                  className="font-mono text-xs"
                  onFocus={(e) => e.currentTarget.select()}
                />
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  onClick={onCopySecret}
                >
                  {copied ? (
                    <Check className="size-4" />
                  ) : (
                    <Copy className="size-4" />
                  )}
                </Button>
              </div>
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="totp-code">Authenticator 当前 6 位码</Label>
              <Input
                id="totp-code"
                inputMode="numeric"
                maxLength={6}
                autoComplete="one-time-code"
                placeholder="123456"
                value={code}
                onChange={(e) =>
                  setCode(e.target.value.replace(/\D/g, "").slice(0, 6))
                }
                className="font-mono tracking-widest"
              />
              {err && <p className="text-xs text-destructive">{err}</p>}
            </div>
          </div>
        )}

        {step === "done" && (
          <div className="flex items-center justify-center gap-2 py-12 text-emerald-600">
            <Check className="size-5" />
            <span>已成功启用</span>
          </div>
        )}

        {err && step === "loading" && (
          <p className="py-6 text-center text-sm text-destructive">{err}</p>
        )}

        <DialogFooter>
          <Button
            type="button"
            variant="outline"
            onClick={() => onOpenChange(false)}
            disabled={busy}
          >
            取消
          </Button>
          {(step === "scan" || step === "verify") && (
            <Button type="button" onClick={onConfirm} disabled={busy || code.length !== 6}>
              {busy ? "验证中..." : "确认启用"}
            </Button>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
