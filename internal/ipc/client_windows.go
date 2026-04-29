//go:build windows

// Cliente del IPC para uso desde el tray, CLI status, o tooling.
// Cada Call abre conexión, manda request, recibe response, cierra.
package ipc

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Microsoft/go-winio"
)

// dialTimeout — cuánto esperamos por abrir la conexión al pipe. Si el
// servicio está corriendo y aceptando, esto debería ser instantáneo
// (<1ms en local). 2s da margen para retries del SCM si el servicio
// está arrancando.
const dialTimeout = 2 * time.Second

// readTimeout — cuánto esperamos por la respuesta una vez conectados.
// Las operaciones soportadas son in-memory (status snapshot) — 3s es
// holgadísimo.
const readTimeout = 3 * time.Second

// ErrServiceUnavailable se retorna cuando el pipe no existe (servicio
// no instalado / no corriendo) o no acepta conexiones. El tray lo
// traduce a "servicio no responde" en el menu.
var ErrServiceUnavailable = errors.New("IPC: servicio no disponible (¿está corriendo?)")

// Call abre la conexión, manda el request, lee la respuesta, cierra.
// Retorna ErrServiceUnavailable si el pipe no existe o el servidor no
// está aceptando. Otros errores son problemas de red/timeout/protocolo.
func Call(req Request) (*Response, error) {
	timeout := dialTimeout
	conn, err := winio.DialPipe(PipeName, &timeout)
	if err != nil {
		// winio devuelve errores específicos cuando el pipe no existe.
		// El test más portable es chequear si el error es algo que se
		// resolvería arrancando el servicio.
		return nil, fmt.Errorf("%w: %v", ErrServiceUnavailable, err)
	}
	defer conn.Close()

	_ = conn.SetDeadline(time.Now().Add(readTimeout))

	data, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	data = append(data, '\n')
	if _, err := conn.Write(data); err != nil {
		return nil, fmt.Errorf("write request: %w", err)
	}

	reader := bufio.NewReader(conn)
	line, err := reader.ReadBytes('\n')
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	var resp Response
	if err := json.Unmarshal(line, &resp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return &resp, nil
}

// GetStatus es un wrapper convencional para el comando más común.
func GetStatus() (*StatusResponse, error) {
	resp, err := Call(Request{Command: CmdGetStatus})
	if err != nil {
		return nil, err
	}
	if !resp.OK {
		return nil, fmt.Errorf("server: %s", resp.Error)
	}
	var st StatusResponse
	if err := json.Unmarshal(resp.Data, &st); err != nil {
		return nil, fmt.Errorf("decode status: %w", err)
	}
	return &st, nil
}

// RestartConnection le dice al servidor que cierre el SSE actual y
// reabra de inmediato. No espera por la nueva conexión — solo confirma
// que la solicitud fue aceptada.
func RestartConnection() error {
	resp, err := Call(Request{Command: CmdRestartConnection})
	if err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf("server: %s", resp.Error)
	}
	return nil
}

// Pair le pide al servicio que ejecute el handshake de pair con el
// código provisto. backendURL puede ser vacío — el servicio usa su
// default (api.novasoft.mx).
//
// Devuelve el agent_id devuelto por el backend en caso de éxito.
// Errores típicos vienen como string en err: "código inválido",
// "código expirado", "no pude conectar con el servidor".
//
// Read timeout extendido a 20s porque el handshake hace round-trip
// HTTP a api.novasoft.mx — más lento que un GET_STATUS local.
func Pair(code, backendURL string) (string, error) {
	payload, err := json.Marshal(PairRequest{Code: code, BackendURL: backendURL})
	if err != nil {
		return "", fmt.Errorf("marshal pair payload: %w", err)
	}

	resp, err := callWithTimeout(
		Request{Command: CmdPair, Payload: payload},
		dialTimeout,
		20*time.Second,
	)
	if err != nil {
		return "", err
	}
	if !resp.OK {
		return "", fmt.Errorf("%s", resp.Error)
	}
	var data PairResponseData
	if err := json.Unmarshal(resp.Data, &data); err != nil {
		return "", fmt.Errorf("decode pair response: %w", err)
	}
	return data.AgentID, nil
}

// callWithTimeout es como Call pero con timeouts custom. Existe porque
// Pair necesita más read budget que GetStatus (handshake remoto).
func callWithTimeout(req Request, dial, read time.Duration) (*Response, error) {
	conn, err := winio.DialPipe(PipeName, &dial)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrServiceUnavailable, err)
	}
	defer conn.Close()

	_ = conn.SetDeadline(time.Now().Add(read))

	data, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	data = append(data, '\n')
	if _, err := conn.Write(data); err != nil {
		return nil, fmt.Errorf("write request: %w", err)
	}

	reader := bufio.NewReader(conn)
	line, err := reader.ReadBytes('\n')
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	var resp Response
	if err := json.Unmarshal(line, &resp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return &resp, nil
}
