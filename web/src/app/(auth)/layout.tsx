import Link from "next/link";

import { clientEnv } from "@/lib/env/client";

export default function AuthLayout({ children }: { children: React.ReactNode }) {
  return (
    <main className="flex min-h-screen items-center justify-center bg-muted/40 px-4 py-10">
      <div className="w-full max-w-md">
        <Link
          href="/"
          className="mb-6 block text-center text-lg font-semibold tracking-tight"
        >
          {clientEnv.NEXT_PUBLIC_APP_NAME}
        </Link>
        {children}
      </div>
    </main>
  );
}
