package main

import (
	cpb "coin/service"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"sync"
	"time"

	"golang.org/x/net/context"
	"google.golang.org/grpc"
)

var (
	index       = flag.Int("index", 0, "RPC port is 50051+index") //; debug port is 36661+index")
	numMiners   = flag.Int("miners", 3, "number of miners")       // DOESNT include the external one
	allowedTime = flag.Int("alive", 2, "number of seconds before miner declared NOT alive")
	debug       = flag.Bool("d", false, "debug mode")
)

type lockMap struct {
	sync.Mutex
	nextID   int
	loggedIn map[string]int
}

type lockString struct {
	sync.Mutex
	data string
}

type lockChan struct {
	sync.Mutex
	winnerFound bool
	ch          chan struct{}
}

var (
	users     lockMap
	block     lockString     // models the block information - basis of 'work'
	run       lockChan       // channel that controls start of run
	signIn    chan string    // for registering users in getwork
	signOut   chan string    // for registering leaving users in getcancel
	stop      sync.WaitGroup // control cancellation issue
	blockchan chan string    // for incoming block
)

// Login implements cpb.CoinServer
func (s *server) Login(ctx context.Context, in *cpb.LoginRequest) (*cpb.LoginReply, error) { // HL
	users.Lock()
	defer users.Unlock()
	if _, ok := users.loggedIn[in.Name]; ok {
		return nil, errors.New("You are already logged in!")
	}
	users.nextID++
	users.loggedIn[in.Name] = users.nextID
	return &cpb.LoginReply{Id: uint32(users.nextID)}, nil
}

// GetWork implements cpb.CoinServer, synchronises start of miners, hands out work
func (s *server) GetWork(ctx context.Context, in *cpb.GetWorkRequest) (*cpb.GetWorkReply, error) {
	debugF("Work request: %+v\n", in)
	signIn <- in.Name // HL
	<-run.ch          // HL

	block.Lock()
	work := &cpb.Work{Coinbase: in.Name, Block: []byte(block.data)}
	block.Unlock()
	return &cpb.GetWorkReply{Work: work}, nil
}

// Announce responds to a proposed solution : implements cpb.CoinServer
func (s *server) Announce(ctx context.Context, soln *cpb.AnnounceRequest) (*cpb.AnnounceReply, error) {
	run.Lock()
	defer run.Unlock()
	if run.winnerFound {
		return &cpb.AnnounceReply{Ok: false}, nil
	}
	// we have a winner
	run.winnerFound = true // HL
	resultchan <- *soln.Win
	fmt.Println("starting signout numminers = ", *numMiners)
	WaitFor(signOut, false)
	run.ch = make(chan struct{}) // HL
	stop.Done()                  // HL
	return &cpb.AnnounceReply{Ok: true}, nil
}

// GetCancel broadcasts a cancel instruction : implements cpb.CoinServer
func (s *server) GetCancel(ctx context.Context, in *cpb.GetCancelRequest) (*cpb.GetCancelReply, error) {
	signOut <- in.Name // HL
	stop.Wait()        // HL
	serv := *index
	return &cpb.GetCancelReply{Index: uint32(serv)}, nil // HL
}

// server is used to implement cpb.CoinServer.
type server struct{}

// IssueBlock receives the new block from Conductor : implements cpb.CoinServer
func (s *server) IssueBlock(ctx context.Context, in *cpb.IssueBlockRequest) (*cpb.IssueBlockReply, error) {
	blockchan <- in.Block
	return &cpb.IssueBlockReply{Ok: true}, nil
}

var resultchan chan cpb.Win //string

// GetResult sends back win to Conductor : implements cpb.CoinServer
func (s *server) GetResult(ctx context.Context, in *cpb.GetResultRequest) (*cpb.GetResultReply, error) {
	result := <-resultchan                             // wait for a result
	fmt.Printf("sendresult: %d, %v\n", *index, result) // send this back to client
	return &cpb.GetResultReply{Winner: &result, Index: uint32(*index)}, nil
}

// utilities
func fatalF(message string, err error) {
	if err != nil {
		log.Fatalf(message+": %v", err)
	}
}

func debugF(format string, args ...interface{}) {
	if *debug {
		log.Printf(format, args...)
	}
}

// WaitFor allows for the loss of a miners
func WaitFor(sign chan string, in bool) {
	alive := make(map[string]bool)
	count := 1
	alive[<-sign] = true // we need at least one!
	// the rest ...
	for i := 1; i < *numMiners; i++ {
		select {
		case <-time.After(time.Duration(*allowedTime) * time.Second): // exit, time is up
			goto done
		case c := <-sign:
			alive[c] = true
			count++
		}
	}
done:
	dir := "out"
	if in {
		dir = "in"
		if count < *numMiners {
			for name := range users.loggedIn {
				if !alive[name] {
					fmt.Printf("DEAD: %s\n", name)
					delete(users.loggedIn, name)
				}
			}
		}
	}
	fmt.Printf("miners %s = %d\n", dir, count)
}

func main() {
	flag.Parse()
	users.loggedIn = make(map[string]int)
	users.nextID = -1
	*numMiners++ // to include the Conductor (EXTERNAL)

	port := fmt.Sprintf(":%d", 50051+*index)
	lis, err := net.Listen("tcp", port)
	fatalF("failed to listen", err)

	signIn = make(chan string, *numMiners)  // register incoming miners
	signOut = make(chan string, *numMiners) // register miners receipt of cancel instructions
	blockchan = make(chan string, 1)        // transfer block data
	run.ch = make(chan struct{})            // signal to start mining
	resultchan = make(chan cpb.Win)         // transfer solution data

	go func() {
		for {
			block.data = <-blockchan // HL
			WaitFor(signIn, true)    // HL
			fmt.Printf("\n--------------------\nNew race!\n")
			run.winnerFound = false // HL
			stop.Add(1)             // HL
			close(run.ch)           // HL
		}
	}()
	s := new(server)
	g := grpc.NewServer()
	cpb.RegisterCoinServer(g, s)
	g.Serve(lis)
}
