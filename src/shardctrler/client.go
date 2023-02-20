package shardctrler

//
// Shardctrler clerk.
//

import (
	"crypto/rand"
	"math/big"

	"6.824/labrpc"
)

type Clerk struct {
	servers []*labrpc.ClientEnd
	// Your data here.

	leaderId  int64
	clientId  int64
	commandId int64 // (clientId, commandId) defines a operation uniquely
}

func nrand() int64 {
	max := big.NewInt(int64(1) << 62)
	bigx, _ := rand.Int(rand.Reader, max)
	x := bigx.Int64()
	return x
}

func MakeClerk(servers []*labrpc.ClientEnd) *Clerk {
	return &Clerk{
		servers:   servers,
		leaderId:  0,
		clientId:  nrand(),
		commandId: 0,
	}
}

func (ck *Clerk) Query(num int) Config {
	return ck.Command(&CommandRequest{
		Num: num,
		Op:  OpQuery,
	})
}

func (ck *Clerk) Join(servers map[int][]string) {
	ck.Command(&CommandRequest{
		Servers: servers,
		Op:      OpJoin,
	})
}

func (ck *Clerk) Leave(gids []int) {
	ck.Command(&CommandRequest{
		GIds: gids,
		Op:   OpLeave,
	})
}

func (ck *Clerk) Move(shard int, gid int) {
	ck.Command(&CommandRequest{
		Shard: shard,
		Op:    OpMove,
		GId:   gid,
	})
}

func (ck *Clerk) Command(req *CommandRequest) Config {
	req.ClientId, req.CommandId = ck.clientId, ck.commandId
	for {
		resp := new(CommandResponse)
		if !ck.servers[ck.leaderId].Call("ShardCtrler.Command", req, resp) ||
			resp.Err == ErrWrongLeader || resp.Err == ErrTimeout {
			ck.leaderId = (ck.leaderId + 1) % int64(len(ck.servers))

			continue
		}

		ck.commandId++
		return resp.Config
	}

}
