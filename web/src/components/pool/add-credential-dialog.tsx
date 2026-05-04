"use client";

import * as React from "react";
import {
  ChevronDown,
  Loader2,
  RefreshCw,
  ShieldAlert,
  ShieldCheck,
} from "lucide-react";
import { toast } from "sonner";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from "@/components/ui/collapsible";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { Textarea } from "@/components/ui/textarea";
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group";
import { ApiError } from "@/lib/api/client";
import { encodePayloadBase64, poolApi } from "@/lib/api/pool";
import { formatDateTime } from "@/lib/datetime";
import {
  qrLoginApi,
  type QRStartResponse,
} from "@/lib/api/qr-login";
import { listCapabilities } from "@/lib/pool-capabilities";
import {
  buildCredentialPayload,
  parseCookieString,
  validateCredentialJSON,
  type QQMusicCredentialFields,
} from "@/lib/qqmusic-credential";
import { cn } from "@/lib/utils";
import { NeteaseLoginPanel } from "@/components/pool/netease-login-panel";

type AddMode = "qr" | "cookie" | "fields" | "json";
type LoginTypeValue = "auto" | "1" | "2";
type QrPlatform = "qq" | "wx" | "mobile";
type QrEvent =
  | "init"
  | "scan"
  | "conf"
  | "done"
  | "timeout"
  | "refuse"
  | "other";

interface AddCredentialDialogProps {
  defaultProvider?: string;
  onAdded?: () => void;
}

/**
 * 添加凭证对话框（多种输入方式）。
 *
 * Tabs：
 *  - **扫码登录**：调用 admin REST 拿二维码，前端轮询直到 done 自动填充字段；
 *  - **Cookie 字符串**：粘贴浏览器导出 cookie，前端解析关键字段 + 预览；
 *  - **字段直填**：musicid / musickey / refresh_token / refresh_key / loginType；
 *  - **JSON 直填**：高级用户直接粘贴完整 Credential JSON。
 *
 * 三种方式最终都构造成 snake_case JSON payload → base64 → 上传后端。
 * 后端 qqmusic.Credential.UnmarshalJSON 同时识别 snake/camel，自动推断 loginType。
 *
 * UI 全部使用 shadcn/ui + Radix 原语（Dialog/Tabs/Select/Checkbox/ToggleGroup/
 * Collapsible/Textarea/Badge），无原生 input/select/textarea/details/checkbox。
 */
