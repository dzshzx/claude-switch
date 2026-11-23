// Package claudeswitch：本机多个 claude.ai 账号与非账号目标之间冷切换，同一时间只用一个。
//
// 用法：claude-switch            显示当前账号或目标、已存档的账号与非账号目标
//
//	claude-switch --json     同上，给程序读的 JSON：账号名、账号身份（编号、邮箱、组织）、套餐、非账号目标、
//	                         在跑的 Claude Code 进程，没有令牌
//	claude-switch <名字>     换到这个账号或非账号目标
//	claude-switch add <账号名>
//	                         加一个账号：在用的登录还没记过时，把它记成这个名字，不改登录；否则在用的号照常存档并退出，
//	                         启动 claude 后用 /login 登录新账号，下次运行时记下它的编号
//	claude-switch remove <账号名>
//	                         删掉一个不在用的账号的存档与记录（只删本机这一份，不在服务端退出登录）
//
// 名字的写法见 account.Valid。account.Default（claude）总能用作目标；别的账号先 add，之后在 accounts.json 里
// 有记录或在状态目录里有存档就能换到。
//
// 非账号目标：~/.config/claude-switch/targets/<名字>.json 是一份 Claude Code 设置（JSON 对象），有这份文件就有这个目标。
// 换到目标时在用的号照常存档并退出登录，切换记录写成目标名；claude 要带 --settings <这份文件> 启动才用上它
// （shell 的接法见 README）。目标没有存档，也不记 accountUuid。claude-switch 只确认设置文件是 JSON 对象，
// 不读其中的值，也不运行其中的命令（如 apiKeyHelper）。
//
// 只有一个配置目录 ~/.claude。换号就是把跟账号走的那部分登录状态整体换掉：
//   - ~/.claude/.credentials.json 里除 sharedCredentialKeys（MCP 服务的授权与设备身份）外的字段都跟账号走，
//     以后新增的令牌默认也跟账号走；
//   - ~/.claude.json 里的账号字段（isAccountKey：登录身份、订阅、额度与各类缓存），其余设置各号共用。
//
// 在用的号的这部分存进 ~/.local/state/claude-switch/<账号名>.json，再载入目标号的存档并删掉该存档，
// 所以每个号的令牌只有一份（在用或在存档）：令牌续期会让旧的续期凭证作废，留着旧副本切回去就要重新登录。
// 目标号没有存档时只退出在用的号，启动 claude 后用 /login 登录目标号。
//
// 在用的是哪个号按 ~/.claude.json 的 accountUuid 认：对照同目录 accounts.json 与各存档里记下的编号，
// 没见过的编号只在还没记过当前号时登记为当前号（当前是非账号目标时一律拒绝），否则拒绝，不把别的账号的令牌存进错的存档；
// 凭据里有跟账号走的字段、~/.claude.json 却没有 accountUuid 时认不出是哪个号，同样拒绝（存档同理）。
// 换号先把整份计划写进 pending.json 再落盘，做完删掉。中途出错时它留着当现场，之后每次运行都拒绝、等人核对，
// 不自动补做：补做会把这期间续期出的新令牌覆盖掉。
//
// 还有 Claude Code 进程在跑（会话、claude rc、后台进程、Desktop 远程会话；用别的配置目录的不算）就拒绝：
// 按账号统计用量的程序假定一个进程只属于一个账号，开着换号会把用量和额度读数分错账号（见 README「为什么是冷切换」）。
// 读不了 /proc 时同样拒绝。
// 名字写进同目录 current（account 包的格式），别的程序据此认一个 Claude Code 进程属于哪个账号。
//
// 退出码：0 已换好或无需换；1 有 Claude Code 进程在跑、认不出在用的账号、要删的是在用的账号，或上次换号中途出错未处理；
// 2 用法错误、没有这个账号或目标、名字已被占用、目标的设置文件不是 JSON 对象、锁被占、读不了 /proc 或读写文件出错。
package claudeswitch

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/dzshzx/claude-switch/account"
	"github.com/dzshzx/claude-switch/internal/safefile"
)

