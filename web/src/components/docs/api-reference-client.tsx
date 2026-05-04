"use client";

// Scalar 的 React 包没把 css 打进 JS bundle，需要手动 side-effect import。
// 注意：即使 ApiReferenceReact 是 next/dynamic 懒加载的，这一行也必须在模块顶层导入，
// 否则 Next.js 不会把 .css 加进 chunk。
import "@scalar/api-reference-react/style.css";

import dynamic from "next/dynamic";
import { useEffect, useMemo, useRef, useState } from "react";

// Scalar 的 React 组件不支持 SSR（用到了 window/document），延后到客户端动态加载。
const ApiReferenceReact = dynamic(
  async () => {
    const mod = await import("@scalar/api-reference-react");
    return mod.ApiReferenceReact;
  },
  { ssr: false, loading: () => <DocsSkeleton /> },
);

function DocsSkeleton() {
  return (
    <div className="flex h-[80vh] items-center justify-center text-sm text-muted-foreground">
      正在加载 API 文档…
    </div>
  );
}

export interface ApiReferenceClientProps {
  /** 已解析为 JS 对象的 OpenAPI 规格（servers/paths 已重写为 /api/proxy 路径）。 */
  spec: Record<string, unknown>;
}

function readCookie(name: string): string | null {
  if (typeof document === "undefined") return null;
  const target = `${name}=`;
  for (const part of document.cookie.split(";")) {
    const t = part.trim();
    if (t.startsWith(target)) return decodeURIComponent(t.slice(target.length));
  }
  return null;
}

// ============== 中文化 ==============
//
// Scalar 0.9.x 没有 i18n / locale 配置。所有 UI 文案都硬编码在 .vue 模板里。
// 我们用 MutationObserver 扫描 .scalar-app 子树，把已知英文文案替换成中文。
// 触发条件是"文本节点的 trim 后内容完全等于字典 key"，所以不会误伤
// 用户从 spec 里写的描述文本（描述会经 markdown 渲染成多个子节点）。

/** 整段精确匹配的英文文案 → 中文。 */
const STATIC_LABELS: Record<string, string> = {
  // 操作 / 按钮
  "Test Request": "测试请求",
  "Show More": "展开更多",
  "Show Less": "收起",
  "Show Schema": "显示数据结构",
  "Show Schema Details": "查看数据结构详情",
  "Show Example": "查看示例",
  "Show additional properties": "显示附加属性",
  "Hide Headers": "隐藏请求头",
  "Show Headers": "显示请求头",
  "Copy link": "复制链接",
  "Copy link to": "复制链接到",
  "Copy link to ": "复制链接到 ",
  "Upload File": "上传文件",
  "Upload Document": "上传文档",
  "Generate": "生成",
  "Send Request": "发送请求",
  "Try it": "在线试用",
  "Try It": "在线试用",
  // 大标题 / 分组
  "Authentication": "鉴权方式",
  "Servers": "服务器",
  "Server": "服务器",
  "Variables": "环境变量",
  "Body": "请求体",
  "Headers": "请求头",
  "Cookies": "Cookie",
  "Path Parameters": "路径参数",
  "Query Parameters": "查询参数",
  "Header Parameters": "请求头参数",
  "Cookie Parameters": "Cookie 参数",
  "Models": "数据模型",
  "Webhooks": "Webhook",
  "Operations": "接口",
  "Responses": "响应",
  "Response": "响应",
  "Request": "请求",
  "Request Body": "请求体",
  "Request Example": "请求示例",
  "Response Example": "响应示例",
  "Examples": "示例",
  "Example": "示例",
  // schema 字段标注
  "Default": "默认值",
  "Required": "必填",
  "required": "必填",
  "deprecated": "已弃用",
  "additional properties": "附加属性",
  "Pattern:": "正则:",
  "Pattern: ": "正则: ",
  "Type:": "类型:",
  "Type: ": "类型: ",
  "Format:": "格式:",
  "Format: ": "格式: ",
  "Status:": "状态:",
  "Status: ": "状态: ",
  "Selected Content Type:": "已选内容类型:",
  "Content Type": "内容类型",
  "enum": "枚举",
  "const:": "常量:",
  "const: ": "常量: ",
  // 开发者工具栏
  "Developer Tools": "开发者工具",
  "Show Sidebar": "显示侧边栏",
  "Show Operation ID": "显示接口 ID",
  "Default Open First Tag": "默认展开第一个分组",
  "Default Open All Tags": "默认展开所有分组",
  "Expand All Model Sections": "展开所有数据模型",
  "Expand All Responses": "展开所有响应",
  "Hide Client Button": "隐藏客户端按钮",
  "Hide Dark Mode Toggle": "隐藏暗色切换",
  "Hide Models": "隐藏数据模型",
  "Hide Search": "隐藏搜索框",
  "Hide Test Request Button": "隐藏测试请求按钮",
  "Theme": "主题",
  "Layout": "布局",
  "Layout Options": "布局选项",
  "Configure": "配置",
  "Share": "分享",
  "Deploy": "部署",
  "Deploy on Scalar": "部署到 Scalar",
  "Search": "搜索",
  // sidebar 顶部"简介"
  "Introduction": "简介",
  "Description": "描述",
  "Overview": "概览",
  // 搜索
  "Search...": "搜索…",
  "No results found": "暂无匹配结果",
  // 鉴权 schema 细节
  "API Key": "API Key",
  "Cookie Auth": "Cookie 鉴权",
  "Bearer": "Bearer Token",
  "Bearer Token": "Bearer Token",
};

