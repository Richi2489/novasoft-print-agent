//go:build !windows

package printer

import (
	"fmt"
	"runtime"
	"strings"
)

// Pinned convierte el printer_name del config en una impresora concreta.
// Acepta:
//
//	tcp://192.168.1.50:9100   impresora de red directa
//	socket://192.168.1.50     (estilo CUPS; puerto 9100 por omisión)
//	192.168.1.50 / 192.168.1.50:9100
//	POS_80                    nombre de cola CUPS
//
// Devuelve nil si name está vacío (= autoselección).
func Pinned(name string) *Printer {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil
	}
	if addr, ok := NetworkAddr(name); ok {
		return &Printer{Name: name, OS: runtime.GOOS, Addr: addr}
	}
	return &Printer{Name: name, OS: runtime.GOOS}
}

// Describe — texto para logs/status.
func Describe(p *Printer) string {
	if p == nil {
		return "(ninguna)"
	}
	if p.Addr != "" {
		return fmt.Sprintf("red directa %s", p.Addr)
	}
	return fmt.Sprintf("cola CUPS %s", p.Name)
}
