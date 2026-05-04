/**
 * 日期/时间格式化工具。
 *
 * 后端 `GET /v1/config/runtime` 返回部署侧配置的全局时区（IANA 名）；
 * 前端把它存到模块级缓存里，所有面板共享。
 *
 * Fallback 链：
 *   后端配置 timezone（"Asia/Shanghai" 等）
 *   → "Local"（后端跟随系统）→ 浏览器 TZ（Intl.DateTimeFormat 默认）
 *   → 永远不抛错（无效 zone 自动回退到浏览器 TZ）。
 *
 * 设计取舍：
 *   - 不依赖 dayjs / date-fns，省一份 bundle。
 *   - 对外暴露 setRuntimeTimeZone()，让 RuntimeConfigProvider 在拉到配置后注入；
 *     拉取失败也不阻塞渲染，formatDateTime 就走浏览器 TZ。
 */

const CHINESE_LOCALE = "zh-CN";

let activeTimeZone: string | undefined;
let resolvedTimeZone: string | undefined;

/** 设置后端下发的全局时区。空 / "Local" 视为"跟随系统"（不绑定具体 IANA）。 */
export function setRuntimeTimeZone(tz: string | null | undefined): void {
  if (!tz || tz === "Local" || tz === "system") {
    activeTimeZone = undefined;
    resolvedTimeZone = undefined;
    return;
  }
  activeTimeZone = tz;
  resolvedTimeZone = undefined; // 触发下次 formatDateTime 时重新校验
}

/** 当前生效的 IANA 时区名；undefined 表示跟随浏览器 TZ。 */
export function getRuntimeTimeZone(): string | undefined {
  return activeTimeZone;
}

/**
 * 校验后端给的 timezone 在浏览器 Intl 里能识别；不能识别就回退到 undefined。
 * 缓存一次结果，避免每次 format 都触发 try/catch 开销。
 */
function effectiveTimeZone(): string | undefined {
  if (!activeTimeZone) return undefined;
  if (resolvedTimeZone === activeTimeZone) return resolvedTimeZone;
  try {
    new Intl.DateTimeFormat(CHINESE_LOCALE, { timeZone: activeTimeZone });
    resolvedTimeZone = activeTimeZone;
    return resolvedTimeZone;
  } catch {
    // 浏览器不识别（例如 "Local"）→ 回退到默认浏览器 TZ
    resolvedTimeZone = undefined;
    return undefined;
  }
}

/** 把任意 Date | ISO 字符串 | undefined 收敛成可用 Date；非法值返回 null。 */
function toDate(input: Date | string | number | null | undefined): Date | null {
  if (input == null || input === "") return null;
  const d = input instanceof Date ? input : new Date(input);
  if (Number.isNaN(d.getTime())) return null;
  return d;
}

export interface FormatOptions {
  /** 不传则使用配置时区或浏览器默认；显式传可临时覆盖。 */
  timeZone?: string;
  /** Intl 选项的额外覆盖。 */
  options?: Intl.DateTimeFormatOptions;
  /** 输入为空 / 非法日期时返回的占位符；默认 "—"。 */
  fallback?: string;
}

/**
 * 默认格式：YYYY/MM/DD HH:mm:ss（zh-CN locale，24 小时制）。
 *
 * 例：formatDateTime("2026-05-04T14:30:00Z") → "2026/05/04 22:30:00"（Asia/Shanghai 下）。
 */
export function formatDateTime(
  input: Date | string | number | null | undefined,
  opts: FormatOptions = {},
): string {
  const d = toDate(input);
  if (!d) return opts.fallback ?? "—";
  const tz = opts.timeZone ?? effectiveTimeZone();
  return new Intl.DateTimeFormat(CHINESE_LOCALE, {
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
    hour12: false,
    timeZone: tz,
    ...opts.options,
  }).format(d);
}

/** 仅日期：YYYY/MM/DD（zh-CN locale）。 */
export function formatDate(
  input: Date | string | number | null | undefined,
  opts: FormatOptions = {},
): string {
  const d = toDate(input);
  if (!d) return opts.fallback ?? "—";
  const tz = opts.timeZone ?? effectiveTimeZone();
  return new Intl.DateTimeFormat(CHINESE_LOCALE, {
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
    timeZone: tz,
    ...opts.options,
  }).format(d);
}

/** 仅时间：HH:mm:ss。 */
export function formatTime(
  input: Date | string | number | null | undefined,
  opts: FormatOptions = {},
): string {
  const d = toDate(input);
  if (!d) return opts.fallback ?? "—";
  const tz = opts.timeZone ?? effectiveTimeZone();
  return new Intl.DateTimeFormat(CHINESE_LOCALE, {
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
    hour12: false,
    timeZone: tz,
    ...opts.options,
  }).format(d);
}

/**
 * 相对时间显示："刚刚" / "X 分钟前" / "X 小时前" / "X 天前" / 绝对日期。
 * 30 天以上会回落到 formatDate。
 */
export function formatRelative(
  input: Date | string | number | null | undefined,
  opts: FormatOptions = {},
): string {
  const d = toDate(input);
  if (!d) return opts.fallback ?? "—";
  const diffMs = Date.now() - d.getTime();
  const seconds = Math.floor(diffMs / 1000);
  if (seconds < 60) return "刚刚";
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return `${minutes} 分钟前`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours} 小时前`;
  const days = Math.floor(hours / 24);
  if (days < 30) return `${days} 天前`;
  return formatDate(d, opts);
}
