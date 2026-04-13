package ipc

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/fyang0507/sundial/internal/model"
)

// Handler processes an incoming RPC request and returns a result or an error.
type Handler interface {
	Handle(method string, params json.RawMessage) (interface{}, *model.RPCError)
}

// Server listens on a Unix domain socket and dispatches incoming
// JSON-RPC requests to the registered Handler.
type Server struct {
	socketPath string
	listener   net.Listener
	handler    Handler
	wg         sync.WaitGroup
	quit       chan struct{}
}

// NewServer creates a Server bound to socketPath.
// It removes any stale socket file, ensures the parent directory exists,
// creates the Unix listener, and sets socket permissions to 0600.
func NewServer(socketPath string, handler Handler) (*Server, error) {
	// Remove stale socket if it exists (ignore ENOENT).
	if err := os.Remove(socketPath); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("remove stale socket: %w", err)
	}

	// Ensure parent directory exists.
	if err := os.MkdirAll(filepath.Dir(socketPath), 0755); err != nil {
		return nil, fmt.Errorf("create socket directory: %w", err)
	}

	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		return nil, fmt.Errorf("listen on unix socket: %w", err)
	}

	if err := os.Chmod(socketPath, 0600); err != nil {
		ln.Close()
		return nil, fmt.Errorf("chmod socket: %w", err)
	}

	return &Server{
		socketPath: socketPath,
		listener:   ln,
		handler:    handler,
		quit:       make(chan struct{}),
	}, nil
}

// Serve starts the accept loop in a goroutine. It returns immediately.
func (s *Server) Serve() {
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		for {
			conn, err := s.listener.Accept()
			if err != nil {
				select {
				case <-s.quit:
					return
				default:
					// Transient accept error; keep going.
					continue
				}
			}
			s.wg.Add(1)
			go s.handleConn(conn)
		}
	}()
}

// handleConn reads one request, dispatches to the handler, writes the
// response, and closes the connection.
func (s *Server) handleConn(conn net.Conn) {
	defer s.wg.Done()
	defer conn.Close()

	data, err := ReadMessage(conn)
	if err != nil {
		// Can't read request — nothing useful to send back.
		return
	}

	req, err := UnmarshalRequest(data)
	if err != nil {
		// Bad JSON — send an error response with nil ID.
		respData, _ := MarshalErrorResponse(&model.RPCError{
			Code:    model.RPCErrCodeInternal,
			Message: "invalid request: " + err.Error(),
		}, nil)
		_ = WriteMessage(conn, respData)
		return
	}

	result, rpcErr := s.handler.Handle(req.Method, req.Params)

	var respData []byte
	if rpcErr != nil {
		respData, err = MarshalErrorResponse(rpcErr, req.ID)
	} else {
		respData, err = MarshalResponse(result, req.ID)
	}
	if err != nil {
		respData, _ = MarshalErrorResponse(&model.RPCError{
			Code:    model.RPCErrCodeInternal,
			Message: "marshal response: " + err.Error(),
		}, req.ID)
	}

	_ = WriteMessage(conn, respData)
}

// Shutdown gracefully stops the server. It closes the listener, signals
// the accept loop to exit, and waits up to 10 seconds for in-flight
// connections to finish.
func (s *Server) Shutdown() {
	close(s.quit)
	s.listener.Close()

	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
	}
}

// SocketPath returns the path of the Unix domain socket.
func (s *Server) SocketPath() string {
	return s.socketPath
}
