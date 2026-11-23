package claudeswitch

// 用例在临时 HOME 里跑：凭据与账号字段在在用文件与各号存档之间搬动，共用设置留下；同一配置目录上有 Claude Code 进程
// （原生安装、Desktop 远程 CLI、被替换的文件或名为 claude 的进程）时拒绝；在用的登录按 accountUuid 认；中途出错的换号
// 之后一律拒绝、不补做；非账号目标只退出登录、不存档。命令在进程内运行，有一条用例走真实命令行（本测试二进制充当 claude-switch）。
// 令牌、邮箱、编号都是假数据。

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestMain：带 CLAUDE_SWITCH_TEST_MAIN=1 时本测试二进制充当 claude-switch。
func TestMain(m *testing.M) {
	if os.Getenv("CLAUDE_SWITCH_TEST_MAIN") == "1" {
		home, _ := os.UserHomeDir()
		os.Exit(New(home).Run(os.Args[1:]))
	}
	os.Exit(m.Run())
}

type obj = map[string]any

var (
	sharedCreds = obj{"mcpOAuth": obj{"server": obj{"accessToken": "mcp"}}}
	mainCreds   = obj{
		"claudeAiOauth": obj{"accessToken": "a-token", "refreshToken": "a-refresh"},
		"designOauth":   obj{"refreshToken": "a-design"},
		"mcpOAuth":      sharedCreds["mcpOAuth"],
	}
	mainConfig = obj{
		"oauthAccount":             obj{"accountUuid": "uuid-a", "emailAddress": "a@example.com"},
		"userID":                   "user-a",
		"cachedGrowthBookFeatures": obj{"flag": "a"},
		"projects":                 obj{"/w": obj{"allowedTools": []any{}}},
		"theme":                    "dark",
	}
	secondCreds  = obj{"claudeAiOauth": obj{"accessToken": "b-token", "refreshToken": "b-refresh"}}
	secondConfig = obj{"oauthAccount": obj{"accountUuid": "uuid-b", "emailAddress": "b@example.com"}, "userID": "user-b"}
	secondSlot   = obj{"credentials": secondCreds, "config": secondConfig}
)

const gatewaySettings = `{"env": {"ANTHROPIC_BASE_URL": "https://llm-gateway.example.com"}, "apiKeyHelper": "cat ~/.config/llm-gateway/key"}`

func merge(parts ...obj) obj {
	out := obj{}
	for _, p := range parts {
		for k, v := range p {
			out[k] = v
		}
	}
	return out
}

type env struct {
	t                          *testing.T
	home, creds, config, state string
	pending, targets           string
	proc                       string
	procs                      []*exec.Cmd
	lockTimeout                time.Duration
	writeJSON                  func(string, any, os.FileMode) error
	stdout, stderr             string
}

// setup：默认账号 claude 在用，second 有存档（还没记进 accounts.json）。
func setup(t *testing.T) *env {
	t.Helper()
	home := t.TempDir()
	e := &env{t: t, home: home, creds: filepath.Join(home, ".claude/.credentials.json"),
		config: filepath.Join(home, ".claude.json"), state: filepath.Join(home, ".local/state/claude-switch"),
		targets: filepath.Join(home, ".config/claude-switch/targets"), lockTimeout: DefaultLockTimeout}
	e.pending = filepath.Join(e.state, "pending.json")
	for _, dir := range []string{filepath.Join(home, ".claude"), e.state} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	e.write(e.creds, mainCreds)
	e.write(e.config, mainConfig)
	e.write(e.slot("second"), secondSlot)
	t.Cleanup(e.stopFakes)
	return e
}

func (e *env) write(path string, data any) {
	e.t.Helper()
	raw, err := json.Marshal(data)
	if err != nil {
		e.t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) writeText(path, text string) {
	e.t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		e.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		e.t.Fatal(err)
	}
}

// target 放一份非账号目标的设置文件。
func (e *env) target(name, settings string) string {
	e.t.Helper()
	path := filepath.Join(e.targets, name+".json")
	e.writeText(path, settings)
	return path
}

func (e *env) read(path string) obj {
	e.t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		e.t.Fatal(err)
	}
	var out obj
	if err := json.Unmarshal(data, &out); err != nil {
		e.t.Fatalf("%s：%v", path, err)
	}
	return out
}

