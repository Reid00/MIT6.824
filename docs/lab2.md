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

## Lab 2A Leader Election
## Summary
整体逻辑, 从 `ticker` goroutine 开始,启动两个Timer, `ElectionTimer` 和 `HeartbeatTimer`. 如果某个raft 节点election timeout,则会触发leader election, 调用`StartElection` 方法. `StartElection` 中发送 `RequestVote RPC`, 根据ReqestVote Response 判断是否收到选票,决定是否成为`Leader`。

如果某个节点,收到大多数节点的选票,成为`Leader` 要通过发送`Heartbeat` 即空LogEntry 的`AppendEntries RPC` 来告诉其他节点自己的 `Leader` 地位。

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

## Lab 2B Log Replication
## Summary
相关的RPC 在[](Lab 2A) 中已经介绍, 这里不再赘述。
启动的Goroutine：
- `ticker`  一个，用于监听 Election Timeout 或者Heartbeat Timeout
- `applier` 一个，监听 leader commit 之后，把log 发送到ApplyCh，然后从applyCh 中持久化到本地
- `replicator ` n-1 个，每一个对应一个 peer。监听心跳广播命令，仅在节点为 Leader 时工作, 唤醒条件变量。接收到命令后，向对应的 peer 发送 AppendEntries RPC。
![lab 2b code summary](https://cdn.staticaly.com/gh/Reid00/image-host@main/20230203/image.6t8huvb8nsg0.webp)

### 快速恢复(Fast Backup)
在前面（7.1）介绍的日志恢复机制中，如果Log有冲突，Leader每次会回退一条Log条目。 这在许多场景下都没有问题。但是在某些现实的场景中，至少在Lab2的测试用例中，每次只回退一条Log条目会花费很长很长的时间。所以，现实的场景中，可能一个Follower关机了很长时间，错过了大量的AppendEntries消息。这时，Leader重启了。按照Raft论文中的图2，如果一个Leader重启了，它会将所有Follower的nextIndex设置为Leader本地Log记录的下一个槽位（7.1有说明）。所以，如果一个Follower关机并错过了1000条Log条目，Leader重启之后，需要每次通过一条RPC来回退一条Log条目来遍历1000条Follower错过的Log记录。这种情况在现实中并非不可能发生。在一些不正常的场景中，假设我们有5个服务器，有1个Leader，这个Leader和另一个Follower困在一个网络分区。但是这个Leader并不知道它已经不再是Leader了。它还是会向它唯一的Follower发送AppendEntries，因为这里没有过半服务器，所以没有一条Log会commit。在另一个有多数服务器的网络分区中，系统选出了新的Leader并继续运行。旧的Leader和它的Follower可能会记录无限多的旧的任期的未commit的Log。当旧的Leader和它的Follower重新加入到集群中时，这些Log需要被删除并覆盖。可能在现实中，这不是那么容易发生，但是你会在Lab2的测试用例中发现这个场景。

所以，为了更快的恢复日志，Raft论文在5.3结尾处，对这种方法有了一些模糊的描述。原文有些晦涩，在这里我会以一种更好的方式，尝试解释论文中有关快速恢复的方法。大致思想是，让Follower返回足够多的信息给Leader，这样Leader可以以`任期（Term）为单位来回退`，`而不用每次只回退一条Log条目`。所以现在，在恢复Follower的Log时，如果Leader和Follower的Log不匹配，Leader只需要对不同任期发生一条AEs，而不需要对每个不通Log条目发送一条AEs。这是一种加速策略，当然也可以有别的日志恢复的加速策略。

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

### 为什么Raft协议不能提交之前任期的日志？
![figure8](https://cdn.staticaly.com/gh/Reid00/image-host@main/20230203/image.5uq9dyccle8.webp)

**如果允许提交之前任期的日志，将导致什么问题?**
我们将论文中的上图展开:
- (a): S1 是leader，将黄色的日志2同步到了S2，然后S1崩溃。
- (b): S5 在任期 3 里通过 S3、S4 和自己的选票赢得选举，将蓝色日志3存储到本地，然后崩溃了。
- (c): S1重新启动，选举成功。注意在这时，如果允许`提交之前任期的日志`，将首先开始同步过往任期的日志，即将S1上的本地黄色的日志2同步到了S3。这时黄色的节点2已经同步到了集群多数节点，然后S1写了一条新日志4，然后S1又崩溃了。
- 接下来会出现两种不同的情况:
- (d1): S5重新当选，如果允许`提交之前任期的日志`，就开始同步往期日志，将本地的蓝色日志3同步到所有的节点。结果已经被同步到半数以上节点的黄色日志2被覆盖了。这说明，如果允许“提交之前任期的日志”，会可能出现即便已经同步到半数以上节点的日志被覆盖，这是不允许的。
- (d2): 反之，如果在崩溃之前，S1不去同步往期的日志，而是首先同步自己任期内的日志4到所有节点，就不会导致黄色日志2被覆盖。因为leader同步日志的流程中，会通过不断的向后重试的方式，将日志同步到其他所有follower，只要日志4被复制成功，在它之前的日志2就会被复制成功。（d2）是想说明：不能直接提交过往任期的日志，即便已经被多数通过，但是可以先同步一条自己任内的日志，如果这条日志通过，就能带着前面的日志一起通过，这是（c）和（d2）两个图的区别。图（c）中，S1先去提交过往任期的日志2，图（d2）中，S1先去提交自己任内的日志4。

我们可以看到的是，如果`允许提交之前任期的日志`这么做，那么：
- (c)中, S1恢复之后，又再次提交在任期2中的黄色日志2。但是，从后面可以看到，即便这个之前任期中的黄色日志2，提交到大部分节点，如果允许`提交之前任期的日志`，仍然存在被覆盖的可能性，因为：
- (d1)中，S5恢复之后，也会提交在自己本地上保存的之前任期3的蓝色日志，这会导致覆盖了前面已经到半数以上节点的黄色日志2。

所以，`如果允许提交之前任期的日志`，即如同(c)和(d1)演示的那样：重新当选之后，马上提交自己本地保存的、之前任期的日志，就会可能导致即便已经同步到半数以上节点的日志，被覆盖的情况。

而`已同步到半数以上节点的日志`，一定在新当选leader上（否则这个节点不可能成为新leader）且达成了一致可提交，即不允许被覆盖。

这就是矛盾的地方，即允许`提交之前任期的日志`，最终导致了违反协议规则的情况。

那么，如何确保新当选的leader节点，其本地的未提交日志被正确提交呢？图(d2)展示了正常的情况：即当选之后，不要首先提交本地已有的黄色日志2，而是首先提交一条新日志4，如果这条新日志被提交成功，那么按照Raft日志的匹配规则（log matching property）：日志4如果能提交，它前面的日志也提交了。

可是，新的问题又出现了，如果在(d2)中，S1重新当选之后，客户端写入没有这条新的日志4，那么前面的日志2是不是永远无法提交了？为了解决这个问题，raft要求每个leader新当选之后，马上写入一条只有任期号和索引、而没有内容的所谓“no-op”日志，以这条日志来驱动在它之前的日志达成一致。

这就是论文中这部分内容想要表达的。这部分内容之所以比较难理解，是因为经常忽略了这个图示展示的是错误的情况，允许`提交之前任期的日志`可能导致的问题。

- (c)和(d2) 有什么区别？
看起来，(c)和(d2)一样，S1当选后都提交了日志1、2、4，那么两者的区别在哪里？

虽然两个场景中，提交的日志都是一样的，但是日志达成一致的顺序并不一致：
- (c)：S1成为leader之后，先提交过往任期、本地的日志2，再提交日志4。这就是`提交之前任期日志`的情况。
- (d2)：S1成为leader之后，先提交本次任期的日志4，如果日志4能提交成功，那么它前面的日志2就能提交成功了。

关于(d2)的这个场景，有可能又存在着下一个疑问：
如何理解(d2)中，“本任期的日志4提交成功，那么它前面的日志2也能提交成功了”？

这是由raft日志的Log Matching Property决定的:
- If two entries in different logs have the same index and term, then they store the same command. If two entries in different logs have the same index and term, then the logs are identical in all preceding entries.
- If two entries in different logs have the same index and term, then the logs are identical in all preceding entries

- 第一条性质，说明的是在不同节点上的已提交的日志，如果任期号、索引一样，那么它们的内容肯定一样。这是由leader节点的安全性和leader上的日志只能添加不能覆盖来保证的，这样leader就永远不会在同一个任期，创建两个相同索引的日志。

- 第二条性质，说明的是在不同节点上的日志中，如果其中有同样的一条日志（即相同任期和索引）已经达成了一致，那么在这不同节点上在这条日志之前的所有日志都是一样的。

第二条性质是由leader节点向follower节点上根据AppendEntries消息同步日志上保证的。leader在AppendEntries消息中会携带新添加entries之前日志的term和index 即`PrevLogTerm`, `PrevLogIndex`，follower会判断在log中是否存在拥有此term和index的消息，如果没有就会拒绝。

- leader为每一个follower维护一个nextIndex，表示待发送的下一个日志的index。初始化为日志长度。
- leader在follower拒绝AppendEntries之后会对nextIndex减一，然后继续重试AppendEntries直到两者一致。

于是，回到我们开始的问题，(d2)场景中，在添加本任期日志4的时候，会发现有一些节点上并不存在过往任期的日志2，这时候就会相应地计算不同节点的nextIndex索引，来驱动同步日志2到这些节点上。

总而言之，根据日志的性质，只要本任期的日志4能达成一致，上一条日志2就能达成一致。

## Lab 2D - Log Compaction
## Summary

快照可以由上层应用触发。当上层应用认为可以将一些已提交的 entry 压缩成 snapshot 时，其会调用节点的 `Snapshot()`函数，将需要压缩的状态机的状态数据传递给节点，作为快照。

在正常情况下，仅由上层应用命令节点进行快照即可。但如果节点出现落后或者崩溃，情况则变得更加复杂。考虑一个日志非常落后的节点 i，当 Leader 向其发送 AppendEntries RPC 时，nextIndex[i] 对应的 entry 已被丢弃，压缩在快照中。这种情况下， Leader 就无法对其进行 AppendEntries。取而代之的是，这里我们应该实现一个新的 `InstallSnapshot` RPC，将 Leader 当前的快照直接发送给非常落后的 Follower。
- `服务端触发的日志压缩`: 上层应用发送快照数据给Raft实例。
- `leader 发送来的 InstallSnapshot`: 领导者发送快照RPC请求给追随者。当raft收到其他节点的压缩请求后，先把请求上报给上层应用，然后上层应用调用`rf.CondInstallSnapshot()`来决定是否安装快照

![compaction](https://cdn.staticaly.com/gh/Reid00/image-host@main/20230206/image.5gq1fub2rvc0.webp)

### 服务端触发的Log Compact
`func (rf *Raft) Snapshot(index int, snapshot []byte)`
应用程序将index（包括）之前的所有日志都打包为了快照，即参数snapshot [] byte。那么对于Raft要做的就是，将打包为快照的日志直接删除，并且要将快照保存起来，因为将来可能会发现某些节点大幅度落后于leader的日志，那么leader就直接发送快照给它，让他的日志“跟上来”。

### 由 Leader 发送来的 InstallSnapshot
`func (rf *Raft) InstallSnapshot(req *InstallSnapshotReq, resp *InstallSnapshotResp)`

对于 leader 发过来的 InstallSnapshot，只需要判断 term 是否正确，如果无误则 follower 只能无条件接受。

### Follower 收到 InstallSnapshot RPC 后
`func (rf *Raft) CondInstallSnapshot(lastIncludedTerm int, lastIncludedIndex int, snapshot []byte) bool`

Follower接收到snapshot后不能够立刻应用并截断日志，raft和状态机都需要应用snapshot，这需要考虑原子性。如果raft应用成功但状态机应用snapshot失败，那么在接下来的时间里客户端读到的数据是不完整的。如果状态机应用snapshot成功但raft应用失败，那么raft会要求重传，状态机应用成功也没啥意义。因此CondInstallSnapshot是异步于raft的，并由应用层调用。

假设有一个节点一直是 crash 的，然后复活了，leader 发现其落后的太多，于是发送 InstallSnapshot() RPC 到落后的节点上面。落后节点收到 InstallSnapshot() 中的 snapshot 后，通过 rf.applyCh 发送给上层 service 。上层的 service 收到 snapshot 时，调用节点的 CondInstallSnapshot() 方法。节点如果在该 snapshot 之后有新的 commit，则拒绝安装此 snapshot，service 也会放弃本次安装。反之如果在该 snapshot 之后没有新的 commit，那么节点会安装此 snapshot 并返回 true，service 收到后也同步安装。

![snapshotRPC](https://cdn.staticaly.com/gh/Reid00/image-host@main/20230206/image.5xbtc7a97o40.webp)


## InstallSnapshot PRC
invoked by leader to send chunks of a snapshot to a follower.Leaders always send chunks in order. 
虽然多数情况都是每个服务器独立创建快照, 但是leader有时候必须发送快照给一些落后太多的follower, 这通常发生在leader已经丢弃了下一条要发给该follower的日志条目(Log Compaction时清除掉了)的情况下。

Args
- `term`: Leader 任期。同样，InstallSnapshot RPC 也要遵循 Figure 2 中的规则。如果节点发现自己的任期小于 Leader 的任期，就要及时更新
- `leaderId`: 用于重定向 client 
- `lastIncludedIndex`: 快照中包含的最后一个 entry 的 index
- `lastIncludedTerm`: 快照中包含的最后一个 entry 的 index 对应的 term
- `offset`: 分块在快照中的偏移量
- `data[]`: 快照数据
- `done`: 如果是最后一块数据则为真

Reply
- `term`: 节点的任期。Leader 发现高于自己任期的节点时，更新任期并转变为 Follower

Receiver Implementation 接收日志的follower需要实现的
1. 如果 term < currentTerm，直接返回
2. 如果是第一个分块 (offset为0) 则创建新的快照
3. 在指定的偏移量写入数据
4. 如果done为false, 则回复并继续等待之后的数据
5. 保存快照文件, 丢弃所有已存在的或者部分有着更小索引号的快照
6. 如果现存的日志拥有相同的最后任期号和索引值, 则后面的数据继续保留并且回复
7. 丢弃全部日志
8. 能够使用快照来恢复状态机 (并且装载快照中的集群配置)

