package model

import "errors"

// Sentinel errors used across packages.
var (
	ErrDaemonUnreachable    = errors.New("daemon is not running or unreachable")
	ErrConfigNotFound       = errors.New("config.yaml not found")
	ErrConfigInvalid        = errors.New("config.yaml is invalid")
	ErrDataRepoInvalid      = errors.New("data_repo path is invalid or not a git repository")
	ErrScheduleNotFound     = errors.New("schedule not found")
	ErrDuplicateSchedule    = errors.New("duplicate schedule exists")
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
)
