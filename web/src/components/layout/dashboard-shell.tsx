"use client";

import * as React from "react";
import Link from "next/link";
import { usePathname, useRouter } from "next/navigation";
import {
  BookOpen,
  CreditCard,
  KeyRound,
  LayoutDashboard,
  LogOut,
  Server,
  Settings,
  ShieldCheck,
  TerminalSquare,
  UserCircle,
  Users,
  Wallet,
  Zap,
} from "lucide-react";
import { toast } from "sonner";

import { ThemeSwitcher } from "@/components/theme/theme-switcher";
import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Separator } from "@/components/ui/separator";
import { authApi } from "@/lib/api/auth";
import { ApiError } from "@/lib/api/client";
import { isAdmin } from "@/lib/auth/rbac";
import { clientEnv } from "@/lib/env/client";
import { cn } from "@/lib/utils";
import type { User } from "@/types/api";

type NavItem = {
  label: string;
  href: string;
  icon: React.ComponentType<{ className?: string }>;
  adminOnly?: boolean;
};

const NAV: NavItem[] = [
  { label: "仪表盘", href: "/dashboard", icon: LayoutDashboard },
  { label: "API Keys", href: "/api-keys", icon: KeyRound },
  { label: "操练场", href: "/playground", icon: TerminalSquare },
  { label: "API 文档", href: "/docs", icon: BookOpen },
  { label: "套餐与计费", href: "/billing", icon: CreditCard },
  { label: "账户余额", href: "/balance", icon: Wallet },
  { label: "超额使用", href: "/extra-usage", icon: Zap },
  { label: "设置", href: "/settings", icon: Settings },
  { label: "用户管理", href: "/admin/users", icon: Users, adminOnly: true },
  { label: "号池管理", href: "/admin/pool", icon: Server, adminOnly: true },
  { label: "系统设置", href: "/admin/settings", icon: ShieldCheck, adminOnly: true },
];

export function DashboardShell({
  user,
  children,
}: {
  user: User;
  children: React.ReactNode;
}) {
  const pathname = usePathname();
  const router = useRouter();
  const admin = isAdmin(user.role);

  const onLogout = async () => {
    try {
      await authApi.logout();
    } catch (err) {
      if (!(err instanceof ApiError)) {
        toast.error("登出失败");
        return;
      }
    }
    toast.success("已登出");
    router.replace("/login");
    router.refresh();
  };

  return (
    <div className="grid min-h-screen grid-cols-[16rem_1fr]">
      <aside className="border-r bg-muted/30">
        <div className="flex h-14 items-center px-6 font-semibold tracking-tight">
          {clientEnv.NEXT_PUBLIC_APP_NAME}
        </div>
        <Separator />
        <nav className="flex flex-col gap-1 p-3">
          {NAV.filter((item) => !item.adminOnly || admin).map((item) => {
            const active = pathname?.startsWith(item.href);
            const Icon = item.icon;
            return (
              <Link
                key={item.href}
                href={item.href}
                className={cn(
                  "flex items-center gap-2 rounded-md px-3 py-2 text-sm",
                  active
                    ? "bg-primary text-primary-foreground"
                    : "text-muted-foreground hover:bg-muted hover:text-foreground",
                )}
              >
                <Icon className="size-4" />
                {item.label}
                {item.adminOnly && admin && (
                  <ShieldCheck className="ml-auto size-3.5 opacity-70" />
                )}
              </Link>
            );
          })}
        </nav>
      </aside>
      <div className="flex flex-col">
        <header className="flex h-14 items-center justify-between border-b px-6">
          <div className="text-sm text-muted-foreground">
            欢迎，{user.email}
          </div>
          <div className="flex items-center gap-1">
            <ThemeSwitcher />
            <DropdownMenu>
              <DropdownMenuTrigger asChild>
                <Button variant="ghost" size="sm" className="gap-2">
                  <UserCircle className="size-4" />
                  {user.role}
                </Button>
              </DropdownMenuTrigger>
              <DropdownMenuContent align="end" className="w-48">
                <DropdownMenuLabel>{user.email}</DropdownMenuLabel>
                <DropdownMenuSeparator />
                <DropdownMenuItem asChild>
                  <Link href="/settings">账户设置</Link>
                </DropdownMenuItem>
                <DropdownMenuItem onClick={onLogout}>
                  <LogOut className="size-4" />
                  登出
                </DropdownMenuItem>
              </DropdownMenuContent>
            </DropdownMenu>
          </div>
        </header>
        <main className="flex-1 overflow-y-auto p-6">{children}</main>
      </div>
    </div>
  );
}
