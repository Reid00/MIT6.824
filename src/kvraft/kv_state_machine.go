package kvraft

// KVStateMachine 抽象化为接口层的操作
type KVStateMachine interface {
	Get(key string) (string, Err)

	Put(key, value string) Err

	Append(key, value string) Err
}

type MemoryKV struct {
	KV map[string]string
}

func NewMemoryKV() *MemoryKV {
	return &MemoryKV{
		KV: make(map[string]string),
	}
}

func (kv *MemoryKV) Get(key string) (string, Err) {
	if val, ok := kv.KV[key]; ok {
		return val, OK
	}
	return "", ErrNoKey
}

func (kv *MemoryKV) Put(key, val string) Err {
	kv.KV[key] = val
	return OK
}

func (kv *MemoryKV) Append(key, val string) Err {
	kv.KV[key] += val
	return OK
}