/** 前缀替换：仅替换文本节点开头匹配的英文，剩余部分保留（适用于 Vue 内 "Hide " + dynamicTitle 这种）。 */
const PREFIX_LABELS: Array<[string, string]> = [
  ["Hide ", "隐藏 "],
  ["Show ", "显示 "],
  ["Example: ", "示例: "],
  ["Default: ", "默认: "],
  ["for ", "适用于 "],
  ["OAS ", "OAS "],
];

/** 属性翻译（aria-label / title / placeholder） */
const ATTR_LABELS: Record<string, string> = {
  ...STATIC_LABELS,
  Search: "搜索",
  Close: "关闭",
  "Close modal": "关闭弹窗",
  "Open in Sidebar": "在侧边栏中打开",
  "Toggle theme": "切换主题",
  "Toggle dark mode": "切换暗色模式",
  "Open API Client": "打开 API 客户端",
};

function translateText(raw: string): string | null {
  const trimmed = raw.trim();
  if (!trimmed) return null;
  // 1. 整段精确匹配（保留前后空白让排版不变）
  const exact = STATIC_LABELS[trimmed];
  if (exact) {
    const leading = raw.slice(0, raw.indexOf(trimmed));
    const trailing = raw.slice(raw.indexOf(trimmed) + trimmed.length);
    return leading + exact + trailing;
  }
  // 2. 前缀匹配（用于 "Hide ${title}" 类拼接）
  for (const [en, zh] of PREFIX_LABELS) {
    if (trimmed.startsWith(en) && trimmed.length > en.length) {
      const rest = trimmed.slice(en.length);
      const leading = raw.slice(0, raw.indexOf(trimmed));
      const trailing = raw.slice(raw.indexOf(trimmed) + trimmed.length);
      return leading + zh + rest + trailing;
    }
  }
  return null;
}

function applyTranslations(root: HTMLElement) {
  // 文本节点
  const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT, {
    acceptNode(node) {
      const v = node.nodeValue;
      if (!v || !v.trim()) return NodeFilter.FILTER_REJECT;
      // 跳过 <code>/<pre>/<script>/<style>，避免改坏代码示例
      const parent = node.parentElement;
      if (!parent) return NodeFilter.FILTER_REJECT;
      const tag = parent.tagName;
      if (
        tag === "CODE" ||
        tag === "PRE" ||
        tag === "SCRIPT" ||
        tag === "STYLE" ||
        tag === "TEXTAREA" ||
        tag === "INPUT"
      ) {
        return NodeFilter.FILTER_REJECT;
      }
      return NodeFilter.FILTER_ACCEPT;
    },
  });

  const updates: Array<[Node, string]> = [];
  let cur: Node | null;
  while ((cur = walker.nextNode())) {
    const next = translateText(cur.nodeValue ?? "");
    if (next != null && next !== cur.nodeValue) {
      updates.push([cur, next]);
    }
  }
  for (const [node, value] of updates) {
    node.nodeValue = value;
  }

  // 属性
  const ATTRS = ["aria-label", "title", "placeholder"] as const;
  for (const attr of ATTRS) {
    root.querySelectorAll(`[${attr}]`).forEach((el) => {
      const v = el.getAttribute(attr);
      if (!v) return;
      const trimmed = v.trim();
      const zh = ATTR_LABELS[trimmed];
      if (zh && zh !== trimmed) {
        el.setAttribute(attr, zh);
      }
    });
  }
}

