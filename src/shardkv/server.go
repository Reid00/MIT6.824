package shardkv

import (
	"bytes"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"6.824/labgob"
	"6.824/labrpc"
	"6.824/raft"
	"6.824/shardctrler"
)

type ShardKV struct {
	mu   sync.RWMutex
	me   int
	rf   *raft.Raft
	dead int32

	applyCh chan raft.ApplyMsg

	makeEnd func(string) *labrpc.ClientEnd
	gid     int
	sc      *shardctrler.Clerk

	maxRaftState int // snapshot if log grows this big
	lastApplied  int // 记录applied Index 防止状态机apply 小的index

	lastConfig    shardctrler.Config
	currentConfig shardctrler.Config

	stateMachine   map[int]*Shard                // {shardId: shard of KV}
	lastOperations map[int64]OperationContext    // {clientId: ctx}
	notifyChans    map[int]chan *CommandResponse // {commitIndex: commandResp}
}

func (kv *ShardKV) Command(req *CommandRquest, resp *CommandResponse) {
	kv.mu.RLock()

	if req.Op != OpGet && kv.isDuplicateRequest(req.ClientId, req.CommandId) {
		lastResp := kv.lastOperations[req.ClientId].LastResponse
		resp.Err = lastResp.Err
		resp.Value = lastResp.Value
		kv.mu.RUnlock()
		return
	}

	if !kv.canServe(key2shard(req.Key)) {
		resp.Err = ErrWrongGroup
		resp.Value = ""
		kv.mu.RUnlock()
		return
	}

	kv.mu.RUnlock()
	kv.Execute(NewOperationCommand(req), resp)
}

// Execute shardKV 执行相关的RPC req
func (kv *ShardKV) Execute(command Command, resp *CommandResponse) {
	// do not hold lock to improve throughput
	// when KVServer holds the lock to take snapshot, underlying raft can still commit raft logs
	index, _, isLeader := kv.rf.Start(command)
	if !isLeader {
		resp.Err = ErrWrongLeader
		return
	}

	defer DPrintf("[Execute]-{Node: %v}-{Group: %v} process Command %v with CommandResponse %v",
		kv.me, kv.gid, command, resp)

	kv.mu.Lock()
	ch := kv.getNotifyChan(index)
	kv.mu.Unlock()

	select {
	case res := <-ch:
		resp.Value, resp.Err = res.Value, res.Err
	case <-time.After(ExecuteTimeout):
		resp.Err = ErrTimeout
	}

	go func() {
		kv.mu.Lock()
		kv.deleteOutdatedNotifyChan(index)
		kv.mu.Unlock()
	}()
}

func (kv *ShardKV) applier() {
	for !kv.killed() {
		for msg := range kv.applyCh {
			DPrintf("[applier]-{Node: %v}-{Group: %v} tries to apply message %v",
				kv.me, kv.gid, msg)

			if msg.CommandValid {
				kv.mu.Lock()
				if msg.CommandIndex <= kv.lastApplied {
					DPrintf("[applier]-{Node: %v}-{Group: %v} discards outdated message %v because a newer snapshot which lastApplied is %v has restored",
						kv.me, kv.gid, msg, kv.lastApplied)
					kv.mu.Unlock()
					return
				}

				kv.lastApplied = msg.CommandIndex

				var resp *CommandResponse

				command := msg.Command.(Command)
				switch command.Op {
				case Operation:
					op := command.Data.(CommandRquest)
					resp = kv.applyOperation(&msg, &op)
				case Configuration:
					nextConfig := command.Data.(shardctrler.Config)
					resp = kv.applyConfiguration(&nextConfig)
				case InsertShards:
					shardInfo := command.Data.(ShardOperationResponse)
					resp = kv.applyInsertShards(&shardInfo)
				case DeleteShards:
					shardsInfo := command.Data.(ShardOperationRequest)
					resp = kv.applyDeleteShards(&shardsInfo)
				case EmptyEntry:
					resp = kv.applyEmptyEntry()
				}

				// only notify related channel for currentTerm's log when node is leader
				if currTerm, isLeader := kv.rf.GetState(); isLeader && msg.CommandTerm == currTerm {
					ch := kv.getNotifyChan(msg.CommandIndex)
					ch <- resp
				}

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
				panic(fmt.Sprintf("unexpected message %v", msg))
			}
		}
	}
}

