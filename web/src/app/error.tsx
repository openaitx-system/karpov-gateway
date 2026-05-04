"use client";

import * as React from "react";
import Link from "next/link";

import { Button } from "@/components/ui/button";

export default function GlobalError({
  error,
  reset,
}: {
  error: Error & { digest?: string };
  reset: () => void;
}) {
  React.useEffect(() => {
    console.error("RSC error", error);
  }, [error]);
  return (
    <main className="flex min-h-screen flex-col items-center justify-center gap-4 px-6 text-center">
      <h1 className="text-2xl font-semibold">出错了</h1>
      <p className="max-w-md text-sm text-muted-foreground">
        服务暂时不可用，请稍后再试。如果问题持续，请联系管理员并提供下方追踪 ID。
      </p>
      {error.digest && (
        <code className="rounded bg-muted px-2 py-1 text-xs">{error.digest}</code>
      )}
      <div className="flex gap-2">
        <Button onClick={reset}>重试</Button>
        <Button variant="outline" asChild>
          <Link href="/">回首页</Link>
        </Button>
      </div>
    </main>
  );
}
