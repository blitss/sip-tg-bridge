package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"time"

	"gotgcalls/bridge"
	"gotgcalls/third_party/ubot"

	"github.com/Laky-64/gologging"
	tg "github.com/amarnathcjd/gogram/telegram"
	"github.com/emiago/diago"
	"github.com/emiago/sipgo"
)

func main() {
	// Reduce verbose WebRTC/ntgcalls logging
	gologging.SetLevel(gologging.WarnLevel)
	gologging.GetLogger("ntgcalls").SetLevel(gologging.WarnLevel)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	configPath := "config.yaml"
	if len(os.Args) > 1 {
		configPath = os.Args[1]
	}

	cfg, err := bridge.LoadConfig(configPath)
	if err != nil {
		slog.Error("config error", "error", err)
		os.Exit(1)
	}

	slog.Info("app id", "id", cfg.TGAppID, "hash", cfg.TGAppHash)
	tgClient, err := tg.NewClient(tg.ClientConfig{
		AppID:   cfg.TGAppID,
		AppHash: cfg.TGAppHash,
	})
	if err != nil {
		slog.Error("telegram client init failed", "error", err)
		os.Exit(1)
	}
	if err := tgClient.Start(); err != nil {
		slog.Error("telegram client start failed", "error", err)
		os.Exit(1)
	}

	if me, err := tgClient.GetMe(); err == nil && me != nil {
		logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
		logger.Info("telegram session", "self_id", me.ID, "first_name", me.FirstName, "last_name", me.LastName, "username", me.Username)
		logger.Info("telegram target", "target_user_id", cfg.TGUserID)
	} else if err != nil {
		slog.Warn("telegram getMe failed", "error", err)
	}

	tgBridge := ubot.NewInstance(tgClient)

	ua, err := sipgo.NewUA()
	if err != nil {
		slog.Error("sip ua init failed", "error", err)
		os.Exit(1)
	}

	udpTransport := diago.Transport{
		Transport:    "udp",
		BindHost:     "0.0.0.0",
		BindPort:     cfg.SIPBindPort,
		ExternalHost: cfg.SIPExternalIP,
	}
	tcpTransport := diago.Transport{
		Transport:    "tcp",
		BindHost:     "0.0.0.0",
		BindPort:     cfg.SIPBindPort,
		ExternalHost: cfg.SIPExternalIP,
	}
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	sipBridge := diago.NewDiago(ua,
		diago.WithTransport(udpTransport),
		diago.WithTransport(tcpTransport),
		diago.WithLogger(logger),
		diago.WithMediaConfig(diago.MediaConfig{
			Codecs: bridge.SIPCodecs(cfg),
		}),
	)

	service := bridge.NewService(cfg, sipBridge, tgBridge, logger)

	tgClient.On("message:[!/.]call", func(message *tg.NewMessage) error {
		if message.SenderID() != cfg.TGUserID {
			return nil
		}
		number := strings.TrimSpace(message.Args())
		if number == "" {
			text := strings.TrimSpace(message.Text())
			parts := strings.Fields(text)
			if len(parts) > 1 {
				number = parts[1]
			}
		}
		if number == "" {
			_, err := message.Reply("Usage: /call +79991004050")
			return err
		}
		_, err := message.Reply("Dialing...")
		if err != nil {
			return err
		}
		go func() {
			if err := service.StartCallFromCommand(ctx, number); err != nil {
				logger.Warn("call command failed", "error", err, "number", number)
			}
		}()
		return nil
	})

	if cfg.SIPAuthUser != "" && cfg.SIPAuthPass != "" {
		go func() {
			recipient := bridge.SIPRegisterRecipient(cfg)
			err := sipBridge.Register(ctx, recipient, diago.RegisterOptions{
				Username:  cfg.SIPAuthUser,
				Password:  cfg.SIPAuthPass,
				ProxyHost: cfg.SIPProvider,
				Expiry:    3600 * time.Second,
			})
			if err != nil && ctx.Err() == nil {
				logger.Warn("sip registration failed", "error", err)
			}
		}()
	}

	err = service.Start(ctx)

	// Graceful shutdown
	logger.Info("shutting down...")

	// Close telegram bridge and client
	tgBridge.Close()
	tgClient.Stop()

	if err != nil && ctx.Err() == nil {
		slog.Error("bridge stopped with error", "error", err)
		os.Exit(1)
	}
	logger.Info("shutdown complete")
}
