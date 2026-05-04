"use client";

import * as React from "react";
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

interface DisableTOTPDialogProps {
  open: boolean;
  onOpenChange: (v: boolean) => void;
  onSuccess: () => void;
}

/**
 * 关闭 TOTP：要求用户输入 Authenticator 当前码二次确认（防止
 * 攻击者借持有 session 的窗口悄悄关掉 2FA）。
 */
export function DisableTOTPDialog({
  open,
  onOpenChange,
  onSuccess,
}: DisableTOTPDialogProps) {
  const [code, setCode] = React.useState("");
  const [busy, setBusy] = React.useState(false);
  const [err, setErr] = React.useState<string | null>(null);

  React.useEffect(() => {
    if (!open) {
      const t = setTimeout(() => {
        setCode("");
        setErr(null);
      }, 200);
      return () => clearTimeout(t);
    }
  }, [open]);

  const onConfirm = async () => {
    if (code.length !== 6 || !/^\d{6}$/.test(code)) {
      setErr("请输入 6 位数字验证码");
      return;
    }
    setBusy(true);
    setErr(null);
    try {
      await authApi.disableTOTP(code);
      toast.success("两步验证已关闭");
      onSuccess();
      onOpenChange(false);
    } catch (e) {
      if (e instanceof ApiError) {
        if (e.status === 401) {
          setErr("验证码错误或已失效");
        } else if (e.status === 412) {
          setErr("当前账号未启用两步验证");
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
      <DialogContent className="sm:max-w-sm">
        <DialogHeader>
          <DialogTitle>关闭两步验证</DialogTitle>
          <DialogDescription>
            为了确认是本人操作，请输入当前 Authenticator 显示的 6 位码
          </DialogDescription>
        </DialogHeader>
        <div className="space-y-1.5">
          <Label htmlFor="disable-totp-code">动态验证码</Label>
          <Input
            id="disable-totp-code"
            inputMode="numeric"
            maxLength={6}
            autoComplete="one-time-code"
            placeholder="123456"
            value={code}
            onChange={(e) =>
              setCode(e.target.value.replace(/\D/g, "").slice(0, 6))
            }
            className="font-mono tracking-widest"
            autoFocus
          />
          {err && <p className="text-xs text-destructive">{err}</p>}
        </div>
        <DialogFooter>
          <Button
            type="button"
            variant="outline"
            onClick={() => onOpenChange(false)}
            disabled={busy}
          >
            取消
          </Button>
          <Button
            type="button"
            variant="destructive"
            onClick={onConfirm}
            disabled={busy || code.length !== 6}
          >
            {busy ? "处理中..." : "确认关闭"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
