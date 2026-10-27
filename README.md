# codex-mcp

A lightweight stdio MCP server that lets coding agents — ZCode, Claude Code,
Cursor, another Codex session, … — delegate work to the Codex CLI. It runs
`codex exec` as an **asynchronous job**, so long tasks never block an MCP tool
call or trip a client timeout.

轻量级 stdio MCP 服务器，让 ZCode、Claude Code、Cursor、另一个 Codex 会话等
编码 agent 把任务交给 Codex CLI 执行。底层用 `codex exec` 跑**异步 job**，
长任务不会卡住 MCP 调用，也不会触发客户端超时。

## Why a bridge? 为什么需要桥接

Codex ships an in-process `codex mcp-server`, but it was deprecated in 2026 and
removed in 0.153. This server wraps the supported `codex exec` command instead:
same CLI, same authentication, no private protocol.

Codex 自带的 `codex mcp-server` 已在 2026 年弃用、0.153 移除。本项目包装官方
支持的 `codex exec`：同样的 CLI、同样的登录态，不依赖私有协议。

## Design goals 设计目标

- **Small context footprint.** Responses are plain text (never JSON), metadata
  is one line, final answers are complete and sequentially paged, and raw logs
  are fetched only on demand. A typical turn is two calls: `codex_run` →
  `codex_result(wait_sec)`.
  **省上下文。** 响应一律纯文本；元数据一行；最终回复完整返回、超长时顺序
  分页；原始日志按需取。典型流程只有两次调用：`codex_run` →
  `codex_result(wait_sec)`。
- **Async by default.** Every run gets a job id immediately. Status, result,
  cancellation and raw output are separate tools. Queueing, per-directory
  serialization and timeouts live in the server.
  **默认异步。** 每次运行立刻返回 job id；状态、结果、取消、原始输出各自独立。
  队列、按目录串行、超时都由服务器管理。
- **Explicit permissions.** Default `workspace-write` with network access and
  Codex's automatic review (`--approve-for-me`). Read-only is one parameter
  away. Full access is environment-gated and uses the explicit yolo flag.
  **权限明确。** 默认 `workspace-write` + 网络 + Codex 自动审批
  （`--approve-for-me`）。按调用可切 `read-only`；完全访问必须由环境变量
  显式开启，并使用明确的 yolo 参数。
- **No new credential store.** Codex handles its own auth in `CODEX_HOME`
  (by default `~/.codex`). This server never reads or copies tokens.
  **不引入新凭据。** 登录态由 Codex 自己在 `CODEX_HOME`（默认 `~/.codex`）
  管理，服务器不读取、不复制 token。

## Requirements 要求

- Go 1.27+ to build.
- Codex CLI with `codex exec` installed and logged in (tested with 0.164).
  `codex exec` must work from a terminal.
- If `codex` is not on the PATH of the process that launches MCP servers, set
  `CODEX_BIN` to its absolute path. The server does not guess at
  app-bundled alpha binaries.

## Build 构建

```sh
go build -o codex-mcp .
go test ./...
```

## Tools 工具

| Tool | Purpose |
|---|---|
| `codex_run` | start a job; returns a job id immediately（创建 job，立即返回 id） |
| `codex_status` | one-line status, optionally wait ≤30 s（一行状态，可等待） |
| `codex_result` | final answer; waits and pages long text（最终回复，可等待、顺序分页） |
| `codex_output` | page raw `final` / `events` / `stderr` by byte offset（按字节分页原始输出） |
| `codex_cancel` | cancel a queued or running job（取消排队或运行中的 job） |
| `codex_list` | recent jobs, one line each（最近 job，一行一个） |

### `codex_run` parameters

| Field | Meaning |
|---|---|
| `prompt` | task for Codex (required, ≤64 KB) |
| `cwd` | working directory; must exist（默认服务器 cwd；配置 root 时默认 root） |
| `thread_id` | resume an existing Codex thread（恢复已有线程） |
| `model` | model override |
| `sandbox` | `read-only`, `workspace-write` (default), `danger-full-access` (env-gated) |
| `profile` | Codex config profile; new runs only（仅新会话） |
| `network` | workspace network access, default `true`（默认开启） |
| `timeout_sec` | default 1800, max 7200 |
| `ephemeral` | do not persist a new Codex session（不落盘新会话） |
| `images` | image files to attach（附加图片） |
| `add_dirs` | extra writable directories（额外可写目录） |
| `output_schema` | JSON Schema for the final response（约束最终 JSON） |

