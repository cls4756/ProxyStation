package engine

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/ProxyStation/proxystation/core/coreObj"
	"github.com/ProxyStation/proxystation/db/configure"
)

// ProbeInstance 是一个独立的探测内核子进程：只含一个本地 SOCKS 入站、
// 目标节点出站（含其前置代理链）和最小化路由。测速完成后调用 Close 立即回收。
// 它与主内核完全隔离，探测不再需要改写主配置或重启主内核。
type ProbeInstance struct {
	SocksPort int
	kernel    KernelType
	binPath   string
	tmpDir    string
	proc      *os.Process
	cancel    context.CancelFunc
	closeOnce sync.Once
}

const probeInstanceStartTimeout = 12 * time.Second

func probeKernelBin(kernel KernelType) (string, error) {
	switch kernel {
	case KernelSingbox:
		return findBin([]string{"sing-box", "sing-box.exe"}, []string{"/usr/local/bin/sing-box", "/usr/bin/sing-box"})
	case KernelXray:
		return findBin([]string{"xray", "xray.exe"}, []string{"/usr/local/bin/xray", "/usr/bin/xray"})
	default:
		return findBin([]string{"v2ray", "v2ray.exe"}, []string{"/usr/local/bin/v2ray", "/usr/bin/v2ray"})
	}
}

// StartProbeInstance 为单个节点启动一个独立探测内核并返回其 SOCKS 端口。
// 调用方必须在测速结束后调用 Close 回收进程与临时文件。
func StartProbeInstance(s *configure.ServerRaw, socksPort int) (*ProbeInstance, error) {
	if s == nil {
		return nil, fmt.Errorf("nil server")
	}
	setting := configure.GetSettingNotNil()
	kernel := probeInstanceKernel(s, setting)

	binPath, err := probeKernelBin(kernel)
	if err != nil {
		// sing-box 不可用时无从回退；xray/v2ray 相互回退
		switch kernel {
		case KernelXray:
			if binPath, err = probeKernelBin(KernelV2ray); err == nil {
				kernel = KernelV2ray
			}
		case KernelV2ray:
			if binPath, err = probeKernelBin(KernelXray); err == nil {
				kernel = KernelXray
			}
		}
		if err != nil {
			return nil, fmt.Errorf("probe kernel %v not found: %w", kernel, err)
		}
	}

	tmpDir, err := os.MkdirTemp("", "ps-probe-")
	if err != nil {
		return nil, fmt.Errorf("probe tmpdir: %w", err)
	}
	inst := &ProbeInstance{SocksPort: socksPort, kernel: kernel, binPath: binPath, tmpDir: tmpDir}

	var configPath string
	var args []string
	switch kernel {
	case KernelSingbox:
		cfg, e := buildProbeSingboxConfig(s, socksPort)
		if e != nil {
			inst.cleanupFiles()
			return nil, e
		}
		configPath = filepath.Join(tmpDir, "singbox.json")
		if e := writeJSON(cfg, configPath); e != nil {
			inst.cleanupFiles()
			return nil, e
		}
		args = []string{binPath, "run", "-c", configPath}
	default: // xray / v2ray
		cfg, e := buildProbeXrayConfig(s, socksPort)
		if e != nil {
			inst.cleanupFiles()
			return nil, e
		}
		configPath = filepath.Join(tmpDir, "config.json")
		if e := writeJSON(cfg, configPath); e != nil {
			inst.cleanupFiles()
			return nil, e
		}
		if e := ensureKernelDataFiles(binPath); e != nil {
			inst.cleanupFiles()
			return nil, fmt.Errorf("prepare probe data files: %w", e)
		}
		args = []string{binPath, "run", "--config=" + configPath}
	}

	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Dir = tmpDir
	if err := cmd.Start(); err != nil {
		cancel()
		inst.cleanupFiles()
		return nil, fmt.Errorf("start probe %v: %w", kernel, explainKernelStartError(binPath, err))
	}
	inst.proc = cmd.Process
	inst.cancel = cancel
	go func() { _ = cmd.Wait() }()

	addr := ProbeInboundListen + ":" + strconv.Itoa(socksPort)
	if err := waitPort(addr, probeInstanceStartTimeout); err != nil {
		inst.Close()
		return nil, fmt.Errorf("probe %v not ready: %w", kernel, err)
	}
	return inst, nil
}

func (pi *ProbeInstance) cleanupFiles() {
	if pi.tmpDir != "" {
		_ = os.RemoveAll(pi.tmpDir)
		pi.tmpDir = ""
	}
}

// Close 终止探测子进程并删除其临时目录。可安全多次调用。
func (pi *ProbeInstance) Close() {
	pi.closeOnce.Do(func() {
		if pi.cancel != nil {
			pi.cancel()
		}
		if pi.proc != nil {
			_ = pi.proc.Kill()
		}
		pi.cleanupFiles()
	})
}

