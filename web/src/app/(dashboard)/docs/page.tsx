import "server-only";

import Link from "next/link";
import { ApiReferenceClient } from "@/components/docs/api-reference-client";
import { ConceptCards } from "@/components/docs/concept-cards";
import { Card } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Download, ExternalLink, Code2 } from "lucide-react";
import { requireUser } from "@/lib/auth/session";
import { isAdmin } from "@/lib/auth/rbac";
import {
  loadOpenApiYaml,
  parseSpec,
  rewireForLocalProxy,
  stripAdminEndpoints,
} from "@/lib/docs/spec";

export const dynamic = "force-dynamic";

const QUICK_START = `# 1. 通过 API Key 调用接口
curl -H "Authorization: Bearer <your_api_key>" \\
  https://api.example.com/v1/qqmusic/songs/0039MnYb0qxYhV

# 2. 搜索歌曲
curl -H "Authorization: Bearer <your_api_key>" \\
  "https://api.example.com/v1/qqmusic/search/songs?q=周杰伦&page=1&page_size=20"

# 3. 获取直链（默认 MP3_320）
curl -H "Authorization: Bearer <your_api_key>" \\
  "https://api.example.com/v1/qqmusic/songs/0039MnYb0qxYhV/url?quality=FLAC"`;

export default async function DocsPage() {
  const user = await requireUser();
  const admin = isAdmin(user.role);

  const rawYaml = await loadOpenApiYaml();
  const parsed = parseSpec(rawYaml);
  // 1) 非管理员剥离 admin 分类
  const filtered = parsed && !admin ? stripAdminEndpoints(parsed) : parsed;
  // 2) 让 "Try it" 走当前站点的 /api/proxy（自动带 cookie），并补充常用参数 example
  const specObject = filtered ? rewireForLocalProxy(filtered) : null;

  // 下载链接走 Next.js /api/docs 路由，会按当前 session 的 role 自动过滤；
  // 非管理员下载到的也是去掉 admin 分类的版本。
  const specDownloadUrl = "/api/docs/openapi.yaml";

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-semibold tracking-tight">API 文档中心</h1>
        <p className="text-sm text-muted-foreground">
          完整的 RESTful API 参考、字段说明、请求/响应示例与在线试用。本文档基于 OpenAPI 3.0
          规范自动生成，与后端版本完全同步。
          {!admin && (
            <span className="ml-1">
              当前帐号为普通用户，已自动隐藏仅管理员可访问的接口。
            </span>
          )}
        </p>
      </div>

      <div className="grid gap-4 md:grid-cols-3">
        <Card className="p-4">
          <div className="flex items-center gap-2 text-sm font-semibold">
            <Code2 className="h-4 w-4" />
            认证方式
          </div>
          <p className="mt-2 text-xs text-muted-foreground">
            两种认证模式：
            <br />
            • 浏览器：登录后自动携带会话 Cookie。
            <br />
            • 程序化调用：在
            <code className="mx-1 rounded bg-muted px-1 py-0.5 text-[11px]">
              Authorization
            </code>
            头中携带 API Key（<code className="text-[11px]">Bearer</code> 前缀）。
          </p>
        </Card>

        <Card className="p-4">
          <div className="flex items-center gap-2 text-sm font-semibold">
            <Download className="h-4 w-4" />
            下载规格
          </div>
          <p className="mt-2 text-xs text-muted-foreground">
            可下载完整的 OpenAPI 3.0 YAML，导入 Postman / Insomnia / Apifox 等工具直接使用。
          </p>
          <Button asChild variant="outline" size="sm" className="mt-3">
            <a href={specDownloadUrl} download="qqmusic-gateway-openapi.yaml">
              下载 openapi.yaml
            </a>
          </Button>
        </Card>

        <Card className="p-4">
          <div className="flex items-center gap-2 text-sm font-semibold">
            <ExternalLink className="h-4 w-4" />
            在线试用
          </div>
          <p className="mt-2 text-xs text-muted-foreground">
            前往 Playground 直接调试接口，自动带上当前会话或 API Key。
          </p>
          <Button asChild variant="outline" size="sm" className="mt-3">
            <Link href="/playground">打开 Playground</Link>
          </Button>
        </Card>
      </div>

      <Card className="p-4">
        <div className="text-sm font-semibold">快速开始</div>
        <pre className="mt-2 overflow-x-auto rounded-md bg-muted p-3 text-[11px] leading-5">
          {QUICK_START}
        </pre>
        <p className="mt-2 text-xs text-muted-foreground">
          所有接口默认返回 <code className="text-[11px]">{`{ code, message, data }`}</code>{" "}
          的统一封装，
          <code className="text-[11px]">code = 0</code> 表示成功；详细错误码见下方文档。
        </p>
      </Card>

      <div>
        <h2 className="text-base font-semibold tracking-tight">概念速查</h2>
        <p className="text-xs text-muted-foreground">
          下面 4 个主题覆盖了使用网关时最容易踩坑的点；详细字段说明见后面的接口文档。
        </p>
      </div>
      <ConceptCards />

      <div>
        <h2 className="text-base font-semibold tracking-tight">完整接口参考</h2>
        <p className="text-xs text-muted-foreground">
          下方为基于 OpenAPI 3.0 的交互式文档，可直接试用、查看请求/响应示例。
        </p>
      </div>
      {specObject ? (
        <ApiReferenceClient spec={specObject} />
      ) : (
        <Card className="p-4 text-sm text-muted-foreground">
          暂时无法加载 API 文档规格。请确认{" "}
          <code>gateway/internal/gateway/docs/openapi.yaml</code> 可读，或网关服务可从 Web
          容器访问。
        </Card>
      )}
    </div>
  );
}
