package main

import (
	"flag"
	"fmt"
	"log"
	"math/rand"
	"time"

	cpb "coin/service"

	"golang.org/x/net/context"
	"google.golang.org/grpc"
)

var (
	debug  = flag.Bool("d", false, "debug mode")
	tosses = flag.Int("t", 2, "number of tosses")
	user   = flag.String("u", "sole", "the client name")
	server = flag.Int("s", 0, "server offset from 50051 - will include full URL later")
)

// login to server c, returns a id
func login(c cpb.CoinClient, name string) uint32 {
	// Contact the server and print out its response.
	r, err := c.Login(context.Background(), &cpb.LoginRequest{Name: name})
	if err != nil {
		log.Fatalf("could not login: %v", err)
	}
	log.Printf("Login successful. Assigned id: %d\n", r.Id)
	return r.Id
}

// sign up with server c
func signUp(c cpb.CoinClient, name string) *cpb.Work {
	// get ready, get set ... this needs to block at each server
	r, err := c.GetWork(context.Background(), &cpb.GetWorkRequest{Name: name})
	if err != nil {
		log.Fatalf("could not get work: %v", err)
	}

	if *debug {
		log.Printf("Got work %+v\n", r.Work)
	}
	return r.Work
}

// annouceWin is what causes the server to issue a cancellation
func annouceWin(c cpb.CoinClient, nonce uint32, coinbase string) bool {
	win := &cpb.Win{Coinbase: coinbase, Nonce: nonce}
	r, err := c.Announce(context.Background(), &cpb.AnnounceRequest{Win: win})
	if err != nil {
		log.Fatalf("could not announce win: %v", err)
	}
	return r.Ok
}

// getCancel makes a blocking request to the server
func getCancel(c cpb.CoinClient, name string, look chan struct{}, quit chan struct{}) {
	if _, err := c.GetCancel(context.Background(), &cpb.GetCancelRequest{Name: name}); err != nil {
		log.Fatalf("could not request cancellation: %v", err)
	}
	look <- struct{}{} // stop search
	quit <- struct{}{} // quit loop
}

// dice
func toss() int {
	return rand.Intn(6)
}

// rools returns true if n tosses are all 5's
func rolls(n int) bool {
	ok := true
	for i := 0; i < n; i++ {
		ok = ok && toss() == 5
		if !ok {
			break
		}
	}
	return ok
}

// search tosses two dice waiting for a double 5. exit on cancel or win
func search(work *cpb.Work, look chan struct{}) (uint32, bool) {
	var theNonce uint32
	var ok bool
	tick := time.Tick(1 * time.Second) // spin wheels
	for cn := 0; ; cn++ {
		if rolls(*tosses) { // a win?
			theNonce = uint32(cn)
			ok = true
			break
		}
		// check for a stop order
		select {
		case <-look:
			goto done // if so ... break out of this cycle, return (with ok=false!)
		default: // continue
		}
		// wait for a second here ...
		<-tick
		if *debug {
			fmt.Println(myID, " ", cn)
		}
	}

done:
	return theNonce, ok
}

func init() {
	rand.Seed(time.Now().UTC().UnixNano())
}

var myID uint32

func main() {
	flag.Parse()

	address := fmt.Sprintf("localhost:%d", 50051+*server) //"localhost:" + *server
	// Set up a connection to the server.
	conn, err := grpc.Dial(address, grpc.WithInsecure())
	if err != nil {
		log.Fatalf("did not connect: %v", err)
	}
	defer conn.Close()

	c := cpb.NewCoinClient(conn)

	name := *user

	// Contact the server and print out its response.
	myID = login(c, name)

	// main cycle
	for {
		fmt.Printf("Fetching work %s ..\n", name)
		// get ready, get set ... this needs to block
		work := signUp(c, name)

		look := make(chan struct{}, 1) // for search
		quit := make(chan struct{}, 1) // for this loop
		// look out for cancellation
		go getCancel(c, name, look, quit)
		// search blocks
		theNonce, ok := search(work, look)
		if ok {
			fmt.Printf("%d ... sending solution (%d) \n", myID, theNonce)
			win := annouceWin(c, theNonce, work.Coinbase)
			if win { // it's possible that my winning nonce was late!
				fmt.Printf("== %d == FOUND -> %d\n", myID, theNonce)
			}
		}

		<-quit // wait here for cancel from server

		fmt.Printf("-----------------------\n")
	}

}
