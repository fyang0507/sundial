package ipc

import (
	"encoding/json"
	"fmt"
	"net"
	"time"

	"github.com/fyang0507/sundial/internal/model"
)

// Client connects to the daemon over a Unix domain socket.
type Client struct {
	socketPath string
	timeout    time.Duration
}

// NewClient returns a Client that dials socketPath with a default 30-second
// read/write timeout.
func NewClient(socketPath string) *Client {
	return &Client{
		socketPath: socketPath,
		timeout:    30 * time.Second,
	}
}

// Call sends an RPC request and decodes the response into result.
// If the daemon is unreachable, it returns model.ErrDaemonUnreachable.
// If the response carries an RPCError, a Go error wrapping its message is returned.
// result may be nil if the caller does not need the response payload.
func (c *Client) Call(method string, params interface{}, result interface{}) error {
	conn, err := net.DialTimeout("unix", c.socketPath, 5*time.Second)
	if err != nil {
		return model.ErrDaemonUnreachable
	}
	defer conn.Close()

	if err := conn.SetDeadline(time.Now().Add(c.timeout)); err != nil {
		return fmt.Errorf("set deadline: %w", err)
	}

	reqData, err := MarshalRequest(method, params, 1)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}

	if err := WriteMessage(conn, reqData); err != nil {
		return fmt.Errorf("write request: %w", err)
	}

	respData, err := ReadMessage(conn)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}

	resp, err := UnmarshalResponse(respData)
	if err != nil {
		return fmt.Errorf("unmarshal response: %w", err)
	}

	if resp.Error != nil {
		return resp.Error
	}

	if result != nil && resp.Result != nil {
		if err := json.Unmarshal(resp.Result, result); err != nil {
			return fmt.Errorf("unmarshal result: %w", err)
		}
	}

	return nil
}

// Ping checks whether the daemon is reachable by dialing and immediately
// closing the socket. Returns model.ErrDaemonUnreachable on failure.
func (c *Client) Ping() error {
	conn, err := net.DialTimeout("unix", c.socketPath, 5*time.Second)
	if err != nil {
		return model.ErrDaemonUnreachable
	}
	conn.Close()
	return nil
}
