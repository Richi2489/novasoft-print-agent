// Package printer — conversión de payload declarativo a bytes ESC/POS.
//
// El backend publica un payload JSON con secciones (text/kv/separator/
// feed/cut/image). Esta unidad traduce esas secciones a los bytes que
// la impresora térmica entiende. No hace IO — eso lo hace
// print_windows.go.
//
// Mantener separado el "qué dibujar" del "cómo hablarle al driver"
// permite que cuando agreguemos Linux/Mac el único archivo que cambia
// es el send; este converter sirve sin modificar.
package printer

import (
	"bytes"
	"fmt"
	"log"
	"strings"

	"github.com/Richi2489/novasoft-print-agent/pkg/escpos"
)

// LineWidth — caracteres por línea a 80 mm con fuente normal.
// GHIA GTP801 y la mayoría de POS-80 usan font A (12x24) que da 48 chars,
// pero usamos 32 porque es el máximo común con font B y mantiene
// legibilidad de tickets con texto largo.
const LineWidth = 32

// Payload refleja el JSON que el backend envía. Unmarshaling directo
// con encoding/json.
type Payload struct {
	FormatVersion string    `json:"format_version"`
	PaperWidthMM  int       `json:"paper_width_mm"`
	Sections      []Section `json:"sections"`
}

// Section — un bloque renderable. Los campos son todos opcionales; el
// Type determina cuáles aplica.
type Section struct {
	Type string `json:"type"`

	// text
	Content string `json:"content,omitempty"`

	// kv
	Label string `json:"label,omitempty"`
	Value string `json:"value,omitempty"`

	// Estilos aplicables a text/kv.
	Align  string `json:"align,omitempty"`  // left|center|right
	Size   string `json:"size,omitempty"`   // normal|large|double_width|double_height|small
	Bold   bool   `json:"bold,omitempty"`
	Italic bool   `json:"italic,omitempty"` // ESC/POS no soporta italic nativo; ignorado.
	Border bool   `json:"border,omitempty"` // rodea el texto con "=" (banner).

	// image — placeholder Fase 1a.
	URL         string `json:"url,omitempty"`
	MaxWidthMM  int    `json:"max_width_mm,omitempty"`

	// feed
	Lines int `json:"lines,omitempty"`
}

// Convert toma un payload declarativo y emite los bytes ESC/POS listos
// para enviar a la impresora. Falla si encuentra un tipo de sección
// desconocido — mejor fallar ruidoso que imprimir un ticket incompleto
// sin que nadie se entere.
//
// Secuencia inicial:
//  1. ESC @ (INIT) — reset completo.
//  2. ESC t 2 (SELECT_CP850) — indica a la impresora que interprete los
//     bytes de texto como Code Page 850. Sin esto, los acentos UTF-8
//     salen rotos (ó → Ã³, ¡ → Ai, etc). Ver pkg/escpos/encoding.go.
//
// Todo el texto user-facing pasa por escpos.EncodeCP850 antes de ser
// escrito al buffer. Runes no representables caen a '?' antes que
// romper el ticket.
func Convert(p Payload) ([]byte, error) {
	var buf bytes.Buffer
	buf.Write(escpos.INIT)
	buf.Write(escpos.SELECT_CP850)

	for i := range p.Sections {
		if err := writeSection(&buf, &p.Sections[i]); err != nil {
			return nil, fmt.Errorf("seccion %d: %w", i, err)
		}
	}
	return buf.Bytes(), nil
}

// writeSection dispatcha por Type. Cada handler deja el canvas en estado
// neutral (sin bold/align/size activos) para que no haya drift entre
// secciones.
func writeSection(buf *bytes.Buffer, s *Section) error {
	switch s.Type {
	case "text":
		writeText(buf, s)
	case "kv":
		writeKV(buf, s)
	case "separator":
		writeSeparator(buf)
	case "feed":
		writeFeed(buf, s)
	case "cut":
		writeCut(buf)
	case "image":
		// Download + convert a raster ESC/POS (GS v 0). Si falla, log
		// warning y sigue — mejor un ticket sin logo que un ticket que
		// no sale.
		if s.URL == "" {
			break
		}
		maxW := s.MaxWidthMM
		if maxW == 0 {
			maxW = 40
		}
		applyAlign(buf, "center")
		logoBytes, err := LoadLogoESCPOS(s.URL, maxW)
		if err != nil {
			log.Printf("⚠️  logo no se pudo cargar (%v); imprimo sin logo", err)
		} else {
			buf.Write(logoBytes)
			buf.Write(escpos.LINE_FEED)
		}
		applyAlign(buf, "left")
	default:
		return fmt.Errorf("tipo desconocido %q", s.Type)
	}
	return nil
}

