package sse

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/cenkalti/backoff/v4"
)

// Defaults de red. Ajustar con cuidado — cambian la vivacidad del
// canal.
const (
	// HandshakeTimeout — cuánto esperamos por el primer byte del stream.
	// 10 s es generoso para redes lentas pero no eterno.
	handshakeTimeout = 10 * time.Second

	// ResultTimeout — timeout del POST de reporte de resultado. Debe
	// ser corto — si el servidor no responde rápido algo está mal.
	resultTimeout = 10 * time.Second

	// reconnectInitial / reconnectMax — bounds del backoff.
	reconnectInitial = 2 * time.Second
	reconnectMax     = 30 * time.Second
)

// Client es un cliente SSE del endpoint /printing/agents/stream del
// backend + un POST-er para reportar resultados. Maneja:
//   - Auth vía Authorization: Bearer header.
//   - Parseo de eventos SSE (`event:`, `data:`, blank line = end of event).
//   - Reconexión automática con backoff exponencial (2s → 30s, infinito).
//
// NO es thread-safe para múltiples calls concurrentes a RunWithReconnect.
// Las calls a ReportJobResult desde distintas goroutines sí son seguras
// porque usan http.Client que es thread-safe.
type Client struct {
	// BackendURL es la raíz del backend, ej. "https://api.novasoft.mx".
	// Derivamos StreamURL y ResultURL de aquí.
	BackendURL string
	Token      string

	// OnPrintJob se invoca por cada evento print_job recibido. Puede
	// ser nil — en ese caso los eventos se loguean y se ignoran (útil
	// para tests).
	OnPrintJob func(*PrintJob)

	// OnConnected se invoca cuando el handshake SSE completa. Se llama
	// cada vez que reconecta (no solo la primera vez). Puede ser nil.
	// El callback corre en la goroutine del read-loop — debe ser barato
	// y NO bloquear (un mutex+set de un flag está bien; abrir un nuevo
	// HTTP request adentro NO).
	OnConnected func()

	// OnDisconnected se invoca cuando el read-loop sale, ya sea por
	// error o por context cancel. err es non-nil si fue por error. Puede
	// ser nil. Mismas restricciones de bloqueo que OnConnected.
	OnDisconnected func(err error)

	// http es reusable entre reconexiones. Sin timeout en el client
	// mismo — los timeouts van en contexto o en request individual,
	// porque el stream GET debe quedarse abierto indefinidamente.
	http *http.Client

	// restartCh es señalado por RequestRestart para forzar al
	// connectAndServe actual a salir y dispararse un reconnect inmediato.
	// Buffer 1 — múltiples requests son colapsadas en un solo restart.
	restartCh chan struct{}

	// restartRequested se setea a true por RequestRestart antes de
	// señalar restartCh, y a false por el loop principal después de
	// honrar el restart. Necesario porque la goroutine watcher consume
	// restartCh para cancelar el connCtx — el loop principal no puede
	// peek en el canal después, así que usa este flag para distinguir
	// "salí por restart explícito" vs "salí por error de red".
	restartRequested atomic.Bool
}

// New construye un Client. BackendURL puede tener trailing slash o no;
// lo normalizamos internamente.
//
// Importante sobre timeouts:
//   - http.Client.Timeout = 0 (sin timeout global). Ese timeout aplica a
//     TODO el request incluyendo body read — para un stream long-lived
//     SSE eso cortaría el read al cumplirse y veríamos
//     "context canceled" a cada rato.
//   - ResponseHeaderTimeout en el Transport sí limita solo los headers
//     (hasta que servidor responda con status + headers). Después de
//     eso, el body lee indefinidamente hasta que el caller cierre
//     o el context parent se cancele.
//   - NO usamos context.WithTimeout atado al request para la apertura:
//     ese patrón ataba el openCtx al body del response, y el cancel()
//     posterior mataba el read con context canceled. Bug de v0.2.0.
func New(backendURL, token string) *Client {
	transport := &http.Transport{
		// Timeout solo para recibir los headers iniciales. Si el
		// servidor tarda más, fallamos el handshake y backoff reintenta.
		// El body que sigue no tiene deadline — es un stream long-lived.
		ResponseHeaderTimeout: handshakeTimeout,
		// ForceAttemptHTTP2 default = true; SSE funciona tanto con
		// HTTP/1.1 chunked como con HTTP/2 streaming. Dejamos que
		// negocie.
	}
	return &Client{
		BackendURL: strings.TrimRight(backendURL, "/"),
		Token:      token,
		http: &http.Client{
			Timeout:   0, // EXPLÍCITO — los stream son long-lived.
			Transport: transport,
		},
		restartCh: make(chan struct{}, 1),
	}
}

