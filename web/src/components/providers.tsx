"use client";

import * as React from "react";
import { ThemeProvider } from "next-themes";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";

import { ColorThemeProvider } from "@/components/theme/color-theme-provider";
import { RuntimeConfigProvider } from "@/components/runtime-config-provider";
import { Toaster } from "@/components/ui/toaster";

export function Providers({ children }: { children: React.ReactNode }) {
  const [client] = React.useState(
    () =>
      new QueryClient({
        defaultOptions: {
          queries: {
            staleTime: 30_000,
            refetchOnWindowFocus: false,
            retry: (count, err) => {
              const status = (err as { status?: number } | null)?.status;
              if (status && status >= 400 && status < 500) return false;
              return count < 2;
            },
          },
          mutations: { retry: 0 },
        },
      }),
  );
  return (
    <ThemeProvider attribute="class" defaultTheme="system" enableSystem>
      <ColorThemeProvider>
        <QueryClientProvider client={client}>
          <RuntimeConfigProvider>{children}</RuntimeConfigProvider>
          <Toaster />
        </QueryClientProvider>
      </ColorThemeProvider>
    </ThemeProvider>
  );
}
