"use client";

import * as React from "react";
import { useRouter } from "next/navigation";
import { ShieldCheck, ShieldAlert } from "lucide-react";

import { Button } from "@/components/ui/button";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { EnableTOTPDialog } from "@/components/settings/enable-totp-dialog";
import { DisableTOTPDialog } from "@/components/settings/disable-totp-dialog";

interface TOTPCardProps {
  enabled: boolean;
}

export function TOTPCard({ enabled }: TOTPCardProps) {
  const router = useRouter();
  const [enableOpen, setEnableOpen] = React.useState(false);
  const [disableOpen, setDisableOpen] = React.useState(false);

  // 任一开关流程成功后刷新 RSC，让上层 page.tsx 拉到最新 me() 反映新状态
  const handleSuccess = React.useCallback(() => {
    router.refresh();
  }, [router]);

  return (
    <Card>
      <CardHeader className="flex flex-row items-center justify-between gap-4 space-y-0">
        <div>
          <CardTitle className="text-base">两步验证 (TOTP)</CardTitle>
          <CardDescription>
            登录时除了密码再要求 Authenticator 6 位动态码
          </CardDescription>
        </div>
        {enabled ? (
          <span className="inline-flex items-center gap-1.5 rounded-full bg-emerald-500/10 px-2.5 py-1 text-xs font-medium text-emerald-700 dark:text-emerald-400">
            <ShieldCheck className="size-3.5" />
            已启用
          </span>
        ) : (
          <span className="inline-flex items-center gap-1.5 rounded-full bg-amber-500/10 px-2.5 py-1 text-xs font-medium text-amber-700 dark:text-amber-400">
            <ShieldAlert className="size-3.5" />
            未启用
          </span>
        )}
      </CardHeader>
      <CardContent className="space-y-3">
        {enabled ? (
          <>
            <p className="text-sm text-muted-foreground">
              账号已开启两步验证。下次登录会要求输入 Authenticator
              当前 6 位码。如果丢失了 Authenticator，请联系管理员手动关闭。
            </p>
            <Button
              variant="destructive"
              size="sm"
              onClick={() => setDisableOpen(true)}
            >
              关闭两步验证
            </Button>
          </>
        ) : (
          <>
            <p className="text-sm text-muted-foreground">
              建议启用两步验证以加强账号安全。需要手机上的 Authenticator
              应用（Google Authenticator / Authy / 1Password 等）。
            </p>
            <Button size="sm" onClick={() => setEnableOpen(true)}>
              启用两步验证
            </Button>
          </>
        )}
      </CardContent>

      <EnableTOTPDialog
        open={enableOpen}
        onOpenChange={setEnableOpen}
        onSuccess={handleSuccess}
      />
      <DisableTOTPDialog
        open={disableOpen}
        onOpenChange={setDisableOpen}
        onSuccess={handleSuccess}
      />
    </Card>
  );
}
