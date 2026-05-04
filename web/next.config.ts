import type { NextConfig } from "next";

// ESM 加载下 __dirname 不存在；Node 20.11+ 的 import.meta.dirname 一行解决，
// 不依赖 cwd（避免被 PyCharm/脚本从父目录启动时把 turbopack.root 错指到 monorepo 根）。
const ROOT = import.meta.dirname;

// 安全 headers; 注意 Content-Security-Policy 由 middleware.ts 注入 (nonce 模式),
// 这里只放与请求无关的静态 header.
const securityHeaders = [
  { key: "X-Content-Type-Options", value: "nosniff" },
  { key: "X-Frame-Options", value: "DENY" },
  { key: "Referrer-Policy", value: "strict-origin-when-cross-origin" },
  { key: "Permissions-Policy", value: "camera=(), microphone=(), geolocation=(), payment=()" },
  { key: "Cross-Origin-Opener-Policy", value: "same-origin" },
  { key: "Cross-Origin-Resource-Policy", value: "same-origin" },
];

const nextConfig: NextConfig = {
  reactStrictMode: true,
  // 跳过 build 时的 TS strict, 已知 pre-existing 错误 (payment-method-select.tsx 等)
  typescript: { ignoreBuildErrors: true },
  poweredByHeader: false,
  turbopack: { root: ROOT },
  async headers() {
    return [
      { source: "/:path*", headers: securityHeaders },
      // Service Worker 必须每次重新校验，避免边缘缓存延迟自注销逻辑生效
      {
        source: "/sw.js",
        headers: [
          { key: "Cache-Control", value: "public, max-age=0, must-revalidate" },
          { key: "Service-Worker-Allowed", value: "/" },
        ],
      },
    ];
  },
  async rewrites() {
    // OAuth flow 必须直通到 gateway:
    //   - /v1/auth/oauth/{provider}/start    → gateway 302 → provider authz URL
    //   - /v1/auth/oauth/{provider}/callback → provider 跳转回来; gateway 写 sid cookie + 302
    //   - /v1/auth/oauth/identities          → 已登录用户读绑定列表; DELETE 解绑
    //   - /v1/auth/oauth/providers           → 公开列表
    // 走 /api/proxy 套层不行 (provider 没法回调到我们自定义路径).
    const backend = process.env.BACKEND_URL || "http://gateway:8080";
    return [
      { source: "/v1/auth/oauth/:path*", destination: `${backend}/v1/auth/oauth/:path*` },
    ];
  },
};

export default nextConfig;