// applyOperation 对状态机的操作, Get, Put, Append
func (kv *ShardKV) applyOperation(msg *raft.ApplyMsg, req *CommandRquest) *CommandResponse {
	var resp *CommandResponse
	shardId := key2shard(req.Key)

	if kv.canServe(shardId) {
		if req.Op != OpGet && kv.isDuplicateRequest(req.ClientId, req.CommandId) {
			DPrintf("[applyOperation]-{Node: %v}-{Group: %v} doesn't apply duplicated message %v to stateMachine because maxAppliedCommandId is %v for client %v",
				kv.me, kv.gid, kv.lastOperations[req.ClientId], kv.lastApplied, req.ClientId)

			lastResp := kv.lastOperations[req.ClientId].LastResponse
			return lastResp
		}

		resp = kv.applyLogToStateMachine(req, shardId)
		if req.Op != OpGet {
			// save max command resp
			kv.lastOperations[req.ClientId] = OperationContext{
				MaxAppliedCommandId: req.CommandId,
				LastResponse:        resp,
			}
		}
		return resp
	}
	return &CommandResponse{ErrWrongGroup, ""}
}

// applyConfiguration 对kv controller 的配置进行更新
func (kv *ShardKV) applyConfiguration(conf *shardctrler.Config) *CommandResponse {
	if conf.Num == kv.currentConfig.Num+1 {
		DPrintf("[applyConfiguration]-{Node: %v}-{Group: %v} updates currentConfig from %v to %v",
			kv.me, kv.gid, kv.currentConfig, conf)

		kv.updateShardStatus(conf)
		kv.lastConfig = kv.currentConfig
		kv.currentConfig = *conf
		return &CommandResponse{
			OK,
			"",
		}
	}

	DPrintf("[applyConfiguration]-{Node: %v}-{Group: %v} rejects outdated config %v when currentConfig is %v",
		kv.me, kv.gid, conf, kv.currentConfig)

	return &CommandResponse{ErrOutDated, ""}
}

// applyInsertShards ConfigOperation 生效后，leader call GetShardsData 获取数据
// 把数据插入最新Gid 对应Shard 上
func (kv *ShardKV) applyInsertShards(shardsInfo *ShardOperationResponse) *CommandResponse {
	if shardsInfo.ConfigNum == kv.currentConfig.Num {
		DPrintf("[applyInsertShards]-{Node: %v}-{Group: %v} accepts shards insertion %v when currentConfig is %v",
			kv.me, kv.gid, shardsInfo, kv.currentConfig)

		for shardId, shardData := range shardsInfo.Shards {
			shard := kv.stateMachine[shardId]
			// 把远端的数据 拉过来
			if shard.Status == Pulling {
				for k, v := range shardData {
					shard.KV[k] = v
				}
				// 数据拉去完毕之后 标记为GCing 通知远端删除数据
				shard.Status = GCing
			} else {
				DPrintf("[applyInsertShards]-{Node: %v}-{Group: %v} encounters duplicated shards insertion %v when currentConfig %v",
					kv.me, kv.gid, shardsInfo, kv.currentConfig)
				break
			}
		}

		for clientId, OpCtx := range shardsInfo.LastOperations {
			// stateMachine 保存的lastOperation command id 小，意味着需要update
			if lastOperation, ok := kv.lastOperations[clientId]; !ok ||
				lastOperation.MaxAppliedCommandId < OpCtx.MaxAppliedCommandId {
				kv.lastOperations[clientId] = OpCtx
			}
		}
		return &CommandResponse{OK, ""}
	}
	DPrintf("[applyInsertShards]-{Node: %v}-{Group: %v} rejects outdated shards insertion %v when currentConfig is %v",
		kv.me, kv.gid, shardsInfo, kv.currentConfig)
	return &CommandResponse{ErrOutDated, ""}

}

