package configure

import (
	"encoding/json"
	"fmt"
	"time"
)

// InboundSubscription 将一个或多个 HTTP/SOCKS/VMess 入站发布为客户端订阅。
type InboundSubscription struct {
	ID         string                    `json:"id"`
	Name       string                    `json:"name"`
	Token      string                    `json:"token"`
	Host       string                    `json:"host"`
	Format     string                    `json:"format"`
	InboundIDs []string                  `json:"inboundIds"`
	Rules      []InboundSubscriptionRule `json:"rules,omitempty"`
	CreatedAt  time.Time                 `json:"createdAt"`
	UpdatedAt  time.Time                 `json:"updatedAt"`
}

// InboundSubscriptionRule 是发布给客户端的 sing-box 路由规则。
type InboundSubscriptionRule struct {
	ID                string   `json:"id,omitempty"`
	Enabled           bool     `json:"enabled"`
	Name              string   `json:"name,omitempty"`
	Domains           []string `json:"domains,omitempty"`
	IPs               []string `json:"ips,omitempty"`
	Ports             string   `json:"ports,omitempty"`
	Protocol          string   `json:"protocol,omitempty"`
	Action            string   `json:"action"` // proxy | direct | block
	OutboundInboundID string   `json:"outboundInboundId,omitempty"`
}

func Bytes2InboundSubscription(b []byte) (*InboundSubscription, error) {
	var value InboundSubscription
	if err := json.Unmarshal(b, &value); err != nil {
		return nil, fmt.Errorf("Bytes2InboundSubscription: %w", err)
	}
	return &value, nil
}

func GetInboundSubscriptions() []InboundSubscription {
	return loadInboundSubscriptions()
}

func AppendInboundSubscription(value *InboundSubscription) error {
	return appendInboundSubscription(value)
}

func SetInboundSubscription(index int, value *InboundSubscription) error {
	return setInboundSubscription(index, value)
}

func DeleteInboundSubscription(index int) error {
	if index < 0 || index >= len(loadInboundSubscriptions()) {
		return fmt.Errorf("index %d out of range", index)
	}
	return deleteInboundSubscription(index)
}
