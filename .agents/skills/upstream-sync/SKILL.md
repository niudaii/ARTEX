---
name: upstream-sync
description: >
  同步 upstream 远程仓库的新提交到本地仓库。分析上游提交类型（bug 修复、新增 feature、
  refactor、性能优化），人工确认后合并，本地为主、只合并必要代码。
  触发词：同步 upstream、sync upstream、合并上游、拉取上游更新、upstream sync、
  同步上游仓库、更新上游代码。
  排除边界：普通的 git pull origin、单分支开发流程、非 upstream/origin 双 remote 场景不调用。
---

# Upstream Sync

将 upstream 远程仓库的新提交同步到本地，按提交类型分类分析，人工确认后合并。核心原则：
**本地为主**——本地改动优先，只合并对本地有价值的上游代码，不盲目全量合并。

## 前置条件

仓库已配置 `upstream` 和 `origin` 两个 remote：

```
origin    ← 自己的 fork（推送用）
upstream  ← 上游原始仓库（拉取用）
```

用 `git remote -v` 确认。如果没有 `upstream`，提示用户添加：
`git remote add upstream <上游仓库 URL>`

## 工作流

### Step 1: 准备同步分支

从 `main`（或当前主开发分支）创建专用同步分支，命名 `sync/upstream-YYYYMMDD`：

```bash
git fetch upstream
git checkout main && git pull origin main
git checkout -b sync/upstream-$(date +%Y%m%d)
```

同步分支隔离合并风险，不直接在 `main` 上操作。

### Step 2: 获取并分析上游提交

列出本地与上游之间的新提交：

```bash
git log main..upstream/main --oneline --no-decorate
```

逐条分析每个提交，按以下分类标注：

| 类型 | 识别关键词 | 合并策略 |
|------|-----------|---------|
| 🐛 Bug 修复 | `fix:`, `bugfix`, `patch`, `hotfix` | 优先合并，除非本地已用不同方式修复 |
| ✨ 新功能 | `feat:`, `add`, `support`, `implement` | 需确认——看是否与本地已有功能重叠 |
| ♻️ 重构 | `refactor:`, `rename`, `move`, `extract` | 谨慎合并——可能改变本地依赖的结构 |
| ⚡ 性能优化 | `perf:`, `optimize`, `speed`, `cache` | 一般直接合并 |
| 📝 文档/杂项 | `docs:`, `chore:`, `tweak`, `style` | 低优先级，按需合并 |
| 🔧 配置变更 | `config`, `CI`, `workflow` | 需确认——可能覆盖本地 CI 配置 |

分析每个提交时用 `git show <hash> --stat` 看影响范围，必要时 `git show <hash>` 看具体 diff。
用 codemap `search_code` 或 `get_dependencies` 评估改动是否触及本地已修改或自定义的模块。

### Step 3: 分类报告 & 人工确认

生成分类汇总表，呈现给用户：

```
上游新增 N 个提交：
  🐛 Bug 修复 (3):  <hash> <message> / <hash> <message> / ...
  ✨ 新功能  (2):  <hash> <message> / ...
  ⚡ 优化    (1):  <hash> <message>
  ♻️ 重构   (1):  <hash> <message>

建议：
  - 直接合并: [bug 修复、性能优化列表]
  - 需确认:   [新功能、重构列表——说明与本地的潜在冲突]
  - 建议跳过: [文档/杂项——说明理由]
```

**等待用户确认后再继续。** 用户可：
- 全部合并
- 选择性合并（指定 hash 或类型）
- 跳过某些提交
- 调整冲突处理策略

### Step 4: 执行合并

#### 无冲突的普通提交

普通优化（bug 修复、性能优化）且无冲突时，直接合并，不打断用户：

```bash
git merge upstream/main --no-ff --no-edit
```

`--no-ff` 保留合并记录，生成 merge commit。

#### 有冲突的提交

**遇到冲突必须找用户确认**，不自行决定冲突解决策略。流程：

1. 合并触发冲突后，列出冲突文件：

```bash
git diff --name-only --diff-filter=U
```

2. **逐个文件分析冲突**，每个文件独立处理。对每个冲突文件：
   - `git diff <file>` 查看双方改动
   - 判断冲突性质：本地新增 vs 上游新增、本地修改 vs 上游修改、一方删除一方修改等
   - **保留双方有效改动**——不是简单选一边，而是合并两边的有效逻辑
   - 用 `apply_patch` 精确编辑冲突文件，保留双方有意义的代码

3. 每个文件解决后 `git add <file>` 标记已解决。

4. 所有冲突解决后，向用户报告每个文件的冲突处理方式，确认后 `git commit --no-edit`。

冲突解决原则（按优先级）：
- **本地为主**：本地已有的自定义逻辑、业务适配、特殊配置，优先保留本地版本
- **上游有价值**：上游的 bug 修复、安全补丁、通用优化，应合入
- **双方有效**：两边都新增了有效逻辑时，合并双方代码，调整使其共存
- **无法自动判断**：列出双方差异，找用户决定

### Step 5: 验证

合并完成后验证构建和类型检查：

```bash
# Go 项目
go build ./... && go vet ./...

# 前端（如有）
cd web && npx tsc --noEmit

# 测试（如项目有）
go test ./... -count=1
```

验证失败时，优先修复因合并引入的问题（如签名不匹配、类型变更）。
修复测试不属于本次合并的预存 bug 时，告知用户但不自行修复。

### Step 6: 完成报告

输出同步结果摘要：

```
✅ 同步完成: sync/upstream-YYYYMMDD

合并提交: N 个
  - 🐛 Bug 修复: X
  - ✨ 新功能: Y
  - ⚡ 优化: Z
  - ♻️ 重构: W

冲突文件: M 个（全部已解决，保留双方有效改动）
跳过提交: K 个（理由: ...）

验证: go build ✅ / tsc ✅
下一步: 确认无误后合并回 main → git checkout main && git merge sync/upstream-YYYYMMDD
```

## 决策原则

- **默认不自行决定合并范围**——Step 3 的人工确认是硬性关卡
- **冲突必须找用户**——Step 4 冲突处理不自行拍板
- **本地为主**——同等条件下优先保留本地改动
- **只合并必要代码**——文档、CI 配置等非必要变更可跳过
- **同步分支隔离**——所有操作在 `sync/upstream-*` 分支上，不污染 `main`