// RequestRestart pide al runloop que cierre la conexión SSE actual y
// reabra inmediatamente. Llamadas concurrentes son colapsadas (canal
// buffer 1) — múltiples requests en rápida sucesión = un solo restart.
//
// Uso típico: el IPC handler "RESTART_CONNECTION" lo invoca cuando el
// admin presiona "Reiniciar" en el tray. Útil cuando el agent quedó en
// backoff largo (>30s) y el admin sabe que la red ya volvió.
//
// Sets restartRequested=true antes de señalar el canal, para que el
// loop principal pueda detectar "fue restart" aún cuando la goroutine
// watcher ya consumió el canal.
func (c *Client) RequestRestart() {
	c.restartRequested.Store(true)
	select {
	case c.restartCh <- struct{}{}:
	default:
		// Ya hay un restart pending — no hace falta encolar otro.
	}
}

// RunWithReconnect bloquea hasta ctx.Done(). Cada desconexión dispara
// backoff antes de reintentar. MaxElapsedTime=0 = infinito.
//
// Soporta restart explícito vía RequestRestart(): cierra la conexión
// actual y reintenta inmediatamente sin respetar el backoff acumulado
// (resetea el backoff a su intervalo inicial). El restart durante
// backoff sleep también acelera — en lugar de esperar el wait
// completo, el sleep se cancela y reconecta inmediato.
//
// Detección "fue restart" usa restartRequested atomic.Bool — la goroutine
// watcher consume restartCh para cancelar el connCtx, así que el loop
// principal no puede peek en el canal después. El flag preserva la
// info "alguien pidió restart" entre la cancelación y el chequeo.
func (c *Client) RunWithReconnect(ctx context.Context) {
	b := backoff.NewExponentialBackOff()
	b.InitialInterval = reconnectInitial
	b.MaxInterval = reconnectMax
	b.MaxElapsedTime = 0
	b.Reset()

	// Limpiar señales viejas que pudieran haber quedado de un Run
	// anterior (caso runner refactor v0.3.0+: el client se reusa
	// entre re-pairs si quedara algún design así en el futuro).
	c.restartRequested.Store(false)
	drainRestartCh(c.restartCh)

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		// Child context para la conexión actual — permite cancelar
		// solo esta sin tumbar el outer ctx (que es lifetime del
		// runner entero).
		connCtx, connCancel := context.WithCancel(ctx)
		// Goroutine que vigila restartCh y cancela connCtx si llega.
		// Sale cuando connCtx termina (por error o por restart).
		restartDone := make(chan struct{})
		go func() {
			defer close(restartDone)
			select {
			case <-c.restartCh:
				log.Println("→ restart explícito solicitado, cerrando conexión SSE")
				connCancel()
			case <-connCtx.Done():
			}
		}()

		err := c.connectAndServe(connCtx)
		connCancel()
		<-restartDone

		// Si el outer ctx murió mientras tanto, salimos.
		if ctx.Err() != nil {
			return
		}

		// Si el cierre fue por restart explícito, resetear backoff
		// y reintentar inmediatamente (skip el wait). Swap garantiza
		// que la próxima iteración no piense que también fue restart.
		if c.restartRequested.Swap(false) {
			log.Println("→ honrando restart: reset backoff + reconnect inmediato")
			b.Reset()
			drainRestartCh(c.restartCh) // por si se duplicó
			continue
		}

		if err != nil {
			wait := b.NextBackOff()
			log.Printf("⚠️  desconectado (%v); reintento en %v", err, wait)
			// Esperar el backoff PERO permitir que un restart durante
			// el sleep acelere la reconexión. Útil cuando el admin
			// click "Reiniciar" mientras estamos en mid-backoff (ej.
			// red volvió y queremos reconnect ya).
			select {
			case <-time.After(wait):
				// Backoff completo — siguiente attempt usa next backoff.
			case <-c.restartCh:
				log.Println("→ restart durante backoff: reset y reconnect inmediato")
				c.restartRequested.Store(false)
				b.Reset()
			case <-ctx.Done():
				return
			}
			continue
		}
		// connectAndServe retornó sin error → ctx cancelado por shutdown.
		return
	}
}

// drainRestartCh consume cualquier señal pendiente del canal sin
// bloquear. Usado para limpiar estado entre iteraciones del loop.
func drainRestartCh(ch <-chan struct{}) {
	select {
	case <-ch:
	default:
	}
}