// builtinAccounts 是总能用作目标的账号，状态显示时排在最前。
var builtinAccounts = []string{account.Default}

// TargetsDir 是非账号目标的设置文件所在目录（相对 HOME），每个目标一份 <名字>.json。
const TargetsDir = ".config/claude-switch/targets"

// sharedCredentialKeys 是凭据里各号共用的字段：Claude Code 完整退出登录时只写回 coworkRemoteDevice，
// mcpOAuth 是用户自配 MCP 服务的授权，换号时留着免得逐个重新授权。
var sharedCredentialKeys = []string{"coworkRemoteDevice", "mcpOAuth"}

var accountKeys = []string{"customApiKeyResponses", "hasAvailableSubscription", "machineID", "oauthAccount", "primaryApiKey", "userID"}

var accountKeyPattern = regexp.MustCompile(`(?i)cache|oauth|account|subscription|apikey|eligib|growthbook|experiment|grove` +
	`|utilization|overage|extrausage|upsell|promo|modelaccess|additionalmodel` +
	`|orgmodel|metricsstatus|remotecontrol|replbridge|claudeai`)

// DefaultLockTimeout 是等 Claude Code 的目录锁的时限。
const DefaultLockTimeout = 10 * time.Second

const usage = "用法: claude-switch [--json | <名字> | add <账号名> | remove <账号名> | version | update]\n" +
	"名字是账号名或非账号目标名（~/" + TargetsDir + "/<名字>.json）：小写字母、数字和连字符，字母或数字开头，至多 32 个字符"

func isAccountKey(key string) bool {
	return slices.Contains(accountKeys, key) || accountKeyPattern.MatchString(key)
}

func isAccountCredential(key string) bool { return !slices.Contains(sharedCredentialKeys, key) }

func isShared(isAccount func(string) bool) func(string) bool {
	return func(k string) bool { return !isAccount(k) }
}

// failure 是要报给用户的拒绝或错误，code 是退出码。
type failure struct {
	msg  string
	code int
}

func (f *failure) Error() string { return f.msg }

func fail(code int, format string, args ...any) error {
	return &failure{msg: fmt.Sprintf(format, args...), code: code}
}

// Switcher 在 Home 下换号。WriteJSON 为空时用 writeJSON；测试用它模拟中途写失败。
// Proc 是 procfs 的挂载点，为空时是 /proc；测试用它模拟读不了 /proc。
type Switcher struct {
	Home        string
	LockTimeout time.Duration
	Stdout      io.Writer
	Stderr      io.Writer
	WriteJSON   func(path string, data any, mode os.FileMode) error
	Proc        string
}

// New 返回操作 home 的 Switcher，输出到标准输出与标准错误。
func New(home string) *Switcher {
	return &Switcher{Home: home, LockTimeout: DefaultLockTimeout, Stdout: os.Stdout, Stderr: os.Stderr}
}

func (s *Switcher) proc() string {
	if s.Proc == "" {
		return "/proc"
	}
	return s.Proc
}

// running 是用默认配置目录的 Claude Code 进程；读不了 procfs 时报错（退出码 2），不当作没有进程。
func (s *Switcher) running(home string) ([]string, error) {
	found, err := claudeProcesses(s.proc(), home)
	if err != nil {
		return nil, fail(2, "读不了 %s，确认不了有没有 Claude Code 在跑，未改动：%v", s.proc(), err)
	}
	return found, nil
}

type paths struct {
	home, credentials, config, state, accounts, pending, targets string
	refreshLock, configLock                                      lockDir
}

func newPaths(home string) paths {
	state := filepath.Join(home, account.StateDir)
	return paths{
		home:        home,
		credentials: filepath.Join(home, ".claude/.credentials.json"),
		config:      filepath.Join(home, ".claude.json"),
		state:       state,
		accounts:    filepath.Join(state, "accounts.json"),
		pending:     filepath.Join(state, "pending.json"),
		targets:     filepath.Join(home, TargetsDir),
		refreshLock: lockDir{filepath.Join(home, ".claude/.oauth_refresh.lock"), refreshLockStale},
		configLock:  lockDir{filepath.Join(home, ".claude.json.lock"), configLockStale},
	}
}

