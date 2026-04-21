package escpos

import (
	"bytes"

	"golang.org/x/text/encoding/charmap"
)

// Código page default para impresoras térmicas 80 mm: CP850 (Latin 1
// multilingüe). Cubre español mexicano con acentos, ñ, ¡, ¿, € etc.
// Usado en La Capital (GHIA GTP801). Si alguna impresora no lo soporta,
// exponer como config del agent en el futuro.
//
// Ref: Epson ESC/POS docs "ESC t n" — selecciona la tabla de caracteres
// que la impresora usa para interpretar los bytes que le envíen.
//   n = 2 → PC850 (Multilingual Latin I)
var SELECT_CP850 = []byte{0x1B, 0x74, 0x02}

// EncodeCP850 transforma un string UTF-8 a bytes CP850 listos para enviar
// a la impresora con SELECT_CP850 activo.
//
// Runas no representables en CP850 (emojis, símbolos exóticos) se
// reemplazan por '?'. Mejor un ticket con un caracter perdido que un
// ticket que no sale porque el encoder devolvió error.
//
// Ejemplo: "Atendió" → bytes CP850 que incluyen 0xA2 para 'ó'.
// Con UTF-8 crudo la 'ó' iría como 0xC3 0xB3 y la impresora mostraría
// "AtendiÃ³" (leyendo cada byte como CP437).
func EncodeCP850(s string) []byte {
	cp := charmap.CodePage850

	// Fast path — strings 100% representables (la mayoría: ASCII + tildes
	// + ñ + signos de puntuación). Un solo round-trip por el encoder.
	enc := cp.NewEncoder()
	if out, err := enc.Bytes([]byte(s)); err == nil {
		return out
	}

	// Slow path: rune-por-rune con fallback a '?'. Se dispara solo si
	// hubo algún caracter fuera del set (emojis, scripts no-latinos).
	var buf bytes.Buffer
	for _, r := range s {
		if r < 0x80 {
			// ASCII 7-bit coincide byte-por-byte entre UTF-8 y CP850.
			buf.WriteByte(byte(r))
			continue
		}
		e := cp.NewEncoder()
		b, err := e.Bytes([]byte(string(r)))
		if err != nil || len(b) == 0 {
			buf.WriteByte('?')
		} else {
			buf.Write(b)
		}
	}
	return buf.Bytes()
}
