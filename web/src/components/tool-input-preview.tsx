"use client";

import { ChevronDownIcon, ChevronRightIcon } from "lucide-react";

import { TableCell, TableRow } from "@/components/ui/table";

const PREVIEW_LIMIT = 120;

// ToolInputPreview renders a tool call's input as a truncated one-line summary
// with a chevron affordance. Clicking toggles the paired ToolInputDetailRow,
// which shows the full pretty-printed JSON — long payloads (Bash commands, file
// writes, …) can no longer only be glimpsed through a fixed-width tooltip.
export function ToolInputPreview({
  input,
  expanded,
  onToggle,
}: {
  input: Record<string, unknown>;
  expanded: boolean;
  onToggle: () => void;
}) {
  const s = JSON.stringify(input);
  const short = s.length > PREVIEW_LIMIT ? `${s.slice(0, PREVIEW_LIMIT)}…` : s;
  return (
    <button
      type="button"
      onClick={onToggle}
      aria-expanded={expanded}
      title={expanded ? "收起" : "展开完整参数"}
      className="flex w-full items-center gap-1 text-left"
    >
      {expanded ? (
        <ChevronDownIcon className="size-3 shrink-0 text-muted-foreground" />
      ) : (
        <ChevronRightIcon className="size-3 shrink-0 text-muted-foreground" />
      )}
      <code className="block min-w-0 flex-1 truncate font-mono text-[11px] text-muted-foreground">{short}</code>
    </button>
  );
}

// ToolInputDetailRow is the expanded row rendered directly below a table row:
// the full input JSON in a scrollable block (mirrors ToolBlock's expand style).
export function ToolInputDetailRow({ colSpan, input }: { colSpan: number; input: Record<string, unknown> }) {
  return (
    <TableRow className="bg-muted/30 hover:bg-muted/30">
      <TableCell colSpan={colSpan} className="p-0">
        <pre className="max-h-72 overflow-auto whitespace-pre-wrap break-all p-3 font-mono text-[11px] leading-relaxed">
          {JSON.stringify(input, null, 2)}
        </pre>
      </TableCell>
    </TableRow>
  );
}
