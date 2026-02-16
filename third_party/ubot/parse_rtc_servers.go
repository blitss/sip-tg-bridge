package ubot

import (
	"fmt"
	"gotgcalls/third_party/ntgcalls"
	"log/slog"
	"strings"

	tg "github.com/amarnathcjd/gogram/telegram"
)

func serverTypeLabel(s ntgcalls.RTCServer) string {
	if len(s.PeerTag) > 0 {
		return "reflector"
	}
	var parts []string
	if s.Stun {
		parts = append(parts, "stun")
	}
	if s.Turn {
		parts = append(parts, "turn")
	}
	if len(parts) == 0 {
		return "unknown"
	}
	return strings.Join(parts, "+")
}

func serverAddr(s ntgcalls.RTCServer) string {
	ip := s.Ipv4
	if ip == "" {
		ip = s.Ipv6
	}
	proto := "udp"
	if s.Tcp {
		proto = "tcp"
	}
	return fmt.Sprintf("%s:%d/%s", ip, s.Port, proto)
}

func parseRTCServers(connections []tg.PhoneConnection) []ntgcalls.RTCServer {
	rtcServers := make([]ntgcalls.RTCServer, len(connections))
	for i, connection := range connections {
		switch connection := connection.(type) {
		case *tg.PhoneConnectionWebrtc:
			// Prefer IPv4 to avoid IPv6/interface issues that can lead to timeouts
			// on hosts with multiple virtual NICs (docker/vm bridges).
			ipv6 := connection.Ipv6
			if connection.Ip != "" {
				ipv6 = ""
			}
			rtcServers[i] = ntgcalls.RTCServer{
				ID:       connection.ID,
				Ipv4:     connection.Ip,
				Ipv6:     ipv6,
				Username: connection.Username,
				Password: connection.Password,
				Port:     connection.Port,
				Turn:     connection.Turn,
				Stun:     connection.Stun,
			}

			slog.Info("tg: rtc server discovered",
				"type", serverTypeLabel(rtcServers[i]),
				"addr", serverAddr(rtcServers[i]),
				"ipv4", connection.Ip,
				"ipv6", connection.Ipv6,
				"port", connection.Port,
				"id", connection.ID,
			)
		case *tg.PhoneConnectionObj:
			ipv6 := connection.Ipv6
			if connection.Ip != "" {
				ipv6 = ""
			}
			rtcServers[i] = ntgcalls.RTCServer{
				ID:      connection.ID,
				Ipv4:    connection.Ip,
				Ipv6:    ipv6,
				Port:    connection.Port,
				Turn:    true,
				Tcp:     connection.Tcp,
				PeerTag: connection.PeerTag,
			}
			slog.Info("tg: rtc server discovered",
				"type", serverTypeLabel(rtcServers[i]),
				"addr", serverAddr(rtcServers[i]),
				"ipv4", connection.Ip,
				"ipv6", connection.Ipv6,
				"port", connection.Port,
				"tcp", connection.Tcp,
				"id", connection.ID,
			)
		}
	}
	return rtcServers
}