// applyDeleteShards 清理本节点状态异常的Shard
func (kv *ShardKV) applyDeleteShards(shardsInfo *ShardOperationRequest) *CommandResponse {
	if shardsInfo.ConfigNum == kv.currentConfig.Num {
		DPrintf("[applyDeleteShards]-{Node: %v}-{Group: %v}'s shards status are %v before accpeting shards deletion %v when currentConfig is %v",
			kv.me, kv.gid, kv.getShardStatus(), shardsInfo, kv.currentConfig)

		for _, shardId := range shardsInfo.ShardIDs {
			shard := kv.stateMachine[shardId]

			// TODO 有没有可能本节点改为GCing 之后，还没有来得及触发GCAction() 删除远端数据?
			// 不太可能，因为ShardId 被穿过来，说明 ShardOperationReq 已经完成 其对应的两种情况
			// Insert(migrationAction) 或者 GcAction 已经完成

			// 如果远端是 GCing Status 表示本节点 Pulling 数据已经结束
			if shard.Status == GCing {
				shard.Status = Serving
			} else if shard.Status == BePulling {
				// 如果是BePulling Status 说明 远端Pulling Insert(migrationAction)
				//  对应的ShardOperation 完成。 那么本节点相同Id
				// 的Shard Status 当初应该为 BePulling 直接在对应的ShardId 上重置Shard
				kv.stateMachine[shardId] = NewShard()
			} else {
				DPrintf("[applyDeleteShards]-{Node: %v}-{Group: %v} encounters duplicated sahrds deletion %v when currentConfig is %v",
					kv.me, kv.gid, shardsInfo, kv.currentConfig)
				break
			}
		}

		DPrintf("[applyDeleteShards]-{Node: %v}-{Group: %v}'s shards status are %v after accepting shards deletion %v when currentConfig is %v",
			kv.me, kv.gid, kv.getShardStatus(), shardsInfo, kv.currentConfig)
		return &CommandResponse{OK, ""}
	}
	DPrintf("[applyDeleteShards]-{Node: %v}-{Group: %v}'s encounters duplicated shards deletion %v when currentConfig is %v",
		kv.me, kv.gid, shardsInfo, kv.currentConfig)
	return &CommandResponse{OK, ""}
}

func (kv *ShardKV) applyEmptyEntry() *CommandResponse {
	return &CommandResponse{OK, ""}
}

// RPC GetShardsData ConfigOperation 生效之后数据迁移， 将request 中shardId的数据，迁移到resp中 返回给调用方
func (kv *ShardKV) GetShardsData(req *ShardOperationRequest, resp *ShardOperationResponse) {
	// only pull shards from leader
	if _, isLeader := kv.rf.GetState(); !isLeader {
		resp.Err = ErrWrongLeader
		// log for bug TestConcurrent3  concurrent configuration change and restart...
		DPrintf("[GetShardsData]-{Node: %v}-{Group: %v} err with resp %v", kv.me, kv.gid, resp)
		return
	}

	kv.mu.RLock()
	defer kv.mu.RUnlock()
	defer DPrintf("[GetShardsData]-{Node: %v}-{Group: %v} processes PullTaskRequest %v with Response %v",
		kv.me, kv.gid, req, resp)

	// 当前server(需要被pulling 数据的server) 配置较低，说明Leader 还没有更新最新配置
	// 有可能自身在进行Pulling
	if kv.currentConfig.Num < req.ConfigNum {
		resp.Err = ErrNotReady
		return
	}

	resp.Shards = make(map[int]map[string]string)
	for _, shardId := range req.ShardIDs {
		resp.Shards[shardId] = kv.stateMachine[shardId].deepCopy()
	}

	// TODO why this action doesn't need to add a record
	// LastOpertion 只记录KV Operation相关的Resp
	resp.LastOperations = make(map[int64]OperationContext)
	for cleintId, opCtx := range kv.lastOperations {
		resp.LastOperations[cleintId] = opCtx.deepCopy()
	}

	resp.ConfigNum, resp.Err = req.ConfigNum, OK

}

