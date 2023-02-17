package kvraft

import (
	"bytes"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"6.824/labgob"
	"6.824/labrpc"
	"6.824/raft"
)

type KVServer struct {
	mu      sync.RWMutex
	me      int
	rf      *raft.Raft
	applyCh chan raft.ApplyMsg
	dead    int32 // set by Kill()

	maxRaftState int // snapshot if log grows this big

	// Your definitions here.
	lastApplied  int            // record the lastApplied index to prevent stateMachine from rollback
	stateMachine KVStateMachine // KV stateMachine

	// 客户端id最后的命令id和回复内容 （clientId，OperationContext{最后的commdId，最后的LastReply}）
	lastOperations  map[int64]OperationContext
	lastOperations2 sync.Map // clientId，OperationContext{最后的commdId，最后的LastReply}

	// Leader回复给客户端的响应（LogIndex， CommandResponse
	notifyChans map[int]chan *CommandResponse
}

// Command 客户端调用的RPC方法
func (kv *KVServer) Command(req *CommandRequest, resp *CommandResponse) {
	defer DPrintf("[Command]- {Node: %v} processes CommandReq %v with CommandResp %v",
		kv.rf.Me(), req, resp)

	// 如果请求是重复的，直接在 OperationContext 中拿到之前的结果返回
	if req.Op != OpGet && kv.isDuplicatedReq(req.ClientId, req.CommandId) {
		optCtx := kv.getLastOperation(req.ClientId)
		if optCtx != nil {
			resp.Value, resp.Err = optCtx.LastResponse.Value, optCtx.LastResponse.Err
			return
		}
		panic("[Command] - OptCtx failed")
	}

	// kv.mu.RLock()
	// if req.Op != OpGet && kv.isDuplicatedReq(req.ClientId, req.CommandId) {
	// 	lastResp := kv.lastOperations[req.ClientId].LastResponse
	// 	resp.Value, resp.Err = lastResp.Value, lastResp.Err
	// 	kv.mu.RUnlock()
	// 	return
	// }
	// kv.mu.RUnlock()

	idx, _, isLeader := kv.rf.Start(Command{req})
	if !isLeader {
		resp.Err = ErrWrongLeader
		return
	}

	kv.mu.Lock()
	ch := kv.getNotifyChan(idx)
	kv.mu.Unlock()

	select {
	case result := <-ch:
		resp.Value, resp.Err = result.Value, result.Err

	case <-time.After(ExecuteTimeOut):
		resp.Err = ErrTimeout
	}

	go func() {
		kv.mu.Lock()
		kv.removeOutdatedNotifyChan(idx)
		kv.mu.Unlock()
	}()

}

// isDuplicatedReq 判断请求是否是重复的
func (kv *KVServer) isDuplicatedReq(clientId int64, requestId int64) bool {
	// operationCtx, ok := kv.lastOperations[clientId]
	// return ok && requestId <= operationCtx.MaxAppliedCommandId
	optCtx := kv.getLastOperation(clientId)
	if optCtx != nil {
		return requestId <= optCtx.MaxAppliedCommandId
	}
	return false
}

// getLastOperation 从Sync.Map 中取出 OperationContext
func (kv *KVServer) getLastOperation(clientId int64) *OperationContext {
	optCtx, ok := kv.lastOperations2.Load(clientId)
	if !ok {
		return nil
	}
	val, ok := optCtx.(OperationContext)
	if ok {
		return &val
	}
	return nil
}

// getNotifyChan return logIndex's corresponding  stateMachine CommandResponse
func (kv *KVServer) getNotifyChan(idx int) chan *CommandResponse {
	if _, ok := kv.notifyChans[idx]; !ok {
		kv.notifyChans[idx] = make(chan *CommandResponse, 1)
	}
	return kv.notifyChans[idx]
}

// removeOutdatedNotifyChan delete outdated log index chan
func (kv *KVServer) removeOutdatedNotifyChan(index int) {
	delete(kv.notifyChans, index)
}

// the tester calls Kill() when a KVServer instance won't
// be needed again. for your convenience, we supply
// code to set rf.dead (without needing a lock),
// and a killed() method to test rf.dead in
// long-running loops. you can also add your own
// code to Kill(). you're not required to do anything
// about this, but it may be convenient (for example)
// to suppress debug output from a Kill()ed instance.
func (kv *KVServer) Kill() {
	// DPrintf("[Kill] - {Node: %v} has been killed - kv.rf.Me()", kv.rf.Me())
	DPrintf("[Kill] - {Node: %v} has been killed - kv.me", kv.me)
	atomic.StoreInt32(&kv.dead, 1)
	kv.rf.Kill()
}

func (kv *KVServer) killed() bool {
	z := atomic.LoadInt32(&kv.dead)
	return z == 1
}

