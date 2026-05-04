/**
 * QQ 音乐凭证客户端工具：把多种用户输入形式统一成后端可消费的 JSON。
 *
 * 后端 `qqmusic.Credential` (gateway/internal/provider/qqmusic/credential.go)
 * 的 UnmarshalJSON 同时接受 snake_case 与 camelCase；本文件直接生成 snake_case
 * 子集，保持最小必要字段。LoginType 留空则后端按 musickey 是否以 W_X 开头自动推断。
 */

export interface QQMusicCredentialFields {
  musicid?: string | number;
  musickey?: string;
  refresh_token?: string;
  refresh_key?: string;
  access_token?: string;
  openid?: string;
  unionid?: string;
  encryptUin?: string;
  /** 1 = QQ (W_X 前缀) / 2 = WX；不填由后端推断 */
  loginType?: 1 | 2;
  /** musickey 创建时的 unix 秒；KeyExpiresIn>0 时参与过期判断 */
  musickeyCreateTime?: number;
  /** musickey 有效期（秒）；0 = 永不过期 */
  keyExpiresIn?: number;
}

export interface ParseCookieResult {
  fields: QQMusicCredentialFields;
  /** 原始 cookie 字符串中无法识别的 name=value 对，便于诊断 */
  unknown: Record<string, string>;
  /** 至少包含哪些可用字段才算"有效"——目前只要有 musickey 即有效 */
  valid: boolean;
}

/**
 * 已知 cookie 名 → Credential 字段的映射表。
 *
 * 来源：
 *   - QQ 音乐官方网页 cookie（uin / qm_keyst / psrf_qqaccess_token / ...）
 *   - 移动端登录派发的 musickey / refresh_token / refresh_key
 *
 * 同名优先级按数组靠前者优先。比如 musickey 既有 `qm_keyst` 也有 `musickey` cookie，
 * 用户两者都贴时，明确的 `musickey=` 优先于 `qm_keyst=`。
 */
const COOKIE_ALIASES: Record<keyof QQMusicCredentialFields, string[]> = {
  musicid: ["musicid", "uin", "p_uin"],
  musickey: ["musickey", "qm_keyst", "p_skey"],
  refresh_token: ["refresh_token"],
  refresh_key: ["refresh_key"],
  access_token: ["access_token", "psrf_qqaccess_token"],
  openid: ["openid", "psrf_qqopenid"],
  unionid: ["unionid"],
  encryptUin: ["encryptUin", "encrypt_uin"],
  loginType: ["loginType", "login_type"],
  musickeyCreateTime: ["musickeyCreateTime", "musickey_create_time"],
  keyExpiresIn: ["keyExpiresIn", "key_expires_in"],
};

/**
 * 解析浏览器导出的 cookie 字符串（document.cookie 形式或 curl --cookie 形式）。
 *
 * 兼容：
 *   - 分隔符：`;`、换行、tab；
 *   - 字段：`name=value` 或 `name="value"`；
 *   - 前缀：`Cookie: ` / `cookie: ` 自动剥掉；
 *   - 包裹：单/双引号自动剥；URL 解码（uin 字段常带 `%2B` 等）。
 */
export function parseCookieString(raw: string): ParseCookieResult {
  const fields: QQMusicCredentialFields = {};
  const unknown: Record<string, string> = {};
  const cleaned = raw
    .replace(/^\s*cookie\s*:\s*/i, "")
    .replace(/\\\n/g, " ")
    .trim();

  const entries = cleaned
    .split(/[;\n\t]+/)
    .map((s) => s.trim())
    .filter(Boolean);

  for (const entry of entries) {
    const eq = entry.indexOf("=");
    if (eq <= 0) continue;
    const name = entry.slice(0, eq).trim();
    let value = entry.slice(eq + 1).trim();
    if (
      (value.startsWith('"') && value.endsWith('"')) ||
      (value.startsWith("'") && value.endsWith("'"))
    ) {
      value = value.slice(1, -1);
    }
    try {
      value = decodeURIComponent(value);
    } catch {
      // value 含原始 % 不解码也行；保留原样
    }
    let matched = false;
    for (const key of Object.keys(COOKIE_ALIASES) as (keyof QQMusicCredentialFields)[]) {
      if (COOKIE_ALIASES[key].includes(name)) {
        if (fields[key] === undefined) {
          if (key === "musicid" && /^\d+$/.test(value)) {
            (fields as Record<string, unknown>)[key] = Number(value);
          } else if (key === "loginType" && /^[12]$/.test(value)) {
            (fields as Record<string, unknown>)[key] = Number(value) as 1 | 2;
          } else if (key === "musickeyCreateTime" || key === "keyExpiresIn") {
            (fields as Record<string, unknown>)[key] = Number(value);
          } else {
            (fields as Record<string, unknown>)[key] = value;
          }
        }
        matched = true;
        break;
      }
    }
    if (!matched) unknown[name] = value;
  }

  return { fields, unknown, valid: Boolean(fields.musickey) };
}

/**
 * 把表单字段（直填或 cookie 解析后）转成最终 payload 字符串（待 base64 编码上传）。
 *
 * 删除空值；保留 0 用户显式给的数值（musicid 可能就是 0 测试用，让后端拒绝）。
 */
export function buildCredentialPayload(fields: QQMusicCredentialFields): string {
  const out: Record<string, unknown> = {};
  for (const [k, v] of Object.entries(fields)) {
    if (v === undefined || v === null || v === "") continue;
    out[k] = v;
  }
  return JSON.stringify(out);
}

/**
 * 校验用户直填的 JSON 字符串能 parse 且至少含 musickey；返回错误信息（中文）或 null。
 */
export function validateCredentialJSON(raw: string): string | null {
  let obj: unknown;
  try {
    obj = JSON.parse(raw);
  } catch (e) {
    return `JSON 解析失败：${(e as Error).message}`;
  }
  if (!obj || typeof obj !== "object") return "顶层必须是 JSON 对象";
  const o = obj as Record<string, unknown>;
  const key = o.musickey ?? o.qm_keyst;
  if (!key || typeof key !== "string") return "缺少 musickey（或 qm_keyst）";
  return null;
}
