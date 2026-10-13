package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"

	"github.com/Richi2489/novasoft-print-agent/internal/journal"
	"github.com/Richi2489/novasoft-print-agent/internal/printer"
	"github.com/Richi2489/novasoft-print-agent/internal/sse"
)

type backendFalso struct {
	mu       sync.Mutex
	reportes []map[string]string
	caido    bool
}

func (b *backendFalso) servidor() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b.mu.Lock()
		defer b.mu.Unlock()
		if b.caido {
			http.Error(w, "caído", http.StatusBadGateway)
			return
		}
		var m map[string]string
		json.NewDecoder(r.Body).Decode(&m)
		b.reportes = append(b.reportes, m)
		w.Write([]byte(`{"ok":true,"applied":true}`))
	}))
}

func contarImpresiones(t *testing.T, resultado string) *int {
	n := 0
	previo := imprimirFn
	imprimirFn = func(_ *printer.Printer, _ *sse.PrintJob) (string, string) {
		n++
		if resultado == journal.EstadoImpreso {
			return resultado, ""
		}
		return resultado, "sin papel"
	}
	t.Cleanup(func() { imprimirFn = previo })
	return &n
}

// La garantía central del protocolo v2: una reentrega del backend (la misma
// delivery_key) NUNCA vuelve a imprimir; contesta con el resultado guardado.
func TestUnaReentregaNoDuplicaElTicket(t *testing.T) {
	impresiones := contarImpresiones(t, journal.EstadoImpreso)
	b := &backendFalso{}
	srv := b.servidor()
	defer srv.Close()
	client := sse.New(srv.URL, "tok")
	diario, err := journal.Abrir(filepath.Join(t.TempDir(), "journal.json"))
	if err != nil {
		t.Fatal(err)
	}
	job := &sse.PrintJob{ID: "j1", JobType: "test", Payload: json.RawMessage(`{}`), DeliveryKey: "j1:0"}

	handleJob(context.Background(), client, diario, &printer.Printer{}, job)
	re := *job
	re.Redelivery, re.DeliveryAttempt = true, 2
	handleJob(context.Background(), client, diario, &printer.Printer{}, &re)

	if *impresiones != 1 {
		t.Fatalf("se imprimió %d veces; una reentrega NO debe reimprimir", *impresiones)
	}
	if len(b.reportes) != 2 || b.reportes[1]["status"] != "printed" || b.reportes[1]["delivery_key"] != "j1:0" {
		t.Fatalf("la reentrega debe reportar el resultado guardado: %v", b.reportes)
	}
}

func TestReintentoManualSiImprimeOtraVez(t *testing.T) {
	impresiones := contarImpresiones(t, journal.EstadoFallido)
	b := &backendFalso{}
	srv := b.servidor()
	defer srv.Close()
	client := sse.New(srv.URL, "tok")
	diario, _ := journal.Abrir(filepath.Join(t.TempDir(), "journal.json"))

	handleJob(context.Background(), client, diario, &printer.Printer{}, &sse.PrintJob{ID: "j1", DeliveryKey: "j1:0", Payload: json.RawMessage(`{}`)})
	handleJob(context.Background(), client, diario, &printer.Printer{}, &sse.PrintJob{ID: "j1", DeliveryKey: "j1:1", Payload: json.RawMessage(`{}`)})
	if *impresiones != 2 {
		t.Fatalf("el reintento manual (clave nueva) debe imprimir: %d", *impresiones)
	}
}

func TestSiNoSePuedeReportarQuedaPendienteEnElDiario(t *testing.T) {
	contarImpresiones(t, journal.EstadoImpreso)
	b := &backendFalso{caido: true}
	srv := b.servidor()
	defer srv.Close()
	client := sse.New(srv.URL, "tok")
	diario, _ := journal.Abrir(filepath.Join(t.TempDir(), "journal.json"))

	handleJob(context.Background(), client, diario, &printer.Printer{}, &sse.PrintJob{ID: "j1", DeliveryKey: "j1:0", Payload: json.RawMessage(`{}`)})
	pend := diario.SinReportar()
	if len(pend) != 1 || pend[0].Estado != journal.EstadoImpreso {
		t.Fatalf("el resultado debe quedar pendiente de reportar: %+v", pend)
	}
}

func TestVersionReportadaSinV(t *testing.T) {
	previo := Version
	t.Cleanup(func() { Version = previo })
	Version = "v0.5.0"
	if versionReportada() != "0.5.0" {
		t.Fatalf("versionReportada() = %q", versionReportada())
	}
}
