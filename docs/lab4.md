# ShardCtrler

# 介绍
在本实验中，我们将构建一个带分片的KV存储系统，即一组副本组上的键。每一个分片都是KV对的子集，例如，所有以“a”开头的键可能是一个分片，所有以“b”开头的键可能是另一个分片。 也可以用range 或者Hash 之后分区。

分片的原因是性能。每个replica group只处理几个分片的 put 和 get，并且这些组并行操作；因此，系统总吞吐量（每单位时间的投入和获取）与组数成比例增加。

本实验中，Group表示一个Leader-Followers集群，Gid为它的标识，Shard表示所有数据的一个子集，Config表示一个划分方案。本实验中，所有数据分为NShards = 10份，Server给测试程序提供四个接口。

分片存储系统必须能够在replica group之间移动分片，因为某些组可能比其他组负载更多，因此需要移动分片以平衡负载；而且replica group可能会加入和离开系统，可能会添加新的副本组以增加容量，或者可能会使现有的副本组脱机以进行修复或报废。

# 实现
本实验的主要挑战是处理重新配置——移动分片所属。在单个副本组中，所有组成员必须就何时发生与客户端 Put/Append/Get 请求相关的重新配置达成一致。例如，Put 可能与重新配置大约同时到达，导致副本组停止对该Put包含的key的分片负责。组中的所有副本必须就 Put 发生在重新配置之前还是之后达成一致。如果之前，Put 应该生效，分片的新所有者将看到它的效果；如果之后，Put 将不会生效，客户端必须在新所有者处重新尝试。推荐的方法是让每个副本组使用 Raft 不仅记录 Puts、Appends 和 Gets 的顺序，还记录重新配置的顺序。您需要确保在任何时候最多有一个副本组为每个分片提供请求。

重新配置还需要副本组之间的交互。例如，在配置 10 中，组 G1 可能负责分片 S1。在配置 11 中，组 G2 可能负责分片 S1。在从 10 到 11 的重新配置过程中，G1 和 G2 必须使用 RPC 将分片 S1（键/值对）的内容从 G1 移动到 G2。

