//go:build !windows

package printer

import (
	"bytes"
	"io"
	"net"
	"testing"
)

// Corre en macOS/Linux (CI o la Mac); en Windows no compila por el tag.
func TestSendRawPorTCP(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	got := make(chan []byte, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		b, _ := io.ReadAll(c)
		c.Close()
		got <- b
	}()
	data := []byte{0x1b, 0x40, 'h', 'o', 'l', 'a', 0x1d, 0x56, 0x00}
	p := Pinned("tcp://" + ln.Addr().String())
	if err := SendRaw(p, data); err != nil {
		t.Fatal(err)
	}
	if b := <-got; !bytes.Equal(b, data) {
		t.Fatalf("recibido %v, quiere %v", b, data)
	}
}

func TestSendRawPorCUPSUsaLaCola(t *testing.T) {
	prev := lpFn
	defer func() { lpFn = prev }()
	var cola string
	var bytesEnviados []byte
	lpFn = func(q string, d []byte) error { cola, bytesEnviados = q, d; return nil }
	if err := SendRaw(Pinned("POS_80"), []byte("x")); err != nil {
		t.Fatal(err)
	}
	if cola != "POS_80" || string(bytesEnviados) != "x" {
		t.Fatalf("cola=%q bytes=%q", cola, bytesEnviados)
	}
}