func (p paths) slot(name string) string { return filepath.Join(p.state, name+".json") }

// settings 是非账号目标 name 的设置文件。
func (p paths) settings(name string) string { return filepath.Join(p.targets, name+".json") }

// isTarget：name 是非账号目标，即目标目录里有 <name>.json 这份普通文件（可以经符号链接）。
func (p paths) isTarget(name string) bool {
	if !account.Valid(name) {
		return false
	}
	info, err := os.Stat(p.settings(name))
	return err == nil && info.Mode().IsRegular()
}

// isAccountName：能当 claude.ai 账号名（非账号目标的名字不算）。
func (p paths) isAccountName(name string) bool { return account.Valid(name) && !p.isTarget(name) }

// targetNames 是全部非账号目标，按文件名排。
func targetNames(p paths) ([]string, error) {
	entries, err := os.ReadDir(p.targets)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fail(2, "读不了 %s：%v", p.targets, err)
	}
	var names []string
	for _, entry := range entries {
		if name, ok := strings.CutSuffix(entry.Name(), ".json"); ok && p.isTarget(name) {
			names = append(names, name)
		}
	}
	return names, nil
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// readJSON 读一个 JSON 对象文件；文件不存在时是空对象。
func readJSON(path string) (*object, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return newObject(), nil
	}
	if err != nil {
		return nil, fail(2, "读不了 %s：%v", path, err)
	}
	o, err := parseObject(data)
	if errors.Is(err, errNotObject) {
		return nil, fail(2, "%s 不是 JSON 对象", path)
	}
	if err != nil {
		return nil, fail(2, "读不了 %s：%v", path, err)
	}
	return o, nil
}

// writeJSON 以两格缩进写 JSON，整份替换、落盘，权限正好是 mode。
func writeJSON(path string, data any, mode os.FileMode) error {
	text, err := pretty(data)
	if err != nil {
		return err
	}
	return safefile.WriteDurable(path, text, mode)
}

func (s *Switcher) write(path string, data any, mode os.FileMode) error {
	if s.WriteJSON != nil {
		return s.WriteJSON(path, data, mode)
	}
	return writeJSON(path, data, mode)
}

// oauthField 取 oauthAccount 里的字段；oauthAccount 不是对象或没有该字段时为 nil。
func oauthField(config *object, key string) json.RawMessage {
	acct, err := config.child("oauthAccount")
	if err != nil {
		return nil
	}
	v, _ := acct.get(key)
	return v
}

// accountUUID 是登录身份 oauthAccount.accountUuid；没有或不是字符串时为空串。
func accountUUID(config *object) string { return str(oauthField(config, "accountUuid")) }

// setUUID 把账号名 name 记成编号 uuid（写成 JSON 字符串，不转义 <、>、&）。
func setUUID(known *object, name, uuid string) {
	raw, _ := marshal(uuid) // 字符串编码不会出错
	known.set(name, raw)
}

func who(config *object) string {
	if accountUUID(config) == "" {
		return "未登录"
	}
	email := str(oauthField(config, "emailAddress"))
	if email == "" {
		return "登录为 （没有邮箱记录）"
	}
	return "登录为 " + email
}

// nameFor 是编号为 uuid 的账号名，没有为空串。
func nameFor(uuid string, known *object) string {
	if uuid == "" {
		return ""
	}
	for _, name := range known.keys {
		if str(known.vals[name]) == uuid {
			return name
		}
	}
	return ""
}

