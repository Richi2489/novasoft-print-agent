// Package pairing — handshake HTTP con el backend para obtener el token
// del agent.
//
// Flujo:
//   1. Admin genera pairing_code en NovaSoft (wizard) → "ABCD-1234-WXYZ".
//   2. Agent llama POST {backend}/printing/agents/pair con el código.
//   3. Backend responde con agent_id + agent_token (una sola vez) +
//      websocket_url.
//   4. Agent persiste todo en config.json.
//
// El backend identifica al agent vía HMAC-SHA256(pepper, token); el
// agent solo conoce el token plain.
package pairing

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"strings"
	"time"
)

// Errores con nombre para que quien llama (el instalador, vía el código de
// salida de `pair`) distinga la causa sin parsear texto.
var (
	ErrCodeInvalid = errors.New("código inválido — revisa que esté bien copiado")
	ErrCodeUsed    = errors.New("este código ya fue usado — genera uno nuevo en NovaSoft")
	ErrCodeExpired = errors.New("el código expiró — genera uno nuevo en NovaSoft")
	ErrNoNetwork   = errors.New("no pude conectar con el servidor — revisa la conexión a internet")
)

// httpTimeout aplica al handshake de pair — debe ser suficiente para
// redes lentas pero no eterno. 15 s es un compromiso razonable.
const httpTimeout = 15 * time.Second

// PairResponse refleja el JSON del backend en AgentPairResponse.
type PairResponse struct {
	AgentID      string `json:"agent_id"`
	AgentToken   string `json:"agent_token"`
	WebsocketURL string `json:"websocket_url"`
}

// pairRequest es el cuerpo JSON que enviamos.
type pairRequest struct {
	PairingCode  string `json:"pairing_code"`
	Hostname     string `json:"hostname,omitempty"`
	AgentVersion string `json:"agent_version,omitempty"`
	OSInfo       string `json:"os_info,omitempty"`
}

// NormalizeCode limpia el input del usuario:
//   - Remueve guiones y espacios ("ABCD-1234-WXYZ" → "ABCD1234WXYZ").
//   - Uppercase ("abcd1234xyzw" → "ABCD1234XYZW").
//
// Así el usuario puede pegar el código con o sin guiones, en mayúsculas
// o minúsculas, con o sin espacios. El servidor recibe siempre la forma
// canónica.
//
// NOTA: el backend genera códigos CON guiones en grupos de 4. Si el
// backend es estricto y espera exactamente "ABCD-1234-XYZW", esta
// normalización rompería. El contrato es: normalizamos del lado cliente
// y re-insertamos los guiones en posiciones 4 y 9 antes de enviar.
func NormalizeCode(raw string) string {
	// Primer pass: remover separadores y whitespace, uppercase.
	s := strings.ToUpper(raw)
	s = strings.ReplaceAll(s, "-", "")
	s = strings.ReplaceAll(s, " ", "")
	s = strings.ReplaceAll(s, "\t", "")
	s = strings.TrimSpace(s)

	// Re-insertar guiones en posiciones 4 y 9 si tenemos exactamente 12
	// caracteres (formato ABCDEFGHIJKL → ABCD-EFGH-IJKL).
	if len(s) == 12 {
		return fmt.Sprintf("%s-%s-%s", s[0:4], s[4:8], s[8:12])
	}

	// Si el usuario pegó algo más (o menos), lo dejamos como está para
	// que el backend devuelva un 404 con mensaje útil en vez de que el
	// cliente oculte el error con una normalización agresiva.
	return s
}

// Pair ejecuta el handshake. Devuelve un PairResponse o un error con
// mensaje en español orientado al usuario.
//
// Errores típicos (basado en el backend Fase 1a):
//   - 404: código inválido.
//   - 409: código ya fue consumido.
//   - 410: código expiró.
//   - 5xx: error de servidor — mostramos el status code.
func Pair(backendURL, pairingCode, agentVersion string) (*PairResponse, error) {
	code := NormalizeCode(pairingCode)
	if code == "" {
		return nil, fmt.Errorf("código vacío")
	}

	hostname, _ := os.Hostname() // Best-effort; no bloquear si falla.
	osInfo := fmt.Sprintf("%s %s", runtime.GOOS, runtime.GOARCH)

	body, err := json.Marshal(pairRequest{
		PairingCode:  code,
		Hostname:     hostname,
		AgentVersion: agentVersion,
		OSInfo:       osInfo,
	})
	if err != nil {
		return nil, fmt.Errorf("serializando request: %w", err)
	}

	// Construir URL — el endpoint FastAPI vive en /printing/agents/pair
	// sin prefijo /api (ese es del BFF Next.js, que aquí NO usamos).
	endpoint := strings.TrimRight(backendURL, "/") + "/printing/agents/pair"

	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("construyendo request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "novasoft-print-agent/"+agentVersion)

	httpClient := &http.Client{Timeout: httpTimeout}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w (%v)", ErrNoNetwork, err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

	switch resp.StatusCode {
	case http.StatusOK:
		var pr PairResponse
		if err := json.Unmarshal(respBody, &pr); err != nil {
			return nil, fmt.Errorf("respuesta inesperada del servidor: %w", err)
		}
		if pr.AgentToken == "" || pr.WebsocketURL == "" {
			return nil, fmt.Errorf("respuesta incompleta del servidor — reporta a soporte")
		}
		return &pr, nil
	case http.StatusNotFound:
		return nil, ErrCodeInvalid
	case http.StatusConflict:
		return nil, ErrCodeUsed
	case http.StatusGone:
		return nil, ErrCodeExpired
	default:
		return nil, fmt.Errorf("servidor retornó %d: %s", resp.StatusCode, previewBody(respBody))
	}
}

// previewBody trunca el cuerpo para los mensajes de error — evita
// vomitar 10KB de HTML de una página de error por error.
func previewBody(b []byte) string {
	const max = 200
	if len(b) > max {
		return string(b[:max]) + "..."
	}
	return string(b)
}
