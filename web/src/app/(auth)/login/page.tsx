import { redirect } from "next/navigation";

import { LoginForm } from "@/components/auth/login-form";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { getCurrentUser } from "@/lib/auth/session";

interface PageProps {
  searchParams: Promise<{ next?: string }>;
}

export default async function LoginPage({ searchParams }: PageProps) {
  const user = await getCurrentUser();
  const { next } = await searchParams;
  if (user) {
    redirect(safeNext(next) ?? "/dashboard");
  }
  return (
    <Card>
      <CardHeader>
        <CardTitle>登录</CardTitle>
        <CardDescription>使用邮箱与密码登录控制台</CardDescription>
      </CardHeader>
      <CardContent>
        <LoginForm next={safeNext(next) ?? "/dashboard"} />
      </CardContent>
    </Card>
  );
}

/** 仅允许跳转到本站内部路径，防开放重定向。 */
function safeNext(next: string | undefined): string | null {
  if (!next) return null;
  if (!next.startsWith("/")) return null;
  if (next.startsWith("//")) return null; // protocol-relative
  if (next.startsWith("/api/")) return null;
  return next;
}
