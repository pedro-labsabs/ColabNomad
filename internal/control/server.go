package control

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
)

type Handler func(Request) Response
type Server struct {
	listener net.Listener
	handler  Handler
}

func NewServer(stateDir string, handler Handler) (*Server, error) {
	if err := os.MkdirAll(stateDir, 0700); err != nil {
		return nil, err
	}
	path := filepath.Join(stateDir, "control.sock")
	_ = os.Remove(path)
	l, err := net.Listen("unix", path)
	if err != nil {
		return nil, fmt.Errorf("listen control socket: %w", err)
	}
	if err := os.Chmod(path, 0600); err != nil {
		l.Close()
		os.Remove(path)
		return nil, err
	}
	return &Server{listener: l, handler: handler}, nil
}
func (s *Server) Serve(ctx context.Context) error {
	go func() { <-ctx.Done(); s.listener.Close() }()
	for {
		c, err := s.listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		go serveConn(c, s.handler)
	}
}
func (s *Server) Close() error { return s.listener.Close() }
func serveConn(c net.Conn, h Handler) error {
	defer c.Close()
	var req Request
	if err := readMessage(c, &req); err != nil {
		return err
	}
	return writeMessage(c, h(req))
}
