package sse

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestBackoffExponencialConJitterYTope(t *testing.T) {
	casos := []struct {
		intento  int
		min, max time.Duration
	}{
		{0, 500 * time.Millisecond, 1 * time.Second},
		{1, 1 * time.Second, 2 * time.Second},
		{2, 2 * time.Second, 4 * time.Second},
		{4, 8 * time.Second, 16 * time.Second},
		{5, 15 * time.Second, 30 * time.Second},
		{50, 15 * time.Second, 30 * time.Second},
	}
	for _, c := range casos {
		lo, hi := Backoff(c.intento, 0), Backoff(c.intento, 1)
		if lo != c.min || hi != c.max {
			t.Errorf("Backoff(%d) en [%v, %v], quería [%v, %v]", c.intento, lo, hi, c.min, c.max)
		}
	}
	if Backoff(3, 0.5) <= Backoff(3, 0) || Backoff(3, 0.5) >= Backoff(3, 1) {
		t.Error("el jitter debe repartir la espera dentro del rango")
	}
}

func TestEventoV2SeParseaYUnBackendV1Tambien(t *testing.T) {
	var j PrintJob
	v2 := `{"job_id":"a","job_type":"test","payload":{},"delivery_key":"a:1","delivery_attempt":2,"redelivery":true,"campo_futuro":7}`
	if err := json.Unmarshal([]byte(v2), &j); err != nil {
		t.Fatal(err)
	}
	if j.Key() != "a:1" || j.DeliveryAttempt != 2 || !j.Redelivery {
		t.Fatalf("v2 mal parseado: %+v", j)
	}
	var j1 PrintJob
	json.Unmarshal([]byte(`{"job_id":"b","job_type":"test","payload":{}}`), &j1)
	if j1.Key() != "b" {
		t.Fatalf("con backend v1 la clave debe ser el job_id, fue %q", j1.Key())
	}
}

// servidor SSE de prueba que registra cabeceras y saludos.
type servidorPrueba struct {
	mu       sync.Mutex
	headers  []http.Header
	saludos  []HelloRequest
	results  []map[string]string
	eventos  []string
	silencio bool // si true, abre el stream y no manda nada
}

func (s *servidorPrueba) handler(t *testing.T) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/printing/agents/hello", func(w http.ResponseWriter, r *http.Request) {
		var h HelloRequest
		json.NewDecoder(r.Body).Decode(&h)
		s.mu.Lock()
		s.saludos = append(s.saludos, h)
		s.mu.Unlock()
		fmt.Fprintf(w, `{"ok":true,"protocol_version":2,"applied_results":%d,"max_redeliveries":3}`, len(h.RecentResults))
	})
	mux.HandleFunc("/printing/agents/jobs/", func(w http.ResponseWriter, r *http.Request) {
		var b map[string]string
		json.NewDecoder(r.Body).Decode(&b)
		s.mu.Lock()
		s.results = append(s.results, b)
		s.mu.Unlock()
		applied := !strings.HasSuffix(b["delivery_key"], ":viejo")
		fmt.Fprintf(w, `{"ok":true,"applied":%v}`, applied)
	})
	mux.HandleFunc("/printing/agents/stream", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.headers = append(s.headers, r.Header.Clone())
		eventos := append([]string(nil), s.eventos...)
		silencio := s.silencio
		s.mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		if silencio {
			<-r.Context().Done()
			return
		}
		for _, e := range eventos {
			fmt.Fprintf(w, "event: print_job\ndata: %s\n\n", e)
			w.(http.Flusher).Flush()
		}
		<-r.Context().Done()
	})
	return mux
}

