"use client";

import * as React from "react";
import { Save } from "lucide-react";
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
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Separator } from "@/components/ui/separator";
import { Textarea } from "@/components/ui/textarea";
import { settingsApi, type PaymentChannel } from "@/lib/api/settings";
import { ApiError } from "@/lib/api/client";

export function PaymentChannelsAdmin() {
  const [channels, setChannels] = React.useState<PaymentChannel[]>([]);
  const [loading, setLoading] = React.useState(true);
  const [saving, setSaving] = React.useState(false);

  React.useEffect(() => {
    settingsApi
      .getPaymentChannels()
      .then((res) => {
        let chs = res.channels ?? [];
        if (!chs.find((c: PaymentChannel) => c.provider === "yipay")) {
          chs = [...chs, { provider: "yipay", enabled: false, yipayBaseUrl: "", yipayMerchantId: "", yipayKey: "", yipayPaymentType: "alipay" }];
        }
        if (!chs.find((c: PaymentChannel) => c.provider === "hupijiao")) {
          chs = [...chs, { provider: "hupijiao", enabled: false, hupijiaoBaseUrl: "", hupijiaoAppId: "", hupijiaoKey: "", hupijiaoWapName: "" }];
        }
        if (!chs.find((c: PaymentChannel) => c.provider === "ldcpay")) {
          chs = [...chs, {
            provider: "ldcpay",
            enabled: false,
            ldcBaseUrl: "https://credit.linux.do/epay",
            ldcClientId: "",
            ldcClientSecret: "",
            ldcMerchantPrivateKey: "",
            ldcPlatformPublicKey: "",
          }];
        }
        setChannels(chs);
      })
      .catch(() => toast.error("加载支付配置失败"))
      .finally(() => setLoading(false));
  }, []);

  const updateChannel = (idx: number, patch: Partial<PaymentChannel>) => {
    setChannels((prev) =>
      prev.map((ch, i) => (i === idx ? { ...ch, ...patch } : ch)),
    );
  };

  const onSave = async () => {
    setSaving(true);
    try {
      const res = await settingsApi.updatePaymentChannels(channels);
      setChannels(res.channels ?? []);
      toast.success("支付渠道配置已保存并生效");
    } catch (err) {
      toast.error(
        err instanceof ApiError ? `保存失败：${err.message}` : "网络异常",
      );
    } finally {
      setSaving(false);
    }
  };

  if (loading) {
    return (
      <Card>
        <CardContent className="pt-6 text-sm text-muted-foreground animate-pulse">
          加载中...
        </CardContent>
      </Card>
    );
  }

  const yipay = channels.find((c) => c.provider === "yipay");
  const hupijiao = channels.find((c) => c.provider === "hupijiao");
  const ldc = channels.find((c) => c.provider === "ldcpay");
  const yipayIdx = channels.findIndex((c) => c.provider === "yipay");
  const hupijiaoIdx = channels.findIndex((c) => c.provider === "hupijiao");
  const ldcIdx = channels.findIndex((c) => c.provider === "ldcpay");

  return (
    <div className="space-y-6">
      {yipay && yipayIdx >= 0 && (
        <Card>
          <CardHeader>
            <div className="flex items-center justify-between">
              <div>
                <CardTitle className="text-base">易支付（Yipay / Epay）</CardTitle>
                <CardDescription>支持支付宝 / 微信 / QQ 钱包</CardDescription>
              </div>
              <Badge variant={yipay.enabled ? "success" : "secondary"}>
                {yipay.enabled ? "已启用" : "未启用"}
              </Badge>
            </div>
          </CardHeader>
          <CardContent className="space-y-4">
            <div className="flex items-center gap-3">
              <Label className="w-20 shrink-0">状态</Label>
              <Select
                value={yipay.enabled ? "on" : "off"}
                onValueChange={(v) =>
                  updateChannel(yipayIdx, { enabled: v === "on" })
                }
              >
                <SelectTrigger className="w-32">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="on">启用</SelectItem>
                  <SelectItem value="off">停用</SelectItem>
                </SelectContent>
              </Select>
            </div>
            <div className="grid gap-3 sm:grid-cols-2">
              <div className="space-y-1">
                <Label>网关地址</Label>
                <Input
                  placeholder="https://pay.example.com"
                  value={yipay.yipayBaseUrl ?? ""}
                  onChange={(e) =>
                    updateChannel(yipayIdx, { yipayBaseUrl: e.target.value })
                  }
                />
              </div>
              <div className="space-y-1">
                <Label>商户 ID (pid)</Label>
                <Input
                  placeholder="10001"
                  value={yipay.yipayMerchantId ?? ""}
                  onChange={(e) =>
                    updateChannel(yipayIdx, { yipayMerchantId: e.target.value })
                  }
                />
              </div>
              <div className="space-y-1">
                <Label>商户密钥 (key)</Label>
                <Input
                  type="password"
                  placeholder="••••••••"
                  value={yipay.yipayKey ?? ""}
                  onChange={(e) =>
                    updateChannel(yipayIdx, { yipayKey: e.target.value })
                  }
                />
              </div>
              <div className="space-y-1">
                <Label>支付方式</Label>
                <Select
                  value={yipay.yipayPaymentType ?? "alipay"}
                  onValueChange={(v) =>
                    updateChannel(yipayIdx, { yipayPaymentType: v })
                  }
                >
                  <SelectTrigger>
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="alipay">支付宝</SelectItem>
                    <SelectItem value="wxpay">微信支付</SelectItem>
                    <SelectItem value="qqpay">QQ 钱包</SelectItem>
                  </SelectContent>
                </Select>
              </div>
            </div>
          </CardContent>
        </Card>
      )}

      {hupijiao && hupijiaoIdx >= 0 && (
        <Card>
          <CardHeader>
            <div className="flex items-center justify-between">
              <div>
                <CardTitle className="text-base">虎皮椒（Xunhupay）</CardTitle>
                <CardDescription>微信个人支付收款方案</CardDescription>
              </div>
              <Badge variant={hupijiao.enabled ? "success" : "secondary"}>
                {hupijiao.enabled ? "已启用" : "未启用"}
              </Badge>
            </div>
          </CardHeader>
          <CardContent className="space-y-4">
            <div className="flex items-center gap-3">
              <Label className="w-20 shrink-0">状态</Label>
              <Select
                value={hupijiao.enabled ? "on" : "off"}
                onValueChange={(v) =>
                  updateChannel(hupijiaoIdx, { enabled: v === "on" })
                }
              >
                <SelectTrigger className="w-32">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="on">启用</SelectItem>
                  <SelectItem value="off">停用</SelectItem>
                </SelectContent>
              </Select>
            </div>
            <div className="grid gap-3 sm:grid-cols-2">
              <div className="space-y-1">
                <Label>API 地址</Label>
                <Input
                  placeholder="https://api.xunhupay.com/payment/do.html"
                  value={hupijiao.hupijiaoBaseUrl ?? ""}
                  onChange={(e) =>
                    updateChannel(hupijiaoIdx, { hupijiaoBaseUrl: e.target.value })
                  }
                />
              </div>
              <div className="space-y-1">
                <Label>App ID</Label>
                <Input
                  placeholder="201xxxxxx"
                  value={hupijiao.hupijiaoAppId ?? ""}
                  onChange={(e) =>
                    updateChannel(hupijiaoIdx, { hupijiaoAppId: e.target.value })
                  }
                />
              </div>
              <div className="space-y-1">
                <Label>App Secret</Label>
                <Input
                  type="password"
                  placeholder="••••••••"
                  value={hupijiao.hupijiaoKey ?? ""}
                  onChange={(e) =>
                    updateChannel(hupijiaoIdx, { hupijiaoKey: e.target.value })
                  }
                />
              </div>
              <div className="space-y-1">
                <Label>应用名称</Label>
                <Input
                  placeholder="Music Gateway"
                  value={hupijiao.hupijiaoWapName ?? ""}
                  onChange={(e) =>
                    updateChannel(hupijiaoIdx, { hupijiaoWapName: e.target.value })
                  }
                />
              </div>
            </div>
          </CardContent>
        </Card>
      )}

      {ldc && ldcIdx >= 0 && (
        <Card>
          <CardHeader>
            <div className="flex items-center justify-between">
              <div>
                <CardTitle className="text-base">Linux Credit (LDC)</CardTitle>
                <CardDescription>
                  linux.do 官方积分流转协议（type=ldcpay，Ed25519 签名）
                </CardDescription>
              </div>
              <Badge variant={ldc.enabled ? "success" : "secondary"}>
                {ldc.enabled ? "已启用" : "未启用"}
              </Badge>
            </div>
          </CardHeader>
          <CardContent className="space-y-4">
            <div className="flex items-center gap-3">
              <Label className="w-20 shrink-0">状态</Label>
              <Select
                value={ldc.enabled ? "on" : "off"}
                onValueChange={(v) =>
                  updateChannel(ldcIdx, { enabled: v === "on" })
                }
              >
                <SelectTrigger className="w-32">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="on">启用</SelectItem>
                  <SelectItem value="off">停用</SelectItem>
                </SelectContent>
              </Select>
            </div>
            <div className="grid gap-3 sm:grid-cols-2">
              <div className="space-y-1">
                <Label>网关地址</Label>
                <Input
                  placeholder="https://credit.linux.do/epay"
                  value={ldc.ldcBaseUrl ?? ""}
                  onChange={(e) =>
                    updateChannel(ldcIdx, { ldcBaseUrl: e.target.value })
                  }
                />
              </div>
              <div className="space-y-1">
                <Label>Client ID</Label>
                <Input
                  placeholder="LDC 控制台展示的 client_id"
                  value={ldc.ldcClientId ?? ""}
                  onChange={(e) =>
                    updateChannel(ldcIdx, { ldcClientId: e.target.value })
                  }
                />
              </div>
              <div className="space-y-1 sm:col-span-2">
                <Label>Client Secret</Label>
                <Input
                  type="password"
                  placeholder="••••••••"
                  value={ldc.ldcClientSecret ?? ""}
                  onChange={(e) =>
                    updateChannel(ldcIdx, { ldcClientSecret: e.target.value })
                  }
                />
              </div>
              <div className="space-y-1 sm:col-span-2">
                <Label>商户 Ed25519 私钥</Label>
                <Textarea
                  rows={5}
                  className="font-mono text-xs"
                  placeholder="支持以下任一格式：&#10;1) PEM 完整文件（含 BEGIN/END）&#10;2) PEM 主体 base64（不带头尾，多行也可）&#10;3) 32 字节 seed 的 base64&#10;4) 64 字节完整私钥的 base64"
                  value={ldc.ldcMerchantPrivateKey ?? ""}
                  onChange={(e) =>
                    updateChannel(ldcIdx, {
                      ldcMerchantPrivateKey: e.target.value,
                    })
                  }
                />
                <p className="text-[11px] text-muted-foreground">
                  本地生成密钥对，把 <b>公钥</b> 上传到 LDC 控制台；私钥仅保留在此处用于请求签名。回显已脱敏。
                </p>
              </div>
              <div className="space-y-1 sm:col-span-2">
                <Label>LDC 平台公钥（用于回调验签，可选）</Label>
                <Textarea
                  rows={4}
                  className="font-mono text-xs"
                  placeholder="支持以下任一格式：&#10;1) PEM 完整文件（含 BEGIN/END）&#10;2) PEM 主体 base64（44 字节 SPKI，类似 MCowBQYDK2VwAyEA…）&#10;3) 32 字节裸公钥的 base64"
                  value={ldc.ldcPlatformPublicKey ?? ""}
                  onChange={(e) =>
                    updateChannel(ldcIdx, {
                      ldcPlatformPublicKey: e.target.value,
                    })
                  }
                />
                <p className="text-[11px] text-muted-foreground">
                  从 LDC 控制台下载平台公钥后填入。未配置时为安全起见仅记录日志，不阻断回调流转。
                </p>
              </div>
            </div>
          </CardContent>
        </Card>
      )}

      <Separator />

      <div className="flex justify-end">
        <Button onClick={onSave} disabled={saving}>
          <Save className="mr-2 size-4" />
          {saving ? "保存中..." : "保存并生效"}
        </Button>
      </div>
    </div>
  );
}
