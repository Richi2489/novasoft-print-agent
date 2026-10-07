package pairing

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

// El instalador decide qué mensaje mostrar a partir de estos errores (vía el
// código de salida de `pair`), así que el mapeo status → error es contrato.
func TestPairMapeaCadaStatusASuError(t *testing.T) {
	casos := []struct {
		status int
		quiere error
	}{
		{http.StatusNotFound, ErrCodeInvalid},
		{http.StatusConflict, ErrCodeUsed},
		{http.StatusGone, ErrCodeExpired},
	}
	for _, c := range casos {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(c.status)
		}))
		_, err := Pair(srv.URL, "ABCD-1234-WXYZ", "test")
		srv.Close()
		if !errors.Is(err, c.quiere) {
			t.Errorf("status %d: esperaba %v, obtuve %v", c.status, c.quiere, err)
		}
	}
}

func TestPairSinServidorEsErrNoNetwork(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close() // puerto cerrado: la conexión no abre
	if _, err := Pair(url, "ABCD-1234-WXYZ", "test"); !errors.Is(err, ErrNoNetwork) {
		t.Fatalf("esperaba ErrNoNetwork, obtuve %v", err)
	}
}

func TestPairOKDevuelveToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/printing/agents/pair" {
			t.Errorf("ruta inesperada %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"agent_id":"a1","agent_token":"tok","websocket_url":"wss://x"}`))
	}))
	defer srv.Close()
	resp, err := Pair(srv.URL, "abcd 1234 wxyz", "test")
	if err != nil || resp.AgentToken != "tok" {
		t.Fatalf("esperaba token, obtuve %+v, %v", resp, err)
	}
}

func TestNormalizeCode(t *testing.T) {
	for in, quiere := range map[string]string{
		"abcd-1234-wxyz":   "ABCD-1234-WXYZ",
		" ABCD 1234 WXYZ ": "ABCD-1234-WXYZ",
		"ABCD1234WXYZ":     "ABCD-1234-WXYZ",
	} {
		if got := NormalizeCode(in); got != quiere {
			t.Errorf("NormalizeCode(%q) = %q, quiere %q", in, got, quiere)
		}
	}
}