// connectAndServe abre el stream GET, parsea eventos hasta EOF/error.
//
// El request se construye con el `ctx` del caller (RunWithReconnect) —
// mismo ctx durante TODO el ciclo de vida de la conexión. El handshake
// timeout NO se impone acá con context.WithTimeout (ese patrón, usado
// en v0.2.0, cancelaba el ctx tras el handshake y mataba el body read
// con "context canceled"); en su lugar el Transport.ResponseHeaderTimeout
// configurado en New() limita solo la fase de headers.
func (c *Client) connectAndServe(ctx context.Context) error {
	url := c.BackendURL + "/printing/agents/stream"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Cache-Control", "no-cache")
	req.Header.Set("User-Agent", "novasoft-print-agent")

	resp, err := c.http.Do(req)
	if err != nil {
		// ResponseHeaderTimeout se manifiesta como net.Error con
		// Timeout()=true. Lo devolvemos tal cual — el caller aplica
		// backoff y reintenta.
		return fmt.Errorf("dial SSE: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		preview := readLimitedBody(resp.Body, 300)
		return fmt.Errorf("SSE rechazado HTTP %d: %s", resp.StatusCode, preview)
	}

	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "text/event-stream") {
		return fmt.Errorf("content-type inesperado: %q", ct)
	}

	log.Println("✓ Conectado al servidor NovaSoft")
	if c.OnConnected != nil {
		c.OnConnected()
	}
	defer func() {
		if c.OnDisconnected != nil {
			c.OnDisconnected(nil)
		}
	}()

	return c.readLoop(ctx, resp.Body)
}

// readLoop parsea eventos SSE. Formato:
//
//	event: <tipo>\n
//	data: <payload>\n
//	\n          ← blank line termina el evento
//
// Líneas que empiezan con ":" son comentarios (keepalive) y se ignoran.
// Líneas sin ":" o con campo desconocido se ignoran tolerantemente.
func (c *Client) readLoop(ctx context.Context, body io.Reader) error {
	// bufio.Scanner con buffer grande — los payloads pueden ser decenas
	// de KB si un ticket tiene muchos items. Default 64 KB de Scanner
	// es OK pero subimos a 1 MB por seguridad.
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)

	var (
		currentEvent string
		dataBuf      bytes.Buffer
	)

	dispatch := func() {
		defer func() {
			currentEvent = ""
			dataBuf.Reset()
		}()
		if dataBuf.Len() == 0 {
			return
		}
		if currentEvent == "" || currentEvent == "message" {
			// Evento sin name / tipo default — no nos interesa.
			return
		}
		if currentEvent != "print_job" {
			log.Printf("⚠️  evento SSE tipo desconocido: %q", currentEvent)
			return
		}
		if c.OnPrintJob == nil {
			return
		}
		var job PrintJob
		if err := json.Unmarshal(dataBuf.Bytes(), &job); err != nil {
			log.Printf("✗ payload SSE inválido: %v", err)
			return
		}
		c.OnPrintJob(&job)
	}

	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return nil
		default:
		}
		line := scanner.Text()

		// Línea vacía → fin del evento actual.
		if line == "" {
			dispatch()
			continue
		}

		// Comentario — se usa para keepalive. Lo ignoramos.
		if strings.HasPrefix(line, ":") {
			continue
		}

		// Formato: "field: value" (el espacio después de ':' es opcional
		// per spec, lo trimmeamos).
		colonIdx := strings.IndexByte(line, ':')
		if colonIdx < 0 {
			continue
		}
		field := line[:colonIdx]
		value := strings.TrimPrefix(line[colonIdx+1:], " ")

		switch field {
		case "event":
			currentEvent = value
		case "data":
			// Pueden venir múltiples líneas `data:` concatenadas con \n.
			if dataBuf.Len() > 0 {
				dataBuf.WriteByte('\n')
			}
			dataBuf.WriteString(value)
		case "id", "retry":
			// Ignorados — no los usamos.
		default:
			// Campo desconocido — tolerante, no error.
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read stream: %w", err)
	}
	// EOF limpio (servidor cerró). Reportamos como error para que
	// RunWithReconnect reintente.
	return fmt.Errorf("stream cerrado por servidor (EOF)")
}

// ReportJobResult POST al endpoint del backend con el resultado del job.
// status debe ser "printed" o "failed"; errMsg es opcional.
// Thread-safe — http.Client maneja concurrencia.
func (c *Client) ReportJobResult(ctx context.Context, jobID, status, errMsg string) error {
	url := fmt.Sprintf("%s/printing/agents/jobs/%s/result", c.BackendURL, jobID)

	body := map[string]string{
		"status":        status,
		"error_message": errMsg,
	}
	bodyJSON, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal body: %w", err)
	}

	postCtx, cancel := context.WithTimeout(ctx, resultTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(postCtx, http.MethodPost, url, bytes.NewReader(bodyJSON))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "novasoft-print-agent")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("POST result: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		preview := readLimitedBody(resp.Body, 300)
		return fmt.Errorf("result rechazado HTTP %d: %s", resp.StatusCode, preview)
	}
	return nil
}

// readLimitedBody lee hasta maxBytes del body y devuelve string. Para
// incluir cuerpo en mensajes de error sin vomitar MB.
func readLimitedBody(r io.Reader, maxBytes int) string {
	b, _ := io.ReadAll(io.LimitReader(r, int64(maxBytes)))
	return string(b)
}
