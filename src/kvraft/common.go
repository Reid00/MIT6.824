package kvraft

import (
	"fmt"
	"log"
	"time"
)

const ExecuteTimeOut = 500 * time.Millisecond
const Debug = true

func DPrintf(format string, a ...interface{}) (n int, err error) {
	if Debug {
		log.Printf(format, a...)
	}
	return
}

// ------------------------------------------------
type Command struct {
	*CommandRequest
}

type OperationContext struct {
	MaxAppliedCommandId int64
	LastResponse        *CommandResponse
}

// ------------------------------------------------
type Err uint8

const (
	OK Err = iota
	ErrNoKey
	ErrWrongLeader
	ErrTimeout
)

func (err Err) String() string {
	switch err {
	case OK:
		return "OK"
	case ErrNoKey:
		return "ErrNoKey"
	case ErrWrongLeader:
		return "ErrWrongLeader"
	case ErrTimeout:
		return "ErrTimeOut"
	default:
		panic(fmt.Sprintf("unexpected Err %d", err))
	}

}

// ------------------------------------------------
type OperationType uint8

const (
	OpPut OperationType = iota
	OpAppend
	OpGet
)

func (op OperationType) String() string {
	switch op {
	case OpPut:
		return "OpPut"
	case OpAppend:
		return "OpAppend"
	case OpGet:
		return "OpGet"
	default:
		panic(fmt.Sprintf("unexpected OperationType %d", op))
	}
}

// ------------------------------------------------
type CommandRequest struct {
	Key       string
	Value     string
	Op        OperationType
	ClientId  int64
	CommandId int64
}

func (req CommandRequest) String() string {
	return fmt.Sprintf("{Key: %v, Value: %v, Op: %v, ClientId: %v, CommandId: %v}",
		req.Key, req.Value, req.Op, req.ClientId, req.CommandId)

}

type CommandResponse struct {
	Err   Err
	Value string
}

func (resp CommandResponse) String() string {
	return fmt.Sprintf("{Err: %v, Value: %v}", resp.Err, resp.Value)
}
