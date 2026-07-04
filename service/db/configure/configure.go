package configure

import (
	"github.com/ProxyStation/proxystation/db"
)

// Configure 全局配置快照，用于导入导出
type Configure struct {
	Servers       []*ServerRaw       `json:"servers"`
	Subscriptions []*SubscriptionRaw `json:"subscriptions"`
	Groups        []*Group           `json:"groups"`
	Setting       *Setting           `json:"setting"`
	Accounts      map[string]string  `json:"accounts"`
}

func IsConfigureNotExists() bool {
	l, err := db.GetBucketLen("system")
	return err != nil || l == 0
}

func SetAccount(username, password string) error {
	return db.Set("accounts", username, password)
}

func GetPasswordOfAccount(username string) (string, error) {
	var pwd string
	err := db.Get("accounts", username, &pwd)
	return pwd, err
}

func ExistsAccount(username string) bool {
	return db.Exists("accounts", username)
}

func HasAnyAccounts() bool {
	l, err := db.GetBucketLen("accounts")
	return err == nil && l > 0
}

func SetRunning(running bool) error {
	return db.Set("system", "running", running)
}

func GetRunning() bool {
	var running bool
	_ = db.Get("system", "running", &running)
	return running
}
