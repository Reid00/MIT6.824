package raft

//
// this is an outline of the API that raft must expose to
// the service (or tester). see comments below for
// each of these functions for more details.
//
// rf = Make(...)
//   create a new Raft server.
// rf.Start(command interface{}) (index, term, isleader)
//   start agreement on a new log entry
// rf.GetState() (term, isLeader)
//   ask a Raft for its current term, and whether it thinks it is leader
// ApplyMsg
//   each time a new entry is committed to the log, each Raft peer
//   should send an ApplyMsg to the service (or tester)
//   in the same server.
//

import (
	//	"bytes"
	"bytes"
	"sync"
	"sync/atomic"
	"time"

	//	"6.824/labgob"
	"6.824/labgob"
	"6.824/labrpc"
)

// A Go object implementing a single Raft peer.
type Raft struct {
	mu        sync.RWMutex        // Lock to protect shared access to this peer's state
	peers     []*labrpc.ClientEnd // RPC end points of all peers
	persister *Persister          // Object to hold this peer's persisted state
	me        int                 // this peer's index into peers[]
	dead      int32               // set by Kill()

	// Your data here (2A, 2B, 2C).
	// Look at the paper's Figure 2 for a description of what
	// state a Raft server must maintain.

	// 2A
	state          NodeState
	currentTerm    int
	votedFor       int
	electionTimer  *time.Timer
	heartbeatTimer *time.Timer

	// 2B
	logs        []Entry // the first is dummy entry which contains LastSnapshotTerm, LastSnapshotIndex and nil Command
	commitIndex int
	lastApplied int
	nextIndex   []int
	matchIndex  []int

	applyCh        chan ApplyMsg
	applyCond      *sync.Cond   // used to wakeup applier goroutine after committing new entries
	replicatorCond []*sync.Cond // used to signal replicator goroutine to batch replicating entries
}

// return currentTerm and whether this server
// believes it is the leader.
func (rf *Raft) GetState() (int, bool) {

	// var term int
	// var isleader bool
	// // Your code here (2A).
	// rf.mu.Lock()
	// defer rf.mu.Unlock()
	// term = rf.currentTerm
	// isleader = rf.state == StateLeader
	// return term, isleader
	// or simple as below
	rf.mu.Lock()
	defer rf.mu.Unlock()
	return rf.currentTerm, rf.state == StateLeader
}

func (rf *Raft) GetRaftStateSize() int {
	rf.mu.Lock()
	defer rf.mu.Unlock()
	return rf.persister.RaftStateSize()

}

// save Raft's persistent state to stable storage,
// where it can later be retrieved after a crash and restart.
// see paper's Figure 2 for a description of what should be persistent.
func (rf *Raft) persist() {
	// Your code here (2C).
	// Example:
	// w := new(bytes.Buffer)
	// e := labgob.NewEncoder(w)
	// e.Encode(rf.xxx)
	// e.Encode(rf.yyy)
	// data := w.Bytes()
	// rf.persister.SaveRaftState(data)
	rf.persister.SaveRaftState(rf.encodeState())
}

func (rf *Raft) encodeState() []byte {
	buf := new(bytes.Buffer)
	enc := labgob.NewEncoder(buf)
	// figure2 Persistent state on all servers
	enc.Encode(rf.currentTerm)
	enc.Encode(rf.votedFor)
	enc.Encode(rf.logs)
	return buf.Bytes()
}

// restore previously persisted state.
func (rf *Raft) readPersist(data []byte) {
	if data == nil || len(data) < 1 { // bootstrap without any state?
		return
	}
	// Your code here (2C).
	// Example:
	// r := bytes.NewBuffer(data)
	// d := labgob.NewDecoder(r)
	// var xxx
	// var yyy
	// if d.Decode(&xxx) != nil ||
	//    d.Decode(&yyy) != nil {
	//   error...
	// } else {
	//   rf.xxx = xxx
	//   rf.yyy = yyy
	// }

}

// A service wants to switch to snapshot.  Only do so if Raft hasn't
// have more recent info since it communicate the snapshot on applyCh.
func (rf *Raft) CondInstallSnapshot(lastIncludedTerm int, lastIncludedIndex int, snapshot []byte) bool {

	// Your code here (2D).

	return true
}