func TestConexionMandaCabecerasV2ySaludaAntes(t *testing.T) {
	sp := &servidorPrueba{eventos: []string{`{"job_id":"j1","job_type":"test","payload":{},"delivery_key":"j1:0"}`}}
	srv := httptest.NewServer(sp.handler(t))
	defer srv.Close()

	c := New(srv.URL, "tok")
	c.AgentVersion = "0.5.0"
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var orden []string
	var mu sync.Mutex
	c.BeforeConnect = func(ctx context.Context) error {
		_, err := c.Hello(ctx, []RecentResult{{JobID: "x", DeliveryKey: "x:0", Status: "printed"}})
		mu.Lock()
		orden = append(orden, "hello")
		mu.Unlock()
		return err
	}
	recibido := make(chan *PrintJob, 1)
	c.OnPrintJob = func(j *PrintJob) {
		mu.Lock()
		orden = append(orden, "job")
		mu.Unlock()
		recibido <- j
		cancel()
	}
	go c.RunWithReconnect(ctx)

	select {
	case j := <-recibido:
		if j.Key() != "j1:0" {
			t.Fatalf("clave %q", j.Key())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no llegó el job")
	}
	sp.mu.Lock()
	defer sp.mu.Unlock()
	h := sp.headers[0]
	if h.Get("X-NovaSoft-Print-Protocol") != "2" || h.Get("X-NovaSoft-Agent-Version") != "0.5.0" {
		t.Fatalf("cabeceras v2 ausentes: %v", h)
	}
	if h.Get("Authorization") != "Bearer tok" {
		t.Fatal("falta el bearer")
	}
	if len(sp.saludos) != 1 || sp.saludos[0].ProtocolVersion != 2 || sp.saludos[0].AgentVersion != "0.5.0" ||
		len(sp.saludos[0].RecentResults) != 1 {
		t.Fatalf("saludo: %+v", sp.saludos)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(orden) < 2 || orden[0] != "hello" || orden[1] != "job" {
		t.Fatalf("el saludo debe ir antes del stream: %v", orden)
	}
}

func TestHeartbeatCortaUnaConexionMuda(t *testing.T) {
	sp := &servidorPrueba{silencio: true}
	srv := httptest.NewServer(sp.handler(t))
	defer srv.Close()

	c := New(srv.URL, "tok")
	c.IdleTimeout = 200 * time.Millisecond
	inicio := time.Now()
	err := c.connectAndServe(context.Background())
	if !errors.Is(err, ErrSinSenal) {
		t.Fatalf("quería ErrSinSenal, obtuve %v", err)
	}
	if d := time.Since(inicio); d > 3*time.Second {
		t.Fatalf("tardó %v en detectar la conexión muda", d)
	}
}

func TestElHeartbeatNoCortaMientrasSeImprime(t *testing.T) {
	sp := &servidorPrueba{eventos: []string{`{"job_id":"j1","job_type":"test","payload":{}}`}}
	srv := httptest.NewServer(sp.handler(t))
	defer srv.Close()

	c := New(srv.URL, "tok")
	c.IdleTimeout = 150 * time.Millisecond
	var finImpresion time.Time
	c.OnPrintJob = func(j *PrintJob) {
		time.Sleep(400 * time.Millisecond) // impresión lenta > idle
		finImpresion = time.Now()
	}
	err := c.connectAndServe(context.Background())
	if !errors.Is(err, ErrSinSenal) {
		t.Fatalf("tras imprimir y quedar mudo debe cortar por heartbeat: %v", err)
	}
	// Si el vigía hubiera disparado DURANTE la impresión, la conexión se
	// cerraría en cuanto terminara de imprimir; pausado, espera otro idle.
	if d := time.Since(finImpresion); d < 100*time.Millisecond {
		t.Fatalf("el heartbeat cortó durante la impresión (cerró %v después)", d)
	}
}

func TestReportJobResultConClave(t *testing.T) {
	sp := &servidorPrueba{}
	srv := httptest.NewServer(sp.handler(t))
	defer srv.Close()
	c := New(srv.URL, "tok")

	applied, err := c.ReportJobResult(context.Background(), "j1", "printed", "", "j1:0")
	if err != nil || !applied {
		t.Fatalf("applied=%v err=%v", applied, err)
	}
	applied, err = c.ReportJobResult(context.Background(), "j1", "failed", "x", "j1:viejo")
	if err != nil || applied {
		t.Fatalf("una clave vieja debe volver applied=false: %v %v", applied, err)
	}
	// Con backend v1 (clave = job_id) no se manda delivery_key.
	c.ReportJobResult(context.Background(), "j2", "printed", "", "j2")
	sp.mu.Lock()
	defer sp.mu.Unlock()
	if sp.results[0]["delivery_key"] != "j1:0" {
		t.Fatalf("faltó delivery_key: %v", sp.results[0])
	}
	if _, ok := sp.results[2]["delivery_key"]; ok {
		t.Fatalf("no debe mandarse delivery_key cuando es el job_id: %v", sp.results[2])
	}
}

func TestReportJobResultBackendV1SinCampoApplied(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()
	applied, err := New(srv.URL, "t").ReportJobResult(context.Background(), "j", "printed", "", "")
	if err != nil || !applied {
		t.Fatalf("backend v1: applied=%v err=%v", applied, err)
	}
}

func TestSaludoContraBackendV1DevuelveError(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	_, err := New(srv.URL, "t").Hello(context.Background(), nil)
	var eh *ErrHTTP
	if !errors.As(err, &eh) || eh.Status != 404 {
		t.Fatalf("quería ErrHTTP 404, obtuve %v", err)
	}
}
