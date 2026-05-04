import { PlaygroundPanel } from "@/components/playground/playground-panel";
import { requireUser } from "@/lib/auth/session";

export const dynamic = "force-dynamic";

export default async function PlaygroundPage() {
  await requireUser();
  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-semibold tracking-tight">API 操练场</h1>
        <p className="text-sm text-muted-foreground">
          选择 API Key，可视化调用各 API 端点并查看响应
        </p>
      </div>
      <PlaygroundPanel />
    </div>
  );
}