// the service says it has created a snapshot that has
// all info up to and including index. this means the
// service no longer needs the log through (and including)
// that index. Raft should now trim its log as much as possible.
func (rf *Raft) Snapshot(index int, snapshot []byte) {
	// Your code here (2D).

}

// used by RequestVote Handler to judge which log is newer
func (rf *Raft) isLogUpToDate(term, index int) bool {
	lastLog := rf.getLastLog()
	return term > lastLog.Term || (term == lastLog.Term && index >= lastLog.Index)
}

// used by Start function to append a new Entry to logs
func (rf *Raft) matchLog(term, index int) bool {
	return index <= rf.getLastLog().Index && rf.logs[index-rf.getFirstLog().Index].Term == term
}

// example code to send a RequestVote RPC to a server.
// server is the index of the target server in rf.peers[].
// expects RPC arguments in args.
// fills in *reply with RPC reply, so caller should
// pass &reply.
// the types of the args and reply passed to Call() must be
// the same as the types of the arguments declared in the
// handler function (including whether they are pointers).
//
// The labrpc package simulates a lossy network, in which servers
// may be unreachable, and in which requests and replies may be lost.
// Call() sends a request and waits for a reply. If a reply arrives
// within a timeout interval, Call() returns true; otherwise
// Call() returns false. Thus Call() may not return for a while.
// A false return can be caused by a dead server, a live server that
// can't be reached, a lost request, or a lost reply.
//
// Call() is guaranteed to return (perhaps after a delay) *except* if the
// handler function on the server side does not return.  Thus there
// is no need to implement your own timeouts around Call().
//
// look at the comments in ../labrpc/labrpc.go for more details.
//
// if you're having trouble getting RPC to work, check that you've
// capitalized all field names in structs passed over RPC, and
// that the caller passes the address of the reply struct with &, not
// the struct itself.
func (rf *Raft) sendRequestVote(server int, args *RequestVoteRequest, reply *RequestVoteResponse) bool {
	ok := rf.peers[server].Call("Raft.RequestVote", args, reply)
	return ok
}

func (rf *Raft) sendAppendEntries(server int, req *AppendEntriesReq, resp *AppendEntriesResp) bool {
	return rf.peers[server].Call("Raft.AppendEntries", req, resp)
}

func (rf *Raft) sendInstallSnapshot(server int, req *InstallSnapshotReq, resp *InstallSnapshotResp) bool {
	return rf.peers[server].Call("Raft.InstallSnapshot", req, resp)
}

func (rf *Raft) RequestVote(req *RequestVoteRequest, resp *RequestVoteResponse) {
	// Your code here (2A, 2B).
	// 2A
	rf.mu.Lock()
	defer rf.mu.Unlock()
	defer rf.persist()
	defer DPrintf("[RequestVote]-{Node %v}'s state is {state %v,term %v,commitIndex %v,lastApplied %v,firstLog %v,lastLog %v} before processing requestVoteRequest %v and reply requestVoteResponse %v",
		rf.me, rf.state, rf.currentTerm, rf.commitIndex, rf.lastApplied, rf.getFirstLog(), rf.getLastLog(), req, resp)

	if req.Term < rf.currentTerm || (req.Term == rf.currentTerm && rf.votedFor != -1 && rf.votedFor != req.CandidateId) {
		resp.Term, resp.VoteGranted = rf.currentTerm, false
		return
	}

	if req.Term > rf.currentTerm {
		rf.ChangeState(StateFollower)
		rf.currentTerm, rf.votedFor = req.Term, -1
	}

	// 2A 可以先不实现
	if !rf.isLogUpToDate(req.LastLogTerm, req.LastLogIndex) {
		resp.Term, resp.VoteGranted = rf.currentTerm, false
		return
	}

	rf.votedFor = req.CandidateId
	rf.electionTimer.Reset(RandomizedElectionTimeout())
	resp.Term, resp.VoteGranted = rf.currentTerm, true
}

