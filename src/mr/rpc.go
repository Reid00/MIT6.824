package mr

//
// RPC definitions.
//
// remember to capitalize all names.
//

import (
	"fmt"
	"os"
	"strconv"
)

type HeartbeatRequest struct {
}

type HeartbeatRepose struct {
	JobType  JobType
	NMap     int
	NReduce  int
	Id       int
	FilePath string
}

func (resp HeartbeatRepose) String() string {
	switch resp.JobType {
	case MapJob:
		return fmt.Sprintf("{JobType:%v,FilePath:%v,Id:%v,NReduce:%v}", resp.JobType, resp.FilePath, resp.Id, resp.NReduce)
	case ReduceJob:
		return fmt.Sprintf("{JobType:%v,Id:%v,NMap:%v,NReduce:%v}", resp.JobType, resp.Id, resp.NMap, resp.NReduce)
	case WaitJob, CompleteJob:
		return fmt.Sprintf("{JobType:%v}", resp.JobType)
	default:
		panic(fmt.Sprintf("unexpected JobType %d", resp.JobType))
	}
}

type ReportRequest struct {
	Id    int
	Phase SchedulePhase
}

func (req ReportRequest) String() string {
	return fmt.Sprintf("{Id: %v, SchedulePhase: %v}", req.Id, req.Phase)
}

type ReportResponse struct {
}

// --------------------------------------------------------------------------
//
// example to show how to declare the arguments
// and reply for an RPC.
//

type ExampleArgs struct {
	X int
}

type ExampleReply struct {
	Y int
}

// Add your RPC definitions here.

// Cook up a unique-ish UNIX-domain socket name
// in /var/tmp, for the coordinator.
// Can't use the current directory since
// Athena AFS doesn't support UNIX-domain sockets.
func coordinatorSock() string {
	s := "/var/tmp/824-mr-"
	s += strconv.Itoa(os.Getuid())
	return s
}
