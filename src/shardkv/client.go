package shardkv

//
// client code to talk to a sharded key/value service.
//
// the client first talks to the shardctrler to find out
// the assignment of shards (keys) to groups, and then
// talks to the group that holds the key's shard.
//

import (
	"crypto/rand"
	"math/big"
	"time"

	"6.824/labrpc"
	"6.824/shardctrler"
)

// which shard is a key in?
// please use this function,
// and please do not change it.
func key2shard(key string) int {
	shard := 0
	if len(key) > 0 {
		shard = int(key[0])
	}
	shard %= shardctrler.NShards
	return shard
}

func nrand() int64 {
	max := big.NewInt(int64(1) << 62)
	bigx, _ := rand.Int(rand.Reader, max)
	x := bigx.Int64()
	return x
}

type Clerk struct {
	sc      *shardctrler.Clerk
	config  shardctrler.Config
	makeEnd func(string) *labrpc.ClientEnd

	// You will have to modify this struct.
	leaderIds map[int]int // {groupid: leader if hardid of this groupid}
	clientId  int64
	commandId int64 //clientId + commandId define unique operation
}

// the tester calls MakeClerk.
//
// ctrlers[] is needed to call shardctrler.MakeClerk().
//
// make_end(servername) turns a server name from a
// Config.Groups[gid][i] into a labrpc.ClientEnd on which you can
// send RPCs.
func MakeClerk(ctrlers []*labrpc.ClientEnd, make_end func(string) *labrpc.ClientEnd) *Clerk {

	ck := &Clerk{
		sc:        shardctrler.MakeClerk(ctrlers),
		makeEnd:   make_end,
		leaderIds: make(map[int]int),
		clientId:  nrand(),
		commandId: 0,
	}

	ck.config = ck.sc.Query(-1)
	return ck
}

func (ck *Clerk) Get(key string) string {
	return ck.Command(&CommandRequest{
		Key: key,
		Op:  OpGet,
	})
}

func (ck *Clerk) Put(key, val string) {
	ck.Command(&CommandRequest{
		Key:   key,
		Value: val,
		Op:    OpPut,
	})
}

func (ck *Clerk) Append(key, val string) {
	ck.Command(&CommandRequest{
		Key:   key,
		Value: val,
		Op:    OpAppend,
	})
}

func (ck *Clerk) Command(req *CommandRequest) string {
	req.ClientId, req.CommandId = ck.clientId, ck.commandId

	for {
		shard := key2shard(req.Key)
		gid := ck.config.Shards[shard]

		if servers, ok := ck.config.Groups[gid]; ok {
			// 找到Group 对应的LeaderId, 如果没有从Id 0 开始轮询
			if _, ok := ck.leaderIds[gid]; !ok {
				ck.leaderIds[gid] = 0
			}

			oldLeaderId := ck.leaderIds[gid]
			newLeaderId := oldLeaderId

			for {
				var resp CommandResponse
				// 循环查找LeaderId
				ok := ck.makeEnd(servers[newLeaderId]).Call("ShardKV.Command", req, &resp)

				if ok && (resp.Err == OK || resp.Err == ErrNoKey) {
					ck.commandId++
					return resp.Value
				} else if ok && resp.Err == ErrWrongGroup {
					break
				} else {
					// Err is 	ErrWrongLeader ErrOutDated ErrTimeout ErrNotReady
					newLeaderId = (newLeaderId + 1) % len(servers)
					if newLeaderId == oldLeaderId {
						// 所有server 轮询一遍之后退出，避免raft 集群处于无leader 状态中一直重试
						break
					}
					continue
				}
			}
		}

		time.Sleep(100 * time.Millisecond)

		ck.config = ck.sc.Query(-1)

	}
}
