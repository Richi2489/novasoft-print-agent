// Parseo de la salida de lpstat (CUPS). Sin build tag para poder
// probarlo en cualquier OS; sólo lo usa discover_unix.go.
package printer

import (
	"bufio"
	"bytes"
	"strings"
)

func parseLpstatE(out []byte) []string {
	var names []string
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		n := strings.TrimSpace(sc.Text())
		if n == "" || strings.Contains(n, " ") {
			continue // los nombres de cola no llevan espacios
		}
		names = append(names, n)
	}
	return names
}

func parseLpstatP(out []byte) []string {
	var names []string
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) >= 2 && f[0] == "printer" {
			names = append(names, f[1])
		}
	}
	return names
}

