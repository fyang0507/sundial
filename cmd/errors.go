package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/fyang0507/sundial/internal/format"
	"github.com/fyang0507/sundial/internal/model"
)

// handleCallError handles errors returned by ipc.Client.Call with structured
// formatting for known error types. It prints the formatted error and exits.
func handleCallError(err error) {
	// Daemon unreachable — structured error from client.
	var daemonErr *model.DaemonUnreachableError
	if errors.As(err, &daemonErr) {
		fmt.Println(format.FormatDaemonUnreachableError(daemonErr, jsonOutput))
		os.Exit(1)
	}

	// RPC errors with structured data.
	var rpcErr *model.RPCError
	if errors.As(err, &rpcErr) {
		if rpcErr.Data != nil {
			if msg := tryFormatRPCError(rpcErr); msg != "" {
				fmt.Println(msg)
				os.Exit(1)
			}
		}
	}

	// Fallback to generic error.
	fmt.Println(format.FormatError(err.Error(), jsonOutput))
	os.Exit(1)
}

// tryFormatRPCError attempts to unmarshal structured data from an RPCError and
// route it to the appropriate rich formatter. Returns "" if no match.
func tryFormatRPCError(rpcErr *model.RPCError) string {
	switch rpcErr.Code {
	case model.RPCErrCodeNotFound:
		var info model.NotFoundInfo
		if json.Unmarshal(rpcErr.Data, &info) == nil {
			return format.FormatNotFoundError(&info, jsonOutput)
		}
	case model.RPCErrCodeDuplicate:
		var info model.DuplicateInfo
		if json.Unmarshal(rpcErr.Data, &info) == nil {
			return format.FormatDuplicateError(&info, jsonOutput)
		}
	case model.RPCErrCodeGitPrecondition:
		var info model.GitPreconditionInfo
		if json.Unmarshal(rpcErr.Data, &info) == nil {
			return format.FormatGitPreconditionError(&info, jsonOutput)
		}
	case model.RPCErrCodeStateConflict:
		var info model.StateConflictInfo
		if json.Unmarshal(rpcErr.Data, &info) == nil {
			return format.FormatStateConflictError(&info, jsonOutput)
		}
	case model.RPCErrCodeInvalidParams:
		var info model.InvalidTriggerInfo
		if json.Unmarshal(rpcErr.Data, &info) == nil && info.TriggerType != "" {
			return format.FormatInvalidTriggerError(&info, jsonOutput)
		}
	}
	return ""
}

// handleClientError handles errors from getClient() (config loading failures).
func handleClientError(err error) {
	if errors.Is(err, model.ErrDataRepoNotResolved) {
		if jsonOutput {
			m := map[string]string{
				"error": "data repo not resolved",
				"hint":  `run 'sundial setup --data-repo <path>' to scaffold a data repo, or set SUNDIAL_DATA_REPO, or invoke from a directory under one with .agents/workspace.yaml`,
			}
			data, _ := json.Marshal(m)
			fmt.Println(string(data))
		} else {
			fmt.Println("Error: data repo not resolved")
			fmt.Println("  hint: run 'sundial setup --data-repo <path>' to scaffold a data repo, or set SUNDIAL_DATA_REPO, or invoke from a directory under one with .agents/workspace.yaml")
		}
		os.Exit(1)
	}

	fmt.Println(format.FormatError(err.Error(), jsonOutput))
	os.Exit(1)
}
