package bridge

import (
	"net"
	"strconv"
	"strings"

	"github.com/emiago/sipgo/sip"
)

func splitHostPort(host string) (string, int) {
	host = strings.TrimSpace(host)
	if host == "" {
		return "", 0
	}
	if h, p, err := net.SplitHostPort(host); err == nil {
		port, err := strconv.Atoi(p)
		if err == nil {
			return h, port
		}
	}
	return host, 0
}

func SIPRegisterRecipient(cfg Config) sip.Uri {
	host, port := splitHostPort(cfg.SIPProvider)
	recipient := sip.Uri{
		User: cfg.SIPAuthUser,
		Host: host,
	}
	if port > 0 {
		recipient.Port = port
	}
	if cfg.SIPTransport != "" {
		recipient.UriParams = sip.HeaderParams{"transport": cfg.SIPTransport}
	}
	return recipient
}
