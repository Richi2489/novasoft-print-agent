package sse

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Defaults de red. Ajustar con cuidado — cambian la vivacidad del
// canal.
const (
	// HandshakeTimeout — cuánto esperamos por el primer byte del stream.
	// 10 s es generoso para redes lentas pero no eterno.
	handshakeTimeout = 10 * time.Second

	// ResultTimeout — timeout del POST de reporte de resultado y del
	// saludo. Debe ser corto — si el servidor no responde rápido algo está
	// mal.
	resultTimeout = 10 * time.Second

	// Backoff de reconexión: exponencial desde 1 s, tope 30 s, con jitter.
	reconnectInitial = 1 * time.Second
	reconnectMax     = 30 * time.Second

	// Una conexión que duró al menos esto se considera sana: el siguiente
	// corte vuelve a empezar el backoff desde reconnectInitial.
	conexionSana = 30 * time.Second

	// DefaultIdleTimeout — heartbeat. El backend manda `: keepalive` cada
	// 15 s; si en 45 s no llega NI UN BYTE, la conexión está muerta aunque
	// el socket siga "abierto" (NAT que la tiró, Wi-Fi que se cayó y
	// volvió). Sin esto el agente esperaba para siempre en el read y el
	// backend seguía creyendo que estaba conectado.
	DefaultIdleTimeout = 45 * time.Second
)

// ErrSinSenal es el error de una conexión que dejó de recibir bytes.
var ErrSinSenal = errors.New("sin señal del servidor")

// Client es un cliente SSE del endpoint /printing/agents/stream del
// backend + un POST-er para reportar resultados. Maneja:
//   - Auth vía Authorization: Bearer header.
//   - Protocolo v2: saludo antes de cada conexión (BeforeConnect) y
//     cabeceras X-NovaSoft-Print-Protocol / X-NovaSoft-Agent-Version.
//   - Parseo de eventos SSE (`event:`, `data:`, blank line = end of event).
//   - Heartbeat: corta la conexión si no llega nada en IdleTimeout.
//   - Reconexión automática con backoff exponencial con jitter (tope 30 s).
//
// NO es thread-safe para múltiples calls concurrentes a RunWithReconnect.
// ReportJobResult y Hello sí se pueden llamar desde otras goroutines.
type Client struct {
	// BackendURL es la raíz del backend, ej. "https://api.novasoft.mx".
	BackendURL string
	Token      string

	// AgentVersion se reporta al backend ("0.5.0", sin "v").
	AgentVersion string

	// OnPrintJob se invoca por cada evento print_job recibido, en la
	// misma goroutine que lee el stream (un job a la vez). Puede ser nil.
	OnPrintJob func(*PrintJob)

	// BeforeConnect se invoca antes de abrir cada conexión (aquí va el
	// saludo v2). Un error se loguea pero NO impide conectar: la
	// reentrega del backend es segura igual gracias al diario.
	BeforeConnect func(ctx context.Context) error

	// IdleTimeout — heartbeat. 0 = DefaultIdleTimeout.
	IdleTimeout time.Duration

	http *http.Client

	rndMu sync.Mutex
	rnd   *rand.Rand
}

