// Reconocimiento de destinos de red (impresora raw por TCP 9100). Sin
// build tag para poder probarlo en cualquier OS; sólo lo usa macOS/Linux.
package printer

import (
	"net"
	"strings"
)

// DefaultRawPort — puerto "raw"/JetDirect de las impresoras de red.
const DefaultRawPort = "9100"

// NetworkAddr reconoce un destino de red y lo normaliza a host:puerto.
// Un nombre suelto que no es IP se toma como cola CUPS (los nombres de
// cola no llevan "://" ni son direcciones IP).
func NetworkAddr(s string) (string, bool) {
	s = strings.TrimSpace(s)
	lower := strings.ToLower(s)
	for _, pre := range []string{"tcp://", "socket://", "raw://"} {
		if strings.HasPrefix(lower, pre) {
			rest := strings.TrimRight(s[len(pre):], "/")
			if rest == "" {
				return "", false
			}
			return withPort(rest), true
		}
	}
	// IP sola o IP:puerto.
	if ip := net.ParseIP(s); ip != nil {
		return net.JoinHostPort(s, DefaultRawPort), true
	}
	if host, port, err := net.SplitHostPort(s); err == nil && net.ParseIP(host) != nil && port != "" {
		return s, true
	}
	return "", false
}

func withPort(hostport string) string {
	if _, _, err := net.SplitHostPort(hostport); err == nil {
		return hostport
	}
	return net.JoinHostPort(strings.Trim(hostport, "[]"), DefaultRawPort)
}

