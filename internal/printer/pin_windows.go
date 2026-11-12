//go:build windows

package printer

// NoPrintersHint — mensaje cuando Windows no tiene impresoras reales.
const NoPrintersHint = "no hay impresoras instaladas en Windows — instala el driver de tu impresora primero"

// Pinned — en Windows el agente conserva su comportamiento de siempre:
// autoselección con SelectThermalPrinter. printer_name no se usa aquí
// (fijarlo en Windows queda para otra versión, con su propia prueba).
func Pinned(name string) *Printer { return nil }

// Describe — texto para logs/status.
func Describe(p *Printer) string {
	if p == nil {
		return "(ninguna)"
	}
	return p.Name
}
