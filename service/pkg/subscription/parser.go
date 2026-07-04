// subscription 包负责解析各种格式的订阅内容，统一输出 []ServerRaw
package subscription

import (
	"fmt"

	"github.com/ProxyStation/proxystation/db/configure"
)

// Parser 订阅解析器接口
type Parser interface {
	// Detect 检测内容是否符合该格式
	Detect(content []byte) bool
	// Parse 解析内容，返回节点列表
	Parse(content []byte) ([]configure.ServerRaw, error)
	// Format 返回格式名称
	Format() configure.SubscriptionFormat
}

var parsers = []Parser{
	&SingboxParser{},
	&ClashParser{},
	&V2rayParser{}, // 放最后，因为它的 Detect 最宽松
}

// Parse 自动检测格式并解析
func Parse(content []byte, hint configure.SubscriptionFormat) ([]configure.ServerRaw, configure.SubscriptionFormat, error) {
	if hint != configure.FormatAuto && hint != "" {
		for _, p := range parsers {
			if p.Format() == hint {
				servers, err := p.Parse(content)
				return servers, hint, err
			}
		}
	}
	// 自动检测
	for _, p := range parsers {
		if p.Detect(content) {
			servers, err := p.Parse(content)
			return servers, p.Format(), err
		}
	}
	return nil, "", fmt.Errorf("unsupported subscription format")
}
