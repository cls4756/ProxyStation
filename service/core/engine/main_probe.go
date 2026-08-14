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

func DialTCPViaOutbound(outboundName, target string, timeout time.Duration) (net.Conn, error) {
	if outboundName == "" {
		return nil, fmt.Errorf("empty outbound name")
	}
	if !Manager.IsRunning() {
		return nil, ErrMainProbeUnavailable
	}
	probeAddr := net.JoinHostPort(ProbeInboundListen, strconv.Itoa(ProbeInboundSocksPort))
	readyTimeout := timeout
	if readyTimeout > 300*time.Millisecond {
		readyTimeout = 300 * time.Millisecond
	}
	readyConn, err := net.DialTimeout("tcp", probeAddr, readyTimeout)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrMainProbeUnavailable, err)
	}
	_ = readyConn.Close()

	credential := mainProbeCredentialForOutbound(outboundName)
	dialer, err := xproxy.SOCKS5("tcp", probeAddr, &xproxy.Auth{
		User: credential.Username, Password: credential.Password,
	}, &net.Dialer{Timeout: timeout})
	if err != nil {
		return nil, err
	}
	contextDialer, ok := dialer.(xproxy.ContextDialer)
	if !ok {
		return nil, fmt.Errorf("main probe SOCKS dialer does not support context")
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return contextDialer.DialContext(ctx, "tcp", target)
}
