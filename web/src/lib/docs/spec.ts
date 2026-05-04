import "server-only";

import { readFile } from "node:fs/promises";
import path from "node:path";

import yaml from "js-yaml";

import { serverEnv } from "@/lib/env/server";

/** 标识"管理员专属"的 tag。这些 tag 关联的接口对普通用户隐藏。
 *  与 gateway/internal/gateway/docs/openapi.yaml 中 tags[].name 严格对齐。 */
export const ADMIN_TAGS = new Set<string>([
  "用户管理（管理员）",
  "账号池（管理员）",
  "系统设置（管理员）",
]);

const HTTP_METHODS = [
  "get",
  "post",
  "put",
  "patch",
  "delete",
  "head",
  "options",
  "trace",
] as const;
type HttpMethod = (typeof HTTP_METHODS)[number];

type OperationObject = { tags?: string[] } & Record<string, unknown>;

/** 加载仓库内的 openapi.yaml 原文。先读盘，失败再退化到网关 HTTP。 */
export async function loadOpenApiYaml(): Promise<string | null> {
  const candidates = [
    path.join(process.cwd(), "..", "gateway", "internal", "gateway", "docs", "openapi.yaml"),
    path.join(process.cwd(), "gateway", "internal", "gateway", "docs", "openapi.yaml"),
  ];
  for (const p of candidates) {
    try {
      const buf = await readFile(p, "utf-8");
      if (buf && buf.length > 100) return buf;
    } catch {
      /* try next */
    }
  }
  const url = `${serverEnv.BACKEND_URL.replace(/\/+$/, "")}/v1/docs/openapi.yaml`;
  try {
    const res = await fetch(url, { cache: "no-store" });
    if (!res.ok) return null;
    return await res.text();
  } catch {
    return null;
  }
}

/** YAML 字符串 → JS 对象。 */
export function parseSpec(text: string | null): Record<string, unknown> | null {
  if (!text) return null;
  try {
    const obj = yaml.load(text) as Record<string, unknown> | null;
    return obj && typeof obj === "object" ? obj : null;
  } catch {
    return null;
  }
}

/**
 * 从 OpenAPI 文档中剔除"管理员"分类的接口，返回新的对象（不修改原对象）。
 * - paths 下每个 method 如果带了 ADMIN_TAGS 中任一 tag，整条 operation 删除；
 * - operation 删完之后没剩 method 的 path 整条删除；
 * - 顶层 tags 列表过滤掉 ADMIN_TAGS 条目。
 */
export function stripAdminEndpoints(
  spec: Record<string, unknown>,
): Record<string, unknown> {
  const out: Record<string, unknown> = { ...spec };

  const paths = spec.paths;
  if (paths && typeof paths === "object") {
    const newPaths: Record<string, unknown> = {};
    for (const [pathKey, pathVal] of Object.entries(paths as Record<string, unknown>)) {
      if (!pathVal || typeof pathVal !== "object") {
        newPaths[pathKey] = pathVal;
        continue;
      }
      const item = pathVal as Record<string, unknown>;
      const filtered: Record<string, unknown> = {};
      let kept = 0;
      for (const [k, v] of Object.entries(item)) {
        if (HTTP_METHODS.includes(k as HttpMethod)) {
          const op = v as OperationObject | undefined;
          const tags = Array.isArray(op?.tags) ? op!.tags! : [];
          if (tags.some((t) => ADMIN_TAGS.has(t))) continue;
          filtered[k] = v;
          kept++;
        } else {
          filtered[k] = v;
        }
      }
      if (kept > 0) newPaths[pathKey] = filtered;
    }
    out.paths = newPaths;
  }

  if (Array.isArray(spec.tags)) {
    out.tags = (spec.tags as Array<{ name?: string }>).filter(
      (t) => !(t && typeof t.name === "string" && ADMIN_TAGS.has(t.name)),
    );
  }

  return out;
}

// =================== "Try it" 联动 ===================
//
// Scalar 调用 servers[0].url + 规格 path。原 spec 里 path 都是真实 backend 路径
// (例如 /v1/auth/login、/healthz)，所以我们走"原样转发"代理：
//   /api/docs-proxy/<verbatim path> → backend/<verbatim path>
//
// 这样无需在 spec 里改写 paths：
//   - /v1/qqmusic/songs/{id}    → /api/docs-proxy/v1/qqmusic/songs/<id>
//   - /healthz                  → /api/docs-proxy/healthz
// CSRF / cookie / 限流通路与主代理一致。

/**
 * 让规格的 server 指向本站文档专用代理，并补充常见参数 example。
 * 不改原对象，返回新对象。
 */
export function rewireForLocalProxy(
  spec: Record<string, unknown>,
): Record<string, unknown> {
  const out: Record<string, unknown> = { ...spec };

  // 1) servers：用原样转发的 docs-proxy
  out.servers = [
    { url: "/api/docs-proxy", description: "本站代理（自动透传 cookie / CSRF）" },
  ];

  // 2) paths：保持不变，但给参数补 example
  const paths = spec.paths;
  if (paths && typeof paths === "object") {
    const newPaths: Record<string, unknown> = {};
    for (const [k, v] of Object.entries(paths as Record<string, unknown>)) {
      newPaths[k] = enhanceOperationExamples(v);
    }
    out.paths = newPaths;
  }

  // 3) components.parameters：保证 Provider 有可点的 example
  const comps = spec.components as Record<string, unknown> | undefined;
  if (comps && typeof comps === "object") {
    const params = comps.parameters as Record<string, unknown> | undefined;
    if (params && typeof params === "object") {
      const provider = params.Provider as Record<string, unknown> | undefined;
      if (provider && typeof provider === "object" && !provider.example) {
        params.Provider = { ...provider, example: "qqmusic" };
      }
    }
  }

  return out;
}

/** 给 path item 下每个 operation 的 path 参数填上常见 example，便于直接点 "Try it"。 */
function enhanceOperationExamples(v: unknown): unknown {
  if (!v || typeof v !== "object") return v;
  const item = v as Record<string, unknown>;
  const out: Record<string, unknown> = { ...item };
  for (const m of HTTP_METHODS) {
    const op = out[m] as Record<string, unknown> | undefined;
    if (!op || typeof op !== "object") continue;
    const params = op.parameters;
    if (!Array.isArray(params)) continue;
    out[m] = {
      ...op,
      parameters: params.map((p) => fillParamExample(p)),
    };
  }
  return out;
}

const PARAM_EXAMPLES: Record<string, string | number> = {
  provider: "qqmusic",
  id: "0039MnYb0qxYhV",
  q: "周杰伦",
  page: 1,
  page_size: 20,
  limit: 20,
  offset: 0,
  quality: "MP3_320",
  days: 30,
  minutes: 60,
};

function fillParamExample(p: unknown): unknown {
  if (!p || typeof p !== "object") return p;
  const param = p as Record<string, unknown>;
  // $ref 形式不动；具体 inline 的才注入 example
  if (typeof param.$ref === "string") return param;
  if (param.example != null) return param;
  const name = typeof param.name === "string" ? param.name : "";
  const ex = PARAM_EXAMPLES[name];
  if (ex === undefined) return param;
  return { ...param, example: ex };
}
