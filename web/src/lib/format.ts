// Shared formatting helpers. Extracted from per-page copies to keep behavior
// consistent across the app. Prefer importing from here over redefining locally.

export function fmtTime(input: string | number | null | undefined): string {
  if (input === null || input === undefined || input === "") return "—";
  const d = new Date(input);
  if (isNaN(d.getTime())) return "—";
  return d.toLocaleString("zh-CN", {
    year: "2-digit",
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
  });
}

export function fmtBytes(n: number | null | undefined): string {
  if (!n || !Number.isFinite(n) || n <= 0) return "—";
  const u = ["B", "KB", "MB"];
  const i = Math.min(Math.floor(Math.log(n) / Math.log(1024)), u.length - 1);
  return `${i === 0 ? n : (n / 1024 ** i).toFixed(1)} ${u[i]}`;
}

export function fmtTokens(n: number): string {
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(n >= 10_000_000 ? 0 : 1)}M`;
  if (n >= 1000) return `${(n / 1000).toFixed(n >= 10000 ? 0 : 1)}k`;
  return String(n);
}

export function statusTone(code: number): string {
  if (code >= 500) return "text-red-500";
  if (code >= 400) return "text-amber-500";
  if (code >= 300) return "text-blue-500";
  if (code >= 200) return "text-emerald-500";
  return "text-muted-foreground";
}