// probeInstanceKernel 按节点协议决定使用哪个内核，比全局 selectKernel 更精准：
// 只要该节点协议要求 sing-box 就用 sing-box，否则回退到用户设置的内核倾向。
func probeInstanceKernel(s *configure.ServerRaw, setting *configure.Setting) KernelType {
	if s != nil {
		if k, ok := ProtocolKernelMap[s.Type]; ok && k == KernelSingbox {
			return KernelSingbox
		}
	}
	switch setting.KernelMode {
	case "singbox":
		return KernelSingbox
	case "xray":
		return KernelXray
	case "v2ray":
		return KernelV2ray
	}
	if _, err := findBin([]string{"xray", "xray.exe"}, []string{"/usr/local/bin/xray", "/usr/bin/xray"}); err == nil {
		return KernelXray
	}
	if _, err := findBin([]string{"v2ray", "v2ray.exe"}, []string{"/usr/local/bin/v2ray", "/usr/bin/v2ray"}); err == nil {
		return KernelV2ray
	}
	return KernelSingbox
}

// frontProxyChainRefs 沿前置代理链上溯，返回链上每一跳的节点（从直接前置到最外层）。
// 带环检测，避免 A→B→A 导致死循环。返回的 tag 即各跳的出站名。
func frontProxyChainRefs(s *configure.ServerRaw) []frontHop {
	var hops []frontHop
	seen := map[string]bool{}
	cur := ""
	if s != nil {
		cur = s.FrontProxy
	}
	for cur != "" {
		if seen[cur] {
			break
		}
		seen[cur] = true
		o := configure.GetOutbound(cur)
		if o == nil {
			break
		}
		next := resolveOutboundServerRaw(cur, o)
		if next == nil {
			break
		}
		hops = append(hops, frontHop{tag: cur, server: next})
		cur = next.FrontProxy
	}
	return hops
}

type frontHop struct {
	tag    string
	server *configure.ServerRaw
}

// buildProbeSingboxConfig 生成只含目标节点（及其前置链）的最小 sing-box 配置。
func buildProbeSingboxConfig(s *configure.ServerRaw, socksPort int) (*SingboxConfig, error) {
	const targetTag = "proxy"
	out, err := singboxServerToOutbound(s, targetTag)
	if err != nil {
		return nil, err
	}
	outbounds := []SingboxOutbound{out}
	// 前置代理链：每一跳作为独立出站，用 detour 由外向内串联。
	prevTag := targetTag
	for i, hop := range frontProxyChainRefs(s) {
		hopTag := fmt.Sprintf("front-%d", i)
		ho, err := singboxServerToOutbound(hop.server, hopTag)
		if err != nil {
			return nil, fmt.Errorf("front proxy %s: %w", hop.tag, err)
		}
		outbounds = append(outbounds, ho)
		// prevTag 的流量先经过 hopTag 出去
		for j := range outbounds {
			if outbounds[j].Tag == prevTag {
				outbounds[j].Detour = hopTag
				break
			}
		}
		prevTag = hopTag
	}
	outbounds = append(outbounds,
		SingboxOutbound{Type: "direct", Tag: "direct"},
	)
	cfg := &SingboxConfig{
		Log:      &SingboxLog{Disabled: true},
		Inbounds: []SingboxInbound{buildSingboxInbound("socks", "probe-in", ProbeInboundListen, socksPort, nil)},
		Outbounds: outbounds,
		Route: &SingboxRoute{
			Rules: []SingboxRouteRule{{InboundTag: []string{"probe-in"}, Outbound: targetTag}},
			Final: targetTag,
		},
	}
	return cfg, nil
}

// buildProbeXrayConfig 生成只含目标节点（及其前置链）的最小 xray/v2ray 配置。
func buildProbeXrayConfig(s *configure.ServerRaw, socksPort int) (*coreObj.Config, error) {
	const targetTag = "proxy"
	out, err := xrayServerToOutbound(s, targetTag)
	if err != nil {
		return nil, err
	}
	outbounds := []coreObj.OutboundObject{out}
	prevIdx := 0
	for i, hop := range frontProxyChainRefs(s) {
		hopTag := fmt.Sprintf("front-%d", i)
		ho, err := xrayServerToOutbound(hop.server, hopTag)
		if err != nil {
			return nil, fmt.Errorf("front proxy %s: %w", hop.tag, err)
		}
		if outbounds[prevIdx].StreamSettings == nil {
			outbounds[prevIdx].StreamSettings = &coreObj.StreamSettings{}
		}
		if outbounds[prevIdx].StreamSettings.Sockopt == nil {
			outbounds[prevIdx].StreamSettings.Sockopt = &coreObj.Sockopt{}
		}
		outbounds[prevIdx].StreamSettings.Sockopt.DialerProxy = hopTag
		outbounds = append(outbounds, ho)
		prevIdx = len(outbounds) - 1
	}
	outbounds = append(outbounds,
		coreObj.OutboundObject{Tag: "direct", Protocol: "freedom"},
		coreObj.OutboundObject{Tag: "block", Protocol: "blackhole"},
	)
	cfg := &coreObj.Config{
		Log:     &coreObj.Log{Loglevel: "none", Error: "none"},
		Routing: coreObj.Routing{DomainStrategy: "AsIs"},
		Inbounds: []coreObj.Inbound{
			{
				Tag: "probe-in", Port: socksPort, Listen: ProbeInboundListen, Protocol: "socks",
				Settings: &coreObj.InboundSettings{UDP: false},
			},
		},
		Outbounds: outbounds,
	}
	cfg.Routing.Rules = []coreObj.RoutingRule{
		{Type: "field", OutboundTag: targetTag, InboundTag: []string{"probe-in"}},
	}
	return cfg, nil
}
