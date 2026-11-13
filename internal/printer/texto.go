package printer

import (
	"strings"
	"unicode/utf8"
)

// PlainLines arma el ticket como renglones de texto plano de LineWidth
// columnas, a partir del MISMO payload que Convert traduce a ESC/POS.
//
// Es el respaldo de SendText (Windows, camino GDI = el de la página de
// prueba de Windows): sin negritas, tamaños ni corte, pero con la misma
// información y alineación. Las imágenes (logo) se omiten.
func PlainLines(p Payload) []string {
	var out []string
	add := func(s string) { out = append(out, s) }
	for i := range p.Sections {
		s := &p.Sections[i]
		switch s.Type {
		case "text":
			if s.Border {
				add(strings.Repeat("=", LineWidth))
			}
			for _, l := range partir(s.Content, LineWidth) {
				add(alinear(l, s.Align))
			}
			if s.Border {
				add(strings.Repeat("=", LineWidth))
			}
		case "kv":
			lw, vw := utf8.RuneCountInString(s.Label), utf8.RuneCountInString(s.Value)
			if pad := LineWidth - lw - vw; pad >= 1 {
				add(s.Label + strings.Repeat(" ", pad) + s.Value)
			} else {
				add(s.Label)
				add(alinear(s.Value, "right"))
			}
		case "separator":
			add(strings.Repeat("-", LineWidth))
		case "feed":
			n := s.Lines
			if n <= 0 {
				n = 1
			}
			for j := 0; j < n; j++ {
				add("")
			}
		case "cut":
			for j := 0; j < 4; j++ {
				add("")
			}
		}
	}
	return out
}

// partir corta por palabras a `ancho` columnas (runas, no bytes).
func partir(texto string, ancho int) []string {
	palabras := strings.Fields(texto)
	if len(palabras) == 0 {
		return []string{""}
	}
	var lineas []string
	actual := ""
	for _, p := range palabras {
		for utf8.RuneCountInString(p) > ancho {
			if actual != "" {
				lineas = append(lineas, actual)
				actual = ""
			}
			r := []rune(p)
			lineas = append(lineas, string(r[:ancho]))
			p = string(r[ancho:])
		}
		switch {
		case actual == "":
			actual = p
		case utf8.RuneCountInString(actual)+1+utf8.RuneCountInString(p) <= ancho:
			actual += " " + p
		default:
			lineas = append(lineas, actual)
			actual = p
		}
	}
	if actual != "" {
		lineas = append(lineas, actual)
	}
	return lineas
}

func alinear(l, align string) string {
	n := utf8.RuneCountInString(l)
	if n >= LineWidth {
		return l
	}
	switch align {
	case "center":
		return strings.Repeat(" ", (LineWidth-n)/2) + l
	case "right":
		return strings.Repeat(" ", LineWidth-n) + l
	}
	return l
}
