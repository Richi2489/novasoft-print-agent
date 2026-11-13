package printer

import (
	"strings"
	"testing"
)

func TestPlainLines(t *testing.T) {
	p := Payload{Sections: []Section{
		{Type: "text", Content: "LA CAPITAL DEL DESPECHO", Align: "center"},
		{Type: "separator"},
		{Type: "kv", Label: "Mesa:", Value: "105"},
		{Type: "text", Content: "1 x Kilo de árabe con queso y tortillas aparte"},
		{Type: "cut"},
	}}
	l := PlainLines(p)
	if !strings.HasPrefix(l[0], "    LA CAPITAL DEL DESPECHO") {
		t.Fatalf("centrado: %q", l[0])
	}
	if l[1] != strings.Repeat("-", 32) {
		t.Fatalf("sep: %q", l[1])
	}
	if !strings.HasPrefix(l[2], "Mesa:") || !strings.HasSuffix(l[2], "105") || len([]rune(l[2])) != 32 {
		t.Fatalf("kv: %q", l[2])
	}
	for _, x := range l {
		if len([]rune(x)) > 32 {
			t.Fatalf("línea larga: %q", x)
		}
	}
}
