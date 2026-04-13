package ipc

import (
	"bytes"
	"encoding/binary"
	"testing"

	"github.com/fyang0507/sundial/internal/model"
)

func TestWriteReadMessageRoundTrip(t *testing.T) {
	original := []byte(`{"method":"health","id":1}`)

	var buf bytes.Buffer
	if err := WriteMessage(&buf, original); err != nil {
		t.Fatalf("WriteMessage: %v", err)
	}

	got, err := ReadMessage(&buf)
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}

	if !bytes.Equal(got, original) {
		t.Fatalf("round-trip mismatch: got %q, want %q", got, original)
	}
}

func TestReadMessageRejectsOversized(t *testing.T) {
	// Write a length prefix that exceeds maxMessageSize.
	var buf bytes.Buffer
	oversized := uint32(maxMessageSize + 1)
	if err := binary.Write(&buf, binary.BigEndian, oversized); err != nil {
		t.Fatalf("write length: %v", err)
	}

	_, err := ReadMessage(&buf)
	if err == nil {
		t.Fatal("expected error for oversized message, got nil")
	}
}

func TestWriteReadEmptyMessage(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteMessage(&buf, []byte{}); err != nil {
		t.Fatalf("WriteMessage: %v", err)
	}

	got, err := ReadMessage(&buf)
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}

	if len(got) != 0 {
		t.Fatalf("expected empty message, got %d bytes", len(got))
	}
}

func TestMarshalUnmarshalRequest(t *testing.T) {
	params := map[string]string{"id": "abc123"}
	data, err := MarshalRequest("show", params, 42)
	if err != nil {
		t.Fatalf("MarshalRequest: %v", err)
	}

	req, err := UnmarshalRequest(data)
	if err != nil {
		t.Fatalf("UnmarshalRequest: %v", err)
	}

	if req.Method != "show" {
		t.Errorf("method: got %q, want %q", req.Method, "show")
	}
	if req.Params == nil {
		t.Fatal("expected non-nil params")
	}
	// ID may be deserialized as float64 from JSON.
	idFloat, ok := req.ID.(float64)
	if !ok || idFloat != 42 {
		t.Errorf("id: got %v (%T), want 42", req.ID, req.ID)
	}
}

func TestMarshalUnmarshalRequestNilParams(t *testing.T) {
	data, err := MarshalRequest("list", nil, 1)
	if err != nil {
		t.Fatalf("MarshalRequest: %v", err)
	}

	req, err := UnmarshalRequest(data)
	if err != nil {
		t.Fatalf("UnmarshalRequest: %v", err)
	}

	if req.Method != "list" {
		t.Errorf("method: got %q, want %q", req.Method, "list")
	}
}

func TestMarshalUnmarshalResponse(t *testing.T) {
	result := map[string]bool{"healthy": true}
	data, err := MarshalResponse(result, 1)
	if err != nil {
		t.Fatalf("MarshalResponse: %v", err)
	}

	resp, err := UnmarshalResponse(data)
	if err != nil {
		t.Fatalf("UnmarshalResponse: %v", err)
	}

	if resp.Error != nil {
		t.Fatalf("unexpected error in response: %v", resp.Error)
	}
	if resp.Result == nil {
		t.Fatal("expected non-nil result")
	}
}

func TestMarshalUnmarshalErrorResponse(t *testing.T) {
	rpcErr := &model.RPCError{
		Code:    model.RPCErrCodeNotFound,
		Message: "schedule not found",
	}
	data, err := MarshalErrorResponse(rpcErr, 1)
	if err != nil {
		t.Fatalf("MarshalErrorResponse: %v", err)
	}

	resp, err := UnmarshalResponse(data)
	if err != nil {
		t.Fatalf("UnmarshalResponse: %v", err)
	}

	if resp.Error == nil {
		t.Fatal("expected error in response")
	}
	if resp.Error.Code != model.RPCErrCodeNotFound {
		t.Errorf("error code: got %d, want %d", resp.Error.Code, model.RPCErrCodeNotFound)
	}
	if resp.Error.Message != "schedule not found" {
		t.Errorf("error message: got %q, want %q", resp.Error.Message, "schedule not found")
	}
}
