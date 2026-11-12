package launchd

import (
	"encoding/xml"
	"strings"
	"testing"
)

func TestPlistEsXMLValidoYLlevaLoEsencial(t *testing.T) {
	exe := "/Users/caja/Library/Application Support/NovaSoftPrintAgent/novasoft-agent"
	p := Plist(exe, "/tmp/a & b/launchd.log")

	dec := xml.NewDecoder(strings.NewReader(p))
	dec.Strict = false // el DOCTYPE no lo resuelve encoding/xml
	for {
		if _, err := dec.Token(); err != nil {
			if err.Error() == "EOF" {
				break
			}
			t.Fatalf("plist no es XML válido: %v", err)
		}
	}

	for _, want := range []string{
		"<string>" + Label + "</string>",
		"<string>" + exe + "</string>",
		"<string>run</string>",
		"<key>RunAtLoad</key>\n\t<true/>",
		"<key>KeepAlive</key>\n\t<true/>",
		"<key>" + EnvMarker + "</key>",
		"a &amp; b", // escapado
	} {
		if !strings.Contains(p, want) {
			t.Errorf("plist sin %q", want)
		}
	}
}

func TestParsePrint(t *testing.T) {
	out := `gui/501/mx.novasoft.printagent = {
	active count = 1
	path = /Users/caja/Library/LaunchAgents/mx.novasoft.printagent.plist
	state = running
	program = /Users/caja/x/novasoft-agent
	pid = 4321
}`
	running, pid := parsePrint(out)
	if !running || pid != 4321 {
		t.Fatalf("got running=%v pid=%d", running, pid)
	}
	running, pid = parsePrint("state = not running\n")
	if running || pid != 0 {
		t.Fatalf("got running=%v pid=%d", running, pid)
	}
}