// same 按 JSON 值比较（把期望值也过一遍 JSON）。
func (e *env) same(got any, want any, what string) {
	e.t.Helper()
	norm := func(v any) any {
		raw, _ := json.Marshal(v)
		var out any
		_ = json.Unmarshal(raw, &out)
		return out
	}
	if !reflect.DeepEqual(norm(got), norm(want)) {
		e.t.Errorf("%s = %v，期望 %v", what, got, want)
	}
}

func (e *env) slot(name string) string { return filepath.Join(e.state, name+".json") }

func (e *env) current() string {
	e.t.Helper()
	data, err := os.ReadFile(filepath.Join(e.state, "current"))
	if err != nil {
		e.t.Fatal(err)
	}
	return strings.TrimSpace(string(data))
}

func (e *env) recordAccounts() {
	e.write(filepath.Join(e.state, "accounts.json"), obj{"claude": "uuid-a", "second": "uuid-b"})
}

func (e *env) run(args ...string) int {
	var out, errb bytes.Buffer
	s := &Switcher{Home: e.home, LockTimeout: e.lockTimeout, Stdout: &out, Stderr: &errb, WriteJSON: e.writeJSON, Proc: e.proc}
	code := s.Run(args)
	e.stdout, e.stderr = out.String(), errb.String()
	return code
}

func (e *env) ok(args ...string) string {
	e.t.Helper()
	if code := e.run(args...); code != 0 {
		e.t.Fatalf("claude-switch %v 退出 %d：%s", args, code, e.stderr)
	}
	return e.stdout
}

func (e *env) exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func (e *env) snapshot() map[string]string {
	e.t.Helper()
	files := []string{e.creds, e.config}
	entries, _ := os.ReadDir(e.state)
	for _, entry := range entries {
		files = append(files, filepath.Join(e.state, entry.Name()))
	}
	sort.Strings(files[2:])
	out := map[string]string{}
	for _, p := range files {
		if info, err := os.Stat(p); err == nil && info.Mode().IsRegular() {
			data, _ := os.ReadFile(p)
			out[p] = string(data)
		}
	}
	return out
}

// assertRefused：拒绝时退出码与提示对，且凭据、配置与 claude-switch 状态目录一个字节都没变。
func (e *env) assertRefused(code int, text string, args ...string) {
	e.t.Helper()
	before := e.snapshot()
	if got := e.run(args...); got != code {
		e.t.Errorf("claude-switch %v 退出 %d，期望 %d：%s", args, got, code, e.stderr)
	}
	if !strings.Contains(e.stderr, text) {
		e.t.Errorf("标准错误没有 %q：%s", text, e.stderr)
	}
	if after := e.snapshot(); !reflect.DeepEqual(after, before) {
		e.t.Errorf("拒绝后文件变了")
	}
}

func contains(t *testing.T, got, want string) {
	t.Helper()
	if !strings.Contains(got, want) {
		t.Errorf("输出没有 %q：\n%s", want, got)
	}
}

// writeExecutable 写一个可执行文件。写的时候拿着 syscall.ForkLock 的读锁：并行用例这时起不了子进程，
// 不会有子进程继承这个写端，随后执行它不会撞上 ETXTBSY（text file busy）。
func writeExecutable(t *testing.T, path string, data []byte) {
	t.Helper()
	syscall.ForkLock.RLock()
	err := os.WriteFile(path, data, 0o755)
	syscall.ForkLock.RUnlock()
	if err != nil {
		t.Fatal(err)
	}
}

// fakeClaude 起一个可执行文件位于 Claude Code 安装位置的进程（复制 bash，不用 sleep：有的 sleep 是多合一程序，
// 改名后不认识自己），返回 pid 与可执行文件路径。
func (e *env) fakeClaude(where, name string, extra ...string) (int, string) {
	e.t.Helper()
	if where == "" {
		where = ".local/share/claude/versions"
	}
	if name == "" {
		name = "9.9.9"
	}
	dir := filepath.Join(e.home, where)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		e.t.Fatal(err)
	}
	exe := filepath.Join(dir, name)
	if _, err := os.Stat(exe); err != nil {
		bash, err := exec.LookPath("bash")
		if err != nil {
			e.t.Fatal(err)
		}
		data, err := os.ReadFile(bash)
		if err != nil {
			e.t.Fatal(err)
		}
		writeExecutable(e.t, exe, data)
	}
	cmd := exec.Command(exe, "-c", "read -r -t 30 _ || true")
	cmd.Env = append(append(os.Environ(), "HOME="+e.home), extra...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		e.t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		e.t.Fatal(err)
	}
	_ = stdin
	e.procs = append(e.procs, cmd)
	link := "/proc/" + strconv.Itoa(cmd.Process.Pid) + "/exe"
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if got, _ := os.Readlink(link); got == exe {
			break
		}
	}
	return cmd.Process.Pid, exe
}