export function ApiReferenceClient({ spec }: ApiReferenceClientProps) {
  // 跟随系统暗色模式
  const [darkMode, setDarkMode] = useState(false);
  useEffect(() => {
    if (typeof window === "undefined") return;
    const root = document.documentElement;
    const observer = new MutationObserver(() => {
      setDarkMode(root.classList.contains("dark"));
    });
    setDarkMode(root.classList.contains("dark"));
    observer.observe(root, { attributes: true, attributeFilter: ["class"] });
    return () => observer.disconnect();
  }, []);

  // Try it：写方法时自动从 csrf_token cookie 注入 X-CSRF-Token 头，
  // GET/HEAD 不需要；同时给 Bearer / Cookie 任一已配置的方案优先。
  const onBeforeRequest = useMemo(
    () =>
      ({ request: builder }: { request: { headers: Headers; method?: string } }) => {
        try {
          const method = (builder.method ?? "GET").toUpperCase();
          if (method !== "GET" && method !== "HEAD") {
            const tok = readCookie("csrf_token");
            if (tok) builder.headers.set("X-CSRF-Token", tok);
          }
        } catch {
          /* noop —— Scalar 内部不同版本 builder 形态可能有差异 */
        }
      },
    [],
  );

  // 默认认证：浏览器登录后访问 /api/proxy 时浏览器会自动带上 sid cookie，
  // 所以默认走 cookieAuth 即可，用户无需在 Scalar UI 填任何 token；
  // 如果想用 API Key，可以在 Scalar 的 Authentication 面板里粘贴 Bearer。
  const authentication = useMemo(
    () => ({ preferredSecurityScheme: "cookieAuth" as const }),
    [],
  );

  // ---- 中文化：MutationObserver 扫描 Scalar 子树并替换硬编码英文 ----
  const containerRef = useRef<HTMLDivElement | null>(null);
  useEffect(() => {
    const root = containerRef.current;
    if (!root) return;

    let scheduled = false;
    let observerRef: MutationObserver | null = null;

    const tick = () => {
      scheduled = false;
      // 暂停观察期间的自身改动，避免无限循环；改完再恢复
      observerRef?.disconnect();
      try {
        applyTranslations(root);
      } finally {
        if (observerRef) {
          observerRef.observe(root, {
            childList: true,
            subtree: true,
            characterData: true,
          });
        }
      }
    };

    const schedule = () => {
      if (scheduled) return;
      scheduled = true;
      // requestAnimationFrame 更稳，避免 Scalar 还在批量挂载时反复触发
      requestAnimationFrame(tick);
    };

    observerRef = new MutationObserver(schedule);
    observerRef.observe(root, {
      childList: true,
      subtree: true,
      characterData: true,
    });

    // Scalar 是异步动态加载的，初次渲染后兜底再跑一次
    const t1 = window.setTimeout(schedule, 200);
    const t2 = window.setTimeout(schedule, 800);

    return () => {
      observerRef?.disconnect();
      window.clearTimeout(t1);
      window.clearTimeout(t2);
    };
  }, []);

  return (
    <div
      ref={containerRef}
      lang="zh-CN"
      className="rounded-lg border bg-background overflow-hidden"
    >
      <ApiReferenceReact
        configuration={{
          // 用 sources[].content 显式注入对象，彻底避免任何 URL 拉取 / 字符串解析路径。
          sources: [
            {
              title: "多平台音乐网关 API",
              slug: "qmg",
              default: true,
              content: spec,
            },
          ],
          theme: "default",
          darkMode,
          layout: "modern",
          hideDownloadButton: false,
          hideTestRequestButton: false,
          showSidebar: true,
          searchHotKey: "k",
          // server URL 由 spec.servers[0] 承载（已在服务端 rewireForLocalProxy 设为 /api/docs-proxy）。
          // 不再设置 baseServerURL，避免与 spec.servers 双向叠加导致 URL 拼错。
          // 显式禁用 Scalar CDN 代理：Try it 直接走同源 fetch → /api/docs-proxy → backend，
          // 这样 cookie 自动随同源请求带过去；走 proxy.scalar.com 会丢 cookie。
          proxyUrl: "",
          authentication,
          onBeforeRequest,
          metaData: {
            title: "多平台音乐网关 API",
          },
        }}
      />
    </div>
  );
}
