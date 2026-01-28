package ubot

import (
	"gotgcalls/third_party/ntgcalls"
	"log/slog"

	tg "github.com/amarnathcjd/gogram/telegram"
)

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

			slog.Info("rtc server", "server", rtcServers[i])
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
			slog.Info("rtc server", "server", rtcServers[i])
		}
	}
	return rtcServers
}