func (e *env) stopFakes() {
	for _, cmd := range e.procs {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}
	e.procs = nil
}

func TestRoundTripMovesAccountAndKeepsShared(t *testing.T) {
	t.Parallel()
	e := setup(t)
	e.ok("second")
	e.same(e.read(e.creds), merge(sharedCreds, secondCreds), "凭据")
	config := e.read(e.config)
	e.same(config["oauthAccount"].(obj)["accountUuid"], "uuid-b", "accountUuid")
	e.same(config["userID"], "user-b", "userID")
	if _, ok := config["cachedGrowthBookFeatures"]; ok {
		t.Error("second 的配置里留着默认账号的账号缓存")
	}
	e.same(config["projects"], mainConfig["projects"], "projects")
	if e.current() != "second" || e.exists(e.slot("second")) {
		t.Errorf("current = %s，second 的存档还在 = %v", e.current(), e.exists(e.slot("second")))
	}
	parked := e.read(e.slot("claude"))
	keys := func(v any) []string {
		var out []string
		for k := range v.(obj) {
			out = append(out, k)
		}
		sort.Strings(out)
		return out
	}
	e.same(keys(parked["credentials"]), []string{"claudeAiOauth", "designOauth"}, "存档的凭据字段")
	e.same(keys(parked["config"]), []string{"cachedGrowthBookFeatures", "oauthAccount", "userID"}, "存档的配置字段")
	for _, path := range []string{e.creds, e.slot("claude")} {
		if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 {
			t.Errorf("%s 权限 %v", path, info.Mode())
		}
	}

	// second 用了一阵：续期换了令牌、改了一项共用设置
	creds := e.read(e.creds)
	creds["claudeAiOauth"].(obj)["refreshToken"] = "b-refresh-2"
	e.write(e.creds, creds)
	config["theme"] = "light"
	e.write(e.config, config)

	e.ok("claude")
	e.same(e.read(e.creds), mainCreds, "换回后的凭据")
	e.same(e.read(e.config), merge(mainConfig, obj{"theme": "light"}), "换回后的配置")
	if e.current() != "claude" || e.exists(e.slot("claude")) {
		t.Errorf("current = %s，默认账号存档还在 = %v", e.current(), e.exists(e.slot("claude")))
	}
	e.same(e.read(e.slot("second"))["credentials"].(obj)["claudeAiOauth"].(obj)["refreshToken"], "b-refresh-2", "second 的续期令牌")
}

func TestTargetParksAccountAndSwitchesBack(t *testing.T) {
	t.Parallel()
	e := setup(t)
	settings := e.target("gateway", gatewaySettings)
	contains(t, e.ok("gateway"), "已换到 gateway；claude 已存档：非账号目标，claude 要带 --settings "+settings+" 启动")
	e.same(e.read(e.creds), sharedCreds, "目标下的凭据")
	e.same(e.read(e.config), obj{"projects": mainConfig["projects"], "theme": "dark"}, "目标下的配置")
	if e.current() != "gateway" || e.exists(e.slot("gateway")) || !e.exists(e.slot("claude")) {
		t.Errorf("current = %s，存档不对", e.current())
	}
	if data, _ := os.ReadFile(settings); string(data) != gatewaySettings {
		t.Errorf("设置文件被改了：%s", data)
	}
	status := e.ok()
	contains(t, status, "当前目标：gateway（非账号目标），claude 要带 --settings "+settings+" 启动\n")
	contains(t, status, "已存档：claude，登录为 a@example.com")
	contains(t, status, "已存档：second")
	contains(t, e.ok("gateway"), "已是 gateway")

	// 目标下写进 ~/.claude.json 的账号类缓存不带进默认账号
	e.write(e.config, merge(e.read(e.config), obj{"cachedGrowthBookFeatures": obj{}}))
	contains(t, e.ok("claude"), "已换到 claude，登录为 a@example.com")
	e.same(e.read(e.creds), mainCreds, "换回后的凭据")
	e.same(e.read(e.config), mainConfig, "换回后的配置")
	if e.current() != "claude" {
		t.Errorf("current = %s", e.current())
	}
	e.same(e.read(filepath.Join(e.state, "accounts.json")), obj{"claude": "uuid-a", "second": "uuid-b"}, "accounts.json")
	contains(t, e.ok(), "非账号目标：gateway（"+settings+"）")
}

