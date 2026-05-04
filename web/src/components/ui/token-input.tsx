"use client";

import * as React from "react";
import { X } from "lucide-react";

import { cn } from "@/lib/utils";

interface TokenInputProps {
  value: string[];
  onChange: (next: string[]) => void;
  placeholder?: string;
  /** 已知值列表，作为下拉建议；空表示不显示建议。 */
  suggestions?: readonly string[];
  /** 单个 token 校验。返回 string 表示错误信息，undefined 表示通过。 */
  validate?: (token: string) => string | undefined;
  /** 触发添加的分隔符（默认 Enter / Tab / 空格 / 逗号）。 */
  separators?: RegExp;
  /** 上限；超过后再 add 会被拒绝并 toast 错误。 */
  max?: number;
  className?: string;
  inputId?: string;
  disabled?: boolean;
}

/**
 * Chip-style token input —— 用 Enter / 逗号 / Tab 添加，点 × 或 Backspace 删除。
 *
 * 数据模型：保持 string[]，调用方自行处理"逗号分隔字符串"⇌"数组"的转换。
 *
 * 不依赖 react-hook-form；用 Controller 包一下即可在 form 中使用。
 */
export function TokenInput({
  value,
  onChange,
  placeholder,
  suggestions,
  validate,
  separators = /[,\s]+/,
  max,
  className,
  inputId,
  disabled,
}: TokenInputProps) {
  const [draft, setDraft] = React.useState("");
  const [error, setError] = React.useState<string | null>(null);
  const [showSugs, setShowSugs] = React.useState(false);
  const inputRef = React.useRef<HTMLInputElement>(null);

  const tryAdd = React.useCallback(
    (raw: string) => {
      const tokens = raw
        .split(separators)
        .map((s) => s.trim())
        .filter(Boolean);
      if (tokens.length === 0) {
        setDraft("");
        return;
      }
      const next = [...value];
      for (const t of tokens) {
        if (max !== undefined && next.length >= max) {
          setError(`最多 ${max} 个`);
          continue;
        }
        if (next.includes(t)) continue; // 去重
        if (validate) {
          const e = validate(t);
          if (e) {
            setError(e);
            continue;
          }
        }
        next.push(t);
      }
      if (next.length !== value.length) {
        onChange(next);
      }
      setDraft("");
    },
    [value, onChange, separators, max, validate],
  );

  const onKeyDown = (e: React.KeyboardEvent<HTMLInputElement>) => {
    if (disabled) return;
    setError(null);
    if (e.key === "Enter" || e.key === ",") {
      e.preventDefault();
      tryAdd(draft);
      return;
    }
    if (e.key === "Backspace" && draft === "" && value.length > 0) {
      onChange(value.slice(0, -1));
    }
  };

  const onPaste = (e: React.ClipboardEvent<HTMLInputElement>) => {
    if (disabled) return;
    const text = e.clipboardData.getData("text");
    if (separators.test(text)) {
      e.preventDefault();
      tryAdd(text);
    }
  };

  const filteredSugs = React.useMemo(() => {
    if (!suggestions || suggestions.length === 0) return [];
    const q = draft.trim().toLowerCase();
    return suggestions.filter(
      (s) => !value.includes(s) && (!q || s.toLowerCase().includes(q)),
    );
  }, [suggestions, value, draft]);

  return (
    <div className={cn("space-y-1", className)}>
      <div
        className={cn(
          "flex flex-wrap items-center gap-1 rounded-md border border-input bg-background px-2 py-1.5 text-sm shadow-sm transition-colors",
          "focus-within:ring-1 focus-within:ring-ring focus-within:border-ring",
          disabled && "opacity-50 cursor-not-allowed",
          error && "border-destructive focus-within:ring-destructive",
        )}
        onClick={() => inputRef.current?.focus()}
        role="presentation"
      >
        {value.map((tok) => (
          <span
            key={tok}
            className="inline-flex items-center gap-1 rounded-sm bg-muted px-1.5 py-0.5 text-xs font-mono"
          >
            {tok}
            {!disabled && (
              <button
                type="button"
                aria-label={`移除 ${tok}`}
                className="rounded-sm text-muted-foreground hover:text-foreground"
                onClick={(e) => {
                  e.stopPropagation();
                  onChange(value.filter((v) => v !== tok));
                }}
              >
                <X className="size-3" />
              </button>
            )}
          </span>
        ))}
        <input
          ref={inputRef}
          id={inputId}
          type="text"
          value={draft}
          onChange={(e) => {
            setDraft(e.target.value);
            setError(null);
            setShowSugs(true);
          }}
          onKeyDown={onKeyDown}
          onPaste={onPaste}
          onFocus={() => setShowSugs(true)}
          onBlur={() => {
            // 离焦时把剩余 draft 提交
            if (draft) tryAdd(draft);
            // 下拉延迟收，避免 click 命中前消失
            setTimeout(() => setShowSugs(false), 150);
          }}
          placeholder={value.length === 0 ? placeholder : ""}
          disabled={disabled}
          className="flex-1 min-w-[80px] bg-transparent outline-none placeholder:text-muted-foreground disabled:cursor-not-allowed text-sm"
        />
      </div>
      {showSugs && filteredSugs.length > 0 && (
        <div className="rounded-md border bg-popover shadow-md max-h-40 overflow-y-auto">
          {filteredSugs.map((s) => (
            <button
              key={s}
              type="button"
              className="block w-full text-left px-2 py-1 text-xs font-mono hover:bg-accent hover:text-accent-foreground"
              // 用 onMouseDown 抢在 input blur 之前
              onMouseDown={(e) => {
                e.preventDefault();
                tryAdd(s);
              }}
            >
              {s}
            </button>
          ))}
        </div>
      )}
      {error && <p className="text-xs text-destructive">{error}</p>}
    </div>
  );
}
