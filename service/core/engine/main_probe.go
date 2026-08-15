package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"strconv"
	"time"

	xproxy "golang.org/x/net/proxy"
)

var ErrMainProbeUnavailable = errors.New("main probe inbound unavailable")

type mainProbeCredential struct {
	Username string
	Password string
}

func mainProbeCredentialForOutbound(outboundName string) mainProbeCredential {
	usernameHash := sha256.Sum256([]byte("proxystation-main-probe-user\x00" + outboundName))
	passwordHash := sha256.Sum256([]byte("proxystation-main-probe-password\x00" + outboundName))
	return mainProbeCredential{
		Username: "ps-" + hex.EncodeToString(usernameHash[:12]),
		Password: hex.EncodeToString(passwordHash[:]),
	}
}

func MainProbeAvailable(timeout time.Duration) bool {
	if !Manager.IsRunning() {
		return false
	}
	conn, err := net.DialTimeout("tcp", mainProbeAddress(), timeout)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func mainProbeAddress() string {
	return net.JoinHostPort(ProbeInboundListen, strconv.Itoa(ProbeInboundSocksPort))
}

func mainProbeContextDialer(outboundName string) (xproxy.ContextDialer, error) {
	if outboundName == "" {
		return nil, fmt.Errorf("empty outbound name")
	}
	if !Manager.IsRunning() {
		return nil, ErrMainProbeUnavailable
	}
	credential := mainProbeCredentialForOutbound(outboundName)
	dialer, err := xproxy.SOCKS5("tcp", mainProbeAddress(), &xproxy.Auth{
		User: credential.Username, Password: credential.Password,
	}, &net.Dialer{})
	if err != nil {
		return nil, err
	}
	contextDialer, ok := dialer.(xproxy.ContextDialer)
	if !ok {
		return nil, fmt.Errorf("main probe SOCKS dialer does not support context")
	}
	return contextDialer, nil
}

func DialContextViaOutbound(ctx context.Context, outboundName, network, target string) (net.Conn, error) {
	if !Manager.IsRunning() {
		return nil, ErrMainProbeUnavailable
	}
	readyCtx, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
	defer cancel()
	readyConn, err := (&net.Dialer{}).DialContext(readyCtx, "tcp", mainProbeAddress())
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrMainProbeUnavailable, err)
	}
	_ = readyConn.Close()

	dialer, err := mainProbeContextDialer(outboundName)
	if err != nil {
		return nil, err
	}
	return dialer.DialContext(ctx, network, target)
}

func DialTCPViaOutbound(outboundName, target string, timeout time.Duration) (net.Conn, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return DialContextViaOutbound(ctx, outboundName, "tcp", target)
}
