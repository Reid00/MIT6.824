package shardctrler

import (
	"sort"
)

type ConfigStateMachine interface {
	Join(groups map[int][]string) Err
	Leave(gids []int) Err
	Move(shard, gid int) Err
	Query(num int) (Config, Err)
}

type MemoryConfigStateMachine struct {
	Configs []Config
}

func NewMemoryConfigStateMachine() *MemoryConfigStateMachine {
	conf := &MemoryConfigStateMachine{
		Configs: make([]Config, 1),
	}
	conf.Configs[0] = DefaultConfig()
	return conf
}

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
		if _, ok := newConfig.Groups[gid]; ok {
			delete(newConfig.Groups, gid)
		}
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

// GetGIDWIthMaxShards 获取shard 最多的GID
// return gid
func GetGIDWIthMaxShards(g2s map[int][]int) int {
	if shards, ok := g2s[0]; ok && len(shards) > 0 {
		return 0
	}
	var keys []int
	for k := range g2s {
		keys = append(keys, k)
	}
	sort.Ints(keys)

	index, max := -1, -1
	for _, gid := range keys {
		if len(g2s[gid]) > max {
			index, max = gid, len(g2s[gid])
		}
	}
	return index
}

// GetGIDWithMinShards 获取shard 最少的GID
// return gid
func GetGIDWithMinShards(g2s map[int][]int) int {
	var keys []int

	for k := range g2s {
		keys = append(keys, k)
	}

	sort.Ints(keys)

	// find GID with min shards
	index, min := -1, NShards+1
	for _, gid := range keys {
		if gid != 0 && len(g2s[gid]) < min {
			index, min = gid, len(g2s[gid])
		}
	}
	return index
}

// Group2Shards
// return gid -> shards
func Group2Shards(conf Config) map[int][]int {
	s2g := make(map[int][]int)

	for gid := range conf.Groups {
		s2g[gid] = make([]int, 0)
	}

	for shard, gid := range conf.Shards {
		s2g[gid] = append(s2g[gid], shard)
	}
	return s2g
}

func deepCopy(groups map[int][]string) map[int][]string {
	newGroups := make(map[int][]string)
	for gid, servers := range groups {
		newServers := make([]string, len(servers))
		copy(newServers, servers)
		newGroups[gid] = newServers
	}
	return newGroups
}