func (rf *Raft) AppendEntries(req *AppendEntriesReq, resp *AppendEntriesResp) {
	rf.mu.Lock()
	defer rf.mu.Unlock()
	defer DPrintf("[AppendEntries]- {Node: %v}'s state is {state %v, term %v, commitIndex %v, lastApplied %v, firstLog %v, lastLog %v} before processing AppendEntriesRequest %v and reply AppendEntries %v",
		rf.me, rf.state, rf.currentTerm, rf.commitIndex, rf.lastApplied, rf.getFirstLog(), rf.getLastLog(), req, resp)

	// 如果发现来自leader的rpc中的term比当前peer要小,那么说明它不适合当leader
	if req.Term < rf.currentTerm {
		resp.Term, resp.Success = rf.currentTerm, false
		return
	}

	// 一般来讲,在vote的时候已经将currentTerm和leader同步
	// 不过,有些peer暂时的掉线或者其他一些情况重连以后,会发现term和leader
	// 不一样,所以收到大于自己的term的rpc也是第一时间同步.而且要将votefor重新设置为-1
	// 等待将来选举 (说明这个peer 不是之前election 中的marjority)
	if req.Term > rf.currentTerm {
		rf.currentTerm, rf.votedFor = req.Term, -1
	}

	rf.ChangeState(StateFollower)
	rf.electionTimer.Reset(RandomizedElectionTimeout())
	resp.Term, resp.Success = rf.currentTerm, true
}

// the service using Raft (e.g. a k/v server) wants to start
// agreement on the next command to be appended to Raft's log. if this
// server isn't the leader, returns false. otherwise start the
// agreement and return immediately. there is no guarantee that this
// command will ever be committed to the Raft log, since the leader
// may fail or lose an election. even if the Raft instance has been killed,
// this function should return gracefully.
//
// the first return value is the index that the command will appear at
// if it's ever committed. the second return value is the current
// term. the third return value is true if this server believes it is
// the leader.
func (rf *Raft) Start(command interface{}) (int, int, bool) {
	index := -1
	term := -1
	isLeader := true

	// Your code here (2B).

	return index, term, isLeader
}

// the tester doesn't halt goroutines created by Raft after each test,
// but it does call the Kill() method. your code can use killed() to
// check whether Kill() has been called. the use of atomic avoids the
// need for a lock.
//
// the issue is that long-running goroutines use memory and may chew
// up CPU time, perhaps causing later tests to fail and generating
// confusing debug output. any goroutine with a long-running loop
// should call killed() to check whether it should stop.
func (rf *Raft) Kill() {
	atomic.StoreInt32(&rf.dead, 1)
	// Your code here, if desired.
}

func (rf *Raft) killed() bool {
	z := atomic.LoadInt32(&rf.dead)
	return z == 1
}

// The ticker go routine starts a new election if this peer hasn't received
// heartsbeats recently.
func (rf *Raft) ticker() {
	// for rf.killed() == false {
	for !rf.killed() {

		// Your code here to check if a leader election should
		// be started and to randomize sleeping time using
		// time.Sleep().
		select {
		case <-rf.electionTimer.C: // start election
			DPrintf("election time out")
			rf.mu.Lock()
			rf.ChangeState(StateCandidate)
			rf.currentTerm += 1
			rf.StartElection()
			rf.electionTimer.Reset(RandomizedElectionTimeout())
			rf.mu.Unlock()

		case <-rf.heartbeatTimer.C: // 领导者发送心跳维持领导力, 2A 可以先不实现
			rf.mu.Lock()
			if rf.state == StateLeader {
				rf.BroadcastHeartbeat(true)
				rf.heartbeatTimer.Reset(StableHeartbeatTimeout())
			}
			rf.mu.Unlock()
		}

	}
}