export function AddCredentialDialog({
  defaultProvider = "qqmusic",
  onAdded,
}: AddCredentialDialogProps) {
  const [open, setOpen] = React.useState(false);
  const [submitting, setSubmitting] = React.useState(false);
  const [mode, setMode] = React.useState<AddMode>("qr");

  const [provider, setProvider] = React.useState(defaultProvider);
  const [label, setLabel] = React.useState("");
  const [selectedCaps, setSelectedCaps] = React.useState<Set<string>>(
    new Set(),
  );
  const availableCaps = listCapabilities(provider);

  const [cookieRaw, setCookieRaw] = React.useState("");
  const cookieParsed = React.useMemo(
    () => parseCookieString(cookieRaw),
    [cookieRaw],
  );

  const [fields, setFields] = React.useState<QQMusicCredentialFields>({
    musicid: "",
    musickey: "",
    refresh_token: "",
    refresh_key: "",
  });
  const [loginType, setLoginType] = React.useState<LoginTypeValue>("auto");

  const [jsonRaw, setJsonRaw] = React.useState("");
  const jsonError = React.useMemo(() => {
    const t = jsonRaw.trim();
    if (!t) return null;
    return validateCredentialJSON(t);
  }, [jsonRaw]);

  React.useEffect(() => {
    setProvider(defaultProvider);
  }, [defaultProvider]);

  const reset = () => {
    setLabel("");
    setSelectedCaps(new Set());
    setCookieRaw("");
    setJsonRaw("");
    setFields({
      musicid: "",
      musickey: "",
      refresh_token: "",
      refresh_key: "",
    });
    setLoginType("auto");
  };

  const buildPayload = (): { payload: string; err: string | null } => {
    if (mode === "cookie") {
      if (!cookieParsed.valid) {
        return {
          payload: "",
          err: "cookie 中未识别到 musickey；请检查输入",
        };
      }
      return {
        payload: buildCredentialPayload(cookieParsed.fields),
        err: null,
      };
    }
    if (mode === "fields") {
      if (!fields.musickey || !String(fields.musickey).trim()) {
        return { payload: "", err: "musickey 不能为空" };
      }
      const out: QQMusicCredentialFields = { ...fields };
      if (loginType !== "auto") out.loginType = Number(loginType) as 1 | 2;
      if (typeof out.musicid === "string" && /^\d+$/.test(out.musicid)) {
        out.musicid = Number(out.musicid);
      }
      return { payload: buildCredentialPayload(out), err: null };
    }
    const t = jsonRaw.trim();
    if (!t) return { payload: "", err: "请粘贴 JSON" };
    if (jsonError) return { payload: "", err: jsonError };
    return { payload: t, err: null };
  };

  const onSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!provider.trim()) {
      toast.error("请输入 provider");
      return;
    }
    if (!label.trim()) {
      toast.error("请输入凭证标签");
      return;
    }
    const { payload, err } = buildPayload();
    if (err || !payload) {
      toast.error(err ?? "未能构造 payload");
      return;
    }
    setSubmitting(true);
    try {
      const caps = Array.from(selectedCaps);
      await poolApi.add({
        provider: provider.trim(),
        label: label.trim(),
        payload: encodePayloadBase64(payload),
        capabilities: caps.length > 0 ? caps : undefined,
      });
      toast.success(`凭证「${label}」已加入 ${provider} 号池`);
      reset();
      setOpen(false);
      onAdded?.();
    } catch (apiErr) {
      toast.error(
        apiErr instanceof ApiError
          ? `保存失败：${apiErr.message}`
          : "网络异常",
      );
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <Dialog
      open={open}
      onOpenChange={(o) => {
        setOpen(o);
        if (!o) reset();
      }}
    >
      <DialogTrigger asChild>
        <Button>添加凭证</Button>
      </DialogTrigger>
      <DialogContent className="max-h-[90vh] overflow-y-auto sm:max-w-3xl">
        <DialogHeader>
          <DialogTitle>添加凭证</DialogTitle>
          <DialogDescription>
            选择平台后按提示登录，凭证将安全保存至号池。
          </DialogDescription>
        </DialogHeader>

        <form
          className="space-y-4"
          onSubmit={onSubmit}
          noValidate
          autoComplete="off"
        >
          <div className="grid gap-3 sm:grid-cols-2">
            <div className="space-y-1.5">
              <Label htmlFor="add-provider">音乐平台</Label>
              <Select value={provider} onValueChange={setProvider}>
                <SelectTrigger id="add-provider">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="qqmusic">QQ 音乐</SelectItem>
                  <SelectItem value="netease">网易云音乐</SelectItem>
                </SelectContent>
              </Select>
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="add-label">标签</Label>
              <Input
                id="add-label"
                value={label}
                onChange={(e) => setLabel(e.target.value)}
                placeholder="vip-account-01"
              />
            </div>
          </div>

          {provider === "netease" ? (
            <NeteaseLoginPanel
              onCredential={async (cred) => {
                try {
                  const credJson = JSON.stringify(cred);
                  const encoded = typeof window !== "undefined"
                    ? btoa(new TextEncoder().encode(credJson).reduce((s, b) => s + String.fromCharCode(b), ""))
                    : Buffer.from(credJson, "utf8").toString("base64");
                  await poolApi.add({
                    provider: "netease",
                    label: label.trim() || `netease-${formatDateTime(new Date())}`,
                    payload: encoded,
                    capabilities: undefined,
                  });
                  toast.success("登录成功，凭证已保存到号池");
                  onAdded?.();
                  setOpen(false);
                } catch (e) {
                  toast.error(`保存失败：${e instanceof Error ? e.message : "未知错误"}`);
                  setJsonRaw(JSON.stringify(cred, null, 2));
                  setMode("json");
                }
              }}
            />
          ) : (
          <Tabs value={mode} onValueChange={(v) => setMode(v as AddMode)}>
            <TabsList>
              <TabsTrigger value="qr">扫码登录</TabsTrigger>
              <TabsTrigger value="cookie">Cookie 字符串</TabsTrigger>
              <TabsTrigger value="fields">字段直填</TabsTrigger>
              <TabsTrigger value="json">JSON 直填</TabsTrigger>
            </TabsList>

            <TabsContent value="qr" className="space-y-2">
              <QRLoginPanel
                onCredential={async (cred) => {
                  // 扫码成功 → 自动保存到号池（不需要手动点保存）
                  try {
                    const credJson = JSON.stringify(cred);
                    const encoded = typeof window !== "undefined"
                      ? btoa(new TextEncoder().encode(credJson).reduce((s, b) => s + String.fromCharCode(b), ""))
                      : Buffer.from(credJson, "utf8").toString("base64");
                    await poolApi.add({
                      provider: provider.trim() || "qqmusic",
                      label: label.trim() || `QR-${formatDateTime(new Date())}`,
                      payload: encoded,
                      capabilities: undefined, // 空=全能力
                    });
                    toast.success("扫码成功，凭证已自动保存到号池");
                    onAdded?.();
                    setOpen(false);
                  } catch (e) {
                    toast.error(`保存失败：${e instanceof Error ? e.message : "未知错误"}`);
                    // fallback: 填到表单让用户手动保存
                    setJsonRaw(JSON.stringify(cred, null, 2));
                    setMode("json");
                  }
                }}
              />
            </TabsContent>

            <TabsContent value="cookie" className="space-y-2">
              <Label htmlFor="cookie-raw">
                浏览器 Cookie（粘贴 document.cookie 或 curl --cookie 内容）
              </Label>
              <Textarea
                id="cookie-raw"
                value={cookieRaw}
                onChange={(e) => setCookieRaw(e.target.value)}
                placeholder="uin=12345; musickey=W_X_abc; refresh_token=...; refresh_key=..."
                className="min-h-[120px] resize-y font-mono"
                spellCheck={false}
                autoComplete="off"
              />
              {cookieRaw.trim() !== "" && (
                <CookiePreview parsed={cookieParsed} />
              )}
            </TabsContent>

            <TabsContent value="fields" className="space-y-3">
              <div className="grid gap-3 sm:grid-cols-2">
                <FieldInput
                  id="f-musicid"
                  label="musicid"
                  value={String(fields.musicid ?? "")}
                  onChange={(v) => setFields({ ...fields, musicid: v })}
                  placeholder="12345（可选）"
                />
                <FieldInput
                  id="f-musickey"
                  label="musickey *"
                  value={String(fields.musickey ?? "")}
                  onChange={(v) => setFields({ ...fields, musickey: v })}
                  placeholder="W_X_xxx... / Q_H_xxx..."
                  required
                />
                <FieldInput
                  id="f-refresh-token"
                  label="refresh_token"
                  value={String(fields.refresh_token ?? "")}
                  onChange={(v) => setFields({ ...fields, refresh_token: v })}
                  placeholder="可选"
                />
                <FieldInput
                  id="f-refresh-key"
                  label="refresh_key"
                  value={String(fields.refresh_key ?? "")}
                  onChange={(v) => setFields({ ...fields, refresh_key: v })}
                  placeholder="可选"
                />
              </div>
              <div className="space-y-1.5">
                <Label htmlFor="f-login-type">登录类型</Label>
                <Select
                  value={loginType}
                  onValueChange={(v) => setLoginType(v as LoginTypeValue)}
                >
                  <SelectTrigger id="f-login-type" className="w-full sm:w-72">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="auto">
                      自动（按 musickey 前缀推断）
                    </SelectItem>
                    <SelectItem value="1">QQ 账号（loginType=1）</SelectItem>
                    <SelectItem value="2">微信账号（loginType=2）</SelectItem>
                  </SelectContent>
                </Select>
                <p className="text-xs text-muted-foreground">
                  W_X 前缀 = QQ；其他默认视为微信。手动覆盖会写入 payload.loginType
                  字段。
                </p>
              </div>
            </TabsContent>

            <TabsContent value="json" className="space-y-2">
              <Label htmlFor="json-raw">JSON Credential</Label>
              <Textarea
                id="json-raw"
                value={jsonRaw}
                onChange={(e) => setJsonRaw(e.target.value)}
                placeholder={`{
  "musicid": 12345,
  "musickey": "W_X_xxxx",
  "refresh_token": "...",
  "refresh_key": "...",
  "loginType": 1
}`}
                className="min-h-[200px] resize-y font-mono"
                spellCheck={false}
                autoComplete="off"
              />
              {jsonError && (
                <p className="text-xs text-destructive">{jsonError}</p>
              )}
              <p className="text-xs text-muted-foreground">
                字段名同时支持 snake_case 与 camelCase；后端按 musickey 前缀自动推断
                loginType。
              </p>
            </TabsContent>
          </Tabs>
          )}

          <div className="space-y-2">
            <div className="flex items-center justify-between">
              <Label>能力（多选；不选 = 该凭证可承担所有能力）</Label>
              <div className="flex items-center gap-1">
                <Button
                  type="button"
                  variant="link"
                  size="sm"
                  className="h-auto px-1 py-0 text-xs"
                  onClick={() =>
                    setSelectedCaps(new Set(availableCaps.map((c) => c.name)))
                  }
                  disabled={availableCaps.length === 0}
                >
                  全选
                </Button>
                <span className="text-xs text-muted-foreground">|</span>
                <Button
                  type="button"
                  variant="link"
                  size="sm"
                  className="h-auto px-1 py-0 text-xs"
                  onClick={() => setSelectedCaps(new Set())}
                  disabled={selectedCaps.size === 0}
                >
                  清空
                </Button>
              </div>
            </div>
            {availableCaps.length === 0 ? (
              <p className="text-xs text-muted-foreground">
                未识别的 provider；该凭证将默认承担所有能力。
              </p>
            ) : (
              <div className="grid grid-cols-2 gap-2 sm:grid-cols-3">
                {availableCaps.map((cap) => {
                  const checked = selectedCaps.has(cap.name);
                  const fieldId = `cap-${cap.name}`;
                  return (
                    <Label
                      key={cap.name}
                      htmlFor={fieldId}
                      title={cap.hint}
                      className={cn(
                        "flex cursor-pointer items-start gap-2 rounded-md border p-2 text-sm font-normal transition-colors",
                        checked
                          ? "border-primary bg-primary/5"
                          : "border-input hover:bg-accent",
                      )}
                    >
                      <Checkbox
                        id={fieldId}
                        checked={checked}
                        className="mt-0.5"
                        onCheckedChange={(v) => {
                          setSelectedCaps((prev) => {
                            const next = new Set(prev);
                            if (v === true) next.add(cap.name);
                            else next.delete(cap.name);
                            return next;
                          });
                        }}
                      />
                      <div className="flex min-w-0 flex-col">
                        <span className="truncate">{cap.label}</span>
                        <code className="truncate font-mono text-[10px] text-muted-foreground">
                          {cap.name}
                        </code>
                      </div>
                    </Label>
                  );
                })}
              </div>
            )}
          </div>

          <DialogFooter>
            <Button
              type="button"
              variant="ghost"
              onClick={() => setOpen(false)}
              disabled={submitting}
            >
              取消
            </Button>
            <Button type="submit" disabled={submitting}>
              {submitting && <Loader2 className="size-4 animate-spin" />}
              {submitting ? "保存中…" : "保存并加入号池"}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

/** 终态字面量集合 — backend 与 ui 此处同名，用 string set 同时覆盖两端类型 */
const TERMINAL_EVENTS: ReadonlySet<string> = new Set([
  "done",
  "timeout",
  "refuse",
  "other",
]);

/** 各阶段轮询间隔（ms） — 已扫码后变快，让确认/完成动作秒级反馈 */
const POLL_INTERVAL_MS: Record<QrEvent, number> = {
  init: 800,
  scan: 400,
  conf: 400,
  done: 0,
  timeout: 0,
  refuse: 0,
  other: 0,
};

/** 倒计时 UI 刷新频率（ms） — 250ms 让秒数视觉流畅但 CPU 几乎不动 */
const COUNTDOWN_TICK_MS = 250;

/** 连续轮询失败容忍次数；超过则视为不可恢复并停止 */
const MAX_CONSECUTIVE_POLL_ERRORS = 3;

/**
 * AbortSignal 友好的 sleep。返回 true 表示等待期间被 abort，false 表示正常超时完成。
 * dialog 关闭 / 平台切换 / 用户重新生成 时立即返回，不再消耗一个完整周期。
 */
function abortableDelay(ms: number, signal: AbortSignal): Promise<boolean> {
  return new Promise((resolve) => {
    if (signal.aborted) return resolve(true);
    const onAbort = () => {
      clearTimeout(timer);
      resolve(true);
    };
    const timer = setTimeout(() => {
      signal.removeEventListener("abort", onAbort);
      resolve(false);
    }, ms);
    signal.addEventListener("abort", onAbort, { once: true });
  });
}

/**
 * QRLoginPanel：扫码登录交互。
 *
 * 设计要点：
 *   - 选 QQ / 微信 → POST /v1/admin/login/qr/start 拿 image+session_id；
 *   - 状态自适应轮询：init=800ms，scan/conf=400ms，让用户扫码 / 确认即时反馈；
 *   - 终态（done/timeout/refuse/other）立刻停止轮询；
 *   - 单次轮询失败连续 ≥3 次才退出（容忍瞬时网络抖动）；
 *   - AbortController 严格管理生命周期：dialog 关闭 / 平台切换 / 重新生成
 *     都会 abort 上一轮，避免并发 polling；
 *   - 倒计时独立 250ms tick，与轮询解耦，秒级流畅。
 */
function QRLoginPanel({
  onCredential,
}: {
  onCredential: (cred: Record<string, unknown>) => void;
}) {
  const [platform, setPlatform] = React.useState<QrPlatform>("qq");
  const [session, setSession] = React.useState<{
    id: string;
    image: string;
    expiresAt: number;
  } | null>(null);
  const [event, setEvent] = React.useState<QrEvent | null>(null);
  const [busy, setBusy] = React.useState(false);
  const [errorMsg, setErrorMsg] = React.useState<string | null>(null);

  // 倒计时 UI 用 now 触发刷新；与轮询间隔完全解耦
  const [now, setNow] = React.useState(() => Date.now());
  const isTerminal = event !== null && TERMINAL_EVENTS.has(event);

  React.useEffect(() => {
    if (!session || isTerminal) return;
    const t = window.setInterval(
      () => setNow(Date.now()),
      COUNTDOWN_TICK_MS,
    );
    return () => window.clearInterval(t);
  }, [session, isTerminal]);

  // 持有当前 polling 的 AbortController；每次 startSession 创建新实例并 abort 旧实例
  const abortRef = React.useRef<AbortController | null>(null);

  // 组件卸载（dialog 关闭）→ 立即终止轮询循环 + 已 await 的 delay
  React.useEffect(() => {
    return () => {
      abortRef.current?.abort();
    };
  }, []);

  const startSession = React.useCallback(async () => {
    abortRef.current?.abort();
    const ctrl = new AbortController();
    abortRef.current = ctrl;
    const { signal } = ctrl;

    setBusy(true);
    setErrorMsg(null);
    setEvent(null);
    setSession(null);

    let res: QRStartResponse;
    try {
      res = await qrLoginApi.start(platform);
    } catch (err) {
      if (!signal.aborted) {
        setErrorMsg(err instanceof ApiError ? err.message : "网络异常");
        setBusy(false);
      }
      return;
    }
    if (signal.aborted) return;

    setSession({
      id: res.session_id,
      image: res.image,
      expiresAt: Date.now() + res.expires_in_sec * 1000,
    });
    setEvent("init");
    setBusy(false);

    // 自适应轮询：状态变化 → 间隔变化（init=800ms / scan,conf=400ms）
    let lastEvent: QrEvent = "init";
    let consecutiveErrors = 0;

    while (!signal.aborted) {
      const aborted = await abortableDelay(
        POLL_INTERVAL_MS[lastEvent],
        signal,
      );
      if (aborted || signal.aborted) return;

      try {
        const poll = await qrLoginApi.poll(res.session_id);
        if (signal.aborted) return;

        consecutiveErrors = 0;
        // 仅在事件变化时才 setEvent，减少无意义 re-render
        if (poll.event !== lastEvent) setEvent(poll.event);
        lastEvent = poll.event;

        if (poll.event === "done" && poll.credential) {
          onCredential(poll.credential);
          return;
        }
        if (TERMINAL_EVENTS.has(poll.event)) {
          if (poll.error) setErrorMsg(poll.error);
          return;
        }
      } catch (e) {
        if (signal.aborted) return;
        consecutiveErrors += 1;
        if (consecutiveErrors >= MAX_CONSECUTIVE_POLL_ERRORS) {
          setErrorMsg(e instanceof Error ? e.message : String(e));
          return;
        }
        // 否则继续 loop（瞬时抖动容忍）
      }
    }
  }, [platform, onCredential]);

  // 切换平台 → abort 当前轮询，清状态
  const onPlatformChange = (next: QrPlatform) => {
    abortRef.current?.abort();
    setPlatform(next);
    setSession(null);
    setEvent(null);
    setErrorMsg(null);
    setBusy(false);
  };

  const remainingSec = session
    ? Math.max(0, Math.ceil((session.expiresAt - now) / 1000))
    : 0;

  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-center gap-3">
        <Label className="font-normal text-muted-foreground">平台</Label>
        <ToggleGroup
          type="single"
          value={platform}
          onValueChange={(v) => {
            if (!v) return; // 阻止 deselect 把 platform 置空
            onPlatformChange(v as QrPlatform);
          }}
          variant="outline"
          size="sm"
        >
          <ToggleGroupItem value="qq" aria-label="QQ">
            QQ
          </ToggleGroupItem>
          <ToggleGroupItem value="wx" aria-label="微信">
            微信
          </ToggleGroupItem>
          <ToggleGroupItem value="mobile" aria-label="手机客户端">
            手机客户端
          </ToggleGroupItem>
        </ToggleGroup>
        <Button
          type="button"
          variant="outline"
          size="sm"
          onClick={() => void startSession()}
          disabled={busy}
        >
          {busy ? (
            <Loader2 className="size-4 animate-spin" />
          ) : (
            <RefreshCw className="size-4" />
          )}
          {busy ? "生成中…" : session ? "重新生成" : "生成二维码"}
        </Button>
      </div>

      <div className="flex flex-col items-center gap-3 rounded-md border p-4">
        {!session ? (
          <p className="py-12 text-sm text-muted-foreground">
            点击「生成二维码」开始扫码登录流程。
          </p>
        ) : (
          <>
            {/* eslint-disable-next-line @next/next/no-img-element */}
            <img
              src={session.image}
              alt={`${platform} 登录二维码`}
              className="size-56 rounded bg-white object-contain p-2"
            />
            <QRStatusBadge event={event} />
            {errorMsg && (
              <p className="text-xs text-destructive">{errorMsg}</p>
            )}
            <p className="text-xs tabular-nums text-muted-foreground">
              请在 <span className="font-medium text-foreground">{remainingSec}</span> 秒内完成扫码。
              {platform === "qq" ? "QQ 移动客户端" : platform === "wx" ? "微信" : "QQ 音乐手机客户端"}扫码后在手机端点击确认。
            </p>
          </>
        )}
      </div>

      <p className="text-xs text-muted-foreground">
        扫码登录走 admin REST：
        <code className="ml-1 rounded bg-muted px-1">
          POST /v1/admin/login/qr/start
        </code>
        +
        <code className="ml-1 rounded bg-muted px-1">
          GET /v1/admin/login/qr/{"{session}"}
        </code>
        ；登录完成后凭证会自动填充到「字段直填」标签页，请确认 label 后点保存。
      </p>
    </div>
  );
}

