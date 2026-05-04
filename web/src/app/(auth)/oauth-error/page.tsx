import { Suspense } from "react";

import { Card, CardContent } from "@/components/ui/card";
import { OAuthErrorClient } from "@/components/auth/oauth-error-client";

// OAuth callback 失败回弹页:
//   /oauth-error?code=email_exists&message=...
// 前端只负责把后端 code 翻成中文提示 + 给出下一步动作.
// SSR 不读 query (跟 activate 一致): 错误信息走 client component 拿, 避免被 SSR 缓存.
export default function OAuthErrorPage() {
  return (
    <Card>
      <CardContent className="pt-6">
        <Suspense fallback={<div className="text-sm text-muted-foreground">加载中...</div>}>
          <OAuthErrorClient />
        </Suspense>
      </CardContent>
    </Card>
  );
}
