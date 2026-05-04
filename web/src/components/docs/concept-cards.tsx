import { AlertCircle, KeyRound, Gauge, Wallet } from "lucide-react";
import { Card } from "@/components/ui/card";

const ERROR_CODES: Array<{ code: number; name: string; desc: string }> = [
  { code: 0, name: "OK", desc: "请求成功" },
  { code: 400, name: "BadRequest", desc: "请求参数错误" },
  { code: 401, name: "Unauthorized", desc: "未认证或凭据失效" },
  { code: 403, name: "Forbidden", desc: "权限不足（如非管理员访问 admin 接口）" },
  { code: 404, name: "NotFound", desc: "资源不存在" },
  { code: 409, name: "Conflict", desc: "冲突（重复创建、版本号不匹配等）" },
  { code: 429, name: "RateLimited", desc: "QPS / 日 / 月配额耗尽，触发限流" },
  { code: 500, name: "Internal", desc: "服务器内部错误" },
];

export function ConceptCards() {
  return (
    <div className="grid gap-4 md:grid-cols-2">
      <Card className="p-4">
        <div className="mb-2 flex items-center gap-2 text-sm font-semibold">
          <KeyRound className="h-4 w-4" />
          认证与 API Key
        </div>
        <ul className="space-y-1 text-xs text-muted-foreground">
          <li>
            • 浏览器：登录后 <code className="text-[11px]">qmg_session</code> 与{" "}
            <code className="text-[11px]">qmg_csrf</code> Cookie 自动下发；
            非 GET 请求需在请求头里带{" "}
            <code className="text-[11px]">X-CSRF-Token</code>。
          </li>
          <li>
            • 程序化：在
            <code className="mx-1 text-[11px]">Authorization</code>
            头中携带{" "}
            <code className="text-[11px]">Bearer &lt;api_key&gt;</code>。
          </li>
          <li>
            • API Key 创建后明文 <strong>仅返回一次</strong>，请妥善保存。
          </li>
          <li>• 新创建的 Key 默认继承用户当前套餐。</li>
        </ul>
      </Card>

      <Card className="p-4">
        <div className="mb-2 flex items-center gap-2 text-sm font-semibold">
          <Gauge className="h-4 w-4" />
          限流与配额
        </div>
        <ul className="space-y-1 text-xs text-muted-foreground">
          <li>
            • <strong>QPS</strong>：按用户套餐限制每秒并发请求数，超限响应{" "}
            <code className="text-[11px]">429</code>。
          </li>
          <li>
            • <strong>每日 / 每月配额</strong>：免费版默认 100 次/日；超出后若开启 Extra Usage，
            将按千次单价从余额扣费。
          </li>
          <li>
            • 软限：达到 <code className="text-[11px]">softLimitPct</code> 时
            前端会提示，但不限流。
          </li>
        </ul>
      </Card>

      <Card className="p-4">
        <div className="mb-2 flex items-center gap-2 text-sm font-semibold">
          <Wallet className="h-4 w-4" />
          余额与超额计费
        </div>
        <ul className="space-y-1 text-xs text-muted-foreground">
          <li>
            • 通过{" "}
            <code className="text-[11px]">POST /v1/billing/balance/topup</code>{" "}
            创建充值订单，支付成功后金额自动入账。
          </li>
          <li>
            • Extra Usage 开启后，超额请求按千次单价从余额扣费；
            可设置月度上限防止意外大额扣费。
          </li>
          <li>
            • 每一次扣费都会写入余额变动明细，可在
            <code className="text-[11px]">
              /v1/billing/balance/transactions
            </code>{" "}
            查询。
          </li>
        </ul>
      </Card>

      <Card className="p-4">
        <div className="mb-2 flex items-center gap-2 text-sm font-semibold">
          <AlertCircle className="h-4 w-4" />
          错误码
        </div>
        <div className="overflow-hidden rounded-md border">
          <table className="w-full text-[11px]">
            <thead className="bg-muted/50">
              <tr>
                <th className="px-2 py-1.5 text-left font-medium">code</th>
                <th className="px-2 py-1.5 text-left font-medium">名称</th>
                <th className="px-2 py-1.5 text-left font-medium">含义</th>
              </tr>
            </thead>
            <tbody>
              {ERROR_CODES.map((e) => (
                <tr key={e.code} className="border-t">
                  <td className="px-2 py-1 font-mono">{e.code}</td>
                  <td className="px-2 py-1 font-mono">{e.name}</td>
                  <td className="px-2 py-1 text-muted-foreground">{e.desc}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </Card>
    </div>
  );
}