function QRStatusBadge({ event }: { event: QrEvent | null }) {
  if (!event) return null;
  const meta: Record<
    QrEvent,
    {
      text: string;
      variant: "secondary" | "warning" | "success" | "destructive";
    }
  > = {
    init: { text: "等待扫码…", variant: "secondary" },
    waiting: { text: "等待扫码…", variant: "secondary" },
    scanned: { text: "已扫码，请在手机点击确认", variant: "warning" },
    done: { text: "✓ 登录成功", variant: "success" },
    timeout: { text: "二维码已过期", variant: "destructive" },
    refuse: { text: "用户拒绝授权", variant: "destructive" },
    other: { text: "状态异常，请重试", variant: "destructive" },
  };
  const m = meta[event] ?? { text: event || "未知状态", variant: "secondary" as const };
  return <Badge variant={m.variant}>{m.text}</Badge>;
}

function FieldInput({
  id,
  label,
  value,
  onChange,
  placeholder,
  required,
}: {
  id: string;
  label: string;
  value: string;
  onChange: (v: string) => void;
  placeholder?: string;
  required?: boolean;
}) {
  return (
    <div className="space-y-1.5">
      <Label htmlFor={id}>{label}</Label>
      <Input
        id={id}
        value={value}
        onChange={(e) => onChange(e.target.value)}
        placeholder={placeholder}
        autoComplete="off"
        required={required}
      />
    </div>
  );
}

