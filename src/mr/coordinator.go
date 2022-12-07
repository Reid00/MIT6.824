package mr

import (
	"log"
	"net"
	"net/http"
	"net/rpc"
	"os"
	"time"
)

const MaxTaskRunInterval = 10 * time.Second

type (
	Task struct {
		fileName  string
		id        int
		startTime time.Time
		status    TaskStatus
	}

	heartbeatMsg struct {
		resp *HeartbeatRepose
		ok   chan struct{}
	}

	reportMsg struct {
		request *ReportRequest
		ok      chan struct{}
	}
)

type Coordinator struct {
	// Your definitions here.
	files   []string
	nReduce int
	nMap    int
	phase   SchedulePhase
	tasks   []Task

	heartbeatCh chan heartbeatMsg
	reportCh    chan reportMsg
	doneCh      chan struct{}
}

func (c *Coordinator) Heartbeat(req *HeartbeatRequest, resp *HeartbeatRepose) error {
	msg := heartbeatMsg{resp, make(chan struct{})}
	c.heartbeatCh <- msg
	<-msg.ok
	return nil
}

func (c *Coordinator) Report(req *ReportRequest, resp *ReportResponse) error {
	msg := reportMsg{request: req, ok: make(chan struct{})}
	c.reportCh <- msg
	<-msg.ok
	return nil
}

func (c *Coordinator) schedule() {
	c.initMapPhase()
	for {
		select {
		case msg := <-c.heartbeatCh:
			if c.phase == CompletePhase {
				msg.resp.JobType = CompleteJob
			} else if c.selectTask(msg.resp) {
				switch c.phase {
				case MapPhase:
					log.Printf("Coordinator: %v finished, start %v \n", MapPhase, ReducePhase)
					c.initReducePhase()
					c.selectTask(msg.resp)
				case ReducePhase:
					log.Printf("Coordinator: %v finished, Congralations", ReducePhase)
					c.initCompletePahse()
					msg.resp.JobType = CompleteJob
				case CompletePhase:
					panic("Coordinator: enter unexpected branch")
				}
			}
			log.Printf("Coordinator: assigned a task %v to worker\n", msg.resp)
			msg.ok <- struct{}{}
		case msg := <-c.reportCh:
			if msg.request.Phase == c.phase {
				log.Printf("Coordinator: Worker has executed task %v\n", msg.request)
				c.tasks[msg.request.Id].status = Finished
			}
			msg.ok <- struct{}{}
		}
	}
}

func (c *Coordinator) selectTask(resp *HeartbeatRepose) bool {
	allFinished, hasNewJob := true, false
	for id, task := range c.tasks {
		switch task.status {
		case Idle:
			allFinished, hasNewJob = false, true
			c.tasks[id].status, c.tasks[id].startTime = Working, time.Now()
			resp.NReduce, resp.Id = c.nReduce, id
			if c.phase == MapPhase {
				resp.JobType, resp.FilePath = MapJob, c.files[id]
			} else {
				resp.JobType = ReduceJob
				resp.NMap = c.nMap
			}
		case Working:
			allFinished = false
			// 超过最长运行间隔，重新运行
			if time.Since(task.startTime) > MaxTaskRunInterval {
				hasNewJob = true
				c.tasks[id].startTime = time.Now()
				resp.NReduce, resp.Id = c.nReduce, id
				if c.phase == MapPhase {
					resp.JobType, resp.FilePath = MapJob, c.files[id]
				} else {
					resp.JobType, resp.NMap = ReduceJob, c.nMap
				}
			}
		case Finished:

		}
		if hasNewJob {
			break
		}
	}
	if !hasNewJob {
		resp.JobType = WaitJob
	}
	return allFinished
}

func (c *Coordinator) initMapPhase() {
	c.phase = MapPhase
	c.tasks = make([]Task, len(c.files))
	for idx, file := range c.files {
		c.tasks[idx] = Task{
			fileName: file,
			id:       idx,
			status:   Idle,
		}
	}
}

func (c *Coordinator) initReducePhase() {
	c.phase = ReducePhase
	c.tasks = make([]Task, c.nReduce)
	for i := 0; i < c.nReduce; i++ {
		c.tasks[i] = Task{
			id:     i,
			status: Idle,
		}
	}
}

func (c *Coordinator) initCompletePahse() {
	c.phase = CompletePhase
	c.doneCh <- struct{}{}

}

// Your code here -- RPC handlers for the worker to call.

// an example RPC handler.
//
// the RPC argument and reply types are defined in rpc.go.
func (c *Coordinator) Example(args *ExampleArgs, reply *ExampleReply) error {
	reply.Y = args.X + 1
	return nil
}

// start a thread that listens for RPCs from worker.go
func (c *Coordinator) server() {
	rpc.Register(c)
	rpc.HandleHTTP()
	//l, e := net.Listen("tcp", ":1234")
	sockname := coordinatorSock()
	os.Remove(sockname) // 忽略第一次socketname 不存在时的报错
	l, e := net.Listen("unix", sockname)
	if e != nil {
		log.Fatal("listen error:", e)
	}
	go http.Serve(l, nil)
}

// main/mrcoordinator.go calls Done() periodically to find out
// if the entire job has finished.
func (c *Coordinator) Done() bool {
	ret := false

	// Your code here.
	<-c.doneCh
	ret = true

	return ret
}

// create a Coordinator.
// main/mrcoordinator.go calls this function.
// nReduce is the number of reduce tasks to use.
func MakeCoordinator(files []string, nReduce int) *Coordinator {
	c := Coordinator{
		files:       files,
		nReduce:     nReduce,
		nMap:        len(files),
		heartbeatCh: make(chan heartbeatMsg),
		reportCh:    make(chan reportMsg),
		doneCh:      make(chan struct{}, 1),
	}

	// Your code here.
	c.server()
	go c.schedule()
	return &c
}
