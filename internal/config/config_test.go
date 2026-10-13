package config

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// La Capital corre v0.3.0, que guardaba el emparejamiento en
// %PROGRAMDATA%\NovaSoft\config.json. Al actualizar, el agente debe
// encontrarlo y conservar el MISMO token (misma impresora en el backend),
// sin pedir un código nuevo.
func TestMigraElConfigDeV030SinReemparejar(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("rutas de %PROGRAMDATA% solo en Windows")
	}
	base := t.TempDir()
	t.Setenv("PROGRAMDATA", base)
	t.Setenv("APPDATA", filepath.Join(base, "appdata-vacio"))

	viejo := filepath.Join(base, "NovaSoft", "config.json")
	os.MkdirAll(filepath.Dir(viejo), 0o700)
	os.WriteFile(viejo, []byte(`{"agent_id":"a1","agent_token":"tok-v030",
		"websocket_url":"wss://api.novasoft.mx/ws/printing",
		"backend_url":"https://novasoft-backend-production.up.railway.app"}`), 0o600)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load debió migrar el config de v0.3.0: %v", err)
	}
	if cfg.AgentToken != "tok-v030" || cfg.AgentID != "a1" {
		t.Fatalf("se perdió el emparejamiento: %+v", cfg)
	}
	if cfg.BackendURL != "https://api.novasoft.mx" {
		t.Fatalf("la URL directa de Railway debe corregirse, quedó %q", cfg.BackendURL)
	}
	nuevo := filepath.Join(base, "NovaSoftAgent", "config.json")
	if _, err := os.Stat(nuevo); err != nil {
		t.Fatalf("el config debió quedar en la ruta nueva: %v", err)
	}
	if _, err := os.Stat(viejo); err != nil {
		t.Fatal("el config viejo NO se borra (rollback a 0.3.0 sigue funcionando)")
	}
}

func TestLaRutaNuevaGanaSobreLasViejas(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("rutas de %PROGRAMDATA% solo en Windows")
	}
	base := t.TempDir()
	t.Setenv("PROGRAMDATA", base)
	t.Setenv("APPDATA", filepath.Join(base, "appdata"))
	if err := Save(&Config{AgentID: "nuevo", AgentToken: "t-nuevo", WebsocketURL: "x", BackendURL: "https://api.novasoft.mx"}); err != nil {
		t.Fatal(err)
	}
	viejo := filepath.Join(base, "NovaSoft", "config.json")
	os.MkdirAll(filepath.Dir(viejo), 0o700)
	os.WriteFile(viejo, []byte(`{"agent_id":"viejo","agent_token":"t-viejo","websocket_url":"x"}`), 0o600)

	if migrated, _ := MigrateLegacy(); migrated {
		t.Fatal("no debe pisar un config nuevo existente")
	}
	cfg, _ := Load()
	if cfg.AgentToken != "t-nuevo" {
		t.Fatalf("ganó el config viejo: %+v", cfg)
	}
}