// New construye un Client. BackendURL puede tener trailing slash o no;
// lo normalizamos internamente.
//
// Importante sobre timeouts:
//   - http.Client.Timeout = 0 (sin timeout global). Ese timeout aplica a
//     TODO el request incluyendo body read — para un stream long-lived
//     SSE eso cortaría el read al cumplirse.
//   - ResponseHeaderTimeout en el Transport sí limita solo los headers.
//   - El heartbeat (IdleTimeout) cancela el contexto de la conexión cuando
//     no llegan bytes — no hay deadline fijo sobre un stream sano.
func New(backendURL, token string) *Client {
	transport := &http.Transport{
		ResponseHeaderTimeout: handshakeTimeout,
	}
	return &Client{
		BackendURL: strings.TrimRight(backendURL, "/"),
		Token:      token,
		http: &http.Client{
			Timeout:   0, // EXPLÍCITO — los stream son long-lived.
			Transport: transport,
		},
		rnd: rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

func (c *Client) userAgent() string {
	if c.AgentVersion == "" {
		return "novasoft-print-agent"
	}
	return "novasoft-print-agent/" + c.AgentVersion
}

func (c *Client) idle() time.Duration {
	if c.IdleTimeout > 0 {
		return c.IdleTimeout
	}
	return DefaultIdleTimeout
}

// Backoff calcula la espera antes del reintento número `intento` (0 = el
// primero): exponencial desde 1 s con tope 30 s y "equal jitter" (entre la
// mitad y el total), para que varias cajas de un mismo local no golpeen
// al servidor todas en el mismo segundo tras un corte.
func Backoff(intento int, aleatorio float64) time.Duration {
	if intento < 0 {
		intento = 0
	}
	d := reconnectInitial
	for i := 0; i < intento && d < reconnectMax; i++ {
		d *= 2
	}
	if d > reconnectMax {
		d = reconnectMax
	}
	if aleatorio < 0 {
		aleatorio = 0
	}
	if aleatorio > 1 {
		aleatorio = 1
	}
	mitad := d / 2
	return mitad + time.Duration(aleatorio*float64(d-mitad))
}

func (c *Client) aleatorio() float64 {
	c.rndMu.Lock()
	defer c.rndMu.Unlock()
	if c.rnd == nil {
		c.rnd = rand.New(rand.NewSource(time.Now().UnixNano()))
	}
	return c.rnd.Float64()
}

// RunWithReconnect bloquea hasta ctx.Done(). Cada desconexión espera un
// backoff antes de reintentar; nunca se rinde.
func (c *Client) RunWithReconnect(ctx context.Context) {
	intento := 0
	for {
		if ctx.Err() != nil {
			return
		}

		if c.BeforeConnect != nil {
			if err := c.BeforeConnect(ctx); err != nil && ctx.Err() == nil {
				log.Printf("⚠️  saludo al servidor falló (%v); se conecta igual", err)
			}
		}

		inicio := time.Now()
		err := c.connectAndServe(ctx)
		if ctx.Err() != nil {
			return
		}
		if time.Since(inicio) >= conexionSana {
			intento = 0
		}
		wait := Backoff(intento, c.aleatorio())
		intento++
		log.Printf("⚠️  desconectado (%v); reintento en %v", err, wait.Round(100*time.Millisecond))
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			return
		}
	}
}

// connectAndServe abre el stream GET y parsea eventos hasta EOF/error.
func (c *Client) connectAndServe(ctx context.Context) error {
	url := c.BackendURL + "/printing/agents/stream"

	connCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	req, err := http.NewRequestWithContext(connCtx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Cache-Control", "no-cache")
	req.Header.Set("User-Agent", c.userAgent())
	req.Header.Set("X-NovaSoft-Print-Protocol", strconv.Itoa(ProtocolVersion))
	if c.AgentVersion != "" {
		req.Header.Set("X-NovaSoft-Agent-Version", c.AgentVersion)
	}

	resp, err := c.http.Do(req)
	if err != nil {
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

	// Heartbeat: si no llega ningún byte en idle, se cancela la conexión.
	vigia := newVigia(c.idle(), cancel)
	defer vigia.detener()

	err = c.readLoop(ctx, resp.Body, vigia)
	if vigia.disparado() {
		return fmt.Errorf("%w en %v", ErrSinSenal, c.idle())
	}
	return err
}

// vigia cancela la conexión si pasa `idle` sin actividad. Se pausa
// mientras se imprime un job (el stream no se lee durante la impresión).
type vigia struct {
	idle   time.Duration
	timer  *time.Timer
	mu     sync.Mutex
	fuego  bool
	pausa  bool
	cancel context.CancelFunc
}

func newVigia(idle time.Duration, cancel context.CancelFunc) *vigia {
	v := &vigia{idle: idle, cancel: cancel}
	v.timer = time.AfterFunc(idle, func() {
		v.mu.Lock()
		if v.pausa {
			v.mu.Unlock()
			return
		}
		v.fuego = true
		v.mu.Unlock()
		cancel()
	})
	return v
}

func (v *vigia) latido() {
	v.mu.Lock()
	defer v.mu.Unlock()
	if !v.fuego {
		v.timer.Reset(v.idle)
	}
}

func (v *vigia) pausar() {
	v.mu.Lock()
	v.pausa = true
	v.mu.Unlock()
	v.timer.Stop()
}

func (v *vigia) reanudar() {
	v.mu.Lock()
	v.pausa = false
	v.mu.Unlock()
	v.latido()
}

func (v *vigia) disparado() bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.fuego
}

func (v *vigia) detener() { v.timer.Stop() }

// readLoop parsea eventos SSE. Formato:
//
//	event: <tipo>\n
//	data: <payload>\n
//	\n          ← blank line termina el evento
//
// Líneas que empiezan con ":" son comentarios (keepalive) y se ignoran,
// pero cuentan como latido para el heartbeat.
func (c *Client) readLoop(ctx context.Context, body io.Reader, v *vigia) error {
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
		if v != nil {
			v.pausar()
			defer v.reanudar()
		}
		c.OnPrintJob(&job)
	}

	for scanner.Scan() {
		if v != nil {
			v.latido()
		}
		if ctx.Err() != nil {
			return nil
		}
		line := scanner.Text()

		if line == "" {
			dispatch()
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}

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
			if dataBuf.Len() > 0 {
				dataBuf.WriteByte('\n')
			}
			dataBuf.WriteString(value)
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read stream: %w", err)
	}
	return fmt.Errorf("stream cerrado por servidor (EOF)")
}

// Hello manda el saludo v2. Un backend v1 responde 404/405: se devuelve
// error y el agente sigue funcionando (la reentrega simplemente no existe
// del otro lado).
func (c *Client) Hello(ctx context.Context, results []RecentResult) (*HelloResponse, error) {
	if results == nil {
		results = []RecentResult{}
	}
	body := HelloRequest{
		ProtocolVersion: ProtocolVersion,
		AgentVersion:    c.AgentVersion,
		RecentResults:   results,
	}
	var out HelloResponse
	if err := c.postJSON(ctx, c.BackendURL+"/printing/agents/hello", body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ReportJobResult POST al endpoint del backend con el resultado del job.
// status debe ser "printed" o "failed"; errMsg y deliveryKey son
// opcionales. Devuelve applied=false si el backend ignoró el reporte por
// venir de un intento viejo (no hay que reintentarlo).
func (c *Client) ReportJobResult(ctx context.Context, jobID, status, errMsg, deliveryKey string) (bool, error) {
	url := fmt.Sprintf("%s/printing/agents/jobs/%s/result", c.BackendURL, jobID)
	body := map[string]string{
		"status":        status,
		"error_message": errMsg,
	}
	if deliveryKey != "" && deliveryKey != jobID {
		body["delivery_key"] = deliveryKey
	}
	var out struct {
		OK      bool  `json:"ok"`
		Applied *bool `json:"applied"`
	}
	if err := c.postJSON(ctx, url, body, &out); err != nil {
		return false, err
	}
	if out.Applied != nil {
		return *out.Applied, nil
	}
	return true, nil
}

// ErrHTTP es un rechazo del servidor con su código.
type ErrHTTP struct {
	Status int
	Body   string
}

func (e *ErrHTTP) Error() string { return fmt.Sprintf("HTTP %d: %s", e.Status, e.Body) }

func (c *Client) postJSON(ctx context.Context, url string, in, out any) error {
	bodyJSON, err := json.Marshal(in)
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
	req.Header.Set("User-Agent", c.userAgent())

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("POST %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &ErrHTTP{Status: resp.StatusCode, Body: readLimitedBody(resp.Body, 300)}
	}
	if out != nil {
		if err := json.NewDecoder(io.LimitReader(resp.Body, 64*1024)).Decode(out); err != nil && !errors.Is(err, io.EOF) {
			return fmt.Errorf("respuesta inválida: %w", err)
		}
	}
	return nil
}

// readLimitedBody lee hasta maxBytes del body y devuelve string. Para
// incluir cuerpo en mensajes de error sin vomitar MB.
func readLimitedBody(r io.Reader, maxBytes int) string {
	b, _ := io.ReadAll(io.LimitReader(r, int64(maxBytes)))
	return string(b)
}
