import { Card, CardContent } from "@/components/ui/card";
import { ActivateClient } from "@/components/auth/activate-client";

// 激活落地页：纯客户端逻辑（读 query.token + 调 API + 显示状态卡片）。
// SSR 不读 token 是因为 query string 是公开的，但前端调 verify 时仍要求 cookie/session=匿名，
// 我们不希望 SSR 把激活成功的状态烙到首屏 HTML 里——一旦有缓存就会泄漏 token 是否有效。
export default function ActivatePage() {
  return (
    <Card>
      <CardContent className="pt-6">
        <ActivateClient />
      </CardContent>
    </Card>
  );
}
