package websocket

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/cenkalti/backoff/v4"
	"github.com/gorilla/websocket"
)

// Defaults del protocolo. Ajustar con cuidado — cambian el contrato
// con el backend (que también espera pings regulares para mantener la
// WS viva tras NATs y proxies).
const (
	handshakeTimeout = 10 * time.Second
	pingInterval     = 15 * time.Second
	writeDeadline    = 5 * time.Second
)

// Client es el wrapper sobre gorilla/websocket que maneja:
//   - Conexión con header Authorization: Bearer.
//   - Reconexión con backoff exponencial (2s → 30s, infinito).
//   - Ping loop cada 15 s para mantener la conexión viva.
//   - Dispatch de mensajes recibidos a callbacks del caller.
//
// NO es thread-safe para múltiples calls concurrentes a
// RunWithReconnect; sí protege send() con mutex para que ReportJobResult
// desde una goroutine distinta no pise un write en progreso.
type Client struct {
	URL   string
	Token string

	// OnPrintJob se invoca por cada mensaje print_job recibido. Puede ser
	// nil — en ese caso los jobs se loguean y se ignoran (útil para tests).
	OnPrintJob func(*PrintJob)

	conn *websocket.Conn
	mu   sync.Mutex // protege conn + writes
}

// New construye un Client. URL debe ser completa (incluyendo "wss://"),
// token es el agent_token plain guardado en config.
func New(url, token string) *Client {
	return &Client{URL: url, Token: token}
}

// RunWithReconnect corre el loop principal del cliente. Bloquea hasta
// ctx.Done(). Cada desconexión dispara un backoff exponencial antes de
// reintentar. MaxElapsedTime=0 significa infinito — el agent reintenta
// indefinidamente porque un corte de internet no debe requerir
// intervención manual del usuario.
func (c *Client) RunWithReconnect(ctx context.Context) {
	b := backoff.NewExponentialBackOff()
	b.InitialInterval = 2 * time.Second
	b.MaxInterval = 30 * time.Second
	b.MaxElapsedTime = 0
	b.Reset()

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		err := c.connectAndServe(ctx)
		if err != nil {
			select {
			case <-ctx.Done():
				return
			default:
			}
			wait := b.NextBackOff()
			log.Printf("⚠️  desconectado (%v); reintento en %v", err, wait)
			select {
			case <-time.After(wait):
			case <-ctx.Done():
				return
			}
			continue
		}
		// connectAndServe retornó sin error → ctx cancelado por shutdown.
		return
	}
}

// connectAndServe abre la WS, corre el ping loop, lee hasta desconexión.
// Bloquea hasta error o ctx.Done. Retorna error para que el caller haga
// backoff; retorna nil solo si ctx.Done.
func (c *Client) connectAndServe(ctx context.Context) error {
	dialer := websocket.Dialer{
		HandshakeTimeout: handshakeTimeout,
	}

	// Header: Authorization: Bearer <token>.
	// Evitamos poner el token en la URL — los logs de Railway/nginx
	// registran path + query pero no headers sensibles.
	headers := http.Header{}
	headers.Set("Authorization", "Bearer "+c.Token)
	headers.Set("User-Agent", "novasoft-print-agent")

	conn, resp, err := dialer.DialContext(ctx, c.URL, headers)
	if err != nil {
		if resp != nil {
			return fmt.Errorf("dial WS (HTTP %d): %w", resp.StatusCode, err)
		}
		return fmt.Errorf("dial WS: %w", err)
	}

	c.mu.Lock()
	c.conn = conn
	c.mu.Unlock()

	log.Println("✓ Conectado al servidor NovaSoft")

	// Ping loop separado para no bloquear el read loop.
	pingCtx, cancelPing := context.WithCancel(ctx)
	defer cancelPing()
	go c.pingLoop(pingCtx)

	defer func() {
		_ = conn.Close()
		c.mu.Lock()
		c.conn = nil
		c.mu.Unlock()
	}()

	// Read loop hasta error o ctx.Done.
	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		var msg Message
		if err := conn.ReadJSON(&msg); err != nil {
			return fmt.Errorf("read: %w", err)
		}
		c.handleMessage(&msg)
	}
}

// handleMessage dispatcha según el type.
func (c *Client) handleMessage(msg *Message) {
	switch msg.Type {
	case "print_job":
		if c.OnPrintJob == nil {
			log.Printf("⚠️  print_job recibido pero OnPrintJob es nil")
			return
		}
		// print_job tiene los campos top-level (ver comentario en messages.go).
		job := &PrintJob{
			ID:      msg.JobID,
			JobType: msg.JobType,
			Payload: msg.Payload,
		}
		c.OnPrintJob(job)
	case "pong":
		// Keepalive — no-op (el backend actualiza last_seen_at al recibir
		// nuestro ping; su pong solo confirma que la WS está viva).
	case "disconnect":
		log.Printf("⚠️  backend pidió disconnect: %s", string(msg.Data))
	default:
		log.Printf("⚠️  mensaje tipo desconocido: %q", msg.Type)
	}
}

// pingLoop envía un ping cada pingInterval. Sale cuando ctx.Done o si
// el send falla (la conexión está rota y el read loop la cerrará).
func (c *Client) pingLoop(ctx context.Context) {
	ticker := time.NewTicker(pingInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := c.send(outgoing{Type: "ping"}); err != nil {
				log.Printf("⚠️  ping falló: %v", err)
				return
			}
		}
	}
}

// ReportJobResult le dice al backend si el job se imprimió o falló.
// status debe ser "printed" o "failed"; errMsg es opcional.
func (c *Client) ReportJobResult(jobID, status, errMsg string) error {
	return c.send(outgoing{
		Type: "job_result",
		Data: map[string]interface{}{
			"job_id":        jobID,
			"status":        status,
			"error_message": errMsg,
		},
	})
}

// send serializa y escribe con deadline. Mutex protege contra writes
// concurrentes desde el ping loop + callback OnPrintJob.
func (c *Client) send(msg outgoing) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn == nil {
		return fmt.Errorf("no hay conexión activa")
	}
	if err := c.conn.SetWriteDeadline(time.Now().Add(writeDeadline)); err != nil {
		return fmt.Errorf("set deadline: %w", err)
	}
	if err := c.conn.WriteJSON(msg); err != nil {
		return fmt.Errorf("write: %w", err)
	}
	return nil
}