func TestTargetWithBadSettingsChangesNothing(t *testing.T) {
	t.Parallel()
	e := setup(t)
	e.assertRefused(2, "没有叫 gateway", "gateway")
	e.target("gateway", "[]")
	e.assertRefused(2, "不是 JSON 对象", "gateway")
	e.target("gateway", `{"env":`)
	e.assertRefused(2, "读不了", "gateway")
	// 不是普通文件、名字不合写法的不算目标
	if err := os.Remove(filepath.Join(e.targets, "gateway.json")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(e.targets, "gateway.json"), 0o755); err != nil {
		t.Fatal(err)
	}
	e.target("Upper", "{}")
	e.assertRefused(2, "没有叫 gateway", "gateway")
	if out := e.ok(); strings.Contains(out, "非账号目标") {
		t.Errorf("状态里列了不算数的目标：%s", out)
	}
}

func TestTargetNamesAreNotAccountNames(t *testing.T) {
	t.Parallel()
	e := setup(t)
	e.target("gateway", "{}")
	e.assertRefused(2, "是非账号目标", "add", "gateway")
	e.assertRefused(2, "是非账号目标", "remove", "gateway")
	// 同名的账号存档：认不清要换到哪个，拒绝
	e.write(e.slot("gateway"), secondSlot)
	e.assertRefused(2, "既是账号又是非账号目标", "gateway")
}

func TestUnknownLoginUnderTargetIsNotRecordedAsTarget(t *testing.T) {
	t.Parallel()
	// 目标下用 /login 登了一个没记过的号：不能把它的令牌当成目标存档或记编号
	e := setup(t)
	e.target("gateway", gatewaySettings)
	e.ok("gateway")
	stranger := obj{"oauthAccount": obj{"accountUuid": "uuid-c", "emailAddress": "c@example.com"}}
	e.write(e.creds, merge(sharedCreds, obj{"claudeAiOauth": obj{"refreshToken": "c"}}))
	e.write(e.config, merge(obj{"theme": "dark"}, stranger))
	e.assertRefused(1, "c@example.com", "second")
	e.assertRefused(1, "c@example.com", "gateway")
	// add 给它起名，不改登录
	contains(t, e.ok("add", "third"), "已把在用的登录记成 third，登录为 c@example.com")
	e.same(e.read(filepath.Join(e.state, "accounts.json"))["third"], "uuid-c", "third 的编号")
}

func TestUnlistedCredentialFollowsAccount(t *testing.T) {
	t.Parallel()
	// 以后新增的令牌默认跟账号走，不留给另一个号用
	e := setup(t)
	e.write(e.creds, merge(mainCreds, obj{"futureOauth": obj{"refreshToken": "f"}}))
	e.ok("second")
	if _, ok := e.read(e.creds)["futureOauth"]; ok {
		t.Error("新令牌留在了 second 的凭据里")
	}
	if _, ok := e.read(e.slot("claude"))["credentials"].(obj)["futureOauth"]; !ok {
		t.Error("新令牌没有存进默认账号的存档")
	}
}

func TestStatusListsCurrentAndParked(t *testing.T) {
	t.Parallel()
	e := setup(t)
	out := e.ok()
	contains(t, out, "当前账号：claude，登录为 a@example.com")
	contains(t, out, "已存档：second，登录为 b@example.com")
}

func TestSameAccountChangesNothing(t *testing.T) {
	t.Parallel()
	e := setup(t)
	contains(t, e.ok("claude"), "已是 claude")
	e.same(e.read(e.creds), mainCreds, "凭据")
	e.same(e.read(e.config), mainConfig, "配置")
	if !e.exists(e.slot("second")) {
		t.Error("second 的存档没了")
	}
}

func TestRunningClaudeBlocksSwitch(t *testing.T) {
	t.Parallel()
	e := setup(t)
	pid, _ := e.fakeClaude("", "")
	e.run("second")
	contains(t, e.stderr, "  "+strconv.Itoa(pid)+" ")
	e.assertRefused(1, "还有 Claude Code 进程在跑", "second")
	var got statusReport
	if err := json.Unmarshal([]byte(e.ok("--json")), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Running) != 1 || !strings.HasPrefix(got.Running[0], strconv.Itoa(pid)+" ") {
		t.Errorf("running = %q", got.Running)
	}
}

