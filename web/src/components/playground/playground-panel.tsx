"use client";

import * as React from "react";
import { Play, Copy, Clock } from "lucide-react";
import { toast } from "sonner";

import { CodeViewer } from "@/components/ui/code-viewer";
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

interface ParamDef {
  key: string;
  label: string;
  placeholder: string;
  required?: boolean;
  isPath?: boolean;
  options?: { value: string; label: string }[];
}

interface EndpointDef {
  id: string;
  label: string;
  method: string;
  path: string;
  params: ParamDef[];
}

const QQMUSIC_ENDPOINTS: EndpointDef[] = [
  {
    id: "search", label: "搜索歌曲", method: "GET", path: "/v1/qqmusic/search/songs",
    params: [
      { key: "q", label: "关键词", placeholder: "周杰伦", required: true },
      { key: "page", label: "页码", placeholder: "1" },
      { key: "page_size", label: "每页数", placeholder: "20" },
    ],
  },
  {
    id: "song", label: "歌曲详情", method: "GET", path: "/v1/qqmusic/songs/{id}",
    params: [{ key: "id", label: "歌曲 MID", placeholder: "001yS0N33jtpMk", required: true, isPath: true }],
  },
  {
    id: "songurl", label: "歌曲链接", method: "GET", path: "/v1/qqmusic/songs/{id}/url",
    params: [
      { key: "id", label: "歌曲 MID", placeholder: "001yS0N33jtpMk", required: true, isPath: true },
      { key: "quality", label: "品质", placeholder: "MP3_320", options: [
        { value: "MP3_128", label: "MP3 128kbps" }, { value: "MP3_320", label: "MP3 320kbps" },
        { value: "FLAC", label: "FLAC 无损" }, { value: "MASTER", label: "臻品母带" },
        { value: "DOLBY", label: "杜比全景声" },
      ]},
    ],
  },
  {
    id: "lyric", label: "歌词", method: "GET", path: "/v1/qqmusic/songs/{id}/lyric",
    params: [{ key: "id", label: "歌曲 MID", placeholder: "001yS0N33jtpMk", required: true, isPath: true }],
  },
  {
    id: "album", label: "专辑", method: "GET", path: "/v1/qqmusic/albums/{id}",
    params: [{ key: "id", label: "专辑 MID", placeholder: "002fRO0N4FftzY", required: true, isPath: true }],
  },
  {
    id: "artist", label: "歌手", method: "GET", path: "/v1/qqmusic/artists/{id}",
    params: [{ key: "id", label: "歌手 MID", placeholder: "0025NhlN2yWrP4", required: true, isPath: true }],
  },
  {
    id: "playlist", label: "歌单", method: "GET", path: "/v1/qqmusic/playlists/{id}",
    params: [{ key: "id", label: "歌单 ID", placeholder: "7272907725", required: true, isPath: true }],
  },
];

const NETEASE_ENDPOINTS: EndpointDef[] = [
  {
    id: "search", label: "搜索歌曲", method: "GET", path: "/v1/netease/search/songs",
    params: [
      { key: "q", label: "关键词", placeholder: "周杰伦", required: true },
      { key: "page", label: "页码", placeholder: "1" },
      { key: "page_size", label: "每页数", placeholder: "30" },
    ],
  },
  {
    id: "song", label: "歌曲详情", method: "GET", path: "/v1/netease/songs/{id}",
    params: [{ key: "id", label: "歌曲 ID", placeholder: "347230", required: true, isPath: true }],
  },
  {
    id: "songurl", label: "歌曲链接", method: "GET", path: "/v1/netease/songs/{id}/url",
    params: [
      { key: "id", label: "歌曲 ID", placeholder: "347230", required: true, isPath: true },
      { key: "quality", label: "品质", placeholder: "exhigh", options: [
        { value: "standard", label: "标准" }, { value: "exhigh", label: "极高" },
        { value: "lossless", label: "无损" }, { value: "hires", label: "Hi-Res" },
        { value: "jyeffect", label: "高清环绕声" }, { value: "jymaster", label: "超清母带" },
      ]},
    ],
  },
  {
    id: "lyric", label: "歌词", method: "GET", path: "/v1/netease/songs/{id}/lyric",
    params: [{ key: "id", label: "歌曲 ID", placeholder: "347230", required: true, isPath: true }],
  },
  {
    id: "album", label: "专辑", method: "GET", path: "/v1/netease/albums/{id}",
    params: [{ key: "id", label: "专辑 ID", placeholder: "32311", required: true, isPath: true }],
  },
  {
    id: "artist", label: "歌手", method: "GET", path: "/v1/netease/artists/{id}",
    params: [{ key: "id", label: "歌手 ID", placeholder: "6452", required: true, isPath: true }],
  },
  {
    id: "playlist", label: "歌单", method: "GET", path: "/v1/netease/playlists/{id}",
    params: [{ key: "id", label: "歌单 ID", placeholder: "24381616", required: true, isPath: true }],
  },
];

