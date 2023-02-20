package shardctrler

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"6.824/labgob"
	"6.824/labrpc"
	"6.824/raft"
)

type ShardCtrler struct {
	mu      sync.RWMutex
	me      int
	dead    int32
	rf      *raft.Raft
	applyCh chan raft.ApplyMsg

	stateMachine   ConfigStateMachine
	lastOperations map[int64]OperationContext    // {clientId: OperationContext}
	notifyChans    map[int]chan *CommandResponse // {raft-logIndex: CommandResponse}
}

func (sc *ShardCtrler) Command(req *CommandRequest, resp *CommandResponse) {
	defer DPrintf("[Command] - {Node: %v}'s state is {}, process command %v with CommandResp %v",
		sc.me, req, resp)

	sc.mu.RLock()
	if req.Op != OpQuery && sc.isDuplicateRequest(req.ClientId, req.CommandId) {
		lastResponse := sc.lastOperations[req.ClientId].LastResponse
		resp.Config, resp.Err = lastResponse.Config, lastResponse.Err
		sc.mu.RUnlock()
		return
	}
	sc.mu.RUnlock()

	// do not hold lock to improve throughput
	index, _, isLeader := sc.rf.Start(Command{req})
	if !isLeader {
		resp.Err = ErrWrongLeader
		return
	}

	sc.mu.Lock()
	ch := sc.getNotifyChan(index)
	sc.mu.Unlock()

	select {
	case result := <-ch:
		resp.Config, resp.Err = result.Config, result.Err
	case <-time.After(ExecuteTimeout):
		resp.Err = ErrTimeout
	}

	go func() {
		sc.mu.Lock()
		sc.removeOutdatedNotifyChan(index)
		sc.mu.Unlock()
	}()

}

// isDuplicateRequest determine whether the latest commandDI of a clientId meets the criteria
func (sc *ShardCtrler) isDuplicateRequest(clientId, commandId int64) bool {
	OperationContext, ok := sc.lastOperations[clientId]
	return ok && commandId <= OperationContext.MaxAppliedCommandId
}

// applier goroutine to apply to state machine
func (sc *ShardCtrler) applier() {
	for !sc.killed() {
		for msg := range sc.applyCh {
			DPrintf("[applier] - {Node: %v} tries to apply message %v", sc.me, msg)

			if msg.CommandValid {
				var resp *CommandResponse
				command := msg.Command.(Command)
				sc.mu.Lock()

				if command.Op != OpQuery && sc.isDuplicateRequest(command.ClientId, command.CommandId) {
					DPrintf("[applier] - {Node: %v} doesn't apply duplicated message %v to stateMachine because maxAppliedCommandId is %v for client %v",
						sc.rf, msg, sc.lastOperations[command.ClientId], command.ClientId)

					resp = sc.lastOperations[command.CommandId].LastResponse
				} else {
					resp = sc.applyLogToStateMachine(command)
					if command.Op != OpQuery {
						sc.lastOperations[command.ClientId] = OperationContext{
							MaxAppliedCommandId: command.CommandId,
							LastResponse:        resp,
						}
					}
				}

				if currentTerm, isLeader := sc.rf.GetState(); isLeader && msg.CommandTerm == currentTerm {
					ch := sc.getNotifyChan(msg.CommandIndex)
					ch <- resp
				}
				sc.mu.Unlock()
			} else {
				panic(fmt.Sprintf("[applier] - unexpected Message %v", msg))
			}
		}
	}
}

func (sc *ShardCtrler) applyLogToStateMachine(command Command) *CommandResponse {
	var conf Config
	var err Err
	switch command.Op {
	case OpJoin:
		err = sc.stateMachine.Join(command.Servers)
	case OpLeave:
		err = sc.stateMachine.Leave(command.GIds)
	case OpMove:
		err = sc.stateMachine.Move(command.Shard, command.GId)
	case OpQuery:
		conf, err = sc.stateMachine.Query(command.Num)
	}

	return &CommandResponse{
		Err:    err,
		Config: conf,
	}
}

// the tester calls Kill() when a ShardCtrler instance won't
// be needed again. you are not required to do anything
// in Kill(), but it might be convenient to (for example)
// turn off debug output from this instance.
func (sc *ShardCtrler) Kill() {
	DPrintf("{ShardCtrler %v} has been killed", sc.rf.Me())
	atomic.StoreInt32(&sc.dead, 1)
	sc.rf.Kill()
}

func (sc *ShardCtrler) killed() bool {
	return atomic.LoadInt32(&sc.dead) == 1
}

// needed by shardkv tester
func (sc *ShardCtrler) Raft() *raft.Raft {
	return sc.rf
}

func (sc *ShardCtrler) getNotifyChan(index int) chan *CommandResponse {
	if _, ok := sc.notifyChans[index]; !ok {
		sc.notifyChans[index] = make(chan *CommandResponse, 1)
	}
	return sc.notifyChans[index]
}

func (sc *ShardCtrler) removeOutdatedNotifyChan(index int) {
	delete(sc.notifyChans, index)
}

// servers[] contains the ports of the set of
// servers that will cooperate via Raft to
// form the fault-tolerant shardctrler service.
// me is the index of the current server in servers[].
func StartServer(servers []*labrpc.ClientEnd, me int, persister *raft.Persister) *ShardCtrler {
	labgob.Register(Command{})

	applyCh := make(chan raft.ApplyMsg)

	sc := &ShardCtrler{
		me:             me,
		dead:           0,
		rf:             raft.Make(servers, me, persister, applyCh),
		applyCh:        applyCh,
		stateMachine:   NewMemoryConfigStateMachine(),
		lastOperations: make(map[int64]OperationContext),
		notifyChans:    make(map[int]chan *CommandResponse),
	}

	go sc.applier()
	DPrintf("{ShardCtrler %v} has started", sc.rf.Me())
	return sc
}