func TestUnreadableProcBlocksSwitch(t *testing.T) {
	t.Parallel()
	// 读不了 /proc（不是 Linux，或 procfs 没挂上）时确认不了有没有进程，拒绝，不当作没有
	e := setup(t)
	e.proc = filepath.Join(e.home, "no-proc")
	e.assertRefused(2, "确认不了有没有 Claude Code 在跑", "second")
	e.assertRefused(2, "确认不了有没有 Claude Code 在跑", "--json")
}

func TestClaudeOnOtherConfigDirDoesNotBlock(t *testing.T) {
	t.Parallel()
	e := setup(t)
	e.fakeClaude("", "", "CLAUDE_CONFIG_DIR="+filepath.Join(e.home, "batch"))
	e.fakeClaude("", "claude", "HOME="+filepath.Join(e.home, "other-user"))
	e.ok("second")
	if e.current() != "second" {
		t.Errorf("current = %s", e.current())
	}
}

func TestDesktopReplacedAndNamedExecutablesBlockSwitch(t *testing.T) {
	t.Parallel()
	e := setup(t)
	cases := []struct{ where, name string }{
		{".claude/remote/ccd-cli", ""},
		{"opt/node/bin", "claude"}, // 其他安装方式按进程名认
	}
	for _, c := range cases {
		t.Run(c.where, func(t *testing.T) {
			pid, _ := e.fakeClaude(c.where, c.name)
			e.run("second")
			contains(t, e.stderr, "  "+strconv.Itoa(pid)+" ")
			e.stopFakes()
		})
	}
	// 升级后旧版本文件被删，长跑进程的 exe 显示为 "<路径> (deleted)"
	pid, exe := e.fakeClaude("", "")
	if err := os.Remove(exe); err != nil {
		t.Fatal(err)
	}
	e.run("second")
	contains(t, e.stderr, "  "+strconv.Itoa(pid)+" ")
	e.assertRefused(1, "还有 Claude Code 进程在跑", "second")
}

func TestMissingSlotLogsOutForLogin(t *testing.T) {
	t.Parallel()
	e := setup(t)
	e.recordAccounts()
	if err := os.Remove(e.slot("second")); err != nil {
		t.Fatal(err)
	}
	contains(t, e.ok("second"), "/login 登录 second")
	e.same(e.read(e.creds), sharedCreds, "凭据")
	e.same(e.read(e.config), obj{"projects": mainConfig["projects"], "theme": "dark"}, "配置")
	if e.current() != "second" || !e.exists(e.slot("claude")) {
		t.Errorf("current = %s，默认账号存档在 = %v", e.current(), e.exists(e.slot("claude")))
	}
}

func TestDefaultAccountIsAlwaysATarget(t *testing.T) {
	t.Parallel()
	// 从 second 起步、默认账号从没登录过：claude 照样能换过去（退出登录后 /login），--json 在它登录过之前不列它
	e := setup(t)
	e.ok("second")
	if err := os.Remove(e.slot("claude")); err != nil {
		t.Fatal(err)
	}
	e.write(filepath.Join(e.state, "accounts.json"), obj{"second": "uuid-b"})
	var got statusReport
	if err := json.Unmarshal([]byte(e.ok("--json")), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Accounts) != 1 || got.Accounts[0].Name != "second" {
		t.Errorf("accounts = %+v", got.Accounts)
	}
	contains(t, e.ok("claude"), "/login 登录 claude")
	if e.current() != "claude" || !e.exists(e.slot("second")) {
		t.Errorf("current = %s，second 的存档在 = %v", e.current(), e.exists(e.slot("second")))
	}
}

func TestLoginAlreadyOnTargetOnlyFixesRecord(t *testing.T) {
	t.Parallel()
	// 用 /login 直接登进了 second：在用的是新令牌，second 的存档已作废
	e := setup(t)
	e.write(e.config, merge(mainConfig, secondConfig))
	beforeCreds, beforeConfig := e.read(e.creds), e.read(e.config)
	contains(t, e.ok(), "实际登录的是 second")
	e.ok("second")
	if e.current() != "second" {
		t.Errorf("current = %s", e.current())
	}
	e.same(e.read(e.creds), beforeCreds, "凭据")
	e.same(e.read(e.config), beforeConfig, "配置")
	if e.exists(e.slot("second")) || e.exists(e.slot("claude")) {
		t.Error("留下了存档")
	}
}

