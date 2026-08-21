

<!-- codemap:start -->
## Codemap MCP

**[Important!!!] Whether in the parent agent or in a subagent, always use codemap MCP tools instead of Grep/Read tools. Only fall back to Grep/Read when codemap is unavailable.**

### Tools

| Tool | Use case |
|------|----------|
| `search_code` | Fuzzy keyword or concept search (FTS + vector). For exact name lookup, use find_symbol. |
| `find_symbol` | Exact/prefix/suffix name lookup. Use when you know the symbol name. |
| `get_symbol_detail` | Get a symbol's location, signature, docs, and body. |
| `get_call_chain` | Trace upstream callers and/or downstream callees. |
| `get_type_hierarchy` | Class/interface parent and child hierarchy. |
| `get_dependencies` | Find all symbols that reference a given symbol. |
| `get_graph_stats` | Graph statistics: file/symbol/edge counts. |
| `search_knowledge` | Business rules and architecture docs from codeindex. |
| `query_cypher` | Count/aggregate structural queries: "how many functions in X?", "which class has most methods?", duplicate names, cross-cutting analysis. |


**Name resolution:** Pass `symbol_name` — simple name (`parse_config`) or qualified (`ClassName.method`). No module prefix needed. **Symbol ID:** `filepath:kind:scopedName` (e.g. `player.py:method:Player.attack`, `models.py:class:Outer.Inner`)

**Slash commands:** `/codemap-exploring`, `/codemap-debugging`, `/codemap-impact-analysis`

### Rules

- **After `get_symbol_detail`: edit immediately.** Do NOT re-Read the same file.
- **Use `search_code` first**, not broad `find_symbol` prefix queries.
- **Use batch queries:** `search_code({matches: ["A", "B"]})`, `find_symbol({symbol_name: ["X", "Y"]})`.
- **For obvious single-file bugs: skip codemap.** Error → Read → Edit.
- **Counting or aggregation questions** (how many, which has most, rank by): use `query_cypher`, NOT read/grep.
- **If a subagent is needed, use `general`, not `explore`** (`explore` does not support MCP and cannot call codemap tools).

<!-- codemap:end -->

## Development Style

- Go: run `gofmt` on every changed `.go` file; keep functions small and prefer table-driven tests.
- Go validation: run `go build ./...`, `go vet ./...`, and focused `go test` packages before handing off.
- Frontend: run `pnpm --dir web exec biome check --write --diagnostic-level=error` on changed files.
- Frontend formatting: 2-space indentation, double quotes, semicolons, trailing commas, and 120-column lines.
- TypeScript: prefer explicit domain types from `web/src/lib/types.ts`; avoid introducing new `any`.
- React: prefer stable callback dependencies; do not add new dependency arrays used only as change triggers.
- React lists: use stable domain IDs as keys; index keys are allowed only for static, non-reorderable legacy data.
- Error handling: empty catch blocks must explain why the error is intentionally ignored.
- Accessibility: new interactive elements need explicit button types and keyboard handlers; new images should use `next/image`.
- Validation: run `pnpm --dir web exec tsc --noEmit`; use `pnpm --dir web run check:all` when auditing legacy warnings.