// accountNames 是已知的账号名：总能用的号，再依次是 accounts.json 里记过的、切换记录里的（刚 add、还没记下编号的号）、
// 状态目录里有存档的（按文件名排）。非账号目标的名字不算。
func accountNames(p paths, recorded *object) ([]string, error) {
	var names []string
	add := func(name string) {
		if p.isAccountName(name) && !slices.Contains(names, name) {
			names = append(names, name)
		}
	}
	for _, name := range builtinAccounts {
		add(name)
	}
	for _, name := range recorded.keys {
		add(name)
	}
	add(account.Current(p.home))
	entries, err := os.ReadDir(p.state)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fail(2, "读不了 %s：%v", p.state, err)
	}
	for _, entry := range entries {
		if name, ok := strings.CutSuffix(entry.Name(), ".json"); ok && entry.Type().IsRegular() {
			add(name)
		}
	}
	return names, nil
}

// knownAccounts 是账号名 → accountUuid（各存档里的登录身份，再叠上 accounts.json）与全部已知账号名。
func knownAccounts(p paths) (*object, []string, error) {
	recorded, err := readJSON(p.accounts)
	if err != nil {
		return nil, nil, err
	}
	names, err := accountNames(p, recorded)
	if err != nil {
		return nil, nil, err
	}
	known := newObject()
	for _, name := range names {
		slot, err := readJSON(p.slot(name))
		if err != nil {
			return nil, nil, err
		}
		config, err := slot.child("config")
		if err != nil {
			return nil, nil, fail(2, "%s 的 config 不是 JSON 对象", p.slot(name))
		}
		if uuid := accountUUID(config); uuid != "" {
			setUUID(known, name, uuid)
		}
	}
	return known.merged(recorded), names, nil
}

// part 是跟账号走的那部分登录状态：凭据与 ~/.claude.json 里的账号字段。
type part struct {
	credentials, config *object
}

func (pt part) MarshalJSON() ([]byte, error) {
	return fields{{"credentials", pt.credentials}, {"config", pt.config}}.MarshalJSON()
}

// unidentified：有跟账号走的凭据却没有账号身份时认不出是哪个号，拒绝，免得这些凭据被丢掉或载错。
func unidentified(where string, pt part) error {
	if pt.credentials.len() > 0 && accountUUID(pt.config) == "" {
		return fail(1, "%s 有登录令牌，但没有账号身份，认不出是哪个号，未改动；先手工核对这个文件", where)
	}
	return nil
}

// liveAccount 是在用的号；未登录为空串。没记过的登录先归切换记录里还没记过的当前号，adding 时再归 target（claude-switch add）。
func liveAccount(p paths, config, known *object, target string, adding bool) (string, error) {
	uuid := accountUUID(config)
	if uuid == "" {
		return "", nil
	}
	if name := nameFor(uuid, known); name != "" {
		return name, nil
	}
	current := account.Current(p.home)
	if _, recorded := known.get(current); !recorded && !p.isTarget(current) {
		return current, nil // 还没记过当前号：第一次见到的登录就是它
	}
	if adding {
		return target, nil // 当前号已有主：这个没记过的登录就是要加的号
	}
	listed := strings.Join(known.keys, "、")
	if listed == "" {
		listed = "还没有"
	}
	return "", fail(1, "在用的账号（%s）不在已记过的账号里（%s），未改动。"+
		"如果是用 /login 换了号，先在 claude 里 /logout 再运行 claude-switch %s；"+
		"如果是有意登录了一个新账号，运行 claude-switch add <账号名> 把它记下",
		who(config), listed, target)
}

// plan 是换号计划，整份写进 pending.json。
type plan struct {
	target, live string
	park         *part // 在用的号要存成的档；nil 为不存
	load         part
	accounts     *object
}

func (pl plan) MarshalJSON() ([]byte, error) {
	var live, park any
	if pl.live != "" {
		live = pl.live
	}
	if pl.park != nil {
		park = fields{{"name", pl.live}, {"slot", pl.park}}
	}
	return fields{{"target", pl.target}, {"live", live}, {"park", park}, {"load", pl.load}, {"accounts", pl.accounts}}.MarshalJSON()
}