func TestLoginIntoOtherAccountIsParkedUnderItsName(t *testing.T) {
	t.Parallel()
	// 记录是默认账号，实际用 /login 登进了 second 且 second 没有存档：换回默认账号时把 second 的令牌存成 second
	e := setup(t)
	e.recordAccounts()
	if err := os.Remove(e.slot("second")); err != nil {
		t.Fatal(err)
	}
	newCreds := obj{"claudeAiOauth": obj{"refreshToken": "b-new"}}
	e.write(e.creds, merge(sharedCreds, newCreds))
	e.write(e.config, merge(obj{"theme": "dark"}, secondConfig))
	e.ok("claude")
	e.same(e.read(e.slot("second"))["credentials"], newCreds, "second 的存档")
	if e.exists(e.slot("claude")) {
		t.Error("默认账号存档还在")
	}
	e.same(e.read(e.creds), sharedCreds, "凭据")
	if e.current() != "claude" {
		t.Errorf("current = %s", e.current())
	}
}

func TestUnknownAccountChangesNothing(t *testing.T) {
	t.Parallel()
	e := setup(t)
	e.recordAccounts()
	e.write(e.config, merge(mainConfig, obj{"oauthAccount": obj{"accountUuid": "uuid-c", "emailAddress": "c@example.com"}}))
	e.assertRefused(1, "c@example.com", "second")
}

func TestInterruptedSwitchBlocksUntilHandled(t *testing.T) {
	t.Parallel()
	e := setup(t)
	e.writeJSON = func(path string, data any, mode os.FileMode) error {
		if filepath.Base(path) == ".credentials.json" {
			return errors.New("disk full")
		}
		return writeJSON(path, data, mode)
	}
	if code := e.run("second"); code != 2 {
		t.Errorf("写凭据失败时退出 %d，期望 2", code)
	}
	e.writeJSON = nil
	if !e.exists(e.pending) {
		t.Fatal("没有留下 pending.json")
	}
	contains(t, e.ok(), "中途出错")
	// 之后照常用了 claude，令牌续期变新：再跑也不能拿计划里的旧令牌覆盖它
	creds := e.read(e.creds)
	creds["claudeAiOauth"].(obj)["refreshToken"] = "a-refresh-2"
	e.write(e.creds, creds)
	for _, target := range []string{"second", "claude"} {
		e.assertRefused(1, "中途出错", target)
	}
	e.assertRefused(1, "中途出错", "remove", "second")
}

func TestTokensWithoutIdentityChangeNothing(t *testing.T) {
	t.Parallel()
	// 没有身份时，claudeAiOauth 以外残留的账号凭据同样不能丢
	e := setup(t)
	e.write(e.config, obj{"theme": "dark"})
	for _, creds := range []obj{mainCreds, merge(sharedCreds, obj{"designOauth": obj{"refreshToken": "d"}})} {
		t.Run(strconv.Itoa(len(creds)), func(t *testing.T) {
			e.write(e.creds, creds)
			e.assertRefused(1, "认不出", "second")
		})
	}
}

func TestSlotWithoutIdentityIsNotLoaded(t *testing.T) {
	t.Parallel()
	e := setup(t)
	for _, credentials := range []obj{secondCreds, {"designOauth": obj{"refreshToken": "d"}}} {
		t.Run(strconv.Itoa(len(credentials)), func(t *testing.T) {
			e.write(e.slot("second"), obj{"credentials": credentials, "config": obj{}})
			e.assertRefused(1, "没有账号身份", "second")
		})
	}
}

func TestLoggedOutLoadsTargetWithoutParking(t *testing.T) {
	t.Parallel()
	e := setup(t)
	e.write(e.creds, sharedCreds)
	e.write(e.config, obj{"theme": "dark"})
	contains(t, e.ok(), "当前账号：claude，未登录")
	contains(t, e.ok("second"), "已换到 second，登录为 b@example.com")
	e.same(e.read(e.creds), merge(sharedCreds, secondCreds), "凭据")
	if e.exists(e.slot("claude")) {
		t.Error("未登录时不该存档")
	}
}

func TestBusyLockChangesNothing(t *testing.T) {
	t.Parallel()
	e := setup(t)
	if err := os.Mkdir(filepath.Join(e.home, ".claude.json.lock"), 0o755); err != nil {
		t.Fatal(err)
	}
	e.lockTimeout = 0
	e.assertRefused(2, "一直被占用", "second")
	if e.exists(filepath.Join(e.home, ".claude/.oauth_refresh.lock")) {
		t.Error("先取的锁没放掉")
	}
}

