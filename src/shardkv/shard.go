package shardkv

import "fmt"

type Shard struct {
	KV     map[string]string
	Status ShardStatus
}

func NewShard() *Shard {
	return &Shard{
		KV:     make(map[string]string),
		Status: Serving,
	}
}

func (s *Shard) Get(key string) (string, Err) {
	if val, ok := s.KV[key]; ok {
		return val, OK
	}
	return "", ErrNoKey
}

func (s *Shard) Put(key, val string) Err {
	s.KV[key] = val
	return OK
}

func (s *Shard) Append(key, val string) Err {
	s.KV[key] += val
	return OK
}

// deepCopy only copy Shard KV data
func (s *Shard) deepCopy() map[string]string {
	newShard := make(map[string]string)

	for key, val := range s.KV {
		newShard[key] = val
	}
	return newShard
}

func (s Shard) String() string {
	return fmt.Sprintf("ShardKV: -, Status: %v", s.Status)
}