func livePart(p paths) (part, *object, error) {
	config, err := readJSON(p.config)
	if err != nil {
		return part{}, nil, err
	}
	credentials, err := readJSON(p.credentials)
	if err != nil {
		return part{}, nil, err
	}
	return part{credentials: credentials.filter(isAccountCredential), config: config.filter(isAccountKey)}, config, nil
}

// parkedPart 读目标号的存档；没有存档时两部分都是空的。
func parkedPart(p paths, target string) (part, error) {
	slot, err := readJSON(p.slot(target))
	if err != nil {
		return part{}, err
	}
	credentials, err := slot.child("credentials")
	if err != nil {
		return part{}, fail(2, "%s 的 credentials 不是 JSON 对象", p.slot(target))
	}
	config, err := slot.child("config")
	if err != nil {
		return part{}, fail(2, "%s 的 config 不是 JSON 对象", p.slot(target))
	}
	return part{credentials: credentials, config: config}, nil
}

// checkTarget 核对要换到（adding 时是要加）的名字：非账号目标不能再当账号名，设置文件要是 JSON 对象；
// 换到的账号要已知，加的账号不能已有。
func checkTarget(p paths, target string, adding bool, known *object, names []string) error {
	_, recorded := known.get(target)
	switch {
	case p.isTarget(target) && adding:
		return fail(2, "%s 是非账号目标（%s），不能再当账号名，未改动", target, p.settings(target))
	case p.isTarget(target) && (recorded || exists(p.slot(target))):
		return fail(2, "%s 既是账号又是非账号目标（%s），未改动；给其中一个改个名字", target, p.settings(target))
	case p.isTarget(target):
		_, err := readJSON(p.settings(target))
		return err
	case adding && slices.Contains(names, target):
		return fail(2, "已经有叫 %s 的账号，未改动；换到它用 claude-switch %s", target, target)
	case !adding && !slices.Contains(names, target):
		return fail(2, "没有叫 %s 的账号或非账号目标，未改动；加新账号用 claude-switch add %s", target, target)
	}
	return nil
}

func makePlan(p paths, target string, adding bool) (plan, error) {
	live, config, err := livePart(p)
	if err != nil {
		return plan{}, err
	}
	if err := unidentified(p.credentials, live); err != nil {
		return plan{}, err
	}
	known, names, err := knownAccounts(p)
	if err != nil {
		return plan{}, err
	}
	if err := checkTarget(p, target, adding, known, names); err != nil {
		return plan{}, err
	}
	name, err := liveAccount(p, config, known, target, adding)
	if err != nil {
		return plan{}, err
	}
	if name != "" {
		setUUID(known, name, accountUUID(config))
	}
	pl := plan{target: target, live: name, load: live, accounts: known}
	if name == target {
		return pl, nil
	}
	if pl.load, err = parkedPart(p, target); err != nil {
		return plan{}, err
	}
	if err := unidentified(p.slot(target), pl.load); err != nil {
		return plan{}, err
	}
	if name != "" {
		pl.park = &live
	}
	if uuid := accountUUID(pl.load.config); uuid != "" {
		setUUID(known, target, uuid)
	}
	return pl, nil
}