const PROVIDERS: { value: string; label: string; endpoints: EndpointDef[] }[] = [
  { value: "qqmusic", label: "QQ 音乐", endpoints: QQMUSIC_ENDPOINTS },
  { value: "netease", label: "网易云音乐", endpoints: NETEASE_ENDPOINTS },
];

type ParamValues = Record<string, string>;

export function PlaygroundPanel() {
  const [apiKeyPlain, setApiKeyPlain] = React.useState("");
  const [provider, setProvider] = React.useState("qqmusic");
  const [endpoint, setEndpoint] = React.useState<EndpointDef>(QQMUSIC_ENDPOINTS[0]);
  const [params, setParams] = React.useState<ParamValues>({});
  const [result, setResult] = React.useState<{
    status: number;
    latency: number;
    body: string;
    url: string;
  } | null>(null);
  const [loading, setLoading] = React.useState(false);

  const currentEndpoints = PROVIDERS.find((p) => p.value === provider)?.endpoints ?? QQMUSIC_ENDPOINTS;

  const onProviderChange = (v: string) => {
    setProvider(v);
    const eps = PROVIDERS.find((p) => p.value === v)?.endpoints ?? [];
    if (eps.length > 0) {
      setEndpoint(eps[0]);
      setParams({});
      setResult(null);
    }
  };

  const buildPath = () => {
    let path: string = endpoint.path;
    const query = new URLSearchParams();
    for (const p of endpoint.params) {
      const val = params[p.key] || p.placeholder;
      if (p.isPath) {
        path = path.replace(`{${p.key}}`, encodeURIComponent(val));
      } else {
        if (val) query.set(p.key, val);
      }
    }
    const qs = query.toString();
    return `${path}${qs ? `?${qs}` : ""}`;
  };

  const onRun = async () => {
    if (!apiKeyPlain.trim()) {
      toast.error("请输入 API Key 明文（mk_...）");
      return;
    }
    const apiPath = buildPath();
    const stripped = apiPath.replace(/^\/v1\//, "");
    const proxyUrl = `/api/proxy/${stripped}`;
    const directUrl = `http://localhost:8080${apiPath}`;
    setLoading(true);
    setResult(null);
    const start = performance.now();
    try {
      const resp = await fetch(proxyUrl, {
        method: endpoint.method,
        headers: { "X-API-Key": apiKeyPlain.trim() },
      });
      const latency = Math.round(performance.now() - start);
      const text = await resp.text();
      let body: string;
      try {
        body = JSON.stringify(JSON.parse(text), null, 2);
      } catch {
        body = text;
      }
      setResult({ status: resp.status, latency, body, url: directUrl });
    } catch (err) {
      const latency = Math.round(performance.now() - start);
      setResult({
        status: 0, latency,
        body: `网络错误: ${err instanceof Error ? err.message : "unknown"}`,
        url: directUrl,
      });
    } finally {
      setLoading(false);
    }
  };

  const statusColor = (s: number) => {
    if (s >= 200 && s < 300) return "text-emerald-600 dark:text-emerald-400";
    if (s >= 400) return "text-destructive";
    return "text-amber-600";
  };

  const curlCommand = result
    ? `curl -X ${endpoint.method} \\\n  '${result.url}' \\\n  -H 'X-API-Key: <your-api-key>'`
    : "";

  const [activeTab, setActiveTab] = React.useState<"body" | "curl">("body");
  const editorContent = activeTab === "body" ? (result?.body ?? "") : curlCommand;

  return (
    <div className="space-y-6">
      <div className="grid gap-4 lg:grid-cols-3">
        <Card>
          <CardHeader className="pb-3">
            <CardTitle className="text-sm">认证</CardTitle>
            <CardDescription>输入 API Key 明文（mk_... 值）</CardDescription>
          </CardHeader>
          <CardContent className="space-y-2">
            <Input
              type="password"
              placeholder="mk_..."
              value={apiKeyPlain}
              onChange={(e) => setApiKeyPlain(e.target.value)}
            />
          </CardContent>
        </Card>

        <Card className="lg:col-span-2">
          <CardHeader className="pb-3">
            <CardTitle className="text-sm">请求配置</CardTitle>
          </CardHeader>
          <CardContent className="space-y-3">
            <div className="grid gap-3 sm:grid-cols-2">
              <div className="space-y-1">
                <Label className="text-xs">平台</Label>
                <Select value={provider} onValueChange={onProviderChange}>
                  <SelectTrigger><SelectValue /></SelectTrigger>
                  <SelectContent>
                    {PROVIDERS.map((p) => (
                      <SelectItem key={p.value} value={p.value}>{p.label}</SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </div>
              <div className="space-y-1">
                <Label className="text-xs">端点</Label>
                <Select
                  value={endpoint.id}
                  onValueChange={(v) => {
                    const ep = currentEndpoints.find((e) => e.id === v);
                    if (ep) { setEndpoint(ep); setParams({}); setResult(null); }
                  }}
                >
                  <SelectTrigger><SelectValue /></SelectTrigger>
                  <SelectContent>
                    {currentEndpoints.map((ep) => (
                      <SelectItem key={ep.id} value={ep.id}>
                        <span className="font-mono text-xs">{ep.method}</span> {ep.label}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </div>
            </div>

            <code className="block rounded bg-muted px-2 py-1 text-xs break-all">
              {endpoint.method} {endpoint.path}
            </code>

            <Separator />

            {endpoint.params.map((p) => (
              <div key={p.key} className="space-y-1">
                <Label className="text-xs">
                  {p.label}{p.required && <span className="text-destructive"> *</span>}
                </Label>
                {p.options ? (
                  <Select
                    value={params[p.key] || ""}
                    onValueChange={(v) => setParams({ ...params, [p.key]: v })}
                  >
                    <SelectTrigger><SelectValue placeholder={p.placeholder} /></SelectTrigger>
                    <SelectContent>
                      {p.options.map((opt) => (
                        <SelectItem key={opt.value} value={opt.value}>{opt.label}</SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                ) : (
                  <Input
                    placeholder={p.placeholder}
                    value={params[p.key] || ""}
                    onChange={(e) => setParams({ ...params, [p.key]: e.target.value })}
                  />
                )}
              </div>
            ))}
          </CardContent>
        </Card>
      </div>

      <div className="flex items-center gap-3">
        <Button onClick={onRun} disabled={loading}>
          <Play className="mr-1 size-4" />
          {loading ? "请求中..." : "发送请求"}
        </Button>
        {result && (
          <div className="flex items-center gap-2 text-sm">
            <Badge variant={result.status >= 200 && result.status < 300 ? "default" : "destructive"}>
              {result.status || "ERR"}
            </Badge>
            <span className="flex items-center gap-1 text-muted-foreground">
              <Clock className="size-3" />{result.latency}ms
            </span>
          </div>
        )}
      </div>

      {result && (
        <Card>
          <CardHeader className="pb-2">
            <div className="flex items-center gap-2">
              <button
                className={`text-sm font-medium ${activeTab === "body" ? "underline" : "text-muted-foreground"}`}
                onClick={() => setActiveTab("body")}
              >
                响应
              </button>
              <button
                className={`text-sm font-medium ${activeTab === "curl" ? "underline" : "text-muted-foreground"}`}
                onClick={() => setActiveTab("curl")}
              >
                cURL
              </button>
              <Button
                size="icon" variant="ghost" className="ml-auto size-7"
                onClick={() => {
                  navigator.clipboard.writeText(editorContent);
                  toast.success("已复制");
                }}
              >
                <Copy className="size-3" />
              </Button>
            </div>
          </CardHeader>
          <CardContent>
            <CodeViewer code={editorContent} language={activeTab === "body" ? "json" : "bash"} />
          </CardContent>
        </Card>
      )}
    </div>
  );
}