func (rf *Raft) StartElection() {
	req := rf.genRequestVoteReq()
	DPrintf("{Note: %v} starts election with RequestVoteReq: %v", rf.me, req)

	// Closure
	grantedVote := 1 // elect for itself
	rf.votedFor = rf.me
	rf.persist()
	for peer := range rf.peers {
		if peer == rf.me {
			continue
		}

		go func(peer int) {
			resp := new(RequestVoteResponse)
			if rf.sendRequestVote(peer, req, resp) {
				rf.mu.Lock()
				defer rf.mu.Unlock()
				DPrintf("[RequestVoteResp]-{Node: %v} receives RequestVoteResponse %v from {Node: %v} after sending RequestVoteRequest %v in term %v",
					rf.me, resp, peer, req, rf.currentTerm)

				// rf.currentTerm == req.Term 为了抛弃过期的RequestVote RPC
				if rf.currentTerm == req.Term && rf.state == StateCandidate { // Candidate node
					if resp.VoteGranted {
						grantedVote += 1
						if grantedVote > len(rf.peers)/2 {
							DPrintf("{Node: %v} receives majority votes in term %v", rf.me, rf.currentTerm)
							rf.ChangeState(StateLeader)
							rf.BroadcastHeartbeat(true)
						}
					} else if resp.Term > rf.currentTerm {
						// candidate 发现有term 比自己大的，立刻转为follower
						DPrintf("{Node %v} finds a new leader {Node %v} with term %v and steps down in term %v",
							rf.me, peer, resp.Term, rf.currentTerm)
						rf.ChangeState(StateFollower)
						rf.currentTerm, rf.votedFor = resp.Term, -1
						rf.persist()
					}
				}
			}
		}(peer)
	}
}

func (rf *Raft) BroadcastHeartbeat(isHeartbeat bool) {
	for peer := range rf.peers {
		if peer == rf.me {
			continue
		}

		if isHeartbeat {
			// need sending at once to maintain leadership
			go rf.replicateOneRound(peer)
		} else {
			// just signal replicator goroutine to send entries in batch
			rf.replicatorCond[peer].Signal()
		}
	}

}

func (rf *Raft) replicateOneRound(peer int) {
	rf.mu.RLock()
	if rf.state != StateLeader {
		rf.mu.RUnlock()
	}

	prevLogIndex := rf.nextIndex[peer] - 1
	if prevLogIndex < rf.getFirstLog().Index {
		// only sanpshot can catch up
		req := rf.genInstallSnapshotRequest()
		rf.mu.RUnlock()
		resp := new(InstallSnapshotResp)

		if rf.sendInstallSnapshot(peer, req, resp) {
			rf.mu.Lock()
			rf.handleInstallSnapshotResponse(peer, req, resp)
			rf.mu.Unlock()
		}

	} else {
		// just entries can catch up
		req := rf.genAppendEntriesRquest(prevLogIndex)
		rf.mu.RUnlock()
		resp := new(AppendEntriesResp)
		if rf.sendAppendEntries(peer, req, resp) {
			rf.mu.Lock()
			rf.handleAppendEntriesResponse(peer, req, resp)
			rf.mu.Unlock()
		}

	}

}

func (rf *Raft) ChangeState(state NodeState) {
	if rf.state == state {
		return
	}
	DPrintf("{Node: %d} changes state from %s to %s in term %d", rf.me, rf.state, state, rf.currentTerm)
	rf.state = state
	switch state {
	case StateFollower:
		rf.heartbeatTimer.Stop() // non-leader stop heartbeat
		rf.electionTimer.Reset(RandomizedElectionTimeout())
	case StateCandidate:
	case StateLeader:
		lastLog := rf.getLastLog()
		for i := 0; i < len(rf.peers); i++ {
			rf.matchIndex[i] = 0
			rf.nextIndex[i] = lastLog.Index + 1
		}

		rf.electionTimer.Stop()
		rf.heartbeatTimer.Reset(StableHeartbeatTimeout())

	}
}

func (rf *Raft) genRequestVoteReq() *RequestVoteRequest {
	lastLog := rf.getLastLog()
	return &RequestVoteRequest{
		Term:         rf.currentTerm,
		CandidateId:  rf.me,
		LastLogIndex: lastLog.Index,
		LastLogTerm:  lastLog.Term,
	}
}

func (rf *Raft) genAppendEntriesRquest(prevLogIndex int) *AppendEntriesReq {
	firstIndex := rf.getFirstLog().Index
	entries := make([]Entry, len(rf.logs[prevLogIndex+1-firstIndex:]))
	copy(entries, rf.logs[prevLogIndex+1-firstIndex:])

	return &AppendEntriesReq{
		Term:            rf.currentTerm,
		LeaderId:        rf.me,
		PreVoteLogIndex: prevLogIndex,
		PreVoteLogTerm:  rf.logs[prevLogIndex-firstIndex].Term,
		Entries:         entries,
		LeaderComment:   rf.commitIndex,
	}
}

