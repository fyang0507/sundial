package ipc

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/fyang0507/sundial/internal/model"
)

// mockHandler implements Handler for tests.
type mockHandler struct{}

func (h *mockHandler) Handle(method string, params json.RawMessage) (interface{}, *model.RPCError) {
	switch method {
	case "echo":
		var p map[string]string
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &model.RPCError{Code: model.RPCErrCodeInvalidParams, Message: err.Error()}
		}
		return p, nil
	case "fail":
		return nil, &model.RPCError{Code: model.RPCErrCodeInternal, Message: "forced error"}
	default:
		return nil, &model.RPCError{Code: model.RPCErrCodeMethodNotFound, Message: "unknown method"}
	}
}

// shortSocket returns a socket path short enough for Unix domain sockets (~104 char limit).
func shortSocket(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	// Use a very short name to stay within the limit.
	return filepath.Join(dir, "s.sock")
}

func TestNewServerRemovesStaleSocket(t *testing.T) {
	sock := shortSocket(t)

	// Create a regular file at the socket path to simulate a stale socket.
	if err := os.WriteFile(sock, []byte("stale"), 0644); err != nil {
		t.Fatalf("create stale file: %v", err)
	}

	srv, err := NewServer(sock, &mockHandler{})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	defer srv.Shutdown()

	// The server should have replaced the stale file with a real socket.
	info, err := os.Stat(sock)
	if err != nil {
		t.Fatalf("stat socket: %v", err)
	}
	// Socket files have ModeSocket bit set.
	if info.Mode()&os.ModeSocket == 0 {
		t.Errorf("expected socket file, got mode %v", info.Mode())
	}
}

func TestNewServerCreatesParentDir(t *testing.T) {
	// Use /tmp directly with a short name to stay within Unix socket path limits.
	base := filepath.Join("/tmp", "sd-test-mkdir")
	sock := filepath.Join(base, "sub", "s.sock")
	t.Cleanup(func() { os.RemoveAll(base) })

	srv, err := NewServer(sock, &mockHandler{})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	defer srv.Shutdown()

	if _, err := os.Stat(sock); err != nil {
		t.Fatalf("socket file not created: %v", err)
	}
}

func TestServerSocketPermissions(t *testing.T) {
	sock := shortSocket(t)

	srv, err := NewServer(sock, &mockHandler{})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	defer srv.Shutdown()

	info, err := os.Stat(sock)
	if err != nil {
		t.Fatalf("stat socket: %v", err)
	}
	// On macOS/Linux the permission bits for sockets can vary, but Chmod(0600)
	// should result in owner-only read/write.
	perm := info.Mode().Perm()
	if perm != 0600 {
		t.Errorf("socket permissions: got %o, want 0600", perm)
	}
}

func TestSocketPath(t *testing.T) {
	sock := shortSocket(t)
	srv, err := NewServer(sock, &mockHandler{})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	defer srv.Shutdown()

	if srv.SocketPath() != sock {
		t.Errorf("SocketPath: got %q, want %q", srv.SocketPath(), sock)
	}
}
