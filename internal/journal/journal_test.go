package journal

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func reloj(t0 time.Time) (func() time.Time, func(time.Duration)) {
	ahora := t0
	return func() time.Time { return ahora }, func(d time.Duration) { ahora = ahora.Add(d) }
}

func TestClaveNuevaSeImprimeUnaSolaVez(t *testing.T) {
	ruta := filepath.Join(t.TempDir(), "journal.json")
	d, err := Abrir(ruta)
	if err != nil {
		t.Fatal(err)
	}
	dec, _, err := d.Comenzar("job1", "job1:0")
	if err != nil || dec != Imprimir {
		t.Fatalf("primera vez: %v %v", dec, err)
	}
	if err := d.Terminar("job1:0", EstadoImpreso, ""); err != nil {
		t.Fatal(err)
	}
	dec, e, _ := d.Comenzar("job1", "job1:0")
	if dec != YaResuelto || e.Estado != EstadoImpreso {
		t.Fatalf("reentrega: quería YaResuelto/printed, obtuve %v/%s", dec, e.Estado)
	}
}

func TestReintentoManualTraeClaveNuevaYSeImprime(t *testing.T) {
	d, _ := Abrir(filepath.Join(t.TempDir(), "journal.json"))
	d.Comenzar("job1", "job1:0")
	d.Terminar("job1:0", EstadoFallido, "sin papel")
	if dec, _, _ := d.Comenzar("job1", "job1:1"); dec != Imprimir {
		t.Fatalf("el reintento manual (clave :1) debe imprimirse, obtuve %v", dec)
	}
}

func TestElDiarioSobreviveAlReinicio(t *testing.T) {
	ruta := filepath.Join(t.TempDir(), "journal.json")
	d, _ := Abrir(ruta)
	d.Comenzar("job1", "job1:0")
	d.Terminar("job1:0", EstadoImpreso, "")

	d2, err := Abrir(ruta)
	if err != nil {
		t.Fatal(err)
	}
	if dec, _, _ := d2.Comenzar("job1", "job1:0"); dec != YaResuelto {
		t.Fatalf("tras reiniciar el agente, quería YaResuelto, obtuve %v", dec)
	}
}

func TestImpresionInterrumpidaNoSeReimprime(t *testing.T) {
	ruta := filepath.Join(t.TempDir(), "journal.json")
	d, _ := Abrir(ruta)
	d.Comenzar("job1", "job1:0") // el proceso muere aquí, sin Terminar

	d2, _ := Abrir(ruta) // proceso nuevo
	dec, e, err := d2.Comenzar("job1", "job1:0")
	if err != nil {
		t.Fatal(err)
	}
	if dec != Interrumpido || e.Estado != EstadoFallido || e.Mensaje != MensajeInterrumpido {
		t.Fatalf("quería Interrumpido/failed, obtuve %v/%s %q", dec, e.Estado, e.Mensaje)
	}
	if dec, _, _ := d2.Comenzar("job1", "job1:0"); dec != YaResuelto {
		t.Fatalf("una vez cerrada debe quedar resuelta, obtuve %v", dec)
	}
}

func TestEnCursoEnElMismoProceso(t *testing.T) {
	d, _ := Abrir(filepath.Join(t.TempDir(), "journal.json"))
	d.Comenzar("job1", "job1:0")
	if dec, _, _ := d.Comenzar("job1", "job1:0"); dec != EnCurso {
		t.Fatalf("quería EnCurso, obtuve %v", dec)
	}
}

func TestRecientesOrdenTopeYRetencion(t *testing.T) {
	ahora, avanzar := reloj(time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC))
	d, err := abrir(filepath.Join(t.TempDir(), "journal.json"), ahora)
	if err != nil {
		t.Fatal(err)
	}
	d.Comenzar("viejo", "viejo:0")
	d.Terminar("viejo:0", EstadoImpreso, "")
	avanzar(8 * 24 * time.Hour)
	for _, k := range []string{"a", "b", "c"} {
		d.Comenzar(k, k+":0")
		d.Terminar(k+":0", EstadoImpreso, "")
		avanzar(time.Minute)
	}
	d.Comenzar("en-curso", "en-curso:0")

	r := d.Recientes(2)
	if len(r) != 2 || r[0].DeliveryKey != "c:0" || r[1].DeliveryKey != "b:0" {
		t.Fatalf("Recientes(2) = %+v", r)
	}
	if todos := d.Recientes(0); len(todos) != 3 {
		t.Fatalf("Recientes no debe incluir lo de hace 8 días ni lo que está imprimiendo: %d", len(todos))
	}
	if err := d.Podar(); err != nil {
		t.Fatal(err)
	}
	if d.Total() != 4 {
		t.Fatalf("tras podar deben quedar a, b, c y en-curso; hay %d", d.Total())
	}
}

func TestSinReportarYMarcar(t *testing.T) {
	d, _ := Abrir(filepath.Join(t.TempDir(), "journal.json"))
	d.Comenzar("a", "a:0")
	d.Terminar("a:0", EstadoImpreso, "")
	d.Comenzar("b", "b:0")
	d.Terminar("b:0", EstadoFallido, "sin papel")
	if n := len(d.SinReportar()); n != 2 {
		t.Fatalf("sin reportar = %d", n)
	}
	d.MarcarReportados("a:0")
	p := d.SinReportar()
	if len(p) != 1 || p[0].DeliveryKey != "b:0" || p[0].Mensaje != "sin papel" {
		t.Fatalf("sin reportar tras marcar = %+v", p)
	}
}

func TestArchivoCorruptoNoTumbaAlAgente(t *testing.T) {
	ruta := filepath.Join(t.TempDir(), "journal.json")
	os.WriteFile(ruta, []byte("{esto no es json"), 0o600)
	d, err := Abrir(ruta)
	if err != nil {
		t.Fatalf("un diario corrupto no debe impedir arrancar: %v", err)
	}
	if d.Total() != 0 {
		t.Fatal("debió empezar vacío")
	}
	if _, err := os.Stat(ruta + ".corrupto"); err != nil {
		t.Fatal("el archivo corrupto debió apartarse para diagnóstico")
	}
}