## Context contract 上下文契约

Chosen per content type; a model reply is never cut in the middle.

- **Metadata** (`codex_status`, `codex_cancel`, `codex_list`): one line per job.
- **Final text**: 32 KB by default. If it fits, it is returned **in full and
  multi-line**. If not, pages are returned sequentially from byte 0 with a
  continuation offset; the middle is never elided.
- **Final JSON** (when `output_schema` is used): 64 KB by default. `codex_result`
  returns the whole document only when it fits. A larger document is **never
  returned partially**; the tool points at `codex_output` for ordered raw
  byte pages instead.
- **Diagnostics** (`stderr` on failures): 8 KB head + tail with the omitted
  range named. Full logs are one `codex_output` call away.
- **Events**: only a count is shown by default; raw JSONL is never injected
  into context unless requested.

```text
> codex_run {prompt:"fix failing tests", cwd:"/repo"}
job=cx-7 queued sandbox=workspace-write network=on cwd=/repo timeout=30m

> codex_result {job:"cx-7", wait_sec:20}
job=cx-7 exit=0 3m12s thread=0199f3c2c8a1… usage=12.3k/678 events=42
---
Fixed 3 failing tests. The failures came from a nil check in …

> codex_result {job:"cx-7"}
job=cx-7 exit=0 3m12s … offset=32768
---
<next complete page>

> codex_output {id:"cx-7:events", offset:0, length:8192}
{"type":"thread.started",...}
```

## Permissions 权限

Defaults were chosen for real coding work, not for a read-only demo:

| Setting | Default |
|---|---|
| Sandbox | `workspace-write` |
| Network in workspace | enabled |
| Approvals | automatic review (`--approve-for-me`) |
| User config | inherited from `CODEX_HOME/config.toml` |
| Extra writable dirs | none |
| Full access | disabled |

- `sandbox:"read-only"` disables writes for one call.
- `network:false` disables workspace network access for one call.
- `sandbox:"danger-full-access"` is rejected unless
  `CODEX_MCP_ALLOW_FULL_ACCESS=1`; when allowed, the server uses
  `--dangerously-bypass-approvals-and-sandbox`. Full access bypasses sandbox
  and network controls, so `network:false` is not enforced in that mode.
- Set `CODEX_MCP_IGNORE_USER_CONFIG=1` to run with `--ignore-user-config`
  (auth still comes from `CODEX_HOME`). This also prevents a nested Codex from
  loading the same MCP servers, plugins and hooks, which avoids recursive
  `codex-mcp` launches when Codex itself is the caller.
- `CODEX_MCP_ROOT` confines `cwd`, `add_dirs` and `images` to one directory
  after resolving symlinks.
- `CODEX_MCP_ALLOW_NON_GIT=1` adds `--skip-git-repo-check` for work outside a
  Git repository.

The server never accepts arbitrary `-c key=value` overrides. The only config
keys it sets are the sandbox mode on resume, workspace network access, and
resume writable roots.

## Resume behavior 恢复会话

`thread_id` maps to `codex exec resume --all <id> -` so the thread is found even
when the cwd differs.

- `model`, `images`, `output_schema`, `ephemeral` and `ignore_user_config` work.
- `sandbox` is applied through `-c sandbox_mode=...`; `network` through
  `sandbox_workspace_write.network_access`; `add_dirs` through
  `sandbox_workspace_write.writable_roots`.
- `profile` is rejected with `thread_id` because the resume CLI does not accept
  `-p`.
- `cwd` sets the process directory. Codex may still use the thread's recorded
  working directory.
- An `--ephemeral` thread is not persisted, so it cannot be resumed later.

## Environment 环境变量