// RPC DeleteShardsData 删除迁移之后的 shard 中的数据
func (kv *ShardKV) DeleteShardsData(req *ShardOperationRequest, resp *ShardOperationResponse) {
	if _, isLeader := kv.rf.GetState(); !isLeader {
		resp.Err = ErrWrongLeader
		return
	}

	defer DPrintf("[DeleteShardsData]-{Node: %v}-{Group: %v} processes GCTaskRequest %v with response %v",
		kv.me, kv.gid, req, resp)

	kv.mu.RLock()
	if kv.currentConfig.Num > req.ConfigNum {
		DPrintf("[DeleteShardsData]-{Node: %v}-{Group: %v} encounters duplicated shards deletions %v when currentConfig is %v",
			kv.me, kv.gid, req, kv.currentConfig)
		resp.Err = OK
		kv.mu.RUnlock()
		return
	}
	kv.mu.RUnlock()

	var commandResp CommandResponse
	kv.Execute(NewDeleteShardsCommand(req), &commandResp)
	resp.Err = commandResp.Err
}

// canServe 判断shard 的状态是否可以对外服务
// Serving 默认初始状态, GCing 表示该shard 的数据刚刚拉取完毕，但是需要清除
// 远端 该shardId 数据
func (kv *ShardKV) canServe(ShardId int) bool {
	return kv.currentConfig.Shards[ShardId] == kv.gid &&
		(kv.stateMachine[ShardId].Status == Serving ||
			kv.stateMachine[ShardId].Status == GCing)
}

func (kv *ShardKV) isDuplicateRequest(clientId int64, commandId int64) bool {
	opCtx, ok := kv.lastOperations[clientId]
	return ok && commandId <= opCtx.MaxAppliedCommandId
}

// getShardStatus 根据shardId 遍历所有Shard 获取其状态
func (kv *ShardKV) getShardStatus() []ShardStatus {
	res := make([]ShardStatus, shardctrler.NShards)
	for i := 0; i < shardctrler.NShards; i++ {
		res[i] = kv.stateMachine[i].Status
	}
	return res
}

// updateShardStatus 根据最新的config 对shard 进行更新
func (kv *ShardKV) updateShardStatus(nextConfig *shardctrler.Config) {
	for i := 0; i < shardctrler.NShards; i++ {
		// shard i 当前配置中不属于当前的Group， 最新的配置要求属于当前配置
		// 需要把 shard i 的数据迁移过来
		if kv.currentConfig.Shards[i] != kv.gid && nextConfig.Shards[i] == kv.gid {
			gid := kv.currentConfig.Shards[i]
			// config 中Num 0, gid 0 是初始的无效配置，没有数据
			if gid != 0 {
				kv.stateMachine[i].Status = Pulling
			}
		}
		// 和上面相反
		if kv.currentConfig.Shards[i] == kv.gid && nextConfig.Shards[i] != kv.gid {
			gid := nextConfig.Shards[i]
			if gid != 0 {
				kv.stateMachine[i].Status = BePulling
			}
		}
	}
	// below for debug bug
	// DPrintf("[updateShardStatus]-{Node: %v}-{Group: %v} current shard status when nextConfig: %v, SM: %v",
	// 	kv.me, kv.gid, nextConfig, kv.stateMachine)
}

