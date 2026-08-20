import packageJson from "../../package.json";

const currentYear = new Date().getFullYear();

export const APP_CONFIG = {
  name: "ATX",
  version: packageJson.version,
  copyright: `© ${currentYear}, ATX.`,
  meta: {
    title: "ATX — 自动化渗透平台",
    description: "自动化渗透平台",
  },
};
