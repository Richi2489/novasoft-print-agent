//go:build windows

// Descubrimiento de impresoras Windows — winspool via
// github.com/alexbrainman/printer (CGO_DISABLED, puro syscall).
//
// NOTA: este archivo solo compila en Windows (build tag arriba). En
// macOS/Linux el descubrimiento vive en discover_unix.go (colas CUPS).
// El tipo Printer y la heurística SelectThermalPrinter son comunes y
// viven en printer.go.
package printer

import (
	"fmt"
	"strings"

	"github.com/alexbrainman/printer"
)

// virtualPrinterBlocklist — nombres que NO son impresoras reales.
// Comparación case-insensitive + substring (cubre variantes como
// "Microsoft Print to PDF" vs "Microsoft PDF to PDF", etc.).
//
// Criterio: si el nombre contiene cualquiera de estos, asumimos virtual.
var virtualPrinterBlocklist = []string{
	"Microsoft Print to PDF",
	"Microsoft XPS Document Writer",
	"Fax",
	"OneNote",
	"Send to OneNote",
}

// ListSystem enumera las impresoras instaladas, filtra las virtuales,
// y devuelve las reales.
func ListSystem() ([]Printer, error) {
	names, err := printer.ReadNames()
	if err != nil {
		return nil, fmt.Errorf("leyendo impresoras del sistema: %w", err)
	}
	var result []Printer
	for _, n := range names {
		if isVirtual(n) {
			continue
		}
		result = append(result, Printer{Name: n, OS: "windows"})
	}
	return result, nil
}

// isVirtual compara insensitive contra la blocklist.
func isVirtual(name string) bool {
	lower := strings.ToLower(name)
	for _, v := range virtualPrinterBlocklist {
		if strings.Contains(lower, strings.ToLower(v)) {
			return true
		}
	}
	return false
}
