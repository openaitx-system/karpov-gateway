"use client";

import * as React from "react";

import {
  COLOR_THEME_ATTRIBUTE,
  COLOR_THEME_STORAGE_KEY,
  DEFAULT_COLOR_THEME,
  isColorThemeId,
  type ColorThemeId,
} from "@/lib/themes";

type ColorThemeContextValue = {
  colorTheme: ColorThemeId;
  setColorTheme: (next: ColorThemeId) => void;
  resetColorTheme: () => void;
};

const ColorThemeContext = React.createContext<ColorThemeContextValue | null>(
  null,
);

function readInitialColorTheme(): ColorThemeId {
  if (typeof document === "undefined") return DEFAULT_COLOR_THEME;
  const attr = document.documentElement.getAttribute(COLOR_THEME_ATTRIBUTE);
  if (isColorThemeId(attr)) return attr;
  try {
    const stored = window.localStorage.getItem(COLOR_THEME_STORAGE_KEY);
    if (isColorThemeId(stored)) return stored;
  } catch {
    /* localStorage 不可用时静默 */
  }
  return DEFAULT_COLOR_THEME;
}

function applyColorTheme(next: ColorThemeId) {
  if (typeof document === "undefined") return;
  document.documentElement.setAttribute(COLOR_THEME_ATTRIBUTE, next);
}

export function ColorThemeProvider({ children }: { children: React.ReactNode }) {
  const [colorTheme, setColorThemeState] = React.useState<ColorThemeId>(
    DEFAULT_COLOR_THEME,
  );

  React.useEffect(() => {
    const initial = readInitialColorTheme();
    setColorThemeState(initial);
    applyColorTheme(initial);
  }, []);

  const setColorTheme = React.useCallback((next: ColorThemeId) => {
    setColorThemeState(next);
    applyColorTheme(next);
    try {
      window.localStorage.setItem(COLOR_THEME_STORAGE_KEY, next);
    } catch {
      /* localStorage 不可用时静默 */
    }
  }, []);

  const resetColorTheme = React.useCallback(() => {
    setColorTheme(DEFAULT_COLOR_THEME);
  }, [setColorTheme]);

  // 跨标签页同步
  React.useEffect(() => {
    const onStorage = (event: StorageEvent) => {
      if (event.key !== COLOR_THEME_STORAGE_KEY) return;
      if (isColorThemeId(event.newValue)) {
        setColorThemeState(event.newValue);
        applyColorTheme(event.newValue);
      }
    };
    window.addEventListener("storage", onStorage);
    return () => window.removeEventListener("storage", onStorage);
  }, []);

  const value = React.useMemo<ColorThemeContextValue>(
    () => ({ colorTheme, setColorTheme, resetColorTheme }),
    [colorTheme, setColorTheme, resetColorTheme],
  );

  return (
    <ColorThemeContext.Provider value={value}>
      {children}
    </ColorThemeContext.Provider>
  );
}

export function useColorTheme(): ColorThemeContextValue {
  const ctx = React.useContext(ColorThemeContext);
  if (!ctx) {
    throw new Error("useColorTheme 必须在 ColorThemeProvider 内使用");
  }
  return ctx;
}
