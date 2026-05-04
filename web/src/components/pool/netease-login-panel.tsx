"use client";

import * as React from "react";
import { Loader2, QrCode, Phone, Mail, UserCircle } from "lucide-react";
import { toast } from "sonner";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { neteaseAuthApi } from "@/lib/api/netease";

type NeteaseLoginMode = "phone" | "email" | "qr" | "anonymous";

interface NeteaseLoginPanelProps {
  onCredential: (cred: Record<string, unknown>) => void;
}

export function NeteaseLoginPanel({ onCredential }: NeteaseLoginPanelProps) {
  const [mode, setMode] = React.useState<NeteaseLoginMode>("phone");

  return (
    <Tabs value={mode} onValueChange={(v) => setMode(v as NeteaseLoginMode)}>
      <TabsList className="grid w-full grid-cols-4">
        <TabsTrigger value="phone"><Phone className="mr-1 size-3" />手机</TabsTrigger>
        <TabsTrigger value="email"><Mail className="mr-1 size-3" />邮箱</TabsTrigger>
        <TabsTrigger value="qr"><QrCode className="mr-1 size-3" />二维码</TabsTrigger>
        <TabsTrigger value="anonymous"><UserCircle className="mr-1 size-3" />游客</TabsTrigger>
      </TabsList>

      <TabsContent value="phone">
        <PhoneLogin onCredential={onCredential} />
      </TabsContent>
      <TabsContent value="email">
        <EmailLogin onCredential={onCredential} />
      </TabsContent>
      <TabsContent value="qr">
        <QRLogin onCredential={onCredential} />
      </TabsContent>
      <TabsContent value="anonymous">
        <AnonymousLogin onCredential={onCredential} />
      </TabsContent>
    </Tabs>
  );
}

function PhoneLogin({ onCredential }: { onCredential: (c: Record<string, unknown>) => void }) {
  const [phone, setPhone] = React.useState("");
  const [password, setPassword] = React.useState("");
  const [captcha, setCaptcha] = React.useState("");
  const [countryCode, setCountryCode] = React.useState("86");
  const [useCaptcha, setUseCaptcha] = React.useState(false);
  const [sending, setSending] = React.useState(false);
  const [logging, setLogging] = React.useState(false);
  const [countdown, setCountdown] = React.useState(0);

  React.useEffect(() => {
    if (countdown <= 0) return;
    const t = setTimeout(() => setCountdown(countdown - 1), 1000);
    return () => clearTimeout(t);
  }, [countdown]);

  const sendCaptcha = async () => {
    if (!phone) { toast.error("请输入手机号"); return; }
    setSending(true);
    try {
      await neteaseAuthApi.captchaSent(phone, countryCode);
      toast.success("验证码已发送");
      setCountdown(60);
    } catch (e) {
      toast.error(e instanceof Error ? e.message : "发送失败");
    } finally {
      setSending(false);
    }
  };

  const login = async () => {
    if (!phone) { toast.error("请输入手机号"); return; }
    if (!useCaptcha && !password) { toast.error("请输入密码"); return; }
    if (useCaptcha && !captcha) { toast.error("请输入验证码"); return; }
    setLogging(true);
    try {
      const resp = await neteaseAuthApi.loginCellphone({
        phone,
        password: useCaptcha ? undefined : password,
        captcha: useCaptcha ? captcha : undefined,
        countryCode,
      });
      if (resp.cookie) {
        onCredential({ cookie: resp.cookie });
        toast.success("登录成功");
      } else {
        toast.error("登录返回无 cookie");
      }
    } catch (e) {
      toast.error(e instanceof Error ? e.message : "登录失败");
    } finally {
      setLogging(false);
    }
  };

  return (
    <div className="space-y-3 pt-2">
      <div className="grid gap-3 sm:grid-cols-2">
        <div className="space-y-1.5">
          <Label>手机号</Label>
          <Input value={phone} onChange={(e) => setPhone(e.target.value)} placeholder="13800138000" />
        </div>
        <div className="space-y-1.5">
          <Label>国家码</Label>
          <Input value={countryCode} onChange={(e) => setCountryCode(e.target.value)} placeholder="86" />
        </div>
      </div>
      <div className="flex items-center gap-2">
        <Button type="button" variant="link" size="sm" className="px-0 text-xs"
          onClick={() => setUseCaptcha(!useCaptcha)}>
          {useCaptcha ? "切换为密码登录" : "切换为验证码登录"}
        </Button>
      </div>
      {useCaptcha ? (
        <div className="flex gap-2">
          <Input value={captcha} onChange={(e) => setCaptcha(e.target.value)} placeholder="验证码" className="flex-1" />
          <Button type="button" variant="outline" size="sm" onClick={sendCaptcha}
            disabled={sending || countdown > 0}>
            {countdown > 0 ? `${countdown}s` : "发送验证码"}
          </Button>
        </div>
      ) : (
        <div className="space-y-1.5">
          <Label>密码</Label>
          <Input type="password" value={password} onChange={(e) => setPassword(e.target.value)} placeholder="密码" />
        </div>
      )}
      <Button type="button" onClick={login} disabled={logging} className="w-full">
        {logging && <Loader2 className="mr-2 size-4 animate-spin" />}
        登录
      </Button>
    </div>
  );
}

