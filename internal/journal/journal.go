// Package journal — diario local de trabajos de impresión (protocolo v2).
//
// Por qué existe: en v1 el backend no puede saber si un ticket salió cuando
// se pierde la confirmación (corte de red, reinicio del contenedor, POST
// /result que no llegó). Para no duplicar tickets los marcaba "failed" al
// reconectar — en La Capital, 11 de 79 en una semana. El diario mueve esa
// decisión al único que sabe la verdad: este agente.
//
// Regla de oro: una misma delivery_key NUNCA se imprime dos veces.
//
//   - Antes de mandar bytes a la impresora se registra "printing" y se
//     persiste en disco.
//   - Al terminar se guarda "printed"/"failed" con su mensaje.
//   - Si llega otra vez una clave con resultado → se reporta el resultado
//     guardado, sin imprimir.
//   - Si la clave quedó en "printing" de un proceso anterior (el agente se
//     murió a media impresión) → NO se reimprime: se cierra como "failed"
//     con un mensaje que pide revisar si salió. Un ticket duplicado en caja
//     es dinero mal cobrado; uno faltante lo resuelve el cajero con
//     "Reintentar" (que genera una clave nueva).
//
// Formato en disco (%PROGRAMDATA%\NovaSoftAgent\journal.json), escrito de
// forma atómica (tmp + rename). Se conservan 7 días.
package journal

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// Estados de una entrada.
const (
	EstadoImprimiendo = "printing"
	EstadoImpreso     = "printed"
	EstadoFallido     = "failed"
)

// Retencion es cuánto vive una entrada en el diario.
const Retencion = 7 * 24 * time.Hour

// MensajeInterrumpido se reporta cuando una clave quedó "printing" en un
// proceso que ya no existe. Contrato con docs/printing/protocolo-v2.md del
// backend.
const MensajeInterrumpido = "El agente se reinició mientras imprimía este ticket; revisa si salió antes de reintentar"

// Entrada es lo que el diario sabe de una entrega.
type Entrada struct {
	JobID       string    `json:"job_id"`
	DeliveryKey string    `json:"delivery_key"`
	Estado      string    `json:"status"`
	Mensaje     string    `json:"error_message,omitempty"`
	Recibido    time.Time `json:"received_at"`
	Terminado   time.Time `json:"finished_at,omitempty"`
	// Reportado = el backend ya conoce el resultado (POST /result o saludo).
	Reportado bool `json:"reported"`
	// Sesion identifica al proceso que registró "printing". Si no coincide
	// con la del proceso actual, esa impresión quedó interrumpida.
	Sesion string `json:"session,omitempty"`
}

// Decision es lo que el agente debe hacer con un job que acaba de llegar.
type Decision int

const (
	// Imprimir: clave nueva. Ya quedó registrada como "printing".
	Imprimir Decision = iota
	// YaResuelto: la clave tiene resultado; reportarlo SIN imprimir.
	YaResuelto
	// EnCurso: otra rutina de este mismo proceso la está imprimiendo.
	EnCurso
	// Interrumpido: quedó "printing" en un proceso anterior. Ya se cerró
	// como "failed" con MensajeInterrumpido; reportarlo SIN imprimir.
	Interrumpido
)

func (d Decision) String() string {
	switch d {
	case Imprimir:
		return "imprimir"
	case YaResuelto:
		return "ya resuelto"
	case EnCurso:
		return "en curso"
	case Interrumpido:
		return "interrumpido"
	}
	return fmt.Sprintf("Decision(%d)", int(d))
}

// Diario es seguro para uso concurrente.
type Diario struct {
	ruta   string
	sesion string
	ahora  func() time.Time

	mu       sync.Mutex
	entradas map[string]*Entrada
}

type archivo struct {
	Version  int        `json:"version"`
	Entradas []*Entrada `json:"entries"`
}

// Abrir carga el diario de `ruta` (o empieza vacío si no existe). Un
// archivo corrupto se aparta como `<ruta>.corrupto` y se empieza vacío:
// un diario ilegible no puede tumbar la impresión.
func Abrir(ruta string) (*Diario, error) {
	return abrir(ruta, time.Now)
}