// 拿着锁的进程被强行结束时留下的锁：修改时间早于 Claude Code 判它过期的时限就接管，换号照做，做完放掉；
// 没到时限的照样当作有人拿着，等到时限为止后拒绝。
func TestStaleLocksAreTakenOver(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		lock string
		age  time.Duration
		took bool
	}{
		{"配置锁过了 10 秒", ".claude.json.lock", 11 * time.Second, true},
		{"配置锁刚建", ".claude.json.lock", 0, false},
		{"续期锁过了 60 秒", ".claude/.oauth_refresh.lock", 61 * time.Second, true},
		{"续期锁 30 秒前", ".claude/.oauth_refresh.lock", 30 * time.Second, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			e := setup(t)
			dir := filepath.Join(e.home, c.lock)
			if err := os.Mkdir(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			old := time.Now().Add(-c.age)
			if err := os.Chtimes(dir, old, old); err != nil {
				t.Fatal(err)
			}
			e.lockTimeout = 0
			if !c.took {
				e.assertRefused(2, "一直被占用", "second")
				if !e.exists(dir) {
					t.Error("没过期的锁不该删")
				}
				return
			}
			e.ok("second")
			e.same(e.read(e.creds), merge(sharedCreds, secondCreds), "凭据")
			for _, l := range []string{".claude.json.lock", ".claude/.oauth_refresh.lock"} {
				if e.exists(filepath.Join(e.home, l)) {
					t.Errorf("%s 用完没放掉", l)
				}
			}
		})
	}
}

func TestCommandLineUsageError(t *testing.T) {
	t.Parallel()
	e := setup(t)
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(self, "add")
	cmd.Env = append(os.Environ(), "HOME="+e.home, "CLAUDE_SWITCH_TEST_MAIN=1")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	cmd.Stdout = io.Discard
	err = cmd.Run()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 2 {
		t.Errorf("退出 %v，期望 2", err)
	}
	contains(t, stderr.String(), "用法")
	contains(t, e.ok("--help"), "用法")
}

// login 模拟用 /login 登进了编号为 uuid 的号。
func (e *env) login(uuid, email, refresh string) {
	e.t.Helper()
	e.write(e.creds, merge(sharedCreds, obj{"claudeAiOauth": obj{"refreshToken": refresh, "subscriptionType": "max", "refreshTokenExpiresAt": 1900000000000}}))
	config := e.read(e.config)
	config["oauthAccount"] = obj{"accountUuid": uuid, "emailAddress": email}
	e.write(e.config, config)
}

func TestAddParksLiveAndRecordsTheNewLoginLater(t *testing.T) {
	t.Parallel()
	e := setup(t)
	contains(t, e.ok("add", "work"), "/login 登录 work")
	if e.current() != "work" || !e.exists(e.slot("claude")) {
		t.Fatalf("current = %s，默认账号存档在 = %v", e.current(), e.exists(e.slot("claude")))
	}
	e.same(e.read(e.creds), sharedCreds, "凭据")
	e.login("uuid-w", "w@example.com", "w-refresh")
	contains(t, e.ok(), "当前账号：work，登录为 w@example.com")
	e.ok("second")
	e.same(e.read(filepath.Join(e.state, "accounts.json"))["work"], "uuid-w", "work 的编号")
	e.same(e.read(e.slot("work"))["credentials"].(obj)["claudeAiOauth"].(obj)["refreshToken"], "w-refresh", "work 的存档")
	contains(t, e.ok(), "已存档：work，登录为 w@example.com")
	e.ok("work")
	if e.current() != "work" || e.exists(e.slot("work")) {
		t.Errorf("current = %s，work 的存档还在 = %v", e.current(), e.exists(e.slot("work")))
	}
}

func TestAddNamesAnUnknownLoginWithoutTouchingIt(t *testing.T) {
	t.Parallel()
	// 记过两个号，又用 /login 登进了第三个：add 只给它起名，不改登录
	e := setup(t)
	e.recordAccounts()
	e.login("uuid-c", "c@example.com", "c-refresh")
	e.assertRefused(1, "claude-switch add", "second")
	creds, config := e.read(e.creds), e.read(e.config)
	contains(t, e.ok("add", "third"), "已把在用的登录记成 third，登录为 c@example.com")
	e.same(e.read(e.creds), creds, "凭据")
	e.same(e.read(e.config), config, "配置")
	e.same(e.read(filepath.Join(e.state, "accounts.json"))["third"], "uuid-c", "third 的编号")
	e.ok("second")
	if !e.exists(e.slot("third")) {
		t.Error("third 没有存档")
	}
}

