# claude-switch

在一台 Linux 机器上给 [Claude Code](https://docs.anthropic.com/en/docs/claude-code) 冷切换多个 claude.ai 账号，或者切到一个不登录账号、改用一份设置文件的「非账号目标」（比如经 API 网关用 API Key）。同一时间只用一个。

非官方工具，与 Anthropic 无关，见文末「与 Claude Code 的关系」。

[English](#english)

## 它做什么

Claude Code 只有一个默认配置目录 `~/.claude`。claude-switch 换号，就是把跟账号走的那部分登录状态整体换掉：

- `~/.claude/.credentials.json`：除 `mcpOAuth`（你配的 MCP 服务的授权）与 `coworkRemoteDevice`（设备身份）外的字段都跟账号走，Claude Code 以后新增的令牌默认也跟账号走；
- `~/.claude.json`：登录身份、订阅、额度与各类缓存这些账号字段跟账号走；项目、主题等其余设置所有账号共用。

在用的账号的这部分存进 `~/.local/state/claude-switch/<账号名>.json`（权限 0600），再载入目标账号的存档，并删掉这份存档。目标账号没有存档时只退出在用的账号，启动 `claude` 后用 `/login` 登录目标账号，claude-switch 下次运行时记下它。

## 为什么是冷切换

- **令牌只有一份。** 令牌续期会让旧的续期凭证作废，留着副本切回去就得重新登录。所以每个账号的令牌要么在用、要么在存档里，从不复制。
- **有 Claude Code 在跑就不换。** 运行中的进程把令牌缓存在内存里，访问令牌到期（约 8 小时）才重读磁盘；趁它在跑换号，它会在之后某个时刻悄悄换到新账号，还可能把旧账号写回 `~/.claude.json`。所以换号前要先退出全部 Claude Code。

## 安全边界

- 只在本机挪文件：不联网，不调用任何接口，不在服务端登录或退出登录。
- 不打印令牌：状态显示与 `--json` 只有账号名、邮箱、组织、套餐这类身份信息。
- 用默认配置目录的 Claude Code 进程还在跑（终端会话、`claude rc`、后台进程、Claude Desktop 的远程会话）就拒绝；用 `CLAUDE_CONFIG_DIR` 另设配置目录的进程不算。读不了 `/proc` 时确认不了，同样拒绝。
- 按 `~/.claude.json` 里的登录编号（`accountUuid`）认在用的是哪个号。认不出（编号没记过）、或凭据里有令牌却没有登录身份时拒绝，不把一个号的令牌存进另一个号的存档。
- 写文件前先把整份计划写进 `pending.json`，写完删掉。中途出错时它留作现场，之后每次运行都拒绝，不自动补做（补做会用计划里的旧令牌覆盖这期间续期出的新令牌）。
- 换号时拿着 Claude Code 写这些文件时用的两把目录锁（`~/.claude/.oauth_refresh.lock`、`~/.claude.json.lock`）。
- 存档与凭据整份替换、落盘，权限 0600。

## 安装

需要 Go（`go.mod` 固定的版本；本机的 `go` 较旧时会自动下载这一版）。

```sh
go install github.com/dzshzx/claude-switch@latest
```

程序装在 `$(go env GOPATH)/bin`（通常是 `~/go/bin`）。

给另一台机器编（比如手机或树莓派上的 Linux，arm64；x86-64 写 `amd64`）：

```sh
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go install github.com/dzshzx/claude-switch@latest
# 架构与本机不同时，程序在 $(go env GOPATH)/bin/linux_arm64/claude-switch
```

`CGO_ENABLED=0` 编出的是静态程序，拷过去就能跑。也可以从源码编：

```sh
git clone https://github.com/dzshzx/claude-switch.git
cd claude-switch
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -o claude-switch .
```

只支持 Linux（含 WSL）：它靠 `/proc` 认 Claude Code 进程，并且只管 Claude Code 在 Linux 上存放登录状态的那两个文件。

## 用法

```text
claude-switch                   显示当前账号或目标、已存档的账号与非账号目标
claude-switch --json            同上，给程序读的 JSON（没有令牌）
claude-switch <名字>            换到这个账号或非账号目标
claude-switch add <账号名>      加一个账号
claude-switch remove <账号名>   删掉一个不在用的账号的存档与记录
claude-switch version           打印版本
```

名字由小写字母、数字和连字符组成，字母或数字开头，至多 32 个字符；`add`、`remove`、`version`、`accounts`、`pending` 不能用。

**第一次用**：默认配置目录里已经登录的那个号叫 `claude`，不用 add。`claude` 总能用作目标。

**加一个号**：

```sh
claude-switch add work   # 在用的号存档并退出登录，切换记录写成 work
claude                   # 启动后用 /login 登录 work 那个账号
claude-switch            # 下次运行时记下 work 的登录编号
claude-switch claude     # 换回去；之后 claude-switch work 换过来
```

如果已经用 `/login` 直接登进了一个没记过的号，claude-switch 会拒绝换号；这时 `claude-switch add <名字>` 只给在用的登录起名，不改登录。

**删一个号**：`claude-switch remove work` 只删本机这份存档与记录，那个账号在服务端仍是登录状态。在用的号不能删。

## 非账号目标

在 `~/.config/claude-switch/targets/` 下放一份 Claude Code 的设置文件（一个 JSON 对象），文件名去掉 `.json` 就是目标名。比如 `~/.config/claude-switch/targets/gateway.json`：

```json
{
  "env": { "ANTHROPIC_BASE_URL": "https://llm-gateway.example.com" },
  "apiKeyHelper": "cat ~/.config/llm-gateway/key"
}
```

`claude-switch gateway`：在用的账号照常存档并退出登录，切换记录写成 `gateway`。claude-switch 只确认这份文件是 JSON 对象，不读其中的值，也不运行其中的命令。要让 `claude` 用上它，启动时加 `--settings <这份文件>`。在 shell 里包一层就不用每次手写（bash、zsh 都适用，放进 `~/.bashrc` 或 `~/.zshrc`）：

```sh
claude() {
  local target="" settings
  { read -r target <"$HOME/.local/state/claude-switch/current"; } 2>/dev/null || true
  settings="$HOME/.config/claude-switch/targets/$target.json"
  if [ -n "$target" ] && [ -f "$settings" ]; then
    set -- --settings "$settings" "$@"
  fi
  command claude "$@"
}
```

想让显式配置的地址（环境变量 `ANTHROPIC_BASE_URL`，或 `~/.claude/settings.json` 里的 `env.ANTHROPIC_BASE_URL`）优先，就在加 `--settings` 前先查一下它们。

换回账号用 `claude-switch claude`（或别的账号名）。目标名不能再当账号名用；同名的账号存档与目标同时存在时，claude-switch 拒绝换过去。

## 退出码

| 退出码 | 含义                                                                                                               |
| ------ | ------------------------------------------------------------------------------------------------------------------ |
| 0      | 已换好或无需换；显示状态成功                                                                                       |
| 1      | 拒绝：有 Claude Code 进程在跑、认不出在用的账号、要删的是在用的账号、上次换号中途出错还没处理                      |
| 2      | 用法错误、没有这个账号或目标、名字已被占用、目标的设置文件不是 JSON 对象、锁一直被占、读不了 `/proc`、读写文件出错 |

出错说明写在标准错误，以 `claude-switch: ` 开头。有进程在跑时那一行含「还有 Claude Code 进程在跑」并逐行列出进程；没有这个账号时含「没有叫」。

## `--json`

给别的程序（比如图形界面）读。不含令牌。

```json
{
  "current": "work",
  "live": {
    "name": "work",
    "current": true,
    "parked": false,
    "uuid": "00000000-0000-4000-8000-000000000002",
    "email": "work@example.com",
    "organization": "Example Org",
    "displayName": "Example",
    "subscription": "max",
    "refreshExpiresAt": 1900000000000
  },
  "accounts": [
    {
      "name": "claude",
      "current": false,
      "parked": true,
      "uuid": "00000000-0000-4000-8000-000000000001",
      "email": "me@example.com",
      "subscription": "pro"
    },
    { "name": "work", "current": true, "parked": false, "uuid": "00000000-0000-4000-8000-000000000002", "email": "work@example.com", "subscription": "max" }
  ],
  "targets": [{ "name": "gateway", "settings": "/home/me/.config/claude-switch/targets/gateway.json" }],
  "pending": false,
  "running": []
}
```

| 字段       | 含义                                                                                                                                                                 |
| ---------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `current`  | 切换记录里的名字（账号名或目标名）                                                                                                                                   |
| `live`     | 在用的登录；未登录为 `null`。`name` 为空串表示认不出是哪个号（换号会拒绝，先 `add`）                                                                                 |
| `accounts` | 已知账号：在用的、有存档的、记过的；在用的那个 `current` 为 `true`，有存档的 `parked` 为 `true`                                                                      |
| `targets`  | 非账号目标与它的设置文件，按名字排                                                                                                                                   |
| `pending`  | 上次换号中途出错、现场待核对（此时一律拒绝换号）                                                                                                                     |
| `running`  | 用默认配置目录的 Claude Code 进程，每项是「pid 命令行」；不为空时换号会拒绝                                                                                          |
| 账号字段   | `uuid` 登录编号；`email`、`organization`、`displayName` 来自 `~/.claude.json`；`subscription` 套餐；`refreshExpiresAt` 续期凭证到期时刻（Unix 毫秒）；没有的字段省略 |

## 文件

| 路径                                          | 内容                                                                |
| --------------------------------------------- | ------------------------------------------------------------------- |
| `~/.local/state/claude-switch/current`        | 切换记录：当前的账号名或目标名，加一个换行                          |
| `~/.local/state/claude-switch/<账号名>.json`  | 存档：`{"credentials": {…}, "config": {…}}`，跟账号走的那部分，0600 |
| `~/.local/state/claude-switch/accounts.json`  | 账号名 → 登录编号（`accountUuid`）                                  |
| `~/.local/state/claude-switch/pending.json`   | 换号中途出错时留下的计划                                            |
| `~/.config/claude-switch/targets/<名字>.json` | 非账号目标的 Claude Code 设置                                       |

状态目录里的其他文件（比如你自己放的 API Key）claude-switch 不碰。

## 中途出错怎么办

`pending.json` 还在时，claude-switch 每次运行都拒绝。它是那次换号的整份计划：`target` 要换到的名字，`live` 当时在用的账号，`park` 要写成的存档，`load` 要载入的那部分，`accounts` 要写成的 `accounts.json`。**里面有令牌，别贴到别处。**

对照 `~/.claude/.credentials.json`、`~/.claude.json` 与各存档，看做到了哪一步，补齐缺的那步（或确认现状可用），然后删掉 `pending.json`。注意现在在用的令牌可能比计划里的新，别用计划里的覆盖它。

## 给别的程序用

想知道一个 Claude Code 进程属于哪个账号（比如在 hook 里按账号统计会话），可以用 Go 包 `github.com/dzshzx/claude-switch/account`：`account.Name()` 在默认配置目录下返回切换记录里的名字，另设 `CLAUDE_CONFIG_DIR` 时返回目录名（去掉开头的点）。

## 与 Claude Code 的关系

这是个人写的非官方工具，与 Anthropic 没有关系，也没有得到 Anthropic 的认可。Claude 与 Claude Code 是 Anthropic 的产品与商标。

claude-switch 不修改 Claude Code，不调用它的接口，只按 Claude Code 在 Linux 上的本地文件挪文件。哪些字段跟账号走，是按这些文件的现有内容定的：凭据里新增的字段默认跟账号走；`~/.claude.json` 里的账号字段按名字规则认（见 `internal/claudeswitch` 的 `accountKeys` 与 `accountKeyPattern`）。Claude Code 改了这些文件的布局时，claude-switch 可能要跟着改。一个人用多个账号是否合乎你所用服务的条款，请自行确认。

## 许可证

[MIT](LICENSE)

---

## English

claude-switch cold-switches [Claude Code](https://docs.anthropic.com/en/docs/claude-code) on one Linux machine between several claude.ai accounts, or to a "settings target" that does not log in to claude.ai and uses a Claude Code settings file instead (for example an API gateway with an API key). One at a time. It is an unofficial tool, not affiliated with or endorsed by Anthropic.

**What it does.** Claude Code has one default config directory, `~/.claude`. Switching swaps the account-bound part of the login state: every field of `~/.claude/.credentials.json` except `mcpOAuth` and `coworkRemoteDevice` (new token fields follow the account by default), and the account fields of `~/.claude.json` (identity, subscription, quota and caches; projects, theme and other settings stay shared). The live account's part is parked in `~/.local/state/claude-switch/<name>.json` (mode 0600), then the target's parked part is loaded and its file deleted. If the target has nothing parked, the live account is just logged out; start `claude`, `/login` to the target, and claude-switch records it on its next run.

**Why cold.** Each account's tokens exist exactly once, live or parked: refreshing invalidates the old refresh token, so a copy would be useless. A running Claude Code caches tokens in memory and rereads the files only when the access token expires (about 8 hours), so claude-switch refuses while any Claude Code process uses the default config directory (terminal sessions, `claude rc`, background processes, Claude Desktop remote sessions). Processes with their own `CLAUDE_CONFIG_DIR` do not count. If `/proc` cannot be read it refuses too.

**Safety.** Local file moves only: no network, no API calls, no server-side logout. Tokens are never printed. The live login is identified by `accountUuid`; an unknown login, or tokens without an identity, are refused rather than parked under the wrong name. The whole plan is written to `pending.json` first and removed when done; if a switch fails midway, every later run refuses until you inspect the files and delete it (replaying it could overwrite newer refreshed tokens; the file contains tokens, do not share it). Claude Code's two lock directories are held while switching. Files are replaced atomically and fsynced, mode 0600.

**Install.** `go install github.com/dzshzx/claude-switch@latest`. For another Linux machine: `GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go install github.com/dzshzx/claude-switch@latest` (the binary lands in `$(go env GOPATH)/bin/linux_arm64/` when cross-compiling; use `amd64` for x86-64). Linux (including WSL) only.

**Usage.** `claude-switch` shows the status; `--json` prints it for programs (no tokens); `claude-switch <name>` switches to an account or settings target; `add <name>` adds an account (parks and logs out the live one, or just names an unrecorded live login); `remove <name>` deletes a parked account's local copy; `version`. The login already present on first use is called `claude`, which is always a valid target. Names: lowercase letters, digits and hyphens, starting with a letter or digit, up to 32 characters.

**Settings targets.** Put a Claude Code settings file (a JSON object) at `~/.config/claude-switch/targets/<name>.json`. `claude-switch <name>` parks and logs out the live account and records `<name>`; it only checks that the file is a JSON object and never runs anything in it. Start `claude` with `--settings <that file>`; the shell function in the Chinese section above does this automatically when the current record names a target.

**Exit codes.** 0 switched, nothing to do, or status shown. 1 refused: Claude Code is running, the live login is unknown, removing the live account, or an unhandled `pending.json`. 2 usage error, no such account or target, name taken, settings file not a JSON object, lock held, `/proc` unreadable, or a file error. Messages go to stderr prefixed with `claude-switch: `.

**`--json` fields.** `current` (recorded name), `live` (live login or `null`; empty `name` means unrecognized), `accounts` (known accounts with `name`, `current`, `parked`, `uuid`, `email`, `organization`, `displayName`, `subscription`, `refreshExpiresAt` in Unix ms; empty fields omitted), `targets` (`name`, `settings`), `pending`, `running` (`"pid command line"` entries).

**For other programs.** The Go package `github.com/dzshzx/claude-switch/account` tells which account a Claude Code process belongs to (`account.Name()`).

License: [MIT](LICENSE).
