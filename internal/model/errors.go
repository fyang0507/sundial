package model

import (
	"errors"
	"fmt"
)

// Sentinel errors used across packages.
var (
	ErrDaemonUnreachable     = errors.New("daemon is not running or unreachable")
	ErrConfigNotFound        = errors.New("config.yaml not found")
	ErrConfigInvalid         = errors.New("config.yaml is invalid")
	ErrDataRepoInvalid       = errors.New("data_repo path is invalid or not a git repository")
	ErrDataRepoNotResolved   = errors.New("data repo could not be resolved")
	ErrScheduleNotFound      = errors.New("schedule not found")
	ErrDuplicateSchedule     = errors.New("duplicate schedule exists")
	ErrGitPreconditionFailed = errors.New("data repo git precondition failed")
)

// RPC error codes.
const (
	RPCErrCodeInternal        = -32603
	RPCErrCodeInvalidParams   = -32602
	RPCErrCodeMethodNotFound  = -32601
	RPCErrCodeDuplicate       = -32001
	RPCErrCodeNotFound        = -32002
	RPCErrCodeGitPrecondition = -32003
	RPCErrCodeStateConflict   = -32004
)

// DaemonUnreachableError is returned when the CLI cannot connect to the daemon.
// It carries structured context (socket path, failure reason) for agent
// self-healing while remaining compatible with errors.Is(err, ErrDaemonUnreachable).
type DaemonUnreachableError struct {
	SocketPath    string `json:"socket_path"`
	FailureReason string `json:"failure_reason"` // "socket_not_found" or "connection_refused"
}

func (e *DaemonUnreachableError) Error() string {
	return fmt.Sprintf("daemon is not running or unreachable (socket: %s, reason: %s)", e.SocketPath, e.FailureReason)
}

func (e *DaemonUnreachableError) Unwrap() error {
	return ErrDaemonUnreachable
}