// configureAction kvctrller execute apply configuration
func (kv *ShardKV) configureAction() {
	canPerformNextConfig := true

	kv.mu.RLock()
	for _, shard := range kv.stateMachine {
		if shard.Status != Serving {
			canPerformNextConfig = false
			DPrintf("[configureAction]-{Node: %v}-{Group: %v} will not try to fetch latest configuration because shards status are %v when currentConfig is %v",
				kv.me, kv.gid, kv.getShardStatus(), kv.currentConfig)
			break
		}
	}

	currentConfigNum := kv.currentConfig.Num
	kv.mu.RUnlock()

	if canPerformNextConfig {
		nextConfig := kv.sc.Query(currentConfigNum + 1)
		if nextConfig.Num == currentConfigNum+1 {
			DPrintf("[configureAction]-{Node: %v}-{Group: %v} fetches latest configuration %v when currentConfigNum is %v",
				kv.me, kv.gid, nextConfig, currentConfigNum)

			kv.Execute(NewConfigurationCommand(&nextConfig), &CommandResponse{})
		}
	}
}

// migrationAction shard migration data when in Pulling status
func (kv *ShardKV) migrationAction() {
	kv.mu.RLock()
	gid2shardIds := kv.getShardIdsByStatus(Pulling)

	var wg sync.WaitGroup
	for gid, shardIds := range gid2shardIds {
		DPrintf("[migrationAction]-{Node: %v}-{Group: %v} starts a PullTask to get shards %v from group %v when config is %v",
			kv.me, kv.gid, shardIds, gid, kv.currentConfig)

		wg.Add(1)
		go func(servers []string, configNum int, shardIds []int) {
			defer wg.Done()
			PullTaskRequest := ShardOperationRequest{
				ConfigNum: configNum,
				ShardIDs:  shardIds,
			}

			// 本Node 向Pulling Status 的Shard 所在Group 的全部Server
			// 发送RPC 但是只有Leader 有响应，其他忽略
			for _, server := range servers {
				var pullTaskResp ShardOperationResponse
				srv := kv.makeEnd(server)
				DPrintf("[migrationAction]-{Node: %v}-{Group: %v} server call %v", kv.me, kv.gid, server)
				if srv.Call("ShardKV.GetShardsData", &PullTaskRequest, &pullTaskResp) && pullTaskResp.Err == OK {
					DPrintf("[migrationAction]-{Node: %v}-{Group: %v} gets a PullTaskResponse %v and tries to commit it when currentConfigNum is %v",
						kv.me, kv.gid, pullTaskResp, configNum)
					kv.Execute(NewInsertShardsCommand(&pullTaskResp), &CommandResponse{})
				}
			}
		}(kv.lastConfig.Groups[gid], kv.currentConfig.Num, shardIds)
	}
	kv.mu.RUnlock()
	wg.Wait()
}

// gcAction Pulling 数据之后把状态改为GCing，调用RPC 删除远端的该ShardId 的数据
func (kv *ShardKV) gcAction() {
	kv.mu.RLock()
	gid2shardIds := kv.getShardIdsByStatus(GCing)
	var wg sync.WaitGroup
	for gid, shardIds := range gid2shardIds {
		DPrintf("[gcAction]-{Node: %v}-{Group: %v} starts a GCTask to delete shards %v in group %v when config is %v",
			kv.me, kv.gid, shardIds, gid, kv.currentConfig)

		wg.Add(1)
		go func(servers []string, configNum int, shardIds []int) {
			defer wg.Done()
			gcTaskReq := ShardOperationRequest{ConfigNum: configNum, ShardIDs: shardIds}
			for _, server := range servers {
				var gcTaskResp ShardOperationResponse
				srv := kv.makeEnd(server)
				// 远端执行删除数据的逻辑
				if srv.Call("ShardKV.DeleteShardsData", &gcTaskReq, &gcTaskResp) && gcTaskResp.Err == OK {
					DPrintf("[gcAction]-{Node: %v}-{Group: %v} deletes shards %v in remote group successfully when currentConfigNum is %v",
						kv.me, kv.gid, shardIds, configNum)

					// 远端删除完数据之后，Raft 本节点同样需要 把GCing 状态恢复为Server 状态
					kv.Execute(NewDeleteShardsCommand(&gcTaskReq), &CommandResponse{})
				}
			}
		}(kv.lastConfig.Groups[gid], kv.currentConfig.Num, shardIds)
	}

	kv.mu.RUnlock()
	wg.Wait()
}

