// Wrapper de servicio Windows usando github.com/kardianos/service.
//
// El mismo binario novasoft-agent.exe puede correr como:
//
//   1. CLI interactivo — el usuario invoca subcomandos (pair, run,
//      install, ...) desde una terminal. service.Interactive() == true.
//
//   2. Servicio Windows — el SCM (Service Control Manager) lanza el
//      proceso sin terminal, sin args. service.Interactive() == false.
//      kardianos/service detecta esto y nos hace llamar a s.Run() que
//      bloquea hasta que el SCM pide Stop.
//
// La instalación / start / stop del servicio se manejan vía los
// subcomandos `install`, `uninstall`, `start`, `stop`, `restart` que
// usan service.Control() de la lib.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"

	"github.com/kardianos/service"

	"github.com/Richi2489/novasoft-print-agent/internal/runner"
)

const (
	// serviceName es el identificador interno (registry key, sc query, etc.).
	// Sin espacios para compatibilidad con sc.exe.
	serviceName = "NovaSoftPrintAgent"

	// serviceDisplayName aparece en services.msc / Get-Service.
	serviceDisplayName = "NovaSoft Print Agent"

	// serviceDescription aparece en el panel de propiedades del servicio.
	serviceDescription = "Servicio de impresión térmica para NovaSoft POS — recibe órdenes desde la nube y las imprime en la impresora local."
)

// program implementa service.Interface para kardianos/service.
//
// Lifecycle:
//   - Start: arranca el runner en una goroutine, retorna inmediato.
//     El SCM espera ~30s antes de marcar el servicio como "fallido"; si
//     Start bloquea más de ese timeout, Windows mata el servicio.
//   - Stop: cancela el ctx del runner y espera a que la goroutine termine.
//     SCM da ~20s antes de forzar la terminación.
type program struct {
	cancel context.CancelFunc
	done   chan struct{}
}

// Start inicia el runner en background. Retorna nil para indicar al
// SCM que el servicio está corriendo. Errores fatales del runner se
// loggean — no abortamos el servicio porque kardianos/service no
// soporta "morir limpiamente desde Start" sin race con SCM.
func (p *program) Start(s service.Service) error {
	ctx, cancel := context.WithCancel(context.Background())
	p.cancel = cancel
	p.done = make(chan struct{})

	go func() {
		defer close(p.done)
		log.Println("=== NovaSoft Print Agent (servicio) — arranque ===")
		if err := runner.Run(ctx); err != nil {
			log.Printf("✗ runner: %v", err)
		}
	}()
	return nil
}

// Stop señala al runner que se detenga y bloquea hasta que la goroutine
// salga limpiamente. Si el runner no responde, kardianos eventualmente
// fuerza la terminación del proceso.
func (p *program) Stop(s service.Service) error {
	log.Println("→ servicio: solicitud de stop recibida, cancelando runner…")
	if p.cancel != nil {
		p.cancel()
	}
	if p.done != nil {
		<-p.done
	}
	log.Println("✓ servicio: stop completado.")
	return nil
}

// newService construye el wrapper kardianos/service con la config
// estándar. Reusable desde main (modo SCM) y desde los subcomandos
// install/uninstall/start/stop.
func newService() (service.Service, *program, error) {
	cfg := &service.Config{
		Name:        serviceName,
		DisplayName: serviceDisplayName,
		Description: serviceDescription,
		// StartType "automatic" — el servicio arranca con Windows.
		// kardianos lo traduce a SERVICE_AUTO_START en el registro.
		Option: service.KeyValue{
			"StartType": "automatic",
			// "DelayedAutoStart": true — arranca después de los servicios
			// críticos del SO. Reduce contención con drivers de impresora
			// que aún no terminaron de inicializar.
			"DelayedAutoStart": true,
		},
	}
	prg := &program{}
	s, err := service.New(prg, cfg)
	return s, prg, err
}

// runAsService es lo que main() llama cuando service.Interactive() es
// false. Bloquea hasta que el SCM emite Stop.
func runAsService() error {
	s, _, err := newService()
	if err != nil {
		return err
	}
	return s.Run()
}

// controlService traduce los subcomandos CLI install/uninstall/start/stop
// a llamadas a service.Control(). Mensajes de error en español.
func controlService(action string) error {
	s, _, err := newService()
	if err != nil {
		return err
	}
	if err := service.Control(s, action); err != nil {
		return translateControlError(action, err)
	}
	return nil
}

// translateControlError convierte errores conocidos de service.Control
// en mensajes amigables. Los unknowns se devuelven con el prefijo del
// action para que el usuario tenga al menos contexto.
func translateControlError(action string, err error) error {
	// Errores comunes — el texto de kardianos no es internacionalizado.
	msg := err.Error()
	switch {
	case errors.Is(err, service.ErrNotInstalled):
		return errors.New("el servicio no está instalado — corre 'install' primero")
	case strings.Contains(msg, "service already exists"):
		return errors.New("el servicio ya está instalado — corre 'uninstall' primero o usa 'start'")
	case strings.Contains(msg, "Access is denied"):
		return errors.New("permiso denegado — abre PowerShell como Administrador para esta operación")
	}
	return logErr(action, err)
}

// logErr formatea con prefijo del action para errores genéricos.
func logErr(action string, err error) error {
	return fmt.Errorf("%s: %w", action, err)
}