Lab4的内容就是将数据按照某种方式分开存储到不同的RAFT集群(Group)上，分片(shard)的策略有很多，比如：所有以a开头的键是一个分片，所有以b开头的键是一个分片。保证相应数据请求引流到对应的集群，降低单一集群的压力，提供更为高效、更为健壮的服务。
![structure](https://cdn.staticaly.com/gh/Reid00/image-host@main/20230221/image.6ikl21aksm80.webp)

- 具体的lab4要实现一个支持 multi-raft分片 、分片数据动态迁移的线性一致性分布式 KV 存储服务。
- shard表示互不相交并且组成完整数据库的每一个数据库子集。group表示server的集合，包含一个或多个server。一个shard只可属于一个group，一个group可包含(管理)多个shard。
- lab4A实现ShardCtrler服务，作用：提供高可用的集群配置管理服务，实现分片的负载均衡，并尽可能少地移动分片。记录了每组（Group）ShardKVServer的集群信息和每个分片（shard）服务于哪组（Group）ShardKVServer。具体实现通过Raft维护 一个Configs数组，单个config具体内容如下：
- Num：config number，Num=0表示configuration无效，边界条件， 即是version 的作用
- Shards：shard -> gid，分片位置信息，Shards[3]=2，说明分片序号为3的分片负贵的集群是Group2（gid=2）
- Groups：gid -> servers[], 集群成员信息，Group[3]=['server1','server2'],说明gid = 3的集群Group3包含两台名称为server1 & server2的机器
- lab4B实现ShardKVServer服务，ShardKVServer则需要实现所有分片的读写任务，相比于MIT 6.824 Lab3 RaftKV的提供基础的读写服务，还需要功能和难点为配置更新，分片数据迁移，分片数据清理，空日志检测

## RPC
- Query RPC。查询配置，参数是一个配置号， shardctrler 回复具有该编号的配置。如果该数字为 -1 或大于已知的最大配置数字，则 shardctrler 应回复最新配置。 Query(-1) 的结果应该反映 shardctrler 在收到 Query(-1) RPC 之前完成处理的每个 Join、Leave 或 Move RPC；

- Join RPC 。添加新的replica group，它的参数是一组从唯一的非零副本组标识符 (GID) 到服务器名称列表的映射。 shardctrler 应该通过创建一个包含新副本组的新配置来做出反应。新配置应在所有组中尽可能均匀地分配分片，并应移动尽可能少的分片以实现该目标。如果 GID 不是当前配置的一部分，则 shardctrler 应该允许重新使用它（即，应该允许 GID 加入，然后离开，然后再次加入）；
> 对于 Join，可以通过多次平均地方式来达到这个目的：每次选择一个拥有 shard 数最多的 raft 组和一个拥有 shard 数最少的 raft，将前者管理的一个 shard 分给后者，周而复始，直到它们之前的差值小于等于 1 且 0 raft 组无 shard 为止。对于 Leave，如果 Leave 后集群中无 raft 组，则将分片所属 raft 组都置为无效的 0；否则将删除 raft 组的分片均匀地分配给仍然存在的 raft 组。通过这样的分配，可以将 shard 分配地十分均匀且产生了几乎最少的迁移任务。

- Leave RPC。删除指定replica group， 参数是以前加入的组的 GID 列表。 shardctrler 应该创建一个不包括这些组的新配置，并将这些组的分片分配给剩余的组。新配置应在组之间尽可能均匀地划分分片，并应移动尽可能少的分片以实现该目标；

- Move RPC。移动分片，的参数是一个分片号和一个 GID。 shardctrler 应该创建一个新配置，其中将分片分配给组。 Move 的目的是让我们能够测试您的软件。移动后的加入或离开可能会取消移动，因为加入和离开会重新平衡。

```go
// Join according to new Group(gid -> servers) to change the Config
func (cf *MemoryConfigStateMachine) Join(groups map[int][]string) Err {
	lastConfig := cf.Configs[len(cf.Configs)-1]
	newConfig := Config{
		Num:    len(cf.Configs),
		Shards: lastConfig.Shards,
		Groups: deepCopy(lastConfig.Groups),
	}

	for gid, servers := range groups {
		if _, ok := newConfig.Groups[gid]; !ok {
			newServers := make([]string, len(servers))
			copy(newServers, servers)
			newConfig.Groups[gid] = newServers
		}
	}

	// 找到group 中shard 最大和最小的组，将数据进行move => reblance
	g2s := Group2Shards(newConfig)
	for {
		src, dst := GetGIDWIthMaxShards(g2s), GetGIDWithMinShards(g2s)
		if src != 0 && len(g2s[src])-len(g2s[dst]) <= 1 {
			break
		}

		g2s[dst] = append(g2s[dst], g2s[src][0])
		g2s[src] = g2s[src][1:]
	}

	var newShards [NShards]int
	for gid, shards := range g2s {
		for _, shard := range shards {
			newShards[shard] = gid
		}
	}
	newConfig.Shards = newShards
	cf.Configs = append(cf.Configs, newConfig)
	return OK
}

// Leave some group leave the cluster
func (cf *MemoryConfigStateMachine) Leave(gids []int) Err {
	lastConfig := cf.Configs[len(cf.Configs)-1]
	newConfig := Config{
		Num:    len(cf.Configs),
		Shards: lastConfig.Shards,
		Groups: deepCopy(lastConfig.Groups),
	}

	g2s := Group2Shards(newConfig)
	orphanShards := make([]int, 0)

	for _, gid := range gids {
		delete(newConfig.Groups, gid)
		if shards, ok := g2s[gid]; ok {
			orphanShards = append(orphanShards, shards...)
			delete(g2s, gid)
		}
	}

	var newShards [NShards]int
	if len(newConfig.Groups) != 0 {
		// reblance
		for _, shard := range orphanShards {
			target := GetGIDWithMinShards(g2s)
			g2s[target] = append(g2s[target], shard)
		}

		// update Shards: share -> gid
		for gid, shards := range g2s {
			for _, shard := range shards {
				newShards[shard] = gid
			}
		}
	}

	newConfig.Shards = newShards
	cf.Configs = append(cf.Configs, newConfig)
	return OK
}

// Move move No.shard to No.gid
func (cf *MemoryConfigStateMachine) Move(shard, gid int) Err {
	lastConfig := cf.Configs[len(cf.Configs)-1]
	newConfig := Config{
		Num:    len(cf.Configs),
		Shards: lastConfig.Shards,
		Groups: lastConfig.Groups,
	}

	newConfig.Shards[shard] = gid
	cf.Configs = append(cf.Configs, newConfig)
	return OK
}

// Query return the version of num config
func (cf *MemoryConfigStateMachine) Query(num int) (Config, Err) {
	if num < 0 || num >= len(cf.Configs) {
		return cf.Configs[len(cf.Configs)-1], OK
	}
	return cf.Configs[num], OK
}
```