"use client";

import * as React from "react";

import { useRouter } from "next/navigation";

import {
  ArchiveIcon,
  BookOpenIcon,
  CheckIcon,
  CodeIcon,
  CopyIcon,
  EyeIcon,
  FileTextIcon,
  Loader2Icon,
} from "lucide-react";
import { toast } from "sonner";

import { Markdown } from "@/components/markdown";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { api } from "@/lib/api";
import { copyToClipboard } from "@/lib/utils";

export function ReportTab({ taskId }: { taskId: string }) {
  const router = useRouter();
  const [report, setReport] = React.useState<string>("");
  const [loading, setLoading] = React.useState(true);
  const [copied, setCopied] = React.useState(false);
  const [filtering, setFiltering] = React.useState(false);
  const [filtered, setFiltered] = React.useState(false);
  const [view, setView] = React.useState<"rendered" | "source">("rendered");
  const pollRef = React.useRef<ReturnType<typeof setTimeout> | null>(null);

  const loadReport = React.useCallback(
    (nofilter: boolean) => {
      setLoading(true);
      api
        .reportWithStatus(taskId, nofilter)
        .then(({ text, filtering: stillFiltering, filtered: isFiltered }) => {
          setReport(text);
          setFiltered(isFiltered);
          if (stillFiltering) {
            setFiltering(true);
            pollArchived();
          }
        })
        .catch(() => setReport(""))
        .finally(() => setLoading(false));
    },
    [taskId],
  );

  React.useEffect(() => {
    loadReport(false);
    return () => {
      if (pollRef.current) clearTimeout(pollRef.current);
    };
  }, [loadReport]);

  async function copy() {
    if (!report) return;
    const ok = await copyToClipboard(report);
    if (ok) {
      setCopied(true);
      toast.success("已复制 Markdown");
      setTimeout(() => setCopied(false), 1500);
    } else {
      toast.error("复制失败，请手动选择文本复制");
    }
  }

  async function runArchive() {
    setFiltering(true);
    try {
      const { conversation_id: convId } = await api.archiveReport(taskId);
      toast.success("报告归档已启动，agent 正在处理…", {
        action: convId ? { label: "查看进度", onClick: () => router.push(`/chat?c=${convId}`) } : undefined,
      });
      pollArchived();
    } catch (e) {
      toast.error(e instanceof Error ? e.message : "归档失败");
      setFiltering(false);
    }
  }

  function pollArchived() {
    let elapsed = 0;
    const interval = 3000;
    // agent 归档 run 的墙钟上限约 10 分钟（auto agent run_seconds 默认 600）
    const maxWait = 600000;
    const poll = async () => {
      try {
        const { text, filtering: stillFiltering } = await api.reportWithStatus(taskId);
        if (!stillFiltering && text) {
          setReport(text);
          setFiltered(true);
          setFiltering(false);
          toast.success("报告归档完成，报告已更新");
          return;
        }
        elapsed += interval;
        if (elapsed >= maxWait) {
          setFiltering(false);
          toast.error("归档超时，请稍后刷新重试");
          return;
        }
        pollRef.current = setTimeout(poll, interval);
      } catch {
        elapsed += interval;
        if (elapsed < maxWait) {
          pollRef.current = setTimeout(poll, interval);
        } else {
          setFiltering(false);
          toast.error("归档超时，请稍后刷新重试");
        }
      }
    };
    pollRef.current = setTimeout(poll, interval);
  }

  async function showAll() {
    setFiltering(true);
    try {
      const text = await api.report(taskId, true);
      setReport(text);
      setFiltered(false);
    } catch (e) {
      toast.error(e instanceof Error ? e.message : "加载失败");
    } finally {
      setFiltering(false);
    }
  }

  return (
    <Card>
      <CardHeader className="flex-row items-center justify-between">
        <CardTitle className="flex items-center gap-2 text-sm">
          <FileTextIcon className="size-4" /> 渗透测试报告（Markdown）
        </CardTitle>
        <div className="flex gap-2">
          {report && (
            <>
              {filtered ? (
                <Button size="sm" variant="outline" onClick={showAll} disabled={filtering}>
                  {filtering ? <Loader2Icon className="animate-spin" /> : <EyeIcon />}
                  显示全部
                </Button>
              ) : (
                <Button size="sm" variant="outline" onClick={runArchive} disabled={filtering}>
                  {filtering ? <Loader2Icon className="animate-spin" /> : <ArchiveIcon />}
                  {filtering ? "归档中…" : "报告归档"}
                </Button>
              )}
              <Button size="sm" variant="outline" onClick={() => setView(view === "rendered" ? "source" : "rendered")}>
                {view === "rendered" ? <CodeIcon /> : <BookOpenIcon />}
                {view === "rendered" ? "源码" : "渲染"}
              </Button>
              <Button size="sm" variant="outline" onClick={copy}>
                {copied ? <CheckIcon /> : <CopyIcon />} 复制
              </Button>
            </>
          )}
        </div>
      </CardHeader>
      <CardContent>
        {loading ? (
          <div className="flex flex-col items-center justify-center gap-2 rounded-md border border-dashed py-16 text-sm text-muted-foreground">
            <FileTextIcon className="size-8 opacity-40" />
            加载中…
          </div>
        ) : report ? (
          <div className="max-h-[60vh] overflow-auto rounded-md border bg-muted/40 p-4">
            {view === "rendered" ? (
              <Markdown text={report} />
            ) : (
              <pre className="font-mono text-xs whitespace-pre-wrap">{report}</pre>
            )}
          </div>
        ) : (
          <div className="flex flex-col items-center justify-center gap-2 rounded-md border border-dashed py-16 text-sm text-muted-foreground">
            <FileTextIcon className="size-8 opacity-40" />
            暂无报告
          </div>
        )}
      </CardContent>
    </Card>
  );
}