// getShardIdsByStatus 根据StateMachine 获取上个config 配置中shard 对应的gid
func (kv *ShardKV) getShardIdsByStatus(status ShardStatus) map[int][]int {
	gid2shardIds := make(map[int][]int)

	for i, shard := range kv.stateMachine {
		if shard.Status == status {
			gid := kv.lastConfig.Shards[i]
			if gid != 0 {
				// if _, ok := gid2shardIds[gid]; !ok {
				// 	gid2shardIds[gid] = make([]int, 0)
				// }
				gid2shardIds[gid] = append(gid2shardIds[gid], i)
			}
		}
	}
	return gid2shardIds
}

func (kv *ShardKV) needSnapshot() bool {
	return kv.maxRaftState != -1 && kv.rf.GetRaftStateSize() >= kv.maxRaftState
}

func (kv *ShardKV) takeSnapshot(index int) {
	w := new(bytes.Buffer)
	enc := labgob.NewEncoder(w)

	enc.Encode(kv.stateMachine)
	enc.Encode(kv.lastOperations)
	enc.Encode(kv.currentConfig)
	enc.Encode(kv.lastConfig)
	kv.rf.Snapshot(index, w.Bytes())
}

func (kv *ShardKV) restoreSnapshot(snapshot []byte) {
	if len(snapshot) == 0 {
		kv.initStateMachines()
		return
	}
	// debug for bug fix
	// DPrintf("[restoreSnapshot]-{Node: %v}-{Group: %v} restore", kv.me, kv.gid)
	r := bytes.NewBuffer(snapshot)
	dec := labgob.NewDecoder(r)
	var (
		stateMachine   map[int]*Shard
		lastOperations map[int64]OperationContext
		currentConfig  shardctrler.Config
		lastConfig     shardctrler.Config
	)

	if dec.Decode(&stateMachine) != nil || dec.Decode(&lastOperations) != nil ||
		dec.Decode(&currentConfig) != nil || dec.Decode(&lastConfig) != nil {
		DPrintf("[restore]-{Node: %v}-{Group: %v} restores snapshot failed", kv.me, kv.gid)
	}
	kv.stateMachine = stateMachine
	kv.lastOperations = lastOperations
	kv.currentConfig = currentConfig
	kv.lastConfig = lastConfig
}

func (kv *ShardKV) initStateMachines() {
	for i := 0; i < shardctrler.NShards; i++ {
		if _, ok := kv.stateMachine[i]; !ok {
			kv.stateMachine[i] = NewShard()
		}
	}
}

func (kv *ShardKV) getNotifyChan(index int) chan *CommandResponse {
	if _, ok := kv.notifyChans[index]; !ok {
		kv.notifyChans[index] = make(chan *CommandResponse)
	}
	return kv.notifyChans[index]
}

func (kv *ShardKV) deleteOutdatedNotifyChan(index int) {
	delete(kv.notifyChans, index)
}

func (kv *ShardKV) applyLogToStateMachine(op *CommandRquest, ShardId int) *CommandResponse {
	var val string
	var err Err
	switch op.Op {
	case OpPut:
		err = kv.stateMachine[ShardId].Put(op.Key, op.Value)
	case OpAppend:
		err = kv.stateMachine[ShardId].Append(op.Key, op.Value)
	case OpGet:
		val, err = kv.stateMachine[ShardId].Get(op.Key)
	}
	return &CommandResponse{
		Err:   err,
		Value: val,
	}
}

func (kv *ShardKV) checkEntryIncurrentTermAction() {
	if !kv.rf.HasLogInCurrentTerm() {
		kv.Execute(NewEmptyEntryCommand(), &CommandResponse{})
	}
}