func abrir(ruta string, ahora func() time.Time) (*Diario, error) {
	d := &Diario{
		ruta:     ruta,
		sesion:   nuevaSesion(),
		ahora:    ahora,
		entradas: map[string]*Entrada{},
	}
	if err := os.MkdirAll(filepath.Dir(ruta), 0o700); err != nil {
		return nil, fmt.Errorf("creando dir del diario: %w", err)
	}
	datos, err := os.ReadFile(ruta)
	switch {
	case err == nil:
		var a archivo
		if jerr := json.Unmarshal(datos, &a); jerr != nil {
			_ = os.Rename(ruta, ruta+".corrupto")
		} else {
			for _, e := range a.Entradas {
				if e != nil && e.DeliveryKey != "" {
					d.entradas[e.DeliveryKey] = e
				}
			}
		}
	case os.IsNotExist(err):
	default:
		return nil, fmt.Errorf("leyendo diario %s: %w", ruta, err)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.podarLocked()
	return d, d.guardarLocked()
}

func nuevaSesion() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("t%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

// Ruta del archivo del diario.
func (d *Diario) Ruta() string { return d.ruta }

// Comenzar decide qué hacer con un job y, si toca imprimir, lo registra
// como "printing" y lo persiste ANTES de devolver.
func (d *Diario) Comenzar(jobID, clave string) (Decision, Entrada, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if e, ok := d.entradas[clave]; ok {
		switch e.Estado {
		case EstadoImpreso, EstadoFallido:
			return YaResuelto, *e, nil
		case EstadoImprimiendo:
			if e.Sesion == d.sesion {
				return EnCurso, *e, nil
			}
			e.Estado = EstadoFallido
			e.Mensaje = MensajeInterrumpido
			e.Terminado = d.ahora().UTC()
			e.Reportado = false
			return Interrumpido, *e, d.guardarLocked()
		}
	}

	e := &Entrada{
		JobID:       jobID,
		DeliveryKey: clave,
		Estado:      EstadoImprimiendo,
		Recibido:    d.ahora().UTC(),
		Sesion:      d.sesion,
	}
	d.entradas[clave] = e
	if err := d.guardarLocked(); err != nil {
		// Sin poder persistir "printing" no hay garantía anti-duplicado tras
		// un reinicio, pero negarse a imprimir sería peor para el cajero.
		return Imprimir, *e, err
	}
	return Imprimir, *e, nil
}

// Terminar guarda el resultado de una clave.
func (d *Diario) Terminar(clave, estado, mensaje string) error {
	if estado != EstadoImpreso && estado != EstadoFallido {
		return fmt.Errorf("estado inválido %q", estado)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	e, ok := d.entradas[clave]
	if !ok {
		e = &Entrada{DeliveryKey: clave, Recibido: d.ahora().UTC()}
		d.entradas[clave] = e
	}
	e.Estado = estado
	e.Mensaje = mensaje
	e.Terminado = d.ahora().UTC()
	e.Reportado = false
	return d.guardarLocked()
}

// MarcarReportados registra que el backend ya conoce esas claves.
func (d *Diario) MarcarReportados(claves ...string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	cambio := false
	for _, c := range claves {
		if e, ok := d.entradas[c]; ok && !e.Reportado && e.Estado != EstadoImprimiendo {
			e.Reportado = true
			cambio = true
		}
	}
	if !cambio {
		return nil
	}
	return d.guardarLocked()
}

// SinReportar devuelve los resultados que el backend todavía no conoce,
// del más viejo al más nuevo.
func (d *Diario) SinReportar() []Entrada {
	d.mu.Lock()
	defer d.mu.Unlock()
	var out []Entrada
	for _, e := range d.entradas {
		if !e.Reportado && e.Estado != EstadoImprimiendo {
			out = append(out, *e)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Terminado.Before(out[j].Terminado) })
	return out
}

// Recientes devuelve hasta `max` resultados terminados dentro de la
// retención, del más reciente al más viejo (lo que va en el saludo v2).
func (d *Diario) Recientes(max int) []Entrada {
	d.mu.Lock()
	defer d.mu.Unlock()
	limite := d.ahora().Add(-Retencion)
	var out []Entrada
	for _, e := range d.entradas {
		if e.Estado == EstadoImprimiendo || e.Terminado.Before(limite) {
			continue
		}
		out = append(out, *e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Terminado.After(out[j].Terminado) })
	if max > 0 && len(out) > max {
		out = out[:max]
	}
	return out
}

// Podar quita lo más viejo que la retención y persiste.
func (d *Diario) Podar() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.podarLocked() == 0 {
		return nil
	}
	return d.guardarLocked()
}

// Total de entradas (para logs/diagnóstico).
func (d *Diario) Total() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.entradas)
}

func (d *Diario) podarLocked() int {
	limite := d.ahora().Add(-Retencion)
	n := 0
	for k, e := range d.entradas {
		ref := e.Terminado
		if ref.IsZero() {
			ref = e.Recibido
		}
		if ref.Before(limite) {
			delete(d.entradas, k)
			n++
		}
	}
	return n
}

func (d *Diario) guardarLocked() error {
	a := archivo{Version: 1, Entradas: make([]*Entrada, 0, len(d.entradas))}
	for _, e := range d.entradas {
		a.Entradas = append(a.Entradas, e)
	}
	sort.Slice(a.Entradas, func(i, j int) bool { return a.Entradas[i].Recibido.Before(a.Entradas[j].Recibido) })
	datos, err := json.MarshalIndent(a, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(d.ruta), "journal-*.tmp")
	if err != nil {
		return fmt.Errorf("diario: creando tmp: %w", err)
	}
	tmpRuta := tmp.Name()
	if _, err := tmp.Write(datos); err != nil {
		tmp.Close()
		os.Remove(tmpRuta)
		return fmt.Errorf("diario: escribiendo: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(tmpRuta)
		return fmt.Errorf("diario: sync: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpRuta)
		return err
	}
	if err := os.Rename(tmpRuta, d.ruta); err != nil {
		os.Remove(tmpRuta)
		return fmt.Errorf("diario: rename: %w", err)
	}
	return nil
}
