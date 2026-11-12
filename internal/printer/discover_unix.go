//go:build !windows

// Descubrimiento de impresoras en macOS/Linux — colas de CUPS vía lpstat.
//
// macOS trae CUPS de fábrica: cada impresora que se agrega en
// Ajustes del Sistema → Impresoras y escáneres es una cola CUPS, y su
// "nombre de cola" (con guiones bajos en vez de espacios) es lo que
// acepta `lp -d`. No hace falta CGO: basta con lpstat/lp, que vienen con
// el sistema.
package printer

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// lpstatTimeout — lpstat contesta en milisegundos; si tarda más es que
// cupsd está colgado y es mejor decirlo que bloquear el arranque.
const lpstatTimeout = 10 * time.Second

// NoPrintersHint — mensaje cuando no hay ninguna cola CUPS.
const NoPrintersHint = "no hay impresoras en el sistema — agrega la térmica en Ajustes del Sistema → Impresoras y escáneres, o fija una impresora de red con 'novasoft-agent printer set 192.168.1.50:9100'"

// lpstatFn ejecuta lpstat con locale C (la salida de `lpstat -p` está
// traducida y no se podría parsear en un Mac en español). Variable para
// los tests.
var lpstatFn = func(args ...string) ([]byte, []byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), lpstatTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "lpstat", args...)
	cmd.Env = append(os.Environ(), "LC_ALL=C", "LANG=C")
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	err := cmd.Run()
	return out.Bytes(), errb.Bytes(), err
}

// ListSystem enumera las colas CUPS. Una máquina sin colas NO es error
// (puede imprimir por red a IP:9100): devuelve lista vacía.
func ListSystem() ([]Printer, error) {
	// `lpstat -e` (CUPS ≥ 1.7, todos los macOS modernos) lista un nombre
	// de destino por línea, sin traducir.
	out, stderr, err := lpstatFn("-e")
	if err == nil {
		return toPrinters(parseLpstatE(out)), nil
	}
	if errors.Is(err, exec.ErrNotFound) {
		// Linux mínimo sin CUPS: sólo funcionará la impresión por red.
		return nil, nil
	}
	if sinDestinos(out, stderr) {
		return nil, nil
	}
	// Fallback: `lpstat -p` ("printer NOMBRE is idle. ...").
	out2, stderr2, err2 := lpstatFn("-p")
	if err2 == nil {
		return toPrinters(parseLpstatP(out2)), nil
	}
	if sinDestinos(out2, stderr2) {
		return nil, nil
	}
	return nil, fmt.Errorf("lpstat falló: %v %s", err2, strings.TrimSpace(string(stderr2)))
}

// sinDestinos reconoce el "No destinations added." con el que lpstat
// sale con código 1 cuando no hay ninguna impresora.
func sinDestinos(out, stderr []byte) bool {
	s := strings.ToLower(string(out) + string(stderr))
	return strings.Contains(s, "no destinations")
}

func toPrinters(names []string) []Printer {
	var res []Printer
	for _, n := range names {
		res = append(res, Printer{Name: n, OS: runtime.GOOS})
	}
	return res
}
