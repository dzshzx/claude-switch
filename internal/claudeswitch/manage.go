package claudeswitch

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"slices"

	"github.com/dzshzx/claude-switch/account"
)

// accountInfo 是 --json 里的一个账号：只有身份与套餐，没有令牌。
type accountInfo struct {
	Name         string `json:"name"`
	Current      bool   `json:"current"`
	Parked       bool   `json:"parked"`
	UUID         string `json:"uuid,omitempty"`
	Email        string `json:"email,omitempty"`
	Organization string `json:"organization,omitempty"`
	DisplayName  string `json:"displayName,omitempty"`
	Subscription string `json:"subscription,omitempty"`
	// Plan 是套餐的展示名（Pro、Max、Ultra…），Claude Code 照服务器的账号资料写进 ~/.claude.json；Subscription 只分 pro、max、
	// team、enterprise，同一类里的档位（Max 与 Ultra）只在这里分得开。没有时省略，调用方回落到 Subscription。
	Plan string `json:"plan,omitempty"`
	// RefreshExpiresAt 是续期凭证的到期时间（Unix 毫秒），过了就要重新 /login；凭据里没有时省略
	RefreshExpiresAt *int64 `json:"refreshExpiresAt,omitempty"`
}

// targetInfo 是 --json 里的一个非账号目标。
type targetInfo struct {
	Name     string `json:"name"`
	Settings string `json:"settings"`
}

// statusReport 是 claude-switch --json 的输出。
//
//	current   切换记录里的名字（账号名或非账号目标名）
//	live      在用的登录，未登录为 null；name 为空串表示认不出是哪个号（换号会拒绝，要先 add）
//	accounts  已知账号，顺序同状态显示；在用的那个 current 为 true，有存档的 parked 为 true
//	targets   非账号目标与它的设置文件，按名字排
//	pending   上次换号中途出错、现场待核对（此时一律拒绝换号）
//	running   用默认配置目录的 Claude Code 进程，每项「pid 命令行」；不为空时换号会拒绝
type statusReport struct {
	Current  string        `json:"current"`
	Live     *accountInfo  `json:"live"`
	Accounts []accountInfo `json:"accounts"`
	Targets  []targetInfo  `json:"targets"`
	Pending  bool          `json:"pending"`
	Running  []string      `json:"running"`
}

func describe(name string, config, credentials *object) accountInfo {
	info := accountInfo{Name: name}
	info.UUID = accountUUID(config)
	info.Email = str(oauthField(config, "emailAddress"))
	info.Organization = str(oauthField(config, "organizationName"))
	info.DisplayName = str(oauthField(config, "displayName"))
	info.Plan = str(oauthField(config, "planDisplayName"))
	if oauth, err := credentials.child("claudeAiOauth"); err == nil {
		raw, _ := oauth.get("subscriptionType")
		info.Subscription = str(raw)
		if raw, ok := oauth.get("refreshTokenExpiresAt"); ok {
			var at int64
			if json.Unmarshal(raw, &at) == nil && at > 0 {
				info.RefreshExpiresAt = &at
			}
		}
	}
	return info
}

// liveInfo 是在用的登录；未登录为 nil。认不出是哪个号时 name 为空串，这里只报告，不拒绝。
func liveInfo(p paths, known *object) (*accountInfo, error) {
	config, err := readJSON(p.config)
	if err != nil {
		return nil, err
	}
	if accountUUID(config) == "" {
		return nil, nil
	}
	credentials, err := readJSON(p.credentials)
	if err != nil {
		return nil, err
	}
	name, _ := liveAccount(p, config, known, "", false)
	live := describe(name, config.filter(isAccountKey), credentials.filter(isAccountCredential))
	live.Current = name != ""
	return &live, nil
}

// listAccounts 是 --json 的 accounts：在用的、有存档的、记过的与切换记录里的；总能用的号没登录过时不列。
func listAccounts(p paths, live *accountInfo, known *object, names []string, current string) ([]accountInfo, error) {
	out := []accountInfo{}
	for _, name := range names {
		parked := exists(p.slot(name))
		info := accountInfo{Name: name}
		switch {
		case live != nil && name == live.Name:
			info = *live
		case parked:
			slot, err := parkedPart(p, name)
			if err != nil {
				return nil, err
			}
			info = describe(name, slot.config, slot.credentials)
		default:
			if _, recorded := known.get(name); !recorded && name != current {
				continue
			}
		}
		info.Parked = parked
		out = append(out, info)
	}
	return out, nil
}

func (s *Switcher) statusJSON(p paths) error {
	known, names, err := knownAccounts(p)
	if err != nil {
		return err
	}
	report := statusReport{Current: account.Current(p.home), Targets: []targetInfo{}, Pending: exists(p.pending)}
	if report.Live, err = liveInfo(p, known); err != nil {
		return err
	}
	if report.Accounts, err = listAccounts(p, report.Live, known, names, report.Current); err != nil {
		return err
	}
	targets, err := targetNames(p)
	if err != nil {
		return err
	}
	for _, name := range targets {
		report.Targets = append(report.Targets, targetInfo{Name: name, Settings: p.settings(name)})
	}
	if report.Running, err = s.running(p.home); err != nil {
		return err
	}
	data, err := marshal(report)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(s.Stdout, "%s\n", data)
	return err
}

// remove 删掉一个不在用的账号的存档与 accounts.json 里的记录。
func (s *Switcher) remove(p paths, name string) error {
	err := s.locked([]lockDir{p.refreshLock, p.configLock}, func() error {
		if exists(p.pending) {
			return fail(1, "%s", interrupted(p))
		}
		config, err := readJSON(p.config)
		if err != nil {
			return err
		}
		known, _, err := knownAccounts(p)
		if err != nil {
			return err
		}
		recorded, err := readJSON(p.accounts)
		if err != nil {
			return err
		}
		_, isRecorded := recorded.get(name)
		switch {
		case !isRecorded && !exists(p.slot(name)) && p.isTarget(name):
			return fail(2, "%s 是非账号目标，没有存档可删，未改动；不用它了就删掉 %s", name, p.settings(name))
		case !isRecorded && !exists(p.slot(name)):
			return fail(2, "没有叫 %s 的账号存档或记录，未改动", name)
		case name == account.Current(p.home) || name == nameFor(accountUUID(config), known):
			return fail(1, "%s 是在用的账号，未改动；先换到别的号再删", name)
		}
		if isRecorded {
			if err := s.write(p.accounts, recorded.filter(func(k string) bool { return k != name }), 0o600); err != nil {
				return err
			}
		}
		if err := os.Remove(p.slot(name)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		return nil
	})
	if err != nil {
		return err
	}
	again := "claude-switch add " + name
	if p.isAccountName(name) && slices.Contains(builtinAccounts, name) {
		again = "claude-switch " + name
	}
	fmt.Fprintf(s.Stdout, "已删掉 %s 的存档与记录；它在服务端仍是登录状态，以后要用就 %s 后重新 /login\n", name, again)
	return nil
}
