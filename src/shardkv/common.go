package shardkv

import (
	"fmt"
	"log"
	"time"

	"6.824/shardctrler"
)

//
// Sharded key/value server.
// Lots of replica groups, each running Raft.
// Shardctrler decides which group serves each shard.
// Shardctrler may change shard assignment from time to time.
//
// You will have to modify these definitions.
//

const (
	ExecuteTimeout            = 500 * time.Millisecond
	ConfigureMonitorTimeout   = 100 * time.Millisecond
	MigrationMonitorTimeout   = 50 * time.Millisecond
	GCMonitorTimeout          = 50 * time.Millisecond
	EmptyEntryDetectorTimeout = 200 * time.Millisecond
)

// -------------------------------------------------------------------------

const Debug = false

func DPrintf(format string, a ...interface{}) (n int, err error) {
	if Debug {
		log.Printf(format, a...)
	}
	return
}

// -------------------------------------------------------------------------

type Err uint8

const (
	OK Err = iota
	ErrNoKey
	ErrWrongGroup
	ErrWrongLeader
	ErrOutDated
	ErrTimeout
	ErrNotReady
)

var errmap = [...]string{
	"OK", "ErrNoKey", "ErrWrongGroup", "ErrWrongLeader",
	"ErrOutDated", "ErrTimeout", "ErrNotReady",
}

func (err Err) String() string {
	return errmap[err]
}

// -------------------------------------------------------------

type ShardStatus uint8

const (
	Serving   ShardStatus = iota // 可以服务的状态
	Pulling                      // 上个config 中不存在的shard 但新config 生效后存在的shardId
	BePulling                    // 上个config 中存在的shard 但新config 生效后不存在的shardId
	GCing                        // 新config 把新迁移的shard 数据拉过来之后的状态(之后需要清楚上个config 遗留的数据)
)

var shardStatusMap = [...]string{
	"Serving", "Pulling", "BePulling", "GCing",
}

func (ss ShardStatus) String() string {
	return shardStatusMap[ss]
}

// -------------------------------------------------------------

type OperationContext struct {
	MaxAppliedCommandId int64
	LastResponse        *CommandResponse
}

func (context OperationContext) deepCopy() OperationContext {
	return OperationContext{context.MaxAppliedCommandId, &CommandResponse{context.LastResponse.Err, context.LastResponse.Value}}
}

// -------------------------------------------------------------

type Command struct {
	Op   CommandType
	Data interface{}
}

func (cmd Command) String() string {
	return fmt.Sprintf("{Op: %v, Data: %v}", cmd.Op, cmd.Data)
}

func NewOperationCommand(req *CommandRequest) Command {
	return Command{
		Op:   Operation,
		Data: *req,
	}
}

func NewConfigurationCommand(config *shardctrler.Config) Command {
	return Command{
		Op:   Configuration,
		Data: *config,
	}
}

func NewInsertShardsCommand(response *ShardOperationResponse) Command {
	return Command{InsertShards, *response}
}

func NewDeleteShardsCommand(request *ShardOperationRequest) Command {
	return Command{DeleteShards, *request}
}

func NewEmptyEntryCommand() Command {
	return Command{EmptyEntry, nil}
}

// -------------------------------------------------------------

type CommandType uint8

const (
	Operation CommandType = iota
	Configuration
	InsertShards
	DeleteShards
	EmptyEntry
)

var ctmap = [...]string{
	"Operation", "Configuration", "InsertShards", "DeleteShards", "EmptyEntry",
}

func (ct CommandType) String() string {
	return ctmap[ct]
}

// -------------------------------------------------------------

type OperationOp uint8

const (
	OpPut OperationOp = iota
	OpAppend
	OpGet
)

var opmap = [...]string{"OpPut", "OpAppend", "OpGet"}

func (op OperationOp) String() string {
	return opmap[op]
}

// -------------------------------------------------------------

type CommandRequest struct {
	Key       string
	Value     string
	Op        OperationOp
	ClientId  int64
	CommandId int64
}

func (req CommandRequest) String() string {
	return fmt.Sprintf("Shard: %v, Key: %v, Value: %v, Op: %v, ClientId: %v, CommandId: %v}",
		key2shard(req.Key), req.Key, req.Value, req.Op, req.ClientId, req.CommandId)
}

type CommandResponse struct {
	Err   Err
	Value string
}

func (resp CommandResponse) String() string {
	return fmt.Sprintf("{Err: %v, Value: %v}", resp.Err, resp.Value)
}

// -------------------------------------------------------------

type ShardOperationRequest struct {
	ConfigNum int
	ShardIDs  []int
}

func (req ShardOperationRequest) String() string {
	return fmt.Sprintf("{ConfigNum: %v, ShardIDs: %v}", req.ConfigNum, req.ShardIDs)
}

type ShardOperationResponse struct {
	Err            Err
	ConfigNum      int
	Shards         map[int]map[string]string // {shardId: shardData -> {map[string]string}}
	LastOperations map[int64]OperationContext
}

func (resp ShardOperationResponse) String() string {
	return fmt.Sprintf("{Err: %v, ConfigNum: %v, Shards: %v, LastOperations: %v}",
		resp.Err, resp.ConfigNum, resp.Shards, resp.LastOperations)
}