func (rf *Raft) handleAppendEntriesResponse(peer int, req *AppendEntriesReq, resp *AppendEntriesResp) {
	defer DPrintf("[handleAppendEntriesResponse]-{Node %v}'s state is {state %v,term %v,commitIndex %v,lastApplied %v,firstLog %v,lastLog %v} after handling AppendEntriesResponse %v for AppendEntriesRequest %v",
		rf.me, rf.state, rf.currentTerm, rf.commitIndex, rf.lastApplied, rf.getFirstLog(), rf.getLastLog(), resp, req)

	if rf.state == StateLeader && rf.currentTerm == req.Term {
		if resp.Success {
			rf.matchIndex[peer] = req.PreVoteLogIndex + len(req.Entries)
			rf.nextIndex[peer] = rf.matchIndex[peer] + 1
			rf.advanceCommitIndexForLeader()
		}
	}
}

func (rf *Raft) genInstallSnapshotRequest() *InstallSnapshotReq {
	firstLog := rf.getFirstLog()
	return &InstallSnapshotReq{
		Term:              rf.currentTerm,
		LeaderId:          rf.me,
		LastIncludedIndex: firstLog.Index,
		LastIncludedTerm:  firstLog.Term,
		Data:              rf.persister.ReadSnapshot(),
	}
}

func (rf *Raft) handleInstallSnapshotResponse(peer int, req *InstallSnapshotReq, resp *InstallSnapshotResp) {
	if rf.state == StateLeader && rf.currentTerm == req.Term {
		if resp.Term > rf.currentTerm {
			rf.ChangeState(StateFollower)
			rf.currentTerm, rf.votedFor = resp.Term, -1
			rf.persist()
		} else {
			rf.matchIndex[peer], rf.nextIndex[peer] = req.LastIncludedIndex, req.LastIncludedIndex+1
		}
	}
	DPrintf("{Node %v}'s state is {state %v,term %v,commitIndex %v,lastApplied %v,firstLog %v,lastLog %v} after handling InstallSnapshotResponse %v for InstallSnapshotRequest %v",
		rf.me, rf.state, rf.currentTerm, rf.commitIndex, rf.lastApplied, rf.getFirstLog(), rf.getLastLog(), resp, req)
}

// advanceCommitIndexForLeader append entry rpc 发现某个节点和leader logIndex 不匹配
// 向前查找match index 一遍append entry
// commit index by matchIndex[]
func (rf *Raft) advanceCommitIndexForLeader() {
	n := len(rf.matchIndex)
	srt := make([]int, n)
	copy(srt, rf.matchIndex)
	insertionSort(srt)
	newCommitIndex := srt[n-(n/2+1)]
	if newCommitIndex > rf.commitIndex {
		// 新的commit Index 大于 follower[rf] 的commit Index, 查看是否和leader 匹配
		// only advance commitIndex for current term's log
		if rf.matchLog(rf.currentTerm, newCommitIndex) {
			// newCommitIndex 及其之前的一同提交
			DPrintf("{Node: %v} advance commitIndex from %d to %d with matchIndex %v in term %d",
				rf.me, rf.commitIndex, newCommitIndex, rf.matchIndex, rf.currentTerm)
			rf.commitIndex = newCommitIndex
			rf.applyCond.Signal()
		} else {
			DPrintf("{Node %d} can not advance commitIndex from %d because the term of newCommitIndex %d is not equal to currentTerm %d",
				rf.me, rf.commitIndex, newCommitIndex, rf.currentTerm)
		}

	}
}

// advanceCommitIndexForFollower advance commitIndex by leaderCommit
func (rf *Raft) advanceCommitIndexForFollower(leaderCommit int) {
	newCommitIndex := Min(leaderCommit, rf.getLastLog().Index)
	if newCommitIndex > rf.commitIndex {
		DPrintf("{Node %d} advance commitIndex from %d to %d with leaderCommit %d in term %d",
			rf.me, rf.commitIndex, newCommitIndex, leaderCommit, rf.currentTerm)

		rf.commitIndex = newCommitIndex
		rf.applyCond.Signal()
	}
}

func (rf *Raft) getLastLog() Entry {
	return rf.logs[len(rf.logs)-1]
}

