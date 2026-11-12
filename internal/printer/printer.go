// Tipos y heurística de selección comunes a todos los sistemas
// operativos. Lo específico de cada OS (listar y enviar bytes) vive en
// discover_*.go / print_*.go.
package printer

import "strings"

// Printer — descriptor mínimo de una impresora instalada.
// En Windows solo guardamos el nombre porque eso es lo único que
// alexbrainman usa como "handle" al abrir. En macOS/Linux Name es el
// nombre de la cola CUPS, salvo que Addr venga lleno.
type Printer struct {
	Name string
	OS   string // "windows" | "darwin" | "linux"
	// Addr — sólo macOS/Linux: "host:puerto" de una impresora de red que
	// se habla directo por TCP (puerto 9100, "raw"/JetDirect) sin pasar
	// por CUPS. Vacío = imprimir por la cola CUPS Name. Windows lo ignora.
	Addr string
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
