"use client";

import * as React from "react";
import { useTheme } from "next-themes";
import { Toaster as Sonner, type ToasterProps } from "sonner";

export function Toaster(props: ToasterProps) {
  const { theme = "system" } = useTheme();

  return (
    <Sonner
      theme={theme as ToasterProps["theme"]}
      position="top-right"
      className="toaster group"
      style={
        {
          "--normal-bg": "var(--popover)",
          "--normal-text": "var(--popover-foreground)",
          "--normal-border": "var(--border)",
        } as React.CSSProperties
      }
      toastOptions={{
        classNames: {
          // 只接管视觉皮肤（背景/边框/圆角/阴影），不动 Sonner 自身的 padding/display/gap，
          // 避免破坏 [data-icon]/[data-content]/[data-close-button] 的内部 flex 布局。
          toast:
            "group toast group-[.toaster]:bg-popover group-[.toaster]:text-popover-foreground group-[.toaster]:border group-[.toaster]:border-border group-[.toaster]:shadow-lg group-[.toaster]:rounded-lg",
          title:
            "group-[.toast]:text-sm group-[.toast]:font-semibold group-[.toast]:leading-tight",
          description:
            "group-[.toast]:text-xs group-[.toast]:text-muted-foreground group-[.toast]:leading-snug",
          actionButton:
            "group-[.toast]:bg-primary group-[.toast]:text-primary-foreground group-[.toast]:hover:bg-primary/90 group-[.toast]:rounded-md group-[.toast]:px-3 group-[.toast]:h-8 group-[.toast]:text-xs group-[.toast]:font-medium group-[.toast]:transition-colors",
          cancelButton:
            "group-[.toast]:bg-muted group-[.toast]:text-muted-foreground group-[.toast]:hover:bg-muted/80 group-[.toast]:rounded-md group-[.toast]:px-3 group-[.toast]:h-8 group-[.toast]:text-xs group-[.toast]:font-medium group-[.toast]:transition-colors",
          // closeButton 默认贴在 toast 左上角（top-right 位置时语义反直觉，且会盖住标题）。
          // 用 !important 把它挪到右上：left-auto + right=-8px 让按钮一半浮在外侧，与 sonner 默认风格一致。
          closeButton:
            "!left-auto !-right-2 !-top-2 group-[.toast]:bg-background group-[.toast]:border group-[.toast]:border-border group-[.toast]:text-muted-foreground group-[.toast]:hover:bg-accent group-[.toast]:hover:text-accent-foreground group-[.toast]:rounded-md group-[.toast]:transition-colors",
          icon: "group-[.toast]:size-4 group-[.toast]:shrink-0",
        },
      }}
      closeButton
      {...props}
    />
  );
}