func TestAddAndSwitchCheckTheName(t *testing.T) {
	t.Parallel()
	e := setup(t)
	e.assertRefused(2, "claude-switch add work", "work")
	for _, args := range [][]string{{"add", "second"}, {"add", "claude"}} {
		e.assertRefused(2, "已经有叫", args...)
	}
	for _, args := range [][]string{{"add", "accounts"}, {"add", "pending"}, {"add", "Work"}, {"remove", "add"}, {"x", "y"}, {"version", "x"}} {
		e.assertRefused(2, "用法", args...)
	}
}

func TestRemoveDeletesOnlyAParkedAccount(t *testing.T) {
	t.Parallel()
	e := setup(t)
	e.recordAccounts()
	for name, want := range map[string]string{"claude": "在用的账号", "work": "没有叫 work"} {
		code := 1
		if name == "work" {
			code = 2
		}
		e.assertRefused(code, want, "remove", name)
	}
	contains(t, e.ok("remove", "second"), "claude-switch add second")
	if e.exists(e.slot("second")) {
		t.Error("存档还在")
	}
	if _, ok := e.read(filepath.Join(e.state, "accounts.json"))["second"]; ok {
		t.Error("记录还在")
	}
	e.same(e.read(e.creds), mainCreds, "凭据")
	// 默认账号删掉以后不用 add，直接换回去
	e.ok("add", "work")
	contains(t, e.ok("remove", "claude"), "以后要用就 claude-switch claude 后")
}

func TestJSONStatusHasIdentitiesButNoTokens(t *testing.T) {
	t.Parallel()
	e := setup(t)
	settings := e.target("gateway", gatewaySettings)
	e.ok("add", "work")
	e.login("uuid-w", "w@example.com", "w-refresh")
	out := e.ok("--json")
	for _, secret := range []string{"a-token", "a-refresh", "a-design", "b-token", "b-refresh", "w-refresh", "mcp", "llm-gateway"} {
		if strings.Contains(out, secret) {
			t.Errorf("--json 里有令牌或设置的内容 %q", secret)
		}
	}
	var got statusReport
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("不是 JSON：%v\n%s", err, out)
	}
	if got.Current != "work" || got.Live == nil || got.Live.Name != "work" || got.Live.Subscription != "max" || got.Pending || len(got.Running) != 0 ||
		got.Live.RefreshExpiresAt == nil || *got.Live.RefreshExpiresAt != 1900000000000 {
		t.Errorf("current/live/pending/running 不对：%+v", got)
	}
	var names []string
	for _, a := range got.Accounts {
		names = append(names, a.Name+"|"+a.Email+"|"+strconv.FormatBool(a.Current)+"|"+strconv.FormatBool(a.Parked))
	}
	e.same(names, []string{"claude|a@example.com|false|true", "second|b@example.com|false|true", "work|w@example.com|true|false"}, "账号")
	e.same(got.Targets, []targetInfo{{Name: "gateway", Settings: settings}}, "targets")

	// 登进了没记过的号：live 的 name 为空，不列进 accounts
	e.ok("second")
	e.login("uuid-x", "x@example.com", "x-refresh")
	if err := json.Unmarshal([]byte(e.ok("--json")), &got); err != nil {
		t.Fatal(err)
	}
	if got.Live == nil || got.Live.Name != "" || got.Live.Email != "x@example.com" {
		t.Errorf("live = %+v", got.Live)
	}
}

// TestJSONFieldNames 固定 --json 的字段名：别的程序（如手机客户端的账号页）按名字读。
func TestJSONFieldNames(t *testing.T) {
	t.Parallel()
	e := setup(t)
	e.recordAccounts()
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(e.ok("--json")), &raw); err != nil {
		t.Fatal(err)
	}
	var top []string
	for k := range raw {
		top = append(top, k)
	}
	sort.Strings(top)
	e.same(top, []string{"accounts", "current", "live", "pending", "running", "targets"}, "顶层字段")
	var live map[string]any
	if err := json.Unmarshal(raw["live"], &live); err != nil {
		t.Fatal(err)
	}
	var fields []string
	for k := range live {
		fields = append(fields, k)
	}
	sort.Strings(fields)
	e.same(fields, []string{"current", "email", "name", "parked", "uuid"}, "账号字段")
}

func TestMarshalKeepsHTMLCharacters(t *testing.T) {
	t.Parallel()
	got, err := marshal(map[string]any{"a": "<b>&", "n": []int{1, 2}})
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"a":"<b>&","n":[1,2]}`; string(got) != want {
		t.Fatalf("marshal = %s, want %s", got, want)
	}
	if _, err := marshal(math.NaN()); err == nil {
		t.Fatal("marshal(NaN) 应报错")
	}
}
