export const COLOR_THEME_IDS = [
  "default",
  "claude",
  "rose",
  "blue",
  "green",
  "violet",
] as const;

export type ColorThemeId = (typeof COLOR_THEME_IDS)[number];

export type ThemePalette = {
  bg: string;
  primary: string;
  accent: string;
};

export type ColorThemeMeta = {
  id: ColorThemeId;
  label: string;
  description: string;
  light: ThemePalette;
  dark: ThemePalette;
};

// 与 globals.css 中 CSS 变量保持同步；UI 预览专用，不参与运行时主题计算
export const COLOR_THEMES: ColorThemeMeta[] = [
  {
    id: "default",
    label: "默认",
    description: "Neutral 中性灰",
    light: {
      bg: "oklch(1 0 0)",
      primary: "oklch(0.205 0 0)",
      accent: "oklch(0.97 0 0)",
    },
    dark: {
      bg: "oklch(0.145 0 0)",
      primary: "oklch(0.985 0 0)",
      accent: "oklch(0.269 0 0)",
    },
  },
  {
    id: "claude",
    label: "Claude",
    description: "暖橙陶土 × 米白沙",
    light: {
      bg: "oklch(0.985 0.006 80)",
      primary: "oklch(0.625 0.155 41)",
      accent: "oklch(0.92 0.030 60)",
    },
    dark: {
      bg: "oklch(0.165 0.014 50)",
      primary: "oklch(0.72 0.145 44)",
      accent: "oklch(0.305 0.028 50)",
    },
  },
  {
    id: "rose",
    label: "玫瑰",
    description: "Rose 玫瑰红 × 微染粉白",
    light: {
      bg: "oklch(0.992 0.004 348)",
      primary: "oklch(0.645 0.246 16.439)",
      accent: "oklch(0.945 0.030 12)",
    },
    dark: {
      bg: "oklch(0.16 0.020 340)",
      primary: "oklch(0.696 0.17 17.567)",
      accent: "oklch(0.31 0.045 10)",
    },
  },
  {
    id: "blue",
    label: "靛蓝",
    description: "Blue 经典蓝 × 微染冰白",
    light: {
      bg: "oklch(0.99 0.005 252)",
      primary: "oklch(0.546 0.215 262.881)",
      accent: "oklch(0.94 0.032 252)",
    },
    dark: {
      bg: "oklch(0.16 0.022 256)",
      primary: "oklch(0.623 0.214 259.815)",
      accent: "oklch(0.31 0.060 256)",
    },
  },
  {
    id: "green",
    label: "翠绿",
    description: "Emerald 翠绿 × 微染薄荷白",
    light: {
      bg: "oklch(0.99 0.006 150)",
      primary: "oklch(0.527 0.154 150.069)",
      accent: "oklch(0.93 0.040 152)",
    },
    dark: {
      bg: "oklch(0.16 0.024 158)",
      primary: "oklch(0.696 0.17 162.48)",
      accent: "oklch(0.31 0.060 155)",
    },
  },
  {
    id: "violet",
    label: "紫罗兰",
    description: "Violet 紫罗兰 × 微染淡紫",
    light: {
      bg: "oklch(0.99 0.006 295)",
      primary: "oklch(0.541 0.281 293.009)",
      accent: "oklch(0.93 0.045 290)",
    },
    dark: {
      bg: "oklch(0.16 0.024 295)",
      primary: "oklch(0.707 0.213 293.009)",
      accent: "oklch(0.31 0.075 293)",
    },
  },
];

export const DEFAULT_COLOR_THEME: ColorThemeId = "default";
export const COLOR_THEME_STORAGE_KEY = "color-theme";
export const COLOR_THEME_ATTRIBUTE = "data-color-theme";

export function isColorThemeId(value: unknown): value is ColorThemeId {
  return (
    typeof value === "string" &&
    (COLOR_THEME_IDS as readonly string[]).includes(value)
  );
}
