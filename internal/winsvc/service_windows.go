//go:build windows

// Package winsvc — integración del agent con el Service Control Manager
// (SCM) de Windows.
//
// Antes de v0.3.0 la única forma soportada de dejar el agent corriendo
// era un acceso directo en la carpeta de Inicio. Eso implicaba que:
//   - si el proceso se caía, nadie lo levantaba;
//   - si alguien cerraba la ventana, moría hasta el próximo login;
//   - si la máquina reiniciaba y nadie iniciaba sesión, no arrancaba —
//     el restaurante abría y no imprimía.
//
// Como servicio de Windows el SCM resuelve los tres casos: arranca en el
// boot sin necesidad de login, no tiene ventana que cerrar, y las
// acciones de recuperación lo reinician si el proceso muere.
//
// El mismo binario sirve para los dos modos: Run() se usa cuando el SCM
// nos arrancó (detectado con svc.IsWindowsService) y el `run` de toda la
// vida cuando lo corre una persona en una consola.
package winsvc

import (
	"context"
	"fmt"
	"log"
	"time"

	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/eventlog"
)

// Identidad del servicio en el SCM. Name es el que se usa en
// `sc query` / `sc qc` / `sc qfailure`; cambiarlo rompe instalaciones
// existentes (habría que desinstalar con el nombre viejo primero).
const (
	Name        = "NovaSoftPrintAgent"
	DisplayName = "NovaSoft Print Agent"
	Description = "Recibe trabajos de impresión de NovaSoft y los envía a la impresora térmica del restaurante."
)

// IDs de evento para el Event Log de Windows. Se mantienen por debajo de
// 1000 porque el mensaje se renderiza vía EventCreate.exe.
const (
	EventStart = 1 // servicio arrancado
	EventStop  = 2 // servicio detenido limpiamente
	EventFatal = 3 // error fatal — el agent no pudo seguir
	EventWarn  = 4 // situación anómala pero no fatal
)

// stopTimeout — cuánto esperamos a que el loop del agent termine tras
// pedirle Stop/Shutdown antes de rendirnos y devolver el control al SCM.
// Windows mata el proceso si tardamos demasiado, así que conviene que
// sea holgado pero finito.
const stopTimeout = 15 * time.Second

// IsService indica si el proceso fue arrancado por el SCM. Cuando es
// false el binario se está corriendo a mano y debe comportarse
// exactamente como antes (salida por consola, Ctrl-C, etc.).
func IsService() bool {
	is, err := svc.IsWindowsService()
	if err != nil {
		// Ante la duda asumimos modo manual: es el modo degradado
		// seguro (una consola de más es mejor que un servicio que no
		// reporta estado y el SCM mata a los 30 s).
		return false
	}
	return is
}

// EventLogger es un wrapper nil-safe sobre el Event Log de Windows.
// Si la fuente no está registrada (por ejemplo el binario corriendo a
// mano sin haber hecho `service install`) todos los métodos son no-op:
// no queremos que la ausencia del Event Log rompa el agent.
type EventLogger struct {
	l *eventlog.Log
}

// OpenEventLog abre la fuente de eventos del agent. Nunca devuelve
// error: si no se puede abrir, devuelve un logger inerte.
func OpenEventLog() *EventLogger {
	l, err := eventlog.Open(Name)
	if err != nil {
		return &EventLogger{}
	}
	return &EventLogger{l: l}
}

func (e *EventLogger) Info(id uint32, msg string) {
	if e == nil || e.l == nil {
		return
	}
	_ = e.l.Info(id, msg)
}

func (e *EventLogger) Warning(id uint32, msg string) {
	if e == nil || e.l == nil {
		return
	}
	_ = e.l.Warning(id, msg)
}

func (e *EventLogger) Error(id uint32, msg string) {
	if e == nil || e.l == nil {
		return
	}
	_ = e.l.Error(id, msg)
}

func (e *EventLogger) Close() {
	if e == nil || e.l == nil {
		return
	}
	_ = e.l.Close()
}