func (rf *Raft) getFirstLog() Entry {
	return rf.logs[0]
}

// used by replicator goroutine to judge whether a peer needs replicating
func (rf *Raft) needReplicating(peer int) bool {
	rf.mu.Lock()
	defer rf.mu.Unlock()
	return rf.state == StateLeader && rf.matchIndex[peer] < rf.getLastLog().Index
}

func (rf *Raft) replicator(peer int) {
	rf.replicatorCond[peer].L.Lock()
	defer rf.replicatorCond[peer].L.Unlock()

	for !rf.killed() {
		// if there is no need to replicate entries for this peer,
		// just release CPU and wait other goroutine's signal if service adds new Command
		// if this peer needs replicating entries, this goroutine will call
		// replicateOneRound(peer) multiple times until this peer catches up, and then wait
		for !rf.needReplicating(peer) {
			rf.replicatorCond[peer].Wait()
		}
		// maybe a pipeline mechanism is better to trade-off the memory usage and catch up time
		rf.replicateOneRound(peer)
	}
}

// applier a dedicated applier goroutine to guarantee that each log will be push into
// applyCh exactly once, ensuring that service's applying entries and raft's
// committing entries can be parallel
func (rf *Raft) applier() {
	for !rf.killed() {
		rf.mu.Lock()
		// if there is no need to apply entries,
		// just release CPU and wait other goroutine's signal if they commit new entries
		for rf.lastApplied >= rf.commitIndex {
			rf.applyCond.Wait()
		}

		firstIndex, commitIndex, lastApplied := rf.getFirstLog().Index, rf.commitIndex, rf.lastApplied
		entries := make([]Entry, commitIndex-lastApplied)
		copy(entries, rf.logs[lastApplied+1-firstIndex:commitIndex+1-firstIndex])
		rf.mu.Unlock()

		for _, entry := range entries {
			rf.applyCh <- ApplyMsg{
				CommandValid: true,
				Command:      entry.Command,
				CommandTerm:  entry.Term,
				CommandIndex: entry.Index,
			}
		}

		rf.mu.Lock()
		DPrintf("{Node %v} applies entries %v-%v in term %v",
			rf.me, rf.lastApplied, commitIndex, rf.currentTerm)

		rf.lastApplied = Max(rf.lastApplied, commitIndex)
		rf.mu.Unlock()
	}
}

// the service or tester wants to create a Raft server. the ports
// of all the Raft servers (including this one) are in peers[]. this
// server's port is peers[me]. all the servers' peers[] arrays
// have the same order. persister is a place for this server to
// save its persistent state, and also initially holds the most
// recent saved state, if any. applyCh is a channel on which the
// tester or service expects Raft to send ApplyMsg messages.
// Make() must return quickly, so it should start goroutines
// for any long-running work.
func Make(peers []*labrpc.ClientEnd, me int,
	persister *Persister, applyCh chan ApplyMsg) *Raft {
	rf := &Raft{
		peers:          peers,
		persister:      persister,
		me:             me,
		dead:           0,
		applyCh:        applyCh,
		replicatorCond: make([]*sync.Cond, len(peers)),
		state:          StateFollower,
		currentTerm:    0,
		votedFor:       -1,
		logs:           make([]Entry, 1),
		nextIndex:      make([]int, len(peers)),
		matchIndex:     make([]int, len(peers)),
		heartbeatTimer: time.NewTimer(StableHeartbeatTimeout()),
		electionTimer:  time.NewTimer(RandomizedElectionTimeout()),
	}

	// Your initialization code here (2A, 2B, 2C).

	// initialize from state persisted before a crash
	rf.readPersist(persister.ReadRaftState())

	rf.applyCond = sync.NewCond(&rf.mu)
	lastLog := rf.getLastLog()
	for i := 0; i < len(peers); i++ {
		rf.matchIndex[i], rf.nextIndex[i] = 0, lastLog.Index+1
		if i != rf.me {
			rf.replicatorCond[i] = sync.NewCond(&sync.Mutex{})

			// start replicator goroutine to replicate entries in batch
			go rf.replicator(i)
		}
	}

	// start ticker goroutine to start elections
	go rf.ticker()

	// start applier goroutine to push committed logs into applyCh exactly once
	go rf.applier()
	return rf
}