function EmailLogin({ onCredential }: { onCredential: (c: Record<string, unknown>) => void }) {
  const [email, setEmail] = React.useState("");
  const [password, setPassword] = React.useState("");
  const [logging, setLogging] = React.useState(false);

  const login = async () => {
    if (!email || !password) { toast.error("请输入邮箱和密码"); return; }
    setLogging(true);
    try {
      const resp = await neteaseAuthApi.loginEmail({ email, password });
      if (resp.cookie) {
        onCredential({ cookie: resp.cookie });
        toast.success("登录成功");
      } else {
        toast.error("登录返回无 cookie");
      }
    } catch (e) {
      toast.error(e instanceof Error ? e.message : "登录失败");
    } finally {
      setLogging(false);
    }
  };

  return (
    <div className="space-y-3 pt-2">
      <div className="space-y-1.5">
        <Label>网易邮箱</Label>
        <Input value={email} onChange={(e) => setEmail(e.target.value)} placeholder="xxx@163.com" />
      </div>
      <div className="space-y-1.5">
        <Label>密码</Label>
        <Input type="password" value={password} onChange={(e) => setPassword(e.target.value)} placeholder="密码" />
      </div>
      <Button type="button" onClick={login} disabled={logging} className="w-full">
        {logging && <Loader2 className="mr-2 size-4 animate-spin" />}
        登录
      </Button>
    </div>
  );
}

function QRLogin({ onCredential }: { onCredential: (c: Record<string, unknown>) => void }) {
  const [qrKey, setQrKey] = React.useState("");
  const [qrURL, setQrURL] = React.useState("");
  const [status, setStatus] = React.useState<number | null>(null);
  const [polling, setPolling] = React.useState(false);
  const [generating, setGenerating] = React.useState(false);
  const abortRef = React.useRef<AbortController | null>(null);

  React.useEffect(() => {
    return () => { abortRef.current?.abort(); };
  }, []);

  const generate = async () => {
    abortRef.current?.abort();
    setGenerating(true);
    setStatus(null);
    try {
      const keyResp = await neteaseAuthApi.qrKey();
      const key = keyResp.key;
      setQrKey(key);
      setQrURL(keyResp.qrurl);
      startPolling(key);
    } catch (e) {
      toast.error(e instanceof Error ? e.message : "生成二维码失败");
    } finally {
      setGenerating(false);
    }
  };

  const startPolling = (key: string) => {
    abortRef.current?.abort();
    const ctrl = new AbortController();
    abortRef.current = ctrl;
    setPolling(true);

    const poll = async () => {
      while (!ctrl.signal.aborted) {
        await new Promise((r) => setTimeout(r, 1500));
        if (ctrl.signal.aborted) break;
        try {
          const resp = await neteaseAuthApi.qrCheck(key);
          const code = Number(resp.code ?? 0);
          setStatus(code);
          if ((resp as any).message) setStatusMsg((resp as any).message);
          if (code === 803) {
            const cookie = resp.cookie || (resp as any).cookies;
            if (cookie) {
              onCredential({ cookie });
              toast.success("扫码登录成功");
            } else {
              toast.error("登录成功但未获取到 cookie");
            }
            break;
          }
          if (code === 800) {
            toast.error("二维码已过期");
            break;
          }
          if (code !== 801 && code !== 802) {
            const msg = (resp as any).message || `状态异常 (${code})`;
            toast.error(msg);
            break;
          }
        } catch {
          // 忽略单次错误继续轮询
        }
      }
      setPolling(false);
    };
    poll();
  };

  const [statusMsg, setStatusMsg] = React.useState("");

  const statusText = (() => {
    if (status === null) return "";
    switch (status) {
      case 801: return "等待扫码";
      case 802: return "已扫码，请在手机确认";
      case 803: return "登录成功";
      case 800: return "二维码已过期";
      default: return statusMsg || `异常 (${status})`;
    }
  })();

  const statusVariant = (() => {
    if (status === 803) return "default" as const;
    if (status === 800) return "destructive" as const;
    if (status === 802) return "secondary" as const;
    if (status !== null && status !== 801) return "destructive" as const;
    return "outline" as const;
  })();

  return (
    <div className="space-y-3 pt-2">
      <div className="flex items-center gap-2">
        <Button type="button" variant="outline" onClick={generate} disabled={generating}>
          {generating ? <Loader2 className="mr-2 size-4 animate-spin" /> : <QrCode className="mr-2 size-4" />}
          {qrKey ? "重新生成" : "生成二维码"}
        </Button>
        {statusText && <Badge variant={statusVariant}>{statusText}</Badge>}
      </div>
      {qrURL && (
        <div className="flex flex-col items-center gap-2 rounded-md border p-4">
          <img
            src={`https://api.qrserver.com/v1/create-qr-code/?size=200x200&data=${encodeURIComponent(qrURL)}`}
            alt="网易云登录二维码"
            className="size-48 rounded bg-white p-1"
          />
          <p className="text-xs text-muted-foreground">使用网易云音乐 APP 扫码</p>
        </div>
      )}
      {!qrURL && (
        <p className="py-8 text-center text-sm text-muted-foreground">
          点击「生成二维码」开始扫码登录
        </p>
      )}
    </div>
  );
}

function AnonymousLogin({ onCredential }: { onCredential: (c: Record<string, unknown>) => void }) {
  const [logging, setLogging] = React.useState(false);

  const login = async () => {
    setLogging(true);
    try {
      const resp = await neteaseAuthApi.registerAnonymous();
      if (resp.cookie) {
        onCredential({ cookie: resp.cookie });
        toast.success("游客登录成功");
      } else {
        toast.error("登录返回无 cookie");
      }
    } catch (e) {
      toast.error(e instanceof Error ? e.message : "登录失败");
    } finally {
      setLogging(false);
    }
  };

  return (
    <div className="space-y-3 pt-2">
      <p className="text-sm text-muted-foreground">
        游客模式无需账号，可获取基础访问 cookie。部分接口（如歌曲 URL）可能受限。
      </p>
      <Button type="button" onClick={login} disabled={logging} className="w-full">
        {logging && <Loader2 className="mr-2 size-4 animate-spin" />}
        获取游客凭证
      </Button>
    </div>
  );
}
