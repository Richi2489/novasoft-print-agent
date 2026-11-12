package printer

import (
	"reflect"
	"testing"
)

func TestNetworkAddr(t *testing.T) {
	casos := []struct {
		in   string
		addr string
		ok   bool
	}{
		{"tcp://192.168.1.50:9100", "192.168.1.50:9100", true},
		{"tcp://192.168.1.50", "192.168.1.50:9100", true},
		{"TCP://impresora.local:9101/", "impresora.local:9101", true},
		{"socket://10.0.0.7", "10.0.0.7:9100", true},
		{"192.168.1.50", "192.168.1.50:9100", true},
		{" 192.168.1.50:9100 ", "192.168.1.50:9100", true},
		{"POS_80", "", false},
		{"Printer_POS_80", "", false},
		{"impresora.local", "", false}, // sin esquema = nombre de cola
		{"tcp://", "", false},
		{"", "", false},
	}
	for _, c := range casos {
		addr, ok := NetworkAddr(c.in)
		if addr != c.addr || ok != c.ok {
			t.Errorf("NetworkAddr(%q) = (%q, %v), quiere (%q, %v)", c.in, addr, ok, c.addr, c.ok)
		}
	}
}

func TestParseLpstat(t *testing.T) {
	e := []byte("POS_80\nHP_LaserJet\n\nalgo con espacios\n")
	if got := parseLpstatE(e); !reflect.DeepEqual(got, []string{"POS_80", "HP_LaserJet"}) {
		t.Errorf("parseLpstatE = %v", got)
	}
	p := []byte("printer POS_80 is idle.  enabled since Tue Nov 11 10:00:00 2026\n" +
		"printer HP_LaserJet disabled since Tue Nov 11 10:00:00 2026 -\n\treason unknown\n")
	if got := parseLpstatP(p); !reflect.DeepEqual(got, []string{"POS_80", "HP_LaserJet"}) {
		t.Errorf("parseLpstatP = %v", got)
	}
}
