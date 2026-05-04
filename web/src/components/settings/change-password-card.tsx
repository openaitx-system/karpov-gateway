"use client";

import * as React from "react";
import { useForm } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
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
import { authApi } from "@/lib/api/auth";
import { ApiError } from "@/lib/api/client";
import {
  changePasswordSchema,
  type ChangePasswordInput,
} from "@/lib/validators";

export function ChangePasswordCard() {
  const [busy, setBusy] = React.useState(false);
  const {
    register,
    handleSubmit,
    reset,
    formState: { errors },
    setError,
  } = useForm<ChangePasswordInput>({
    resolver: zodResolver(changePasswordSchema),
    defaultValues: { oldPassword: "", newPassword: "", confirm: "" },
  });

  const onSubmit = async (v: ChangePasswordInput) => {
    setBusy(true);
    try {
      await authApi.changePassword({
        oldPassword: v.oldPassword,
        newPassword: v.newPassword,
      });
      toast.success("密码已更新");
      reset();
    } catch (err) {
      if (err instanceof ApiError) {
        if (err.status === 401) {
          setError("oldPassword", { message: "原密码不正确" });
        } else if (err.status === 422 || err.status === 400) {
          setError("newPassword", { message: err.message || "密码强度不足" });
        } else {
          toast.error(err.message);
        }
      } else {
        toast.error("网络异常");
      }
    } finally {
      setBusy(false);
    }
  };

  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-base">修改密码</CardTitle>
        <CardDescription>更新后会立即终止其他会话</CardDescription>
      </CardHeader>
      <CardContent>
        <form
          className="space-y-3"
          onSubmit={handleSubmit(onSubmit)}
          noValidate
          autoComplete="off"
        >
          <div className="space-y-1">
            <Label htmlFor="old">当前密码</Label>
            <Input id="old" type="password" {...register("oldPassword")} />
            {errors.oldPassword && (
              <p className="text-xs text-destructive">
                {errors.oldPassword.message}
              </p>
            )}
          </div>
          <div className="space-y-1">
            <Label htmlFor="new">新密码</Label>
            <Input id="new" type="password" {...register("newPassword")} />
            {errors.newPassword && (
              <p className="text-xs text-destructive">
                {errors.newPassword.message}
              </p>
            )}
          </div>
          <div className="space-y-1">
            <Label htmlFor="confirm">确认新密码</Label>
            <Input id="confirm" type="password" {...register("confirm")} />
            {errors.confirm && (
              <p className="text-xs text-destructive">{errors.confirm.message}</p>
            )}
          </div>
          <Button type="submit" disabled={busy}>
            {busy ? "提交中..." : "更新密码"}
          </Button>
        </form>
      </CardContent>
    </Card>
  );
}