// apply 按计划落盘，最后删掉 pending.json。
func (s *Switcher) apply(p paths, pl plan) error {
	if pl.park != nil {
		if err := s.write(p.slot(pl.live), pl.park, 0o600); err != nil {
			return err
		}
	}
	config, err := readJSON(p.config)
	if err != nil {
		return err
	}
	credentials, err := readJSON(p.credentials)
	if err != nil {
		return err
	}
	mode := os.FileMode(0o600)
	if info, err := os.Stat(p.config); err == nil {
		mode = info.Mode().Perm()
	}
	if err := s.write(p.config, config.filter(isShared(isAccountKey)).merged(pl.load.config), mode); err != nil {
		return err
	}
	if err := s.write(p.credentials, credentials.filter(isShared(isAccountCredential)).merged(pl.load.credentials), 0o600); err != nil {
		return err
	}
	if err := s.write(p.accounts, pl.accounts, 0o600); err != nil {
		return err
	}
	if err := account.WriteCurrent(p.home, pl.target); err != nil {
		return err
	}
	for _, path := range []string{p.slot(pl.target), p.pending} {
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	return nil
}

func interrupted(p paths) string {
	target := "未知"
	if pending, err := readJSON(p.pending); err == nil {
		if raw, ok := pending.get("target"); ok {
			target = text(raw)
		}
	}
	return fmt.Sprintf("上次换号（换到 %s）中途出错，现场留在 %s；不自动补做，先别再换号，核对处理后删掉这个文件", target, p.pending)
}

func (s *Switcher) status(p paths) error {
	current := account.Current(p.home)
	config, err := readJSON(p.config)
	if err != nil {
		return err
	}
	if p.isTarget(current) {
		fmt.Fprintf(s.Stdout, "当前目标：%s（非账号目标），claude 要带 --settings %s 启动\n", current, p.settings(current))
	} else {
		fmt.Fprintf(s.Stdout, "当前账号：%s，%s\n", current, who(config))
	}
	if exists(p.pending) {
		fmt.Fprintln(s.Stdout, interrupted(p)) // 现场待核对，下面的改正建议与存档列表都可能不准
		return nil
	}
	known, names, err := knownAccounts(p)
	if err != nil {
		return err
	}
	if live := nameFor(accountUUID(config), known); live != "" && live != current {
		fmt.Fprintf(s.Stdout, "注意：实际登录的是 %s，运行 claude-switch %s 改正记录\n", live, live)
	}
	for _, name := range names {
		if name == current || !exists(p.slot(name)) {
			continue
		}
		parked, err := parkedPart(p, name)
		if err != nil {
			return err
		}
		fmt.Fprintf(s.Stdout, "已存档：%s，%s\n", name, who(parked.config))
	}
	targets, err := targetNames(p)
	if err != nil {
		return err
	}
	for _, name := range targets {
		if name != current {
			fmt.Fprintf(s.Stdout, "非账号目标：%s（%s）\n", name, p.settings(name))
		}
	}
	return nil
}

// lockDir 是 Claude Code 写文件前取的一把 mkdir 锁（它内置的 proper-lockfile）和 Claude Code 判它过期的时限：
// 锁目录的修改时间早于「现在减 stale」就算过期（持锁的进程活着时会在这之前刷新修改时间），等锁的一方删掉它再取。
// 拿着锁的进程被强行结束（kill -9、内存不足被杀、断电）时留下的锁靠这个收回，不然会一直挡着换号。
type lockDir struct {
	path  string
	stale time.Duration
}

// Claude Code（2.1.330）给这两把锁的过期时限：~/.claude.json.lock 没指定，用加锁库默认的 10 秒；
// OAuth 续期锁指定 60 秒。
const (
	configLockStale  = 10 * time.Second
	refreshLockStale = 60 * time.Second
)

// locked 依次取 locks，跑完 run 后倒序放掉。锁被占着就等，等到 LockTimeout 为止；过期的锁当作没人拿着，删掉再取。
func (s *Switcher) locked(locks []lockDir, run func() error) error {
	if len(locks) == 0 {
		return run()
	}
	l := locks[0]
	deadline := time.Now().Add(s.LockTimeout)
	for {
		err := os.Mkdir(l.path, 0o777)
		if err == nil {
			break
		}
		if !errors.Is(err, fs.ErrExist) {
			return err
		}
		if stale(l) {
			if err := os.Remove(l.path); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return err
			}
			continue
		}
		if !time.Now().Before(deadline) {
			return fail(2, "%s 一直被占用：有 Claude Code 拿着它，或拿着它的进程刚结束（%v 没更新才算过期）；稍后再试", l.path, l.stale)
		}
		time.Sleep(50 * time.Millisecond)
	}
	defer func() { _ = os.Remove(l.path) }()
	return s.locked(locks[1:], run)
}

