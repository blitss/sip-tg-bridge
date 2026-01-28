package bridge

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	defaultSIPBindPort = 5060
	defaultTransport   = "udp"
	defaultSessionName = "session"
	defaultSampleRate  = 48000
	defaultChannels    = 1
	defaultFrameMs     = 20
)

type Config struct {
	TGAppID       int32
	TGAppHash     string
	TGSession     string
	TGUserID      int64
	SIPProvider   string
	SIPBindPort   int
	SIPTransport  string
	SIPExternalIP string
	SIPAuthUser   string
	SIPAuthPass   string
	SIPAuthRealm  string

	EstablishTimeout time.Duration
	SampleRate       int
	Channels         int
	FrameDuration    time.Duration

	JitterMinPackets  uint16
	EnableEarlyMedia  bool
	DriftTargetFrames int
	DriftMaxBurst     int

	MaxActiveCalls int64
	EnableDTMF     bool
}

type yamlConfig struct {
	Telegram struct {
		AppID   int32  `yaml:"app_id"`
		AppHash string `yaml:"app_hash"`
		Session string `yaml:"session"`
		UserID  int64  `yaml:"user_id"`
	} `yaml:"telegram"`
	SIP struct {
		ProviderHost string `yaml:"provider_host"`
		BindPort     int    `yaml:"bind_port"`
		Transport    string `yaml:"transport"`
		ExternalIP   string `yaml:"external_ip"`
		AuthUser     string `yaml:"auth_user"`
		AuthPassword string `yaml:"auth_password"`
		AuthRealm    string `yaml:"auth_realm"`
		DTMFEnabled  bool   `yaml:"dtmf_enabled"`
		EarlyMedia   bool   `yaml:"early_media"`
	} `yaml:"sip"`
	Audio struct {
		SampleRate int `yaml:"sample_rate"`
		Channels   int `yaml:"channels"`
		FrameMs    int `yaml:"frame_ms"`
	} `yaml:"audio"`
	Call struct {
		EstablishTimeout string `yaml:"establish_timeout"`
		MaxActiveCalls   int64  `yaml:"max_active_calls"`
	} `yaml:"call"`
	Jitter struct {
		MinPackets        int `yaml:"min_packets"`
		DriftTargetFrames int `yaml:"drift_target_frames"`
		DriftMaxBurst     int `yaml:"drift_max_burst"`
	} `yaml:"jitter"`
}

func LoadConfig(path string) (Config, error) {
	cfg := Config{
		TGSession:        defaultSessionName,
		SIPBindPort:      defaultSIPBindPort,
		SIPTransport:     defaultTransport,
		EstablishTimeout: 25 * time.Second,
		SampleRate:       defaultSampleRate,
		Channels:         defaultChannels,
		FrameDuration:    defaultFrameMs * time.Millisecond,
		// More jitter buffering reduces packet-loss-like glitches (at cost of latency).
		JitterMinPackets: 10,
		EnableEarlyMedia: true,
		// Target backlog (10ms TG frames). Higher reduces drop-induced microstutters.
		DriftTargetFrames: 10,
		DriftMaxBurst:     2,
		EnableDTMF:        true,
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("failed to read config file: %w", err)
	}

	var yc yamlConfig
	if err := yaml.Unmarshal(data, &yc); err != nil {
		return Config{}, fmt.Errorf("failed to parse config file: %w", err)
	}

	// Telegram
	if yc.Telegram.AppID == 0 {
		return Config{}, errors.New("telegram.app_id is required")
	}
	cfg.TGAppID = yc.Telegram.AppID

	if yc.Telegram.AppHash == "" {
		return Config{}, errors.New("telegram.app_hash is required")
	}
	cfg.TGAppHash = yc.Telegram.AppHash

	if yc.Telegram.Session != "" {
		cfg.TGSession = yc.Telegram.Session
	}

	if yc.Telegram.UserID == 0 {
		return Config{}, errors.New("telegram.user_id is required")
	}
	cfg.TGUserID = yc.Telegram.UserID

	// SIP
	if yc.SIP.ProviderHost == "" {
		return Config{}, errors.New("sip.provider_host is required")
	}
	cfg.SIPProvider = yc.SIP.ProviderHost

	if yc.SIP.BindPort > 0 {
		cfg.SIPBindPort = yc.SIP.BindPort
	}

	if yc.SIP.Transport != "" {
		cfg.SIPTransport = strings.ToLower(yc.SIP.Transport)
	}
	if cfg.SIPTransport != "udp" && cfg.SIPTransport != "tcp" {
		return Config{}, fmt.Errorf("sip.transport must be 'udp' or 'tcp', got %q", cfg.SIPTransport)
	}

	cfg.SIPExternalIP = yc.SIP.ExternalIP

	cfg.SIPAuthUser = yc.SIP.AuthUser
	cfg.SIPAuthPass = yc.SIP.AuthPassword
	if (cfg.SIPAuthUser == "") != (cfg.SIPAuthPass == "") {
		return Config{}, errors.New("sip.auth_user and sip.auth_password must be set together")
	}
	cfg.SIPAuthRealm = yc.SIP.AuthRealm

	cfg.EnableDTMF = yc.SIP.DTMFEnabled
	cfg.EnableEarlyMedia = yc.SIP.EarlyMedia

	// Audio
	if yc.Audio.SampleRate > 0 {
		cfg.SampleRate = yc.Audio.SampleRate
	}
	if yc.Audio.Channels > 0 {
		cfg.Channels = yc.Audio.Channels
	}
	if cfg.Channels != 1 {
		return Config{}, fmt.Errorf("audio.channels must be 1 for now, got %d", cfg.Channels)
	}
	if yc.Audio.FrameMs > 0 {
		cfg.FrameDuration = time.Duration(yc.Audio.FrameMs) * time.Millisecond
	}

	// Call
	if yc.Call.EstablishTimeout != "" {
		timeout, err := time.ParseDuration(yc.Call.EstablishTimeout)
		if err != nil {
			return Config{}, fmt.Errorf("invalid call.establish_timeout: %w", err)
		}
		cfg.EstablishTimeout = timeout
	}
	if yc.Call.MaxActiveCalls > 0 {
		cfg.MaxActiveCalls = yc.Call.MaxActiveCalls
	}

	// Jitter
	if yc.Jitter.MinPackets > 0 {
		cfg.JitterMinPackets = uint16(yc.Jitter.MinPackets)
	}
	if yc.Jitter.DriftTargetFrames > 0 {
		cfg.DriftTargetFrames = yc.Jitter.DriftTargetFrames
	}
	if yc.Jitter.DriftMaxBurst > 0 {
		cfg.DriftMaxBurst = yc.Jitter.DriftMaxBurst
	}

	return cfg, nil
}
