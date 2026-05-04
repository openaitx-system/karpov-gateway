"use client";

import * as React from "react";
import { useTheme } from "next-themes";
import { Check, Monitor, Moon, Palette, Sun } from "lucide-react";

import { useColorTheme } from "@/components/theme/color-theme-provider";
import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { cn } from "@/lib/utils";
import {
  COLOR_THEMES,
  type ColorThemeId,
  type ColorThemeMeta,
} from "@/lib/themes";

type AppearanceMode = "light" | "dark" | "system";

const APPEARANCES: Array<{
  id: AppearanceMode;
  label: string;
  icon: React.ComponentType<{ className?: string }>;
}> = [
  { id: "light", label: "浅色", icon: Sun },
  { id: "dark", label: "深色", icon: Moon },
  { id: "system", label: "跟随系统", icon: Monitor },
];

export function ThemeSwitcher() {
  const { colorTheme, setColorTheme } = useColorTheme();
  const { theme, setTheme, resolvedTheme } = useTheme();
  const [mounted, setMounted] = React.useState(false);

  React.useEffect(() => {
    setMounted(true);
  }, []);

  const activeAppearance = (theme ?? "system") as AppearanceMode;
  const isDark = mounted && resolvedTheme === "dark";

  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button
          variant="ghost"
          size="icon"
          aria-label="切换主题"
          title="切换主题"
        >
          <Palette className="size-4" />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="w-60">
        <DropdownMenuLabel className="text-xs text-muted-foreground">
          配色方案
        </DropdownMenuLabel>
        {COLOR_THEMES.map((meta) => {
          const active = mounted && meta.id === colorTheme;
          return (
            <ThemeOption
              key={meta.id}
              label={meta.label}
              description={meta.description}
              active={active}
              onSelect={() => setColorTheme(meta.id as ColorThemeId)}
              leading={<ThemeSwatch meta={meta} dark={isDark} />}
            />
          );
        })}

        <DropdownMenuSeparator />

        <DropdownMenuLabel className="text-xs text-muted-foreground">
          外观模式
        </DropdownMenuLabel>
        {APPEARANCES.map(({ id, label, icon: Icon }) => {
          const active = mounted && id === activeAppearance;
          return (
            <ThemeOption
              key={id}
              label={label}
              active={active}
              onSelect={() => setTheme(id)}
              leading={
                <span className="flex size-6 items-center justify-center rounded-md border bg-background">
                  <Icon className="size-3.5 text-muted-foreground" />
                </span>
              }
            />
          );
        })}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

function ThemeSwatch({
  meta,
  dark,
}: {
  meta: ColorThemeMeta;
  dark: boolean;
}) {
  const palette = dark ? meta.dark : meta.light;
  return (
    <span
      aria-hidden
      className="flex size-6 shrink-0 overflow-hidden rounded-md border border-border shadow-sm"
      style={{ background: palette.bg }}
    >
      <span
        className="flex-1"
        style={{ background: palette.bg }}
      />
      <span
        className="flex-1"
        style={{ background: palette.accent }}
      />
      <span
        className="flex-1"
        style={{ background: palette.primary }}
      />
    </span>
  );
}

function ThemeOption({
  label,
  description,
  active,
  onSelect,
  leading,
}: {
  label: string;
  description?: string;
  active: boolean;
  onSelect: () => void;
  leading: React.ReactNode;
}) {
  return (
    <DropdownMenuItem
      onSelect={(event) => {
        event.preventDefault();
        onSelect();
      }}
      className={cn("gap-2", active && "bg-accent/60")}
    >
      {leading}
      <div className="flex min-w-0 flex-1 flex-col">
        <span className="truncate text-sm">{label}</span>
        {description ? (
          <span className="truncate text-xs text-muted-foreground">
            {description}
          </span>
        ) : null}
      </div>
      <Check
        className={cn(
          "size-4 shrink-0 transition-opacity",
          active ? "opacity-100" : "opacity-0",
        )}
      />
    </DropdownMenuItem>
  );
}
