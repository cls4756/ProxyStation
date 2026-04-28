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
	// 默认入站认证
	Socks5Username string `json:"socks5Username,omitempty"`
	Socks5Password string `json:"socks5Password,omitempty"`
	HttpUsername   string `json:"httpUsername,omitempty"`
	HttpPassword   string `json:"httpPassword,omitempty"`
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
		SubscriptionAutoUpdateMode:         "off",
		SubscriptionAutoUpdateIntervalHour: 12,
		TransparentMode:                    "close",
		LanSharingEnabled:                  false,
		Socks5Port:                         20260,
		HttpPort:                           20261,
		MaxLogLines:                        500,
		MaxLogFileSize:                     2 * 1024 * 1024,
		DownloadProxy:                      "",
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
	if s.WebPassword == "" {
		s.WebPassword = s.WebPasswordOrDefault()
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
