package configure

import (
	"strings"

	"github.com/ProxyStation/proxystation/db"
	"golang.org/x/crypto/bcrypt"
)

const (
	DefaultWebUsername = "admin"
	DefaultWebPassword = "admin"
)

type Setting struct {
	LogLevel                           string `json:"logLevel"`
	KernelMode                         string `json:"kernelMode"`
	EnableSniff                        bool   `json:"enableSniff"`
	EnableHijackDNS                    bool   `json:"enableHijackDNS"`
	DNSMode                            string `json:"dnsMode"`
	SubscriptionAutoUpdateMode         string `json:"subscriptionAutoUpdateMode"`
	SubscriptionAutoUpdateIntervalHour int    `json:"subscriptionAutoUpdateIntervalHour"`
	TransparentMode                    string `json:"transparentMode"`
	LanSharingEnabled                  bool   `json:"lanSharingEnabled"`
	Socks5Port                         int    `json:"socks5Port"`
	HttpPort                           int    `json:"httpPort"`
	MaxLogLines                        int    `json:"maxLogLines"`
	MaxLogFileSize                     int64  `json:"maxLogFileSize"`
	// 下载代理（用于下载 GitHub 文件，如 rule-set）
	DownloadProxy string `json:"downloadProxy"`
	// GitHub 加速镜像（如 https://ghproxy.com）
	GithubMirror string `json:"githubMirror,omitempty"`
	// 可用性探测目标（逗号/空格/换行分隔域名）
	ProbeTargets string `json:"probeTargets,omitempty"`
	// 分组出站后台测速间隔（秒）
	GroupRealProbeIntervalSec int `json:"groupRealProbeIntervalSec,omitempty"`
	// 分组/订阅更新后的默认测速方式：real | fast
	GroupProbeMode string `json:"groupProbeMode,omitempty"`
	// 分组出站自动切换阈值（ms），仅当当前节点比最快节点慢超过该阈值才切换
	GroupSwitchThresholdMs int `json:"groupSwitchThresholdMs,omitempty"`
	// 分组出站切换冷却时间（秒），冷却期内不重复切换
	GroupSwitchCooldownSec int `json:"groupSwitchCooldownSec,omitempty"`
	// 默认入站认证
	Socks5Username string           `json:"socks5Username,omitempty"`
	Socks5Password string           `json:"socks5Password,omitempty"`
	HttpUsername   string           `json:"httpUsername,omitempty"`
	HttpPassword   string           `json:"httpPassword,omitempty"`
	Socks5Accounts []InboundAccount `json:"socks5Accounts,omitempty"`
	HttpAccounts   []InboundAccount `json:"httpAccounts,omitempty"`
	// Web 管理端认证
	WebUsername string `json:"webUsername,omitempty"`
	WebPassword string `json:"webPassword,omitempty"`
}

func NewSetting() *Setting {
	defaultPassword, err := HashWebPassword(DefaultWebPassword)
	if err != nil {
		panic(err)
	}
	return &Setting{
		LogLevel:                           "info",
		KernelMode:                         "auto",
		EnableSniff:                        false,
		EnableHijackDNS:                    false,
		DNSMode:                            "lightweight",
		SubscriptionAutoUpdateMode:         "off",
		SubscriptionAutoUpdateIntervalHour: 12,
		TransparentMode:                    "close",
		LanSharingEnabled:                  false,
		Socks5Port:                         20260,
		HttpPort:                           20261,
		MaxLogLines:                        500,
		MaxLogFileSize:                     2 * 1024 * 1024,
		DownloadProxy:                      "",
		ProbeTargets:                       "www.gstatic.com\nwww.google.com\nwww.youtube.com\ncp.cloudflare.com\nexample.com",
		GroupRealProbeIntervalSec:          300,
		GroupProbeMode:                     "real",
		GroupSwitchThresholdMs:             100,
		GroupSwitchCooldownSec:             600,
		WebUsername:                        DefaultWebUsername,
		WebPassword:                        defaultPassword,
	}
}

func GetSettingNotNil() *Setting {
	s := NewSetting()
	_ = db.Get("system", "setting", s)
	changed := false
	if strings.TrimSpace(s.WebUsername) == "" {
		s.WebUsername = DefaultWebUsername
		changed = true
	}
	if s.KernelMode == "" {
		s.KernelMode = "auto"
		changed = true
	}
	if s.DNSMode == "" {
		s.DNSMode = "lightweight"
		changed = true
	}
	if s.WebPassword == "" {
		s.WebPassword = s.WebPasswordOrDefault()
		changed = true
	}
	if s.GroupRealProbeIntervalSec <= 0 {
		s.GroupRealProbeIntervalSec = 300
		changed = true
	}
	if s.GroupProbeMode != "real" && s.GroupProbeMode != "fast" {
		s.GroupProbeMode = "real"
		changed = true
	}
	if s.GroupSwitchThresholdMs < 0 {
		s.GroupSwitchThresholdMs = 100
		changed = true
	}
	if s.GroupSwitchCooldownSec < 0 {
		s.GroupSwitchCooldownSec = 600
		changed = true
	}
	if changed {
		_ = db.Set("system", "setting", s)
	}
	return s
}

func SetSetting(s *Setting) error {
	return db.Set("system", "setting", s)
}

func (s *Setting) WebPasswordOrDefault() string {
	hashed, err := HashWebPassword(DefaultWebPassword)
	if err != nil {
		panic(err)
	}
	return hashed
}

func IsWebPasswordHashed(password string) bool {
	return strings.HasPrefix(password, "$2a$") || strings.HasPrefix(password, "$2b$") || strings.HasPrefix(password, "$2y$")
}

func HashWebPassword(password string) (string, error) {
	hashed, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(hashed), nil
}

func VerifyWebPassword(encoded string, plain string) bool {
	if encoded == "" {
		return false
	}
	if IsWebPasswordHashed(encoded) {
		return bcrypt.CompareHashAndPassword([]byte(encoded), []byte(plain)) == nil
	}
	return encoded == plain
}

func BuiltinProxyListenAddress(setting *Setting) string {
	if setting != nil && setting.LanSharingEnabled {
		return "0.0.0.0"
	}
	return "127.0.0.1"
}

func (s *Setting) Socks5AuthAccounts() []InboundAccount {
	if s == nil {
		return nil
	}
	return normalizeInboundAccounts(s.Socks5Accounts, s.Socks5Username, s.Socks5Password)
}

func (s *Setting) HTTPAuthAccounts() []InboundAccount {
	if s == nil {
		return nil
	}
	return normalizeInboundAccounts(s.HttpAccounts, s.HttpUsername, s.HttpPassword)
}

func normalizeInboundAccounts(accounts []InboundAccount, fallbackUser, fallbackPass string) []InboundAccount {
	out := make([]InboundAccount, 0, len(accounts))
	for _, a := range accounts {
		if a.Username == "" && a.Password == "" {
			continue
		}
		out = append(out, a)
	}
	if len(out) > 0 {
		return out
	}
	if fallbackUser != "" || fallbackPass != "" {
		return []InboundAccount{{Username: fallbackUser, Password: fallbackPass}}
	}
	return nil
}