| Variable | Default | Meaning |
|---|---|---|
| `CODEX_BIN` | `codex` in PATH | Codex executable |
| `CODEX_HOME` | Codex default | Auth and session store (passed through) |
| `CODEX_MCP_JOBS_DIR` | `~/.codex-mcp/jobs` | Job artifacts |
| `CODEX_MCP_ROOT` | unset | Optional path sandbox |
| `CODEX_MCP_TIMEOUT` | `1800s` | Default job timeout |
| `CODEX_MCP_MAX_CONCURRENCY` | `1` | Concurrent jobs (1–16) |
| `CODEX_MCP_QUEUE_LIMIT` | `32` | Pending job limit |
| `CODEX_MCP_MAX_FINAL` | `32768` | Inline final text bytes |
| `CODEX_MCP_MAX_JSON` | `65536` | Inline final JSON bytes |
| `CODEX_MCP_MAX_DIAG` | `8192` | stderr head+tail bytes |
| `CODEX_MCP_MAX_PROMPT` | `65536` | Prompt bytes |
| `CODEX_MCP_RETENTION` | `100` | Job directories kept |
| `CODEX_MCP_IGNORE_USER_CONFIG` | `0` | Isolate from `config.toml`, plugins and hooks |
| `CODEX_MCP_ALLOW_NON_GIT` | `0` | Allow non-Git cwds |
| `CODEX_MCP_ALLOW_FULL_ACCESS` | `0` | Enable `danger-full-access` |

`CODEX_MCP_TIMEOUT` accepts seconds (`1800`) or a duration (`30m`). Booleans
accept `1/true/yes/on` and `0/false/no/off`.

## Wire it up 接入

**ZCode** (`.zcode/mcp.json` or user config):

```json
{
  "mcpServers": {
    "codex": {
      "command": "/path/to/codex-mcp",
      "env": {
        "CODEX_BIN": "/opt/homebrew/bin/codex"
      }
    }
  }
}
```

**Codex** (`~/.codex/config.toml`):

```toml
[mcp_servers.codex]
command = "/path/to/codex-mcp"
```

When Codex is the caller, consider setting
`CODEX_MCP_IGNORE_USER_CONFIG=1` in the server's environment so nested runs do
not load the same MCP servers and plugins.

**Claude Code:**

```sh
claude mcp add codex -- /path/to/codex-mcp
```

## Storage and cleanup 存储与清理

```text
~/.codex-mcp/jobs/cx-7/
  final.txt      # final answer
  events.jsonl   # raw codex exec --json stream
  stderr.log     # Codex stderr
  schema.json    # optional output schema
```

Directories are created `0700`, files `0600`. Only the newest
`CODEX_MCP_RETENTION` job directories are kept. Job state is in memory: after a
restart, old directories may still exist, but they are not addressable by
`codex_result` or `codex_output`.

Prompts are not written to the job directory. Codex's own session store is
controlled by `CODEX_HOME` and `--ephemeral`; use `ephemeral:true` when a prompt
must not be recorded there.

Every tool response and tool error passes a redaction filter for private key
blocks, `sk-…` API keys, `Bearer …` tokens and `OPENAI_API_KEY=…`. This is a
backstop, not a reason to put secrets in prompts.

## Security and terms 安全与使用条款

- The server is stdio-only and single-user. It opens no network port. Do not
  expose it to multiple users, resell it, or use it to pool or share Codex
  accounts.
- Prompts, file contents read by Codex, and final answers are sent to the
  provider configured in Codex. The OpenAI/Codex terms apply; personal,
  single-owner automation of your own account is the intended use.
- Codex runs with the permissions you configured. The default is not a full
  security boundary: `workspace-write` can still modify the repository and run
  commands, and `--approve-for-me` delegates approval decisions to Codex.
  Review `cwd` before delegating work.
- Inheriting `config.toml` grants the nested Codex the same MCP servers,
  plugins, hooks, profiles and trusted-project state. Use
  `CODEX_MCP_IGNORE_USER_CONFIG=1` when that is not desired.

## Limitations 限制

- No HTTP transport, approval UI, audit log or cross-process job lock in v0.
- The job registry is per server process. Files survive restarts; job metadata
  does not.
- Concurrency defaults to one job; same-cwd jobs are serialized even when the
  limit is raised.
- Windows process cancellation uses `taskkill /T /F`; Unix uses process groups
  with SIGTERM → grace → SIGKILL.

## License

MIT