func (kv *KVServer) applier() {
	for !kv.killed() {
		for msg := range kv.applyCh {
			DPrintf("[applier] - {Node: %v} tries to apply message %v", kv.rf.Me(), msg)
			if msg.CommandValid {
				// kv.mu.Lock()
				if msg.CommandIndex <= kv.lastApplied {
					DPrintf("[applier] - {Node: %v} discards outdated message %v since a newer snapshot which lastapplied is %v has been restored",
						kv.rf.Me(), msg, kv.lastApplied)

					// kv.mu.Unlock()
					continue
				}
				kv.mu.Lock()
				kv.lastApplied = msg.CommandIndex
				kv.mu.Unlock()

				var resp = new(CommandResponse)
				command := msg.Command.(Command)
				if command.Op != OpGet && kv.isDuplicatedReq(command.ClientId, command.CommandId) {
					// DPrintf("[applier] - {Node: %v} doesn't apply duplicated message %v to state machine since maxAppliedCommandId is %v for client %v",
					// 	kv.rf.Me(), msg, kv.lastOperations[command.ClientId], command.ClientId)

					// resp = kv.lastOperations[command.ClientId].LastResponse
					DPrintf("[applier] - {Node: %v} doesn't apply duplicated message %v to state machine since maxAppliedCommandId is %v for client %v",
						kv.rf.Me(), msg, kv.getLastOperation(command.ClientId), command.ClientId)
					optCtx := kv.getLastOperation(command.ClientId)
					if optCtx != nil {
						resp = optCtx.LastResponse
					}
				} else {
					kv.mu.Lock()
					resp = kv.applyLogToStateMachine(command)
					kv.mu.Unlock()
					if command.Op != OpGet {
						// kv.lastOperations[command.ClientId] = OperationContext{
						// 	MaxAppliedCommandId: command.CommandId,
						// 	LastResponse:        resp,
						// }
						kv.lastOperations2.Store(command.ClientId, OperationContext{
							MaxAppliedCommandId: command.CommandId,
							LastResponse:        resp,
						})
					}
				}

				// 记录每个idx apply 到state machine 的 CommandResponse
				// 为了保证强一致性，仅对当前 term 日志的 notifyChan 进行通知，
				// 让之前 term 的客户端协程都超时重试。避免leader 降级为 follower
				// 后又迅速重新当选了 leader，而此时依然有客户端协程未超时在阻塞等待，
				// 那么此时 apply 日志后，根据 index 获得 channel 并向其中 push 执行结果就可能出错，因为可能并不对应
				kv.mu.Lock()
				if currentTerm, isLeader := kv.rf.GetState(); isLeader && msg.CommandTerm == currentTerm {
					ch := kv.getNotifyChan(msg.CommandIndex)
					ch <- resp
				}

				// part 2
				needSnapshot := kv.needSnapshot()
				if needSnapshot {
					kv.takeSnapshot(msg.CommandIndex)
				}
				kv.mu.Unlock()
			} else if msg.SnapshotValid {
				kv.mu.Lock()
				if kv.rf.CondInstallSnapshot(msg.SnapshotTerm, msg.SnapshotIndex, msg.Snapshot) {
					kv.restoreSnapshot(msg.Snapshot)
					kv.lastApplied = msg.SnapshotIndex
				}
				kv.mu.Unlock()
			} else {
				panic(fmt.Sprintf("unexpected Message: %v", msg))
			}
		}
	}
}

// applyLogToStateMachine apply to state machine and log CommandResponse
func (kv *KVServer) applyLogToStateMachine(command Command) *CommandResponse {
	var value string
	var err Err
	switch command.Op {
	case OpPut:
		err = kv.stateMachine.Put(command.Key, command.Value)
	case OpAppend:
		err = kv.stateMachine.Append(command.Key, command.Value)
	case OpGet:
		value, err = kv.stateMachine.Get(command.Key)
	}

	return &CommandResponse{
		Err:   err,
		Value: value,
	}
}

// needSnapshot whether to take snapshot
func (kv *KVServer) needSnapshot() bool {
	return kv.maxRaftState != -1 && kv.rf.GetRaftStateSize() >= kv.maxRaftState
}

// takeSnapshot stateMachine do Snapshot from index location
func (kv *KVServer) takeSnapshot(index int) {
	buf := new(bytes.Buffer)
	enc := labgob.NewEncoder(buf)

	enc.Encode(kv.stateMachine)
	enc.Encode(kv.lastOperations)
	kv.rf.Snapshot(index, buf.Bytes())
}

func (kv *KVServer) restoreSnapshot(snapshot []byte) {
	if len(snapshot) == 0 {
		return
	}

	buf := bytes.NewBuffer(snapshot)
	dec := labgob.NewDecoder(buf)

	var stateMachine MemoryKV
	var lastOperations map[int64]OperationContext
	if dec.Decode(&stateMachine) != nil || dec.Decode(&lastOperations) != nil {
		DPrintf("[restoreSnapshot] - {Node: %v} restore snapshot failed", kv.rf.Me())
	}
	kv.stateMachine, kv.lastOperations = &stateMachine, lastOperations
}

// servers[] contains the ports of the set of
// servers that will cooperate via Raft to
// form the fault-tolerant key/value service.
// me is the index of the current server in servers[].
// the k/v server should store snapshots through the underlying Raft
// implementation, which should call persister.SaveStateAndSnapshot() to
// atomically save the Raft state along with the snapshot.
// the k/v server should snapshot when Raft's saved state exceeds maxraftstate bytes,
// in order to allow Raft to garbage-collect its log. if maxraftstate is -1,
// you don't need to snapshot.
// StartKVServer() must return quickly, so it should start goroutines
// for any long-running work.
func StartKVServer(servers []*labrpc.ClientEnd, me int, persister *raft.Persister, maxraftstate int) *KVServer {
	// call labgob.Register on structures you want
	// Go's RPC library to marshall/unmarshall.
	labgob.Register(Command{})

	applyCh := make(chan raft.ApplyMsg)

	kv := &KVServer{
		me:             me,
		maxRaftState:   maxraftstate,
		applyCh:        applyCh,
		dead:           0,
		lastApplied:    0,
		rf:             raft.Make(servers, me, persister, applyCh),
		stateMachine:   NewMemoryKV(),
		lastOperations: make(map[int64]OperationContext),
		notifyChans:    make(map[int]chan *CommandResponse),
	}

	kv.restoreSnapshot(persister.ReadSnapshot())

	go kv.applier()

	DPrintf("[StartKVServer] - {Node: %v} has started", kv.rf.Me())
	return kv
}