// stale 是锁目录已经过期：修改时间早于「现在减 l.stale」。读不到（刚被放掉）时不算。
func stale(l lockDir) bool {
	info, err := os.Stat(l.path)
	return err == nil && info.ModTime().Before(time.Now().Add(-l.stale))
}

func (s *Switcher) switchTo(p paths, target string, adding bool) error {
	if err := mkdirState(p.state); err != nil {
		return err
	}
	var pl plan
	var before string
	err := s.locked([]lockDir{p.refreshLock, p.configLock}, func() error {
		running, err := s.running(p.home)
		if err != nil {
			return err
		}
		if len(running) > 0 {
			return fail(1, "还有 Claude Code 进程在跑，先全部退出再换号：\n  %s", strings.Join(running, "\n  "))
		}
		if exists(p.pending) {
			return fail(1, "%s", interrupted(p))
		}
		before = account.Current(p.home)
		if pl, err = makePlan(p, target, adding); err != nil {
			return err
		}
		if err := s.write(p.pending, pl, 0o600); err != nil {
			return err
		}
		return s.apply(p, pl)
	})
	if err != nil {
		return err
	}
	s.report(p, pl, before, adding)
	return nil
}

func (s *Switcher) report(p paths, pl plan, before string, adding bool) {
	loaded, parked := who(pl.load.config), ""
	if pl.park != nil {
		parked = fmt.Sprintf("；%s 已存档", pl.live)
	}
	switch {
	case p.isTarget(pl.target):
		done := "已换到"
		if before == pl.target && parked == "" {
			done = "已是"
		}
		fmt.Fprintf(s.Stdout, "%s %s%s：非账号目标，claude 要带 --settings %s 启动\n", done, pl.target, parked, p.settings(pl.target))
	case adding && pl.live == pl.target:
		fmt.Fprintf(s.Stdout, "已把在用的登录记成 %s，%s\n", pl.target, loaded)
	case pl.live == pl.target:
		fmt.Fprintf(s.Stdout, "已是 %s，%s\n", pl.target, loaded)
	case pl.load.credentials.len() > 0:
		fmt.Fprintf(s.Stdout, "已换到 %s，%s%s\n", pl.target, loaded, parked)
	default:
		fmt.Fprintf(s.Stdout, "%s 没有存好的登录，现在是未登录状态：启动 claude 后用 /login 登录 %s%s\n", pl.target, pl.target, parked)
	}
}

// mkdirState 建状态目录：上级按默认权限，状态目录本身新建时为 0700。
func mkdirState(dir string) error {
	if err := os.MkdirAll(filepath.Dir(dir), 0o777); err != nil {
		return err
	}
	if err := os.Mkdir(dir, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
		return err
	}
	return nil
}

// Run 执行 claude-switch 的参数（不含程序名），返回退出码。
func (s *Switcher) Run(args []string) int {
	p := newPaths(s.Home)
	var err error
	switch {
	case len(args) == 0:
		err = s.status(p)
	case len(args) == 1 && args[0] == "--json":
		err = s.statusJSON(p)
	case len(args) == 1 && (args[0] == "-h" || args[0] == "--help"):
		fmt.Fprintln(s.Stdout, usage)
		return 0
	case len(args) == 1 && account.Valid(args[0]):
		err = s.switchTo(p, args[0], false)
	case len(args) == 2 && args[0] == "add" && account.Valid(args[1]):
		err = s.switchTo(p, args[1], true)
	case len(args) == 2 && args[0] == "remove" && account.Valid(args[1]):
		err = s.remove(p, args[1])
	default:
		fmt.Fprintln(s.Stderr, usage)
		return 2
	}
	var f *failure
	switch {
	case err == nil:
		return 0
	case errors.As(err, &f):
		fmt.Fprintf(s.Stderr, "claude-switch: %s\n", f.msg)
		return f.code
	default:
		fmt.Fprintf(s.Stderr, "claude-switch: 读写文件出错：%v\n", err)
		return 2
	}
}