function CookiePreview({
  parsed,
}: {
  parsed: ReturnType<typeof parseCookieString>;
}) {
  const recognized = Object.entries(parsed.fields).filter(
    ([, v]) => v !== undefined && v !== "",
  );
  const unknownEntries = Object.entries(parsed.unknown);

  return (
    <div className="rounded-md border bg-muted/30 p-3 text-xs">
      <div className="mb-2">
        {parsed.valid ? (
          <Badge variant="success" className="gap-1.5">
            <ShieldCheck className="size-3" /> 已识别 musickey，可保存
          </Badge>
        ) : (
          <Badge variant="warning" className="gap-1.5">
            <ShieldAlert className="size-3" /> 未识别 musickey，请检查 cookie
          </Badge>
        )}
      </div>

      {recognized.length > 0 && (
        <div className="space-y-1">
          <p className="text-muted-foreground">识别到字段：</p>
          <div className="grid gap-1 font-mono">
            {recognized.map(([k, v]) => (
              <div key={k} className="flex">
                <span className="w-40 shrink-0 text-muted-foreground">{k}</span>
                <span className="truncate" title={String(v)}>
                  {maskSensitive(k, String(v))}
                </span>
              </div>
            ))}
          </div>
        </div>
      )}

      {unknownEntries.length > 0 && (
        <Collapsible className="mt-2">
          <CollapsibleTrigger className="group flex items-center gap-1 text-muted-foreground transition-colors hover:text-foreground focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring focus-visible:ring-offset-1 rounded-sm">
            <ChevronDown className="size-3 transition-transform duration-200 group-data-[state=open]:rotate-180" />
            其他 cookie 字段（{unknownEntries.length} 项，不会上传）
          </CollapsibleTrigger>
          <CollapsibleContent className="mt-1 grid gap-1 font-mono data-[state=closed]:animate-out data-[state=closed]:fade-out-0 data-[state=open]:animate-in data-[state=open]:fade-in-0">
            {unknownEntries.map(([k, v]) => (
              <div key={k} className="flex text-muted-foreground">
                <span className="w-40 shrink-0">{k}</span>
                <span className="truncate" title={v}>
                  {v}
                </span>
              </div>
            ))}
          </CollapsibleContent>
        </Collapsible>
      )}
    </div>
  );
}

function maskSensitive(name: string, value: string): string {
  const sensitive = ["musickey", "refresh_token", "refresh_key", "access_token"];
  if (sensitive.includes(name) && value.length > 16) {
    return value.slice(0, 8) + "…" + value.slice(-4);
  }
  return value;
}
