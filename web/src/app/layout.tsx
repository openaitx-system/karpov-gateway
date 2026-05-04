import type { Metadata } from "next";

import "./globals.css";
import { Providers } from "@/components/providers";
import { clientEnv } from "@/lib/env/client";
import {
  COLOR_THEME_ATTRIBUTE,
  COLOR_THEME_IDS,
  COLOR_THEME_STORAGE_KEY,
  DEFAULT_COLOR_THEME,
} from "@/lib/themes";

export const metadata: Metadata = {
  title: clientEnv.NEXT_PUBLIC_APP_NAME,
  description: "Karpov Console — 用户、号池、配额、计费一体化管理",
  robots: { index: false, follow: false },
};

// SSR → 客户端 hydration 前同步主题，避免色板闪烁
const colorThemeBootstrap = `(function(){try{var k=${JSON.stringify(
  COLOR_THEME_STORAGE_KEY,
)};var a=${JSON.stringify(COLOR_THEME_ATTRIBUTE)};var d=${JSON.stringify(
  DEFAULT_COLOR_THEME,
)};var allow=${JSON.stringify(
  COLOR_THEME_IDS,
)};var v=localStorage.getItem(k);if(!v||allow.indexOf(v)===-1){v=d;}document.documentElement.setAttribute(a,v);}catch(e){}})();`;

export default function RootLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  return (
    <html lang="zh-CN" suppressHydrationWarning>
      <head>
        <script
          dangerouslySetInnerHTML={{ __html: colorThemeBootstrap }}
        />
      </head>
      <body className="min-h-screen bg-background text-foreground">
        <Providers>{children}</Providers>
      </body>
    </html>
  );
}
