// Package escpos — constantes de bytes ESC/POS para impresoras térmicas
// 80 mm.
//
// Referencia: Epson ESC/POS Command Reference (compatible con la mayoría
// de impresoras genéricas POS-80, GHIA GTP801, Star TSP, etc.).
//
// Los nombres siguen la convención del datasheet: ESC @ = reset,
// ESC E 1 = bold ON, GS V 1 = corte parcial, etc. Los bytes van como
// slices para que el caller los concatene con buf.Write() sin
// conversiones.
package escpos

// Bytes crudos de control. No modificar — son la ABI del hardware.
var (
	// ESC @ — reset completo: limpia buffer, restaura tipografía, alineación,
	// tamaño. Debe ser la primera instrucción en cada job.
	INIT = []byte{0x1B, 0x40}

	// Alineación (ESC a n): 0=izq, 1=centro, 2=der.
	ALIGN_LEFT   = []byte{0x1B, 0x61, 0x00}
	ALIGN_CENTER = []byte{0x1B, 0x61, 0x01}
	ALIGN_RIGHT  = []byte{0x1B, 0x61, 0x02}

	// Negrita (ESC E n): 1=on, 0=off.
	BOLD_ON  = []byte{0x1B, 0x45, 0x01}
	BOLD_OFF = []byte{0x1B, 0x45, 0x00}

	// Subrayado (ESC - n): 0=off, 1=1-dot, 2=2-dot.
	UNDERLINE_ON  = []byte{0x1B, 0x2D, 0x01}
	UNDERLINE_OFF = []byte{0x1B, 0x2D, 0x00}

	// Tamaño de caracter (GS ! n): nibble alto = alto, nibble bajo = ancho.
	// 0x00 = 1x1 (normal). 0x01 = 1x2 (doble ancho). 0x10 = 2x1 (doble alto).
	// 0x11 = 2x2 (grande).
	SIZE_NORMAL        = []byte{0x1D, 0x21, 0x00}
	SIZE_DOUBLE_WIDTH  = []byte{0x1D, 0x21, 0x01}
	SIZE_DOUBLE_HEIGHT = []byte{0x1D, 0x21, 0x10}
	SIZE_LARGE         = []byte{0x1D, 0x21, 0x11}

	// Corte. GS V 0 = corte completo, GS V 1 = corte parcial (deja una
	// pestañita por la que el usuario rompe el papel). Preferimos parcial
	// porque en muchos cortadores el completo deja el siguiente ticket sin
	// pestaña de sujeción.
	CUT_FULL    = []byte{0x1D, 0x56, 0x00}
	CUT_PARTIAL = []byte{0x1D, 0x56, 0x01}

	// LF — avanzar una línea.
	LINE_FEED = []byte{0x0A}
)

// FeedLines retorna los bytes para avanzar n líneas sin imprimir (ESC d n).
// n se clampa a [0, 255] porque es un byte sin signo en el protocolo.
// n<=0 devuelve slice vacío (no-op) en vez de enviar un avance de 0 que
// algunas firmwares interpretan como 256.
func FeedLines(n int) []byte {
	if n <= 0 {
		return nil
	}
	if n > 255 {
		n = 255
	}
	return []byte{0x1B, 0x64, byte(n)}
}
