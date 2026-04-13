package ipc

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"

	"github.com/fyang0507/sundial/internal/model"
)

// maxMessageSize is the maximum allowed message size (10 MB).
const maxMessageSize = 10 * 1024 * 1024

// WriteMessage writes a length-prefixed message to w.
// The frame is a 4-byte big-endian uint32 length followed by that many bytes of data.
func WriteMessage(w io.Writer, data []byte) error {
	length := uint32(len(data))
	if err := binary.Write(w, binary.BigEndian, length); err != nil {
		return fmt.Errorf("write length prefix: %w", err)
	}
	if _, err := w.Write(data); err != nil {
		return fmt.Errorf("write message body: %w", err)
	}
	return nil
}

// ReadMessage reads a length-prefixed message from r.
// It rejects messages larger than maxMessageSize (10 MB).
func ReadMessage(r io.Reader) ([]byte, error) {
	var length uint32
	if err := binary.Read(r, binary.BigEndian, &length); err != nil {
		return nil, fmt.Errorf("read length prefix: %w", err)
	}
	if length > maxMessageSize {
		return nil, fmt.Errorf("message size %d exceeds maximum %d", length, maxMessageSize)
	}
	buf := make([]byte, length)
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, fmt.Errorf("read message body: %w", err)
	}
	return buf, nil
}

// MarshalRequest creates a JSON-encoded RPCRequest.
func MarshalRequest(method string, params interface{}, id interface{}) ([]byte, error) {
	var rawParams json.RawMessage
	if params != nil {
		p, err := json.Marshal(params)
		if err != nil {
			return nil, fmt.Errorf("marshal params: %w", err)
		}
		rawParams = p
	}
	req := model.RPCRequest{
		Method: method,
		Params: rawParams,
		ID:     id,
	}
	data, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	return data, nil
}

// UnmarshalRequest decodes a JSON-encoded RPCRequest.
func UnmarshalRequest(data []byte) (*model.RPCRequest, error) {
	var req model.RPCRequest
	if err := json.Unmarshal(data, &req); err != nil {
		return nil, fmt.Errorf("unmarshal request: %w", err)
	}
	return &req, nil
}

// MarshalResponse creates a JSON-encoded RPCResponse with a marshaled result.
func MarshalResponse(result interface{}, id interface{}) ([]byte, error) {
	var rawResult json.RawMessage
	if result != nil {
		r, err := json.Marshal(result)
		if err != nil {
			return nil, fmt.Errorf("marshal result: %w", err)
		}
		rawResult = r
	}
	resp := model.RPCResponse{
		Result: rawResult,
		ID:     id,
	}
	data, err := json.Marshal(resp)
	if err != nil {
		return nil, fmt.Errorf("marshal response: %w", err)
	}
	return data, nil
}

// MarshalErrorResponse creates a JSON-encoded RPCResponse containing an error.
func MarshalErrorResponse(rpcErr *model.RPCError, id interface{}) ([]byte, error) {
	resp := model.RPCResponse{
		Error: rpcErr,
		ID:    id,
	}
	data, err := json.Marshal(resp)
	if err != nil {
		return nil, fmt.Errorf("marshal error response: %w", err)
	}
	return data, nil
}

// UnmarshalResponse decodes a JSON-encoded RPCResponse.
func UnmarshalResponse(data []byte) (*model.RPCResponse, error) {
	var resp model.RPCResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}
	return &resp, nil
}
