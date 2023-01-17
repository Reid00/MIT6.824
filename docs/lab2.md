# Raft

- [论文](https://raft.github.io/raft.pdf)
- [官网](https://raft.github.io/)
- [动画展示](http://thesecretlivesofdata.com/raft/#overview)
- [Students' Guide to Raft](https://thesquareplanet.com/blog/students-guide-to-raft/)
- [MIT6.824](https://pdos.csail.mit.edu/6.824/index.html)

论文的Firgure2 是整个 `Raft` 代码实现的核心,现在一一解释下.

## State
### Persistent state for all servers 所有Raft 节点都需要维护的持久化状态:
- `currentTerm`: 此节点当前的任期。保证重启后任期不丢失。启动时初始值为0(无意义状态)，单调递增 (Lab 2A)
- `votedFor`:  当前任期内,此节点将选票给了谁。 `一个任期内,节点只能将选票投给某个节点`。需要持久化，从而避免节点重启后重复投票。(Lab 2A)
- `logs`: 日志条目, 每条 Entry 包含一条待施加至状态机的命令。Entry 也要记录其被发送至 Leader 时，Leader 当时的任期。Lab2B 中，在内存存储日志即可，不用担心 server 会 down 掉，测试中仅会模拟网络挂掉的情景。初始Index从1开始，0为dummy index。

为什么 `currentTerm` 和 `votedFor` 需要持久化?

**votedFor 保证每个任期最多只有一个Leader！**

考虑如下一种场景：
因为在`Raft`协议中每个任期内有且仅有一个Leader。现假设有几个`Raft`节点在当前任期下投票给了`Raft`节点A，并且`Raft` A顺利成为了Leader。现故障系统被重启，重启后如果收到一个相同任期的`Raft`节点B的投票请求，由于每个节点并没有记录其投票状态，那么这些节点就有可能投票给`Raft` B，并使B成为Leader。此时，在同一个任期内就会存在两个Leader，与`Raft`的要求不符。

**保证每个Index位置只会有一个Term! (也等价于每个任期内最多有一个Leader)**

![currentTerm](https://cdn.staticaly.com/gh/Reid00/image-host@main/20230113/image.25rcj5suacxs.webp)

在这里例子中，S1关机了，S2和S3会尝试选举一个新的Leader。它们需要证据证明，正确的任期号是8，而不是6。如果仅仅是S2和S3为彼此投票，它们不知道当前的任期号，它们只能查看自己的Log，它们或许会认为下一个任期是6（因为Log里的上一个任期是5）。如果它们这么做了，那么它们会从任期6开始添加Log。但是接下来，就会有问题了，因为我们有了两个不同的任期6（另一个在S1中）。这就是为什么currentTerm需要被持久化存储的原因，因为它需要用来保存已经被使用过的任期号。

这些数据需要在每次你修改它们的时候存储起来。所以可以确定的是，安全的做法是每次你添加一个Log条目，更新currentTerm或者更新votedFor，你或许都需要持久化存储这些数据。在一个真实的Raft服务器上，这意味着将数据写入磁盘，所以你需要一些文件来记录这些数据。如果你发现，直到服务器与外界通信时，才有可能持久化存储数据，那么你可以通过一些批量操作来提升性能。例如，只在服务器回复一个RPC或者发送一个RPC时，服务器才进行持久化存储，这样可以节省一些持久化存储的操作。

### Volatile state on all servers 每一个节点都应该有的非持久化状态：
- `commitIndex`: 已提交的最大 index。被提交的定义为，当 Leader 成功在大部分 server 上复制了一条 Entry，那么这条 Entry 就是一条已提交的 Entry。leader 节点重启后可以通过 appendEntries rpc 逐渐得到不同节点的 matchIndex，从而确认 commitIndex，follower 只需等待 leader 传递过来的 commitIndex 即可。（初始值为0，单调递增）
- `lastApplied`: 已被状态机应用的最大 index。已提交和已应用是不同的概念，已应用指这条 Entry 已经被运用到状态机上。已提交先于已应用。同时需要注意的是，Raft 保证了已提交的 Entry 一定会被应用（通过对选举过程增加一些限制，下面会提到）。raft 算法假设了状态机本身是易失的，所以重启后状态机的状态可以通过 log[] （部分 log 可以压缩为 snapshot) 来恢复。（初始值为0，单调递增）

`commitIndex` 和 `lastApplied` 分别维护 log 已提交和已应用的状态，当节点发现 commitIndex > lastApplied 时，代表着 `commitIndex` 和 `lastApplied` 间的 entries 处于已提交，未应用的状态。因此应将其间的 entries `按序应用至状态机`。

对于 Follower，commitIndex 通过 Leader AppendEntries RPC 的参数 leaderCommit 更新。对于 Leader，commitIndex 通过其维护的 matchIndex 数组更新。

### Volatile state on leaders leader 的非持久化状态：
- `nextIndex[]`:  由 Leader 维护，nextIndex[i] 代表需要同步给 peer[i] 的下一个 entry 的 index。在 Leader 当选后，重新初始化为 Leader 的 lastLogIndex + 1。
- `matchIndex[]`:  由 Leader 维护，matchIndex[i] 代表 Leader 已知的已在 peer[i] 上成功复制的最高 entry index。在 Leader 当选后，重新初始化为 0。

每次选举后，leader 的此两个数组都应该立刻重新初始化并开始探测。

不能简单地认为 matchIndex = nextIndex - 1。

nextIndex `是对追加位置的一种猜测`，是乐观的估计。因此，当 Leader 上任时，会将 nextIndex 全部初始化为 lastLogIndex + 1，即乐观地估计所有 Follower 的 log 已经与自身相同。AppendEntries PRC 中，Leader 会根据 nextIndex 来决定向 Follower 发送哪些 entry。当返回失败时，则会将 nextIndex 减一，猜测仅有一条 entry 不一致，再次乐观地尝试。实际上，使用 nextIndex 是为了提升性能，仅向 Follower 发送不一致的 entry，减小 RPC 传输量。

matchIndex `则是对同步情况的保守确认`，为了保证安全性。matchIndex 及此前的 entry 一定都成功地同步。matchIndex 的作用是帮助 Leader 更新自身的 commitIndex。当 Leader 发现一个 Index N 值，N 大于过半数的 matchIndex，则可将其 commitIndex 更新为 N（需要注意任期号的问题，后文会提到）。matchIndex 在 Leader 上任时被初始化为 0。

nextIndex 是最乐观的估计，被初始化为最大可能值；matchIndex 是最悲观的估计，被初始化为最小可能值。在一次次心跳中，nextIndex 不断减小，matchIndex 不断增大，直至 matchIndex = nextIndex - 1，则代表该 Follower 已经与 Leader 成功同步。

## Rules for Servers
### All Servers
- 如果commitIndex > lastApplied, 那么将lastApplied自增, 并把对应日志log[lastApplied]应用到状态机
- 如果来自其他节点的 RPC `请求`(RequestVote, AppendEntries, InstallSnapshot)中，或发给其他节点的 RPC 的`回复`中，包含一个term T大于`currentTerm`, 那么将`currentTerm`赋值为T并立即切换状态为 Follower。(Lab 2A)

### Followers
- 响应来自 Candidate 和 Leader 的 RPC 请求。(Lab 2A)
- 如果在 election timeout 到期时，Follower 未收到来自当前 Leader 的 AppendEntries RPC，也没有收到来自 Candidate 的 RequestVote RPC，则转变为 Candidate。(Lab 2A)

### Candidate
- 转变 Candidate时，开始一轮选举：(Lab 2A)
    - currentTerm ++ 
    - 为自己投票, votedFor = me
    - 重置 election timer
    - 向其他所有节点`并行`发送 RequestVote RPC
- 如果收到了大多数节点的选票（voteCnt > n/2），当选 Leader。(Lab 2A)
- 在选举过程中，如果收到了来自新 Leader 的 AppendEntries RPC，停止选举，转变为 Follower。(Lab 2A)
- 如果 election timer 超时时，还未当选 Leader，则放弃此轮选举，开启新一轮选举。(Lab 2A)

### Leader
- 刚上任时，向所有节点发送一轮心跳信息(empty AppendEntries)。此后，每隔一段固定时间，向所有节点发送一轮心跳信息，重置其他节点的 election timer，以维持自己 Leader 的身份。(Lab 2A)
- 如果收到了来自 client 的 command，将 command 以 entry 的形式添加到日志。在收到大多数响应后将该条目应用到状态机并回复响应给客户端。在 lab2B 中，client 通过 Start() 函数传入 command。
- 如果 lastLogIndex >= nextIndex[i]，向 peer[i] 发送 AppendEntries RPC，RPC 中包含从 nextIndex[i] 开始的日志。
    - 如果返回值为 true，更新 nextIndex[i] 和 matchIndex[i]。
    - 如果因为 entry 冲突，RPC 返回值为 false，则将 nextIndex[i] 减1并重试。这里的重试不一定代表需要立即重试，实际上可以仅将 nextIndex[i] 减1，下次心跳时则是以新值重试。
- 如果存在 index 值 N 满足：N > commitIndex && 过半数 matchIndex[i] >= N && log[N].term == currentTerm, 则令commitIndex = N。

这里最后一条是 Leader 更新 commitIndex 的方式。前两个要求都比较好理解，第三个要求是 Raft 的一个特性，即 Leader 仅会直接提交其任期内的 entry。存在这样一种情况，Leader 上任时，其最新的一些条目可能被认为处于未被提交的状态（但这些条目实际已经成功同步到了大部分节点上）。Leader 在上任时并不会检查这些 entry 是不是实际上已经可以被提交，而是通过提交此后的 entry 来间接地提交这些 entry。这种做法能够 work 的基础是 Log Matching Property：
>Log Matching: if two logs contain an entry with the same index and term, then the logs are identical in all entries up through the given index.

## Lab 2A
## Summary
整体逻辑, 从 `ticker` goroutine 开始,启动两个Timer, `ElectionTimer` 和 `HeartbeatTimer`. 如果某个raft 节点election timeout,则会触发leader election, 调用`StartElection` 方法. `StartElection` 中发送 `RequestVote RPC`, 根据ReqestVote Response 判断是否收到选票,决定是否成为`Leader`。

如何某个节点,收到大多数节点的选票,成为`Leader` 要通过发送`Heartbeat` 即空LogEntry 的`AppendEntries RPC` 来告诉其他节点自己的 `Leader` 地位。

所以Lab2A 中,主要实现 `RequestVote`, `AppendEntries` 的逻辑。

![lab2A](https://cdn.staticaly.com/gh/Reid00/image-host@main/20230111/image.5nn1zuw5exc0.webp)

## RequestVote RPC
Invoked by candidates to gather votes (§5.2).
会被 Candidate 调用，以此获取选票。

Args
- `term`: Candidate 的任期 (Lab 2A)
- `candidateId`: 发起投票请求的候选人id (Lab 2A)
- `lastLogIndex`: 候选人最新的日志条目索引， Candidate 最后一个 entry 的 index，是投票的额外判据
- `lastLogTerm`: 候选人最新日志条目对应的任期号

Reply
- `term`: 收到`RequestVote RPC` Raft节点的任期。假如 Candidate 发现 Follower 的任期高于自己，则会放弃 Candidate 身份并更新自己的任期
- `voteGranted`: 是否同意 Candidate 当选。

Receiver Implementation 接收日志的follower需要实现的
1. 当 Candidate 任期小于当前节点任期时，返回 false。
2. 如果 `votedFor` 为 null（即当前任期内此节点还未投票, Go 代码中用-1）或者 `votedFor`为 `candidateId`（即当前任期内此节点已经向此 Candidate 投过票），则同意投票；否则拒绝投票（Lab 2A 只需要实现到这个程度）。 事实上还要: 只有 Candidate 的 log 至少与 Receiver 的 log 一样新（up-to-date）时，才同意投票。Raft 通过两个日志的最后一个 entry 来判断哪个日志更 up-to-date。假如两个 entry 的 term 不同，term 更大的更新。term 相同时，index 更大的更新。

这里投票的额外限制(up-to-date)是为了保证已经被 commit 的 entry 一定不会被覆盖。仅有当 Candidate 的 log 包含所有已提交的 entry，才有可能当选为 Leader。

## AppendEntries RPC
Invoked by leader to replicate log entries (§5.3); also used as heartbeat (§5.2).
在领导选举的过程中，AppendEntries RPC 用来实现 Leader 的心跳机制。节点的 AppendEntries RPC 会被 Leader 定期调用。正常存在Leader 时，用来进行Log Replacation。

Args
- `term`: Leader 任期 (Lab 2A)
- `leadId`: Client 可能将请求发送至 Follower 节点，得知 leaderId 后 Follower 可将 Client 的请求重定位至 Leader 节点。因为 Raft 的请求信息必须先经过 Leader 节点，再由 Leader 节点流向其他节点进行同步，信息是单向流动的。在选主过程中，leaderId暂时只有 debug 的作用 (Lab 2A)
- `prevLogIndex`: 添加 Entries 的前一条 Entry 的 index
- `prevLogTerm`: prevLogIndex 对应 entry 的 term
- `entries[]`: 需要同步的 entries。若为空，则代表是一次 heartbeat。需要注意的是，不需要特别判断是否为 heartbeat，即使是 heartbeat，也需要进行一系列的检查。因此本文也不再区分心跳和 AppendEntries RPC
- `leaderCommit`: Leader 的 commitIndex，帮助 Follower 更新自身的 commitIndex

Reply
- `term`: 此节点的任期。假如 Leader 发现 Follower 的任期高于自己，则会放弃 Leader 身份并更新自己的任期。
- `success`: 此节点是否认同 Leader 发送的RPC。

Receiver Implementation 接收日志的follower需要实现的
1. 当 Leader 任期小于当前节点任期时，返回 false。
2. 若 Follower 在 prevLogIndex 位置的 entry 的 term 与 Args 中的 prevLogTerm 不同（或者 prevLogIndex 的位置没有 entry），返回 false。
3. 如果 Follower 的某一个 entry 与需要同步的 entries 中的一个 entry 冲突，则需要删除冲突 entry 及其之后的所有 entry。需要特别注意的是，假如没有冲突，不能删除任何 entry。因为存在 Follower 的 log 更 up-to-date 的可能。
4. 添加 Log 中不存在的新 entry。
5. 如果 leaderCommit > commitIndex，令 commitIndex = min(leaderCommit, index of last new entry)。此即 Follower 更新 commitIndex 的方式。

### 快速恢复(Fast Backup)
在前面（7.1）介绍的日志恢复机制中，如果Log有冲突，Leader每次会回退一条Log条目。 这在许多场景下都没有问题。但是在某些现实的场景中，至少在Lab2的测试用例中，每次只回退一条Log条目会花费很长很长的时间。所以，现实的场景中，可能一个Follower关机了很长时间，错过了大量的AppendEntries消息。这时，Leader重启了。按照Raft论文中的图2，如果一个Leader重启了，它会将所有Follower的nextIndex设置为Leader本地Log记录的下一个槽位（7.1有说明）。所以，如果一个Follower关机并错过了1000条Log条目，Leader重启之后，需要每次通过一条RPC来回退一条Log条目来遍历1000条Follower错过的Log记录。这种情况在现实中并非不可能发生。在一些不正常的场景中，假设我们有5个服务器，有1个Leader，这个Leader和另一个Follower困在一个网络分区。但是这个Leader并不知道它已经不再是Leader了。它还是会向它唯一的Follower发送AppendEntries，因为这里没有过半服务器，所以没有一条Log会commit。在另一个有多数服务器的网络分区中，系统选出了新的Leader并继续运行。旧的Leader和它的Follower可能会记录无限多的旧的任期的未commit的Log。当旧的Leader和它的Follower重新加入到集群中时，这些Log需要被删除并覆盖。可能在现实中，这不是那么容易发生，但是你会在Lab2的测试用例中发现这个场景。

我将可能出现的场景分成3类，为了简化，这里只画出一个Leader（S2）和一个Follower（S1），S2将要发送一条任期号为6的AppendEntries消息给Follower。
- 场景1：S1(Follower)没有任期6的任何Log，因此我们需要回退一整个任期的Log。

![scenario](https://cdn.staticaly.com/gh/Reid00/image-host@main/20230117/image.5vhjr3670to0.webp)

- 场景2：S1收到了任期4的旧Leader的多条Log，但是作为新Leader，S2只收到了一条任期4的Log。所以这里，我们需要覆盖S1中有关旧Leader的一些Log。

![scenario](https://cdn.staticaly.com/gh/Reid00/image-host@main/20230117/image.75o42ybpazo0.webp)

- 场景3: S1与S2的Log不冲突，但是S1缺失了部分S2中的Log

![scenario](https://cdn.staticaly.com/gh/Reid00/image-host@main/20230117/image.29pfjgga39j4.webp)

可以让Follower在回复Leader的AppendEntries消息中，携带3个额外的信息，来加速日志的恢复。这里的回复是指，Follower因为Log信息不匹配，拒绝了Leader的AppendEntries之后的回复。这里的三个信息是指：
- XTerm: 这个是Follower中与Leader冲突的Log对应的任期号。在之前（7.1）有介绍Leader会在prevLogTerm中带上本地Log记录中，前一条Log的任期号。如果Follower在对应位置的任期号不匹配，它会拒绝Leader的AppendEntries消息，并将自己的任期号放在XTerm中。如果Follower在对应位置没有Log，那么这里会返回 -1。
- XIndex: 这个是Follower中，对应任期号为XTerm的第一条Log条目的槽位号。
- XLen: 如果Follower在对应位置没有Log，那么XTerm会返回-1，XLen表示空白的Log槽位数。

我们再来看这些信息是如何在上面3个场景中，帮助Leader快速回退到适当的Log条目位置。
- 场景1: Follower（S1）会返回XTerm=5，XIndex=2。Leader（S2）发现自己没有任期5的日志，它会将自己本地记录的，S1的nextIndex设置到XIndex，也就是S1中，任期5的第一条Log对应的槽位号。所以，如果Leader完全没有XTerm的任何Log，那么它应该回退到XIndex对应的位置（这样，Leader发出的下一条AppendEntries就可以一次覆盖S1中所有XTerm对应的Log）
- 场景2： Follower（S1）会返回XTerm=4，XIndex=1。Leader（S2）发现自己其实有任期4的日志，它会将自己本地记录的S1的nextIndex设置到本地在XTerm位置的Log条目后面，也就是槽位2。下一次Leader发出下一条AppendEntries时，就可以一次覆盖S1中槽位2和槽位3对应的Log。
- 场景3: Follower（S1）会返回XTerm=-1，XLen=2。这表示S1中日志太短了，以至于在冲突的位置没有Log条目，Leader应该回退到Follower最后一条Log条目的下一条，也就是槽位2，并从这开始发送AppendEntries消息。槽位2可以从XLen中的数值计算得到。