//go:build windows

// Envío de bytes crudos a la impresora Windows via winspool.
//
// El flujo Win32 para "raw printing" (sin pasar por el driver GDI, que
// renderizaría el texto como gráfico) es:
//   OpenPrinter → StartDocPrinter(raw) → StartPagePrinter →
//   WritePrinter → EndPagePrinter → EndDocPrinter → ClosePrinter.
//
// alexbrainman/printer envuelve cada paso en métodos Go con error.
// Los defer garantizan que incluso si Write falla, el handle se
// cierra correctamente — sin esto, un job fallido dejaría la cola
// de Windows "ocupada" hasta que alguien reinicie el spooler.
package printer

import (
	"fmt"

	winprinter "github.com/alexbrainman/printer"
)

// SendRaw envía bytes ESC/POS directo a la impresora, sin pasar por
// el driver de renderizado. Es la forma correcta de imprimir tickets
// térmicos en Windows — si se pasara por GDI, Windows intentaría
// interpretar los bytes como gráficos y el resultado sería basura.
//
// docName es el nombre que aparece en la cola de impresión de
// Windows ("Configuración → Impresoras → [impresora] → Ver cola"). Útil
// para troubleshoot: ver si los jobs realmente salieron del agent.
func SendRaw(p *Printer, data []byte) error {
	if p == nil {
		return fmt.Errorf("printer es nil")
	}
	if len(data) == 0 {
		return fmt.Errorf("data vacia — nada que imprimir")
	}

	h, err := winprinter.Open(p.Name)
	if err != nil {
		return fmt.Errorf("abriendo impresora %q: %w", p.Name, err)
	}
	defer func() {
		_ = h.Close()
	}()

	// StartRawDocument pone el job en "modo raw" — los bytes se envían
	// a la impresora sin transformación. Critico para ESC/POS: cualquier
	// interpretación del driver destruiría los bytes de control.
	if err := h.StartRawDocument("NovaSoft Print Job"); err != nil {
		return fmt.Errorf("iniciando documento raw: %w", err)
	}
	// EndDocument es defer-able — si Write falla, aún cerramos.
	defer func() {
		_ = h.EndDocument()
	}()

	if err := h.StartPage(); err != nil {
		return fmt.Errorf("iniciando pagina: %w", err)
	}
	defer func() {
		_ = h.EndPage()
	}()

	n, err := h.Write(data)
	if err != nil {
		return fmt.Errorf("escribiendo bytes: %w", err)
	}
	if n != len(data) {
		// Short write — parcial. El ticket saldrá incompleto pero el
		// Write regresó sin error. Se considera fallo.
		return fmt.Errorf("write parcial: %d de %d bytes", n, len(data))
	}

	return nil
}
