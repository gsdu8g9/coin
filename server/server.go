package main

import (
	cpb "coin/service"
	"errors"
	"fmt"
	"log"
	"net"
	"sync"
	"time"

	"golang.org/x/net/context"
	"google.golang.org/grpc"
)

const (
	port      = ":50051"
	numMiners = 3
	timeOut   = 14
)

// server is used to implement cpb.CoinServer.
type server struct{}

// logger type is for the users login details
type logger struct {
	sync.Mutex //anonymous
	nextID     int
	loggedIn   map[string]int
}

var users logger

// Login implements cpb.CoinServer
func (s *server) Login(ctx context.Context, in *cpb.LoginRequest) (*cpb.LoginReply, error) {
	users.Lock()
	defer users.Unlock()
	if _, ok := users.loggedIn[in.Name]; ok {
		return nil, errors.New("You are already logged in!")
	}
	users.nextID++
	users.loggedIn[in.Name] = users.nextID
	return &cpb.LoginReply{Id: uint32(users.nextID)}, nil
}

var minersIn, run sync.WaitGroup

// GetWork implements cpb.CoinServer, synchronise start of miners
func (s *server) GetWork(ctx context.Context, in *cpb.GetWorkRequest) (*cpb.GetWorkReply, error) {
	fmt.Printf("Work requested: %+v\n", in)
	minersIn.Done() //Add(-1) // we work downwards from numMiners
	run.Wait()      // all must wait here
	return &cpb.GetWorkReply{Work: fetchWork(in.Name)}, nil
}

var block string

// prepares the candidate block and also provides user specific coibase data
func fetchWork(name string) *cpb.Work { // TODO -this should return err as well
	return &cpb.Work{Coinbase: name, Block: []byte(block)}
}

var settled struct {
	sync.Mutex
	ch chan struct{}
}

// vetWins handle wins - all are directed here
func vetWin(thewin cpb.Win) bool {
	settled.Lock()
	defer settled.Unlock()
	select {
	case <-settled.ch: // closed if already have a winner
		return false
	default:
		fmt.Printf("\nWinner is: %+v\n", thewin)
		close(settled.ch) // until call for new run resets this one
		for i := 0; i < numMiners; i++ {
			miner := <-signOut
			fmt.Printf("[%d] de_register %s\n", i, miner)
		}
		stop.Done() // issue cancellations
		run.Add(1)
		// endRun()          // SOLE call to endRun
		return true
	}
}

/*
var endrun chan struct{}

func endRun() {
	for i := 0; i < numMiners; i++ {
		miner := <-signOut
		fmt.Printf("[%d] de_register %s\n", i, miner)
	}
	close(endrun) // issue cancellation to our clients
	workgate = make(chan struct{})
	block = fmt.Sprintf("BLOCK: %v", time.Now())

	fmt.Printf("\nNew race!\n--------------------\n")
}



*/

// Announce responds to a proposed solution : implements cpb.CoinServer
func (s *server) Announce(ctx context.Context, soln *cpb.AnnounceRequest) (*cpb.AnnounceReply, error) {
	won := vetWin(*soln.Win)
	return &cpb.AnnounceReply{Ok: won}, nil
}

// extAnnounce is the analogue of 'Announce'
func extAnnounce() {
	// to be filled
}

var stop sync.WaitGroup
var signOut chan string

// GetCancel blocks until a valid stop condition then broadcasts a cancel instruction : implements cpb.CoinServer
func (s *server) GetCancel(ctx context.Context, in *cpb.GetCancelRequest) (*cpb.GetCancelReply, error) {
	signOut <- in.Name // register
	stop.Wait()        // wait for cancel signal
	// minersOut <- struct{}{}
	return &cpb.GetCancelReply{Ok: true}, nil
}

// initalise
func init() {
	users.loggedIn = make(map[string]int)
	users.nextID = -1
}

var minersOut chan struct{}

//var cancels chan struct{}
// cancels = make(chan struct{}, numMiners)

func main() {
	lis, err := net.Listen("tcp", port)
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}

	block = fmt.Sprintf("BLOCK: %v", time.Now()) // new work
	run.Add(1)                                   // updated in vetWin
	minersIn.Add(numMiners)
	signOut = make(chan string)

	go func() {
		for i := 0; i < 30; i++ {
			settled.Lock()
			settled.ch = make(chan struct{})
			settled.Unlock()
			// toolate = make(chan struct{})
			// minersOut = make(chan struct{}, numMiners)

			// start the race
			go func() {
				minersIn.Wait()         // for a work request
				minersIn.Add(numMiners) // so that we pause above
				stop.Add(1)
				run.Done() // free the miners to start
			}()

			// handle the win, issue the cancellation
			// firstWin := <-wins.ch // wait and grab this one
			//<-toolate   // wait for this to close
			<-settled.ch
			// stop.Done() // issue cancellations --- becomes negative ?...
			// run.Add(1)
			block = fmt.Sprintf("BLOCK: %v", time.Now()) // new work
			fmt.Printf("\nNew race!\n--------------------\n")
			// close(wins.ch) // rather than drain it - no idea how many?
			// for range minersOut {
			// 	<-minersOut
			// }
		}
	}()

	s := grpc.NewServer()
	cpb.RegisterCoinServer(s, &server{})
	s.Serve(lis)
}