// the tester calls Kill() when a ShardKV instance won't
// be needed again. you are not required to do anything
// in Kill(), but it might be convenient to (for example)
// turn off debug output from this instance.
func (kv *ShardKV) Kill() {
	DPrintf("{Node: %v}-{Group: %v} has been killed", kv.me, kv.gid)
	atomic.AddInt32(&kv.dead, 1)
	kv.rf.Kill()
}

func (kv *ShardKV) killed() bool {
	return atomic.LoadInt32(&kv.dead) == 1
}

func (kv *ShardKV) Monitor(action func(), timeout time.Duration) {
	for !kv.killed() {
		if _, isLeader := kv.rf.GetState(); isLeader {
			action()
		}
		time.Sleep(timeout)
	}
}

// servers[] contains the ports of the servers in this group.
//
// me is the index of the current server in servers[].
//
// the k/v server should store snapshots through the underlying Raft
// implementation, which should call persister.SaveStateAndSnapshot() to
// atomically save the Raft state along with the snapshot.
//
// the k/v server should snapshot when Raft's saved state exceeds
// maxraftstate bytes, in order to allow Raft to garbage-collect its
// log. if maxraftstate is -1, you don't need to snapshot.
//
// gid is this group's GID, for interacting with the shardctrler.
//
// pass ctrlers[] to shardctrler.MakeClerk() so you can send
// RPCs to the shardctrler.
//
// make_end(servername) turns a server name from a
// Config.Groups[gid][i] into a labrpc.ClientEnd on which you can
// send RPCs. You'll need this to send RPCs to other groups.
//
// look at client.go for examples of how to use ctrlers[]
// and make_end() to send RPCs to the group owning a specific shard.
//
// StartServer() must return quickly, so it should start goroutines
// for any long-running work.
func StartServer(servers []*labrpc.ClientEnd, me int, persister *raft.Persister,
	maxraftstate int, gid int, ctrlers []*labrpc.ClientEnd,
	make_end func(string) *labrpc.ClientEnd) *ShardKV {
	// call labgob.Register on structures you want
	// Go's RPC library to marshall/unmarshall.
	labgob.Register(Command{})
	labgob.Register(CommandRquest{})
	labgob.Register(shardctrler.Config{})
	labgob.Register(ShardOperationRequest{})
	labgob.Register(ShardOperationResponse{})

	applyCh := make(chan raft.ApplyMsg)

	kv := &ShardKV{
		me:             me,
		rf:             raft.Make(servers, me, persister, applyCh),
		dead:           0,
		applyCh:        applyCh,
		makeEnd:        make_end,
		gid:            gid,
		sc:             shardctrler.MakeClerk(ctrlers),
		maxRaftState:   maxraftstate,
		lastApplied:    0,
		lastConfig:     shardctrler.DefaultConfig(),
		currentConfig:  shardctrler.DefaultConfig(),
		stateMachine:   make(map[int]*Shard),
		lastOperations: make(map[int64]OperationContext),
		notifyChans:    make(map[int]chan *CommandResponse),
	}

	kv.restoreSnapshot(persister.ReadSnapshot())
	// start applier goroutine to apply committed logs to stateMachine
	go kv.applier()

	// start configuration monitor goroutine to fetch latest configuration
	go kv.Monitor(kv.configureAction, ConfigureMonitorTimeout)
	// start migration monitor goroutine to pull related shards
	go kv.Monitor(kv.migrationAction, MigrationMonitorTimeout)
	// start gc monitor goroutine to delete useless shards in remote groups
	go kv.Monitor(kv.gcAction, GCMonitorTimeout)

	// start entry-in-currentTerm monitor goroutine to advance commitIndex by
	// appending empty entries in current term periodically to avoid live locks
	go kv.Monitor(kv.checkEntryIncurrentTermAction, EmptyEntryDetectorTimeout)

	DPrintf("[StartServer]-{Node: %v}-{Group: %v} has started", kv.me, kv.gid)
	return kv
}
