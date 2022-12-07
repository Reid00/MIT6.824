package mr

import (
	"bytes"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io"
	"log"
	"net/rpc"
	"os"
	"sort"
	"sync"
	"time"
)

// Map functions return a slice of KeyValue.
type KeyValue struct {
	Key   string
	Value string
}

// for sorting by key.
type ByKey []KeyValue

// for sorting by key.
func (a ByKey) Len() int           { return len(a) }
func (a ByKey) Swap(i, j int)      { a[i], a[j] = a[j], a[i] }
func (a ByKey) Less(i, j int) bool { return a[i].Key < a[j].Key }

// use ihash(key) % NReduce to choose the reduce
// task number for each KeyValue emitted by Map.
func ihash(key string) int {
	h := fnv.New32a()
	h.Write([]byte(key))
	return int(h.Sum32() & 0x7fffffff)
}

type mapF func(string, string) []KeyValue
type reduceF func(string, []string) string

// main/mrworker.go calls this function.
func Worker(mapf func(string, string) []KeyValue,
	reducef func(string, []string) string) {

	// Your worker implementation here.
	for {
		response := doHeartBeat()
		log.Printf("Worker: receive coordinator's heartbeat %v \n", response)
		switch response.JobType {
		case MapJob:
			doMapTask(mapf, response)
		case ReduceJob:
			doReduceTask(reducef, response)
		case WaitJob:
			time.Sleep(1 * time.Second)
		case CompleteJob:
			return
		default:
			panic(fmt.Sprintf("unexpected jobType %v", response.JobType))
		}
	}

}

func doMapTask(mapf mapF, resp *HeartbeatRepose) {
	fileName := resp.FilePath
	file, err := os.Open(fileName)
	if err != nil {
		log.Fatalf("cannot open %v", fileName)
	}
	defer file.Close()

	content, err := io.ReadAll(file)
	if err != nil {
		log.Fatalf("cannot ReadAll %v", err)
	}

	kva := mapf(fileName, string(content))
	intermediates := make([][]KeyValue, resp.NReduce)
	for _, kv := range kva {
		index := ihash(kv.Key) % resp.NReduce
		intermediates[index] = append(intermediates[index], kv)
	}

	var wg sync.WaitGroup
	for idx, intermediate := range intermediates {
		wg.Add(1)
		go func(idx int, intermediate []KeyValue) {
			defer wg.Done()
			intermediateFilePath := generateMapResultFileName(resp.Id, idx)
			// log.Println("intermediateFilePath: ", intermediateFilePath)
			var buf bytes.Buffer
			enc := json.NewEncoder(&buf)

			for _, kv := range intermediate {
				err := enc.Encode(&kv)
				if err != nil {
					log.Fatalf("cannot encode json %v", kv.Key)
				}
			}
			err := atomicWriteFile(intermediateFilePath, &buf)
			if err != nil {
				panic(fmt.Sprintf("atomicWriteFile failed, %v", err))
			}
		}(idx, intermediate)
	}

	wg.Wait()
	doReport(resp.Id, MapPhase)
}

func doReduceTask(reducef reduceF, resp *HeartbeatRepose) {
	var kva []KeyValue
	for i := 0; i < resp.NMap; i++ {
		filePath := generateMapResultFileName(i, resp.Id)
		file, err := os.Open(filePath)
		if err != nil {
			log.Fatalf("ReduceTask: cannot open %v", filePath)
		}
		dec := json.NewDecoder(file)
		for {
			var kv KeyValue
			if err := dec.Decode(&kv); err != nil {
				break
			}
			kva = append(kva, kv)
		}
		file.Close()
	}
	sort.Sort(ByKey(kva))
	results := make(map[string][]string)
	for _, kv := range kva {
		results[kv.Key] = append(results[kv.Key], kv.Value)
	}
	var buf bytes.Buffer
	for k, v := range results {
		output := reducef(k, v)
		fmt.Fprintf(&buf, "%v, %v \n", k, output)
	}
	atomicWriteFile(generateReduceResultFileName(resp.Id), &buf)
	doReport(resp.Id, ReducePhase)
}

func doHeartBeat() *HeartbeatRepose {
	resp := HeartbeatRepose{}
	ok := call("Coordinator.Heartbeat", &HeartbeatRequest{}, &resp)
	if ok {
		return &resp
	} else {
		panic("call error")
	}
}

func doReport(id int, phase SchedulePhase) {
	call("Coordinator.Report", &ReportRequest{id, phase}, &ReportResponse{})
}

// example function to show how to make an RPC call to the coordinator.
//
// the RPC argument and reply types are defined in rpc.go.
func CallExample() {

	// declare an argument structure.
	args := ExampleArgs{}

	// fill in the argument(s).
	args.X = 99

	// declare a reply structure.
	reply := ExampleReply{}

	// send the RPC request, wait for the reply.
	// the "Coordinator.Example" tells the
	// receiving server that we'd like to call
	// the Example() method of struct Coordinator.
	ok := call("Coordinator.Example", &args, &reply)
	if ok {
		// reply.Y should be 100.
		fmt.Printf("reply.Y %v\n", reply.Y)
	} else {
		fmt.Printf("call failed!\n")
	}
}

// send an RPC request to the coordinator, wait for the response.
// usually returns true.
// returns false if something goes wrong.
func call(rpcname string, args interface{}, reply interface{}) bool {
	// c, err := rpc.DialHTTP("tcp", "127.0.0.1"+":1234")
	sockname := coordinatorSock()
	c, err := rpc.DialHTTP("unix", sockname)
	if err != nil {
		log.Fatal("dialing:", err)
	}
	defer c.Close()

	err = c.Call(rpcname, args, reply)
	if err == nil {
		return true
	}

	fmt.Println(err)
	return false
}
