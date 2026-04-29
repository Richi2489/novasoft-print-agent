//go:build windows

// Package ipc — implementación Windows del servidor (named pipe).
//
// Servidor: levanta un listener en \\.\pipe\NovaSoftAgent. SDDL aplicada
// permite a Authenticated Users (cualquier sesión usuario en la PC)
// conectar al servidor LocalSystem. Importante para el tray: el tray
// corre como user, el servicio como SYSTEM.
//
// Cada conexión es de vida corta: el cliente envía 1 Request en JSON
// terminado en newline, el servidor responde 1 Response, ambos cierran.
package ipc

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"sync"
	"time"

	"github.com/Microsoft/go-winio"
)

// PipeSDDL — Security Descriptor Definition Language string.
//   D:           DACL flag
//   P            Protected (no hereda ACEs del padre)
//   (A;;GA;;;SY) Allow GenericAll to LocalSystem
//   (A;;GRGW;;;AU) Allow GenericRead+GenericWrite to Authenticated Users
//
// AU (Authenticated Users) excluye al ANONYMOUS LOGIN — un proceso sin
// credenciales no puede conectar. El servidor identifica al cliente por
// ImpersonateNamedPipeClient si necesitamos audit; v0.3.0 no lo usa.
const PipeSDDL = "D:P(A;;GA;;;SY)(A;;GRGW;;;AU)"

// Handlers son las callbacks que el runner provee al IPC server. El
// servidor las invoca al recibir cada comando. Si un handler es nil,
// la respuesta es {OK: false, Error: "comando no soportado"}.
type Handlers struct {
	GetStatus func() StatusResponse
	Restart   func() error
	Pair      func(code, backendURL string) (agentID string, err error)
}

// Server abre el named pipe y procesa requests hasta que ctx se cancela.
// Bloquea — el caller típicamente lo arranca en una goroutine.
//
// Errores de listener (pipe ya en uso, permiso denegado al crear) se
// devuelven inmediatamente. Errores por-conexión se loguean pero no
// abortan el server.
func Serve(ctx context.Context, h Handlers) error {
	cfg := &winio.PipeConfig{
		SecurityDescriptor: PipeSDDL,
		// MessageMode false (default = byte mode). Nuestro protocolo es
		// line-delimited, no necesita boundaries de mensaje a nivel pipe.
		// InputBufferSize / OutputBufferSize: defaults OK para JSON corto.
	}

	listener, err := winio.ListenPipe(PipeName, cfg)
	if err != nil {
		return fmt.Errorf("listening pipe %s: %w", PipeName, err)
	}
	log.Printf("→ IPC: escuchando en %s", PipeName)

	// Cuando ctx termina, cerrar el listener para que Accept() retorne
	// ErrPipeListenerClosed. En esa goroutine NO usamos defer porque
	// puede correr después del return de Serve.
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-ctx.Done()
		_ = listener.Close()
	}()

	for {
		conn, err := listener.Accept()
		if err != nil {
			// Si el ctx cerró, esto es expected. Otro error es real.
			if ctx.Err() != nil {
				log.Println("← IPC: listener cerrado por shutdown")
				wg.Wait()
				return nil
			}
			if errors.Is(err, winio.ErrPipeListenerClosed) {
				wg.Wait()
				return nil
			}
			log.Printf("⚠ IPC accept error: %v", err)
			continue
		}
		go handleConn(conn, h)
	}
}

// handleConn procesa una sola conexión: lee 1 request, escribe 1
// response, cierra. Idempotente — si el cliente envía algo malformado,
// respondemos error y cerramos.
func handleConn(conn net.Conn, h Handlers) {
	defer conn.Close()

	// Timeout para evitar que un cliente colgado mantenga la conexión
	// abierta indefinidamente. 5s es generoso para un JSON corto.
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))

	reader := bufio.NewReader(conn)
	line, err := reader.ReadBytes('\n')
	if err != nil {
		log.Printf("⚠ IPC read: %v", err)
		return
	}

	var req Request
	if err := json.Unmarshal(line, &req); err != nil {
		writeResponse(conn, Response{OK: false, Error: "JSON inválido"})
		return
	}

	resp := dispatch(req, h)
	writeResponse(conn, resp)
}

// dispatch resuelve el comando. Comandos desconocidos → 4xx-style error.
func dispatch(req Request, h Handlers) Response {
	switch req.Command {
	case CmdGetStatus:
		if h.GetStatus == nil {
			return Response{OK: false, Error: "GetStatus handler no configurado"}
		}
		st := h.GetStatus()
		data, err := json.Marshal(st)
		if err != nil {
			return Response{OK: false, Error: "serialize status: " + err.Error()}
		}
		return Response{OK: true, Data: data}

	case CmdRestartConnection:
		if h.Restart == nil {
			return Response{OK: false, Error: "Restart handler no configurado"}
		}
		if err := h.Restart(); err != nil {
			return Response{OK: false, Error: err.Error()}
		}
		return Response{OK: true}

	case CmdPair:
		if h.Pair == nil {
			return Response{OK: false, Error: "Pair handler no configurado"}
		}
		var pr PairRequest
		if err := json.Unmarshal(req.Payload, &pr); err != nil {
			return Response{OK: false, Error: "payload pair inválido: " + err.Error()}
		}
		if pr.Code == "" {
			return Response{OK: false, Error: "código vacío"}
		}
		agentID, err := h.Pair(pr.Code, pr.BackendURL)
		if err != nil {
			return Response{OK: false, Error: err.Error()}
		}
		data, err := json.Marshal(PairResponseData{AgentID: agentID})
		if err != nil {
			return Response{OK: false, Error: "serialize pair response: " + err.Error()}
		}
		return Response{OK: true, Data: data}

	default:
		return Response{OK: false, Error: "comando desconocido: " + req.Command}
	}
}

// writeResponse serializa la respuesta + newline y la manda al cliente.
// Errores de escritura solo se loguean — el cliente probablemente cerró.
func writeResponse(conn net.Conn, resp Response) {
	data, err := json.Marshal(resp)
	if err != nil {
		log.Printf("⚠ IPC marshal response: %v", err)
		return
	}
	data = append(data, '\n')
	if _, err := conn.Write(data); err != nil {
		log.Printf("⚠ IPC write: %v", err)
	}
}