// writeText — un bloque de texto con estilos opcionales.
// Si Border=true, enmarca con "=" x LineWidth arriba y abajo (util para
// el banner "CUENTA — NO ES COMPROBANTE FISCAL").
func writeText(buf *bytes.Buffer, s *Section) {
	applyAlign(buf, s.Align)
	applySize(buf, s.Size)
	if s.Bold {
		buf.Write(escpos.BOLD_ON)
	}

	if s.Border {
		// Separadores de "=" son ASCII — no necesitan encoding, pero por
		// consistencia usamos la misma ruta.
		buf.WriteString(strings.Repeat("=", LineWidth))
		buf.Write(escpos.LINE_FEED)
		buf.Write(escpos.EncodeCP850(s.Content))
		buf.Write(escpos.LINE_FEED)
		buf.WriteString(strings.Repeat("=", LineWidth))
		buf.Write(escpos.LINE_FEED)
	} else {
		buf.Write(escpos.EncodeCP850(s.Content))
		buf.Write(escpos.LINE_FEED)
	}

	// Reset estilos — deja canvas neutro.
	resetStyles(buf)
}

// writeKV — label a la izquierda, value a la derecha, alineados a
// LineWidth chars. Si label+value > ancho, el value se imprime en la
// siguiente línea (wrap-fallback que preserva legibilidad).
func writeKV(buf *bytes.Buffer, s *Section) {
	applyAlign(buf, "left") // kv siempre izquierda para la label
	applySize(buf, s.Size)
	if s.Bold {
		buf.Write(escpos.BOLD_ON)
	}

	label := s.Label
	value := s.Value

	// Encoding CP850 antes de medir padding: en UTF-8 cada acento son 2
	// bytes mientras que en CP850 es 1 byte (que coincide con el dot de
	// impresión). Si midiéramos con len(label) sobre UTF-8, una label
	// "Atendió" (8 bytes UTF-8 pero 7 chars visibles) calcularía padding
	// demasiado corto y el value se desalinearía a la derecha.
	labelEnc := escpos.EncodeCP850(label)
	valueEnc := escpos.EncodeCP850(value)

	padding := LineWidth - len(labelEnc) - len(valueEnc)
	if padding >= 1 {
		buf.Write(labelEnc)
		buf.WriteString(strings.Repeat(" ", padding))
		buf.Write(valueEnc)
	} else {
		// No cabe en una línea — label arriba, value alineado a la
		// derecha abajo.
		buf.Write(labelEnc)
		buf.Write(escpos.LINE_FEED)
		rightPad := LineWidth - len(valueEnc)
		if rightPad < 0 {
			rightPad = 0
		}
		buf.WriteString(strings.Repeat(" ", rightPad))
		buf.Write(valueEnc)
	}
	buf.Write(escpos.LINE_FEED)

	resetStyles(buf)
}

// writeSeparator — línea de guiones de ancho LineWidth.
func writeSeparator(buf *bytes.Buffer) {
	applyAlign(buf, "left")
	buf.WriteString(strings.Repeat("-", LineWidth))
	buf.Write(escpos.LINE_FEED)
}

// writeFeed — avanza N líneas sin imprimir. Default 1 si no se especifica.
func writeFeed(buf *bytes.Buffer, s *Section) {
	n := s.Lines
	if n <= 0 {
		n = 1
	}
	buf.Write(escpos.FeedLines(n))
}

// writeCut — avanza 3 líneas para limpiar del cortador + corte parcial.
// Sin el feed, el corte se haría encima de la última línea impresa.
func writeCut(buf *bytes.Buffer) {
	buf.Write(escpos.FeedLines(3))
	buf.Write(escpos.CUT_PARTIAL)
}

// applyAlign traduce "left"/"center"/"right" a los bytes ESC a n.
// Default = left (maneja string vacío o desconocido).
func applyAlign(buf *bytes.Buffer, align string) {
	switch align {
	case "center":
		buf.Write(escpos.ALIGN_CENTER)
	case "right":
		buf.Write(escpos.ALIGN_RIGHT)
	default:
		buf.Write(escpos.ALIGN_LEFT)
	}
}

// applySize traduce "large"/"double_*"/"normal"/"small" al byte GS ! n.
// "small" se mapea a normal porque ESC/POS no tiene un size dedicado
// menor al default en font A; si hace falta más pequeño, cambiar a
// font B vía ESC M 1 — fuera del scope Fase 1a.
func applySize(buf *bytes.Buffer, size string) {
	switch size {
	case "large":
		buf.Write(escpos.SIZE_LARGE)
	case "double_width":
		buf.Write(escpos.SIZE_DOUBLE_WIDTH)
	case "double_height":
		buf.Write(escpos.SIZE_DOUBLE_HEIGHT)
	default:
		buf.Write(escpos.SIZE_NORMAL)
	}
}

// resetStyles vuelve el canvas a estado neutro (align izq, tamaño normal,
// bold off, underline off). Se llama al final de cada sección con
// estilos para evitar drift de una a otra.
func resetStyles(buf *bytes.Buffer) {
	buf.Write(escpos.SIZE_NORMAL)
	buf.Write(escpos.BOLD_OFF)
	buf.Write(escpos.UNDERLINE_OFF)
	buf.Write(escpos.ALIGN_LEFT)
}
