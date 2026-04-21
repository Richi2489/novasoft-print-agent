//go:build windows

// Descubrimiento de impresoras Windows — winspool via
// github.com/alexbrainman/printer (CGO_DISABLED, puro syscall).
//
// NOTA: este archivo solo compila en Windows (build tag arriba). El
// código non-Windows debe usar print_stub.go (si algún día agregamos
// agent Linux/Mac) para dar un error comprensible al listar printers.
package printer

import (
	"fmt"
	"strings"

	"github.com/alexbrainman/printer"
)

// Printer — descriptor mínimo de una impresora instalada.
// Solo guardamos el nombre porque eso es lo único que alexbrainman usa
// como "handle" al abrir. Atributos extra (driver, puerto) son accesibles
// vía GetPrinter si hacen falta después.
type Printer struct {
	Name string
	OS   string // "windows"
}

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

// thermalKeywords — indicios de que una impresora es térmica 80mm.
// Los nombres en Windows vienen del driver, que suele incluir el
// modelo o una variante con "POS"/"thermal"/"TM-"/etc.
// Orden importa: primero los más específicos (marca/modelo concreto),
// luego los genéricos.
var thermalKeywords = []string{
	// Genéricos POS 80mm (mayoría en Mercado Libre)
	"POS-80", "POS80", "POS 80", "THERMAL",
	// Marcas certificadas
	"GHIA", "GTP801",
	// Familias comunes del ecosistema
	"EPSON", "TM-", "TM ",
	"STAR", "TSP",
	"BIXOLON", "SRP-",
}

// SelectThermalPrinter elige la impresora más probable de ser térmica.
// Heurística:
//  1. Busca la primera con keyword conocido (match por substring,
//     case-insensitive).
//  2. Si no hay match, devuelve la primera de la lista — mejor que nada
//     cuando el cliente tiene un modelo exotico no catalogado.
//  3. nil si la lista está vacía.
//
// El caller (main.go) imprime qué eligió para que el admin pueda
// detectar si eligió la equivocada y pinear manualmente via config.
func SelectThermalPrinter(printers []Printer) *Printer {
	if len(printers) == 0 {
		return nil
	}
	for _, p := range printers {
		upper := strings.ToUpper(p.Name)
		for _, kw := range thermalKeywords {
			if strings.Contains(upper, kw) {
				return &p
			}
		}
	}
	// Fallback: primera de la lista.
	return &printers[0]
}
