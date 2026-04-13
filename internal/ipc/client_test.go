package ipc

import (
	"errors"
	"sync"
	"testing"

	"github.com/fyang0507/sundial/internal/model"
)

func startTestServer(t *testing.T) (*Server, string) {
	t.Helper()
	sock := shortSocket(t)
	srv, err := NewServer(sock, &mockHandler{})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	srv.Serve()
	t.Cleanup(srv.Shutdown)
	return srv, sock
}

func TestClientCallEcho(t *testing.T) {
	_, sock := startTestServer(t)

	client := NewClient(sock)
	var result map[string]string
	err := client.Call("echo", map[string]string{"msg": "hello"}, &result)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if result["msg"] != "hello" {
		t.Errorf("result: got %v, want {msg: hello}", result)
	}
}

func TestClientCallErrorResponse(t *testing.T) {
	_, sock := startTestServer(t)

	client := NewClient(sock)
	err := client.Call("fail", nil, nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if got := err.Error(); got != "rpc error -32603: forced error" {
		t.Errorf("error message: got %q", got)
	}
}

func TestClientCallMethodNotFound(t *testing.T) {
	_, sock := startTestServer(t)

	client := NewClient(sock)
	err := client.Call("nonexistent", nil, nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestClientCallConcurrent(t *testing.T) {
	_, sock := startTestServer(t)

	client := NewClient(sock)

	var wg sync.WaitGroup
	errs := make(chan error, 10)

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var result map[string]string
			if err := client.Call("echo", map[string]string{"msg": "concurrent"}, &result); err != nil {
				errs <- err
				return
			}
			if result["msg"] != "concurrent" {
				errs <- errors.New("unexpected result")
			}
		}()
	}

	wg.Wait()
	close(errs)

	for err := range errs {
		t.Errorf("concurrent call error: %v", err)
	}
}

func TestClientPing(t *testing.T) {
	_, sock := startTestServer(t)

	client := NewClient(sock)
	if err := client.Ping(); err != nil {
		t.Fatalf("Ping: %v", err)
	}
}

func TestClientPingDaemonUnreachable(t *testing.T) {
	client := NewClient("/tmp/sundial-nonexistent-test.sock")
	err := client.Ping()
	if !errors.Is(err, model.ErrDaemonUnreachable) {
		t.Fatalf("Ping: got %v, want ErrDaemonUnreachable", err)
	}
}

func TestClientCallDaemonUnreachable(t *testing.T) {
	client := NewClient("/tmp/sundial-nonexistent-test.sock")
	err := client.Call("health", nil, nil)
	if !errors.Is(err, model.ErrDaemonUnreachable) {
		t.Fatalf("Call: got %v, want ErrDaemonUnreachable", err)
	}
}

func TestClientCallNilResult(t *testing.T) {
	_, sock := startTestServer(t)

	client := NewClient(sock)
	// Call with nil result — should not panic.
	err := client.Call("echo", map[string]string{"msg": "ignored"}, nil)
	if err != nil {
		t.Fatalf("Call with nil result: %v", err)
	}
}
