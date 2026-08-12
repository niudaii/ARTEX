import localFont from "next/font/local";

const inter = localFont({
  src: "./files/inter-latin-wght-normal.woff2",
  variable: "--font-inter",
  weight: "100 900",
});

const notoSans = localFont({
  src: "./files/noto-sans-latin-wght-normal.woff2",
  variable: "--font-noto-sans",
  weight: "100 900",
});

const roboto = localFont({
  src: [
    { path: "./files/roboto-latin-400-normal.woff2", weight: "400" },
    { path: "./files/roboto-latin-500-normal.woff2", weight: "500" },
    { path: "./files/roboto-latin-700-normal.woff2", weight: "700" },
  ],
  variable: "--font-roboto",
});

const geist = localFont({
  src: "./files/geist-sans-variable.woff2",
  variable: "--font-geist",
  weight: "100 900",
});

const outfit = localFont({
  src: "./files/outfit-latin-wght-normal.woff2",
  variable: "--font-outfit",
  weight: "100 900",
});

const geistMono = localFont({
  src: "./files/geist-mono-variable.woff2",
  variable: "--font-geist-mono",
  weight: "100 900",
});

const dmSans = localFont({
  src: "./files/dm-sans-latin-wght-normal.woff2",
  variable: "--font-dm-sans",
  weight: "100 900",
});

const nunitoSans = localFont({
  src: "./files/nunito-sans-latin-wght-normal.woff2",
  variable: "--font-nunito-sans",
  weight: "100 900",
});

const figtree = localFont({
  src: "./files/figtree-latin-wght-normal.woff2",
  variable: "--font-figtree",
  weight: "100 900",
});

const raleway = localFont({
  src: "./files/raleway-latin-wght-normal.woff2",
  variable: "--font-raleway",
  weight: "100 900",
});

const publicSans = localFont({
  src: "./files/public-sans-latin-wght-normal.woff2",
  variable: "--font-public-sans",
  weight: "100 900",
});

const jetBrainsMono = localFont({
  src: "./files/jetbrains-mono-latin-wght-normal.woff2",
  variable: "--font-jetbrains-mono",
  weight: "100 900",
});

const notoSerif = localFont({
  src: [
    { path: "./files/noto-serif-latin-400-normal.woff2", weight: "400" },
    { path: "./files/noto-serif-latin-500-normal.woff2", weight: "500" },
    { path: "./files/noto-serif-latin-700-normal.woff2", weight: "700" },
  ],
  variable: "--font-noto-serif",
});

const robotoSlab = localFont({
  src: [
    { path: "./files/roboto-slab-latin-400-normal.woff2", weight: "400" },
    { path: "./files/roboto-slab-latin-500-normal.woff2", weight: "500" },
    { path: "./files/roboto-slab-latin-700-normal.woff2", weight: "700" },
  ],
  variable: "--font-roboto-slab",
});

const merriweather = localFont({
  src: [
    { path: "./files/merriweather-latin-400-normal.woff2", weight: "400" },
    { path: "./files/merriweather-latin-700-normal.woff2", weight: "700" },
  ],
  variable: "--font-merriweather",
});

const lora = localFont({
  src: "./files/lora-latin-wght-normal.woff2",
  variable: "--font-lora",
  weight: "100 900",
});

const playfairDisplay = localFont({
  src: "./files/playfair-display-latin-wght-normal.woff2",
  variable: "--font-playfair-display",
  weight: "100 900",
});

const geistPixelSquare = localFont({
  src: "./files/geist-pixel-square.woff2",
  variable: "--font-geist-pixel-square",
  weight: "500",
});

export const fontRegistry = {
  geist: {
    label: "Geist",
    font: geist,
  },
  inter: {
    label: "Inter",
    font: inter,
  },
  notoSans: {
    label: "Noto Sans",
    font: notoSans,
  },
  nunitoSans: {
    label: "Nunito Sans",
    font: nunitoSans,
  },
  figtree: {
    label: "Figtree",
    font: figtree,
  },
  roboto: {
    label: "Roboto",
    font: roboto,
  },
  raleway: {
    label: "Raleway",
    font: raleway,
  },
  dmSans: {
    label: "DM Sans",
    font: dmSans,
  },
  publicSans: {
    label: "Public Sans",
    font: publicSans,
  },
  outfit: {
    label: "Outfit",
    font: outfit,
  },
  geistMono: {
    label: "Geist Mono",
    font: geistMono,
  },
  geistPixelSquare: {
    label: "Geist Pixel Square",
    font: geistPixelSquare,
  },
  jetBrainsMono: {
    label: "JetBrains Mono",
    font: jetBrainsMono,
  },
  notoSerif: {
    label: "Noto Serif",
    font: notoSerif,
  },
  robotoSlab: {
    label: "Roboto Slab",
    font: robotoSlab,
  },
  merriweather: {
    label: "Merriweather",
    font: merriweather,
  },
  lora: {
    label: "Lora",
    font: lora,
  },
  playfairDisplay: {
    label: "Playfair Display",
    font: playfairDisplay,
  },
} as const;

export type FontKey = keyof typeof fontRegistry;

export const fontVars = (Object.values(fontRegistry) as Array<(typeof fontRegistry)[FontKey]>)
  .map((f) => f.font.variable)
  .join(" ");

export const fontOptions = (Object.entries(fontRegistry) as Array<[FontKey, (typeof fontRegistry)[FontKey]]>).map(
  ([key, f]) => ({
    key,
    label: f.label,
    variable: f.font.variable,
  }),
);
