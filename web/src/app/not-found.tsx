import Link from "next/link";

import { Button } from "@/components/ui/button";

export default function NotFound() {
  return (
    <main className="flex min-h-screen flex-col items-center justify-center gap-4">
      <h1 className="text-2xl font-semibold">404 — 页面不存在</h1>
      <p className="text-sm text-muted-foreground">您访问的资源未找到</p>
      <Button asChild>
        <Link href="/">回到首页</Link>
      </Button>
    </main>
  );
}