// handler implementa svc.Handler. Traduce entre el protocolo del SCM y
// un context.Context, que es lo que el resto del agent entiende.
type handler struct {
	run    func(context.Context) error
	events *EventLogger
}

// Execute es el callback del SCM. Contrato:
//   - reportar StartPending antes de hacer trabajo lento;
//   - reportar Running en cuanto estemos listos, o el SCM nos mata;
//   - aceptar Stop y Shutdown para poder cerrar limpio (Shutdown es el
//     que llega cuando apagan la máquina — sin él Windows mata el
//     proceso en seco y el último job en vuelo se pierde).
//
// El valor de retorno (ssec, errno): errno != 0 le dice al SCM que el
// servicio terminó con error, que es lo que dispara las acciones de
// recuperación (junto con SetRecoveryActionsOnNonCrashFailures).
func (h *handler) Execute(args []string, r <-chan svc.ChangeRequest, s chan<- svc.Status) (bool, uint32) {
	const accepted = svc.AcceptStop | svc.AcceptShutdown

	s <- svc.Status{State: svc.StartPending}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- h.run(ctx) }()

	s <- svc.Status{State: svc.Running, Accepts: accepted}
	h.events.Info(EventStart, fmt.Sprintf("%s arrancó correctamente.", DisplayName))
	log.Printf("→ servicio %s en estado RUNNING", Name)

	for {
		select {
		case c := <-r:
			switch c.Cmd {
			case svc.Interrogate:
				s <- c.CurrentStatus

			case svc.Stop, svc.Shutdown:
				reason := "Stop"
				if c.Cmd == svc.Shutdown {
					reason = "Shutdown (apagado de Windows)"
				}
				log.Printf("→ SCM pidió %s, cerrando…", reason)
				s <- svc.Status{State: svc.StopPending, WaitHint: uint32(stopTimeout / time.Millisecond)}
				cancel()

				select {
				case <-done:
					log.Println("✓ agent cerrado limpiamente")
				case <-time.After(stopTimeout):
					log.Printf("⚠️  el agent no cerró en %v — saliendo de todos modos", stopTimeout)
					h.events.Warning(EventWarn,
						fmt.Sprintf("%s no cerró en %v tras %s; se forzó la salida.", DisplayName, stopTimeout, reason))
				}
				h.events.Info(EventStop, fmt.Sprintf("%s se detuvo (%s).", DisplayName, reason))
				return false, 0

			default:
				log.Printf("⚠️  comando SCM no manejado: %d", c.Cmd)
			}

		case err := <-done:
			// El loop del agent terminó por su cuenta. Sin petición de
			// Stop eso siempre es una falla: el modo normal es bloquear
			// para siempre reconectando. Salimos con error para que el
			// SCM aplique las acciones de recuperación.
			if err != nil {
				log.Printf("✗ error fatal: %v", err)
				h.events.Error(EventFatal,
					fmt.Sprintf("%s terminó con error: %v\n\nWindows lo reiniciará según las acciones de recuperación configuradas.", DisplayName, err))
				s <- svc.Status{State: svc.StopPending}
				return false, 1
			}
			log.Println("⚠️  el agent terminó sin error y sin petición de Stop")
			h.events.Warning(EventWarn,
				fmt.Sprintf("%s terminó inesperadamente sin error.", DisplayName))
			s <- svc.Status{State: svc.StopPending}
			return false, 1
		}
	}
}

// Run entrega el control al SCM. Bloquea hasta que el servicio se
// detiene. runFn debe bloquear hasta que su context se cancele.
//
// Solo debe llamarse cuando IsService() == true.
func Run(runFn func(context.Context) error) error {
	events := OpenEventLog()
	defer events.Close()

	if err := svc.Run(Name, &handler{run: runFn, events: events}); err != nil {
		events.Error(EventFatal, fmt.Sprintf("%s no pudo conectarse al SCM: %v", DisplayName, err))
		return fmt.Errorf("svc.Run: %w", err)
	}
	return nil
}
