package control

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"
)

type Handler func(Request) Response
type Server struct {
	listener net.Listener
	handler  Handler
	path     string
	owner    string
	identity ownerIdentity
}

func NewServer(stateDir string, handler Handler) (*Server, error) {
	if err := os.MkdirAll(stateDir, 0700); err != nil {
		return nil, err
	}
	path := filepath.Join(stateDir, "control.sock")
	if c, err := net.DialTimeout("unix", path, 100*time.Millisecond); err == nil {
		c.Close()
		return nil, fmt.Errorf("control socket is already live")
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	l, err := net.Listen("unix", path)
	if err != nil {
		return nil, fmt.Errorf("listen control socket: %w", err)
	}
	if err := os.Chmod(path, 0600); err != nil {
		l.Close()
		os.Remove(path)
		return nil, err
	}
	identity, err := currentOwnerIdentity()
	if err != nil {
		l.Close()
		os.Remove(path)
		return nil, err
	}
	owner := ownerPath(stateDir)
	if err := writeOwner(owner, identity); err != nil {
		l.Close()
		os.Remove(path)
		return nil, err
	}
	return &Server{listener: l, handler: handler, path: path, owner: owner, identity: identity}, nil
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
func (s *Server) Close() error {
	err := s.listener.Close()
	removeErr := os.Remove(s.path)
	if os.IsNotExist(removeErr) {
		removeErr = nil
	}
	if sameOwner(s.owner, s.identity) {
		if ownerErr := os.Remove(s.owner); ownerErr != nil && !os.IsNotExist(ownerErr) && removeErr == nil {
			removeErr = ownerErr
		}
	}
	if err != nil {
		return err
	}
	return removeErr
}
func serveConn(c net.Conn, h Handler) error {
	defer c.Close()
	var req Request
	if err := readMessage(c, &req); err != nil {
		return err
	}
	return writeMessage(c, h(req))
}
